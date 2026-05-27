package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEmbeddingsClient_Embed(t *testing.T) {
	tests := []struct {
		name        string
		apiKey      string
		text        string
		serverFunc  func(t *testing.T) http.Handler
		wantErrSub  string
		wantVecLen  int
		wantFirstEl float32
	}{
		{
			name:   "happy path returns vector",
			apiKey: "sk-test",
			text:   "hello world",
			serverFunc: func(t *testing.T) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/v1/embeddings" {
						t.Errorf("unexpected path %s", r.URL.Path)
					}
					if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
						t.Errorf("auth header = %q", got)
					}
					body, _ := io.ReadAll(r.Body)
					var req embeddingsRequest
					if err := json.Unmarshal(body, &req); err != nil {
						t.Fatalf("decode request: %v", err)
					}
					if req.Input != "hello world" {
						t.Errorf("input = %q", req.Input)
					}
					if req.Model != DefaultEmbeddingModel {
						t.Errorf("model = %q", req.Model)
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{
						"data": []map[string]any{{"embedding": []float32{0.1, 0.2, 0.3}}},
					})
				})
			},
			wantVecLen:  3,
			wantFirstEl: 0.1,
		},
		{
			name:   "empty input returns error without HTTP call",
			apiKey: "sk-test",
			text:   "   ",
			serverFunc: func(t *testing.T) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					t.Fatal("must not call server for empty input")
				})
			},
			wantErrSub: "empty input",
		},
		{
			name:   "missing api key returns error",
			apiKey: "",
			text:   "hello",
			serverFunc: func(t *testing.T) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					t.Fatal("must not call server when api key empty")
				})
			},
			wantErrSub: "missing api key",
		},
		{
			name:   "non-200 surfaces status in error",
			apiKey: "sk-test",
			text:   "hi",
			serverFunc: func(t *testing.T) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
				})
			},
			wantErrSub: "status 401",
		},
		{
			name:   "api error block surfaces type+message",
			apiKey: "sk-test",
			text:   "hi",
			serverFunc: func(t *testing.T) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_ = json.NewEncoder(w).Encode(map[string]any{
						"error": map[string]string{"type": "invalid_request_error", "message": "model gone"},
					})
				})
			},
			wantErrSub: "invalid_request_error",
		},
		{
			name:   "empty data array is treated as error",
			apiKey: "sk-test",
			text:   "hi",
			serverFunc: func(t *testing.T) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = w.Write([]byte(`{"data":[]}`))
				})
			},
			wantErrSub: "empty embedding",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.serverFunc(t))
			defer srv.Close()

			client := NewEmbeddingsClient(tc.apiKey, "", srv.URL)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			vec, err := client.Embed(ctx, tc.text)
			if tc.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(vec) != tc.wantVecLen {
				t.Fatalf("vector len = %d, want %d", len(vec), tc.wantVecLen)
			}
			if vec[0] != tc.wantFirstEl {
				t.Fatalf("vec[0] = %f, want %f", vec[0], tc.wantFirstEl)
			}
		})
	}
}

func TestEmbeddingsClient_NilSafe(t *testing.T) {
	var c *EmbeddingsClient
	if _, err := c.Embed(context.Background(), "x"); err == nil {
		t.Fatal("expected error on nil client")
	}
	if c.Model() != "" {
		t.Fatal("expected empty model on nil client")
	}
}

func TestEmbeddingsClient_CustomModel(t *testing.T) {
	c := NewEmbeddingsClient("k", "custom-model", "http://example.com")
	if c.Model() != "custom-model" {
		t.Fatalf("model = %q", c.Model())
	}
}
