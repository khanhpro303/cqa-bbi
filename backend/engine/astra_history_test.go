package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/ai"
)

// captureSave is a thin Astra DB stub: it records the first received insertOne
// document so assertions can verify the on-wire payload shape.
type captureSave struct {
	server    *httptest.Server
	receivedB []byte
	method    string
	tokenHdr  string
	path      string
}

func newAstraStub(t *testing.T, status int) *captureSave {
	cap := &captureSave{}
	cap.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read astra request body: %v", err)
		}
		cap.receivedB = body
		cap.method = r.Method
		cap.tokenHdr = r.Header.Get("Token")
		cap.path = r.URL.Path
		w.WriteHeader(status)
	}))
	t.Cleanup(cap.server.Close)
	return cap
}

func newEmbeddingStub(t *testing.T, vec []float32) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"embedding": vec}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSaveChatMessage_SkipsWhenUnconfigured(t *testing.T) {
	tests := []struct {
		name                              string
		apiEndpoint, token, keyspace, col string
	}{
		{"no endpoint", "", "tok", "ks", "col"},
		{"no token", "https://x", "", "ks", "col"},
		{"no collection", "https://x", "tok", "ks", ""},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := SaveChatMessage(context.Background(), tc.apiEndpoint, tc.token, tc.keyspace, tc.col, "u1", "s1", "user", "hello", nil)
			if err != nil {
				t.Fatalf("expected nil error when unconfigured, got %v", err)
			}
		})
	}
}

func TestSaveChatMessage_WritesLangflowShape(t *testing.T) {
	astra := newAstraStub(t, http.StatusOK)
	embedSrv := newEmbeddingStub(t, []float32{0.5, -0.25, 0.125})
	embedder := ai.NewEmbeddingsClient("sk-test", "", embedSrv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const userID = "zalo_user_42"
	const sessionID = "sess_xyz"
	const content = "Tôi tìm thấy nhiều sản phẩm khớp với 'FF901'.\n1. SP458484\n2. SP458516\n3. SP001187"

	if err := SaveChatMessage(ctx, astra.server.URL, "tok-123", "cqa_bbi", "zalo_chat_history", userID, sessionID, "assistant", content, embedder); err != nil {
		t.Fatalf("SaveChatMessage: %v", err)
	}

	if astra.method != http.MethodPost {
		t.Errorf("method = %s, want POST", astra.method)
	}
	if astra.tokenHdr != "tok-123" {
		t.Errorf("Token header = %q", astra.tokenHdr)
	}
	wantPath := "/api/json/v1/cqa_bbi/zalo_chat_history"
	if astra.path != wantPath {
		t.Errorf("path = %q, want %q", astra.path, wantPath)
	}

	var envelope struct {
		InsertOne struct {
			Document map[string]any `json:"document"`
		} `json:"insertOne"`
	}
	if err := json.Unmarshal(astra.receivedB, &envelope); err != nil {
		t.Fatalf("decode astra body: %v", err)
	}
	doc := envelope.InsertOne.Document
	mustEqual := map[string]any{
		"zalo_user_id": userID,
		"session_id":   sessionID,
		"role":         "assistant",
		"content":      content,
		"text":         content,
	}
	for k, want := range mustEqual {
		if got, ok := doc[k]; !ok || got != want {
			t.Errorf("doc[%q] = %v (present=%v), want %v", k, got, ok, want)
		}
	}
	if _, ok := doc["created_at"]; !ok {
		t.Error("created_at missing from document")
	}
	vec, ok := doc["$vector"].([]any)
	if !ok {
		t.Fatalf("$vector missing or wrong type: %T", doc["$vector"])
	}
	if len(vec) != 3 {
		t.Errorf("$vector len = %d, want 3", len(vec))
	}
}

func TestSaveChatMessage_DegradedWhenEmbedderFails(t *testing.T) {
	astra := newAstraStub(t, http.StatusOK)
	failingEmbed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failingEmbed.Close()
	embedder := ai.NewEmbeddingsClient("sk", "", failingEmbed.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := SaveChatMessage(ctx, astra.server.URL, "t", "ks", "col", "u", "s", "user", "hello", embedder); err != nil {
		t.Fatalf("SaveChatMessage: %v", err)
	}

	var envelope struct {
		InsertOne struct {
			Document map[string]any `json:"document"`
		} `json:"insertOne"`
	}
	if err := json.Unmarshal(astra.receivedB, &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := envelope.InsertOne.Document["$vector"]; ok {
		t.Error("$vector should be absent when embed fails (degraded mode)")
	}
	if envelope.InsertOne.Document["text"] != "hello" {
		t.Errorf("text field missing/wrong: %v", envelope.InsertOne.Document["text"])
	}
}

func TestSaveChatMessage_NoEmbedderOmitsVector(t *testing.T) {
	astra := newAstraStub(t, http.StatusOK)
	if err := SaveChatMessage(context.Background(), astra.server.URL, "t", "", "col", "u", "s", "user", "hi", nil); err != nil {
		t.Fatalf("SaveChatMessage: %v", err)
	}
	if !strings.Contains(astra.path, "default_keyspace") {
		t.Errorf("expected default_keyspace fallback, got path %s", astra.path)
	}
}

func TestSaveChatMessage_PropagatesAstraError(t *testing.T) {
	astra := newAstraStub(t, http.StatusBadRequest)
	err := SaveChatMessage(context.Background(), astra.server.URL, "t", "ks", "col", "u", "s", "user", "hi", nil)
	if err == nil || !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("expected status 400 error, got %v", err)
	}
}
