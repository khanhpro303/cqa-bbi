package chatmemory

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestTurnSubject(t *testing.T) {
	if typ, id := (Turn{ZaloUserID: "u1"}).Subject(); typ != "user" || id != "u1" {
		t.Fatalf("1:1 subject = %s/%s", typ, id)
	}
	if typ, id := (Turn{ZaloUserID: "u1", ZaloGroupID: "g1"}).Subject(); typ != "group" || id != "g1" {
		t.Fatalf("group subject = %s/%s", typ, id)
	}
	if got := ExternalConversationID(Turn{ZaloUserID: "u1", ZaloGroupID: "g1"}); got != "g1" {
		t.Fatalf("group conversation id = %s", got)
	}
}

func TestParseSummaryResult(t *testing.T) {
	content := "```json\n" + `{"summary":" Khách hỏi tồn kho mã A1. ","intents":["hỏi tồn kho"],"products":["A1"],
"outcome":"answered","sentiment":"neutral",
"issues":[{"type":"complaint","severity":"HIGH","detail":"Giao thiếu hàng","evidence":"thiếu 2 thùng"},{"type":"other","severity":"low","detail":""}],
"profile":{"address_form":"anh"},"card":"` + strings.Repeat("x", 700) + `"}` + "\n```"
	r, err := ParseSummaryResult(content)
	if err != nil {
		t.Fatal(err)
	}
	if r.Summary != "Khách hỏi tồn kho mã A1." {
		t.Fatalf("summary = %q", r.Summary)
	}
	if len(r.Issues) != 1 || r.Issues[0].Severity != "high" {
		t.Fatalf("issues = %+v", r.Issues)
	}
	if n := len([]rune(r.Card)); n > maxCardRunes+1 {
		t.Fatalf("card has %d runes", n)
	}
	if r.Profile == nil || !strings.Contains(string(*r.Profile), "address_form") {
		t.Fatalf("profile = %v", r.Profile)
	}

	if _, err := ParseSummaryResult(`{"summary":""}`); err == nil {
		t.Fatal("empty summary accepted")
	}
	if _, err := ParseSummaryResult("không phải JSON"); err == nil {
		t.Fatal("non-JSON accepted")
	}
}

func TestBuildSummaryInput(t *testing.T) {
	loc := time.FixedZone("ICT", 7*3600)
	base := time.Date(2026, 11, 18, 2, 0, 0, 0, time.UTC)
	msgs := []models.Message{
		{SenderType: "customer", SenderName: "Anh Bình", Content: "còn mã A1 không", SentAt: base},
		{SenderType: "agent", Content: "Dạ còn 12 thùng", SentAt: base.Add(time.Minute), RawData: `{"cqa_live":true,"cqa_source":"chatbot"}`},
		{SenderType: "agent", Content: "Em gọi lại anh nhé", SentAt: base.Add(2 * time.Minute), RawData: `{}`},
		{SenderType: "customer", Content: "   ", SentAt: base.Add(3 * time.Minute)},
	}
	in := BuildSummaryInput(models.ChatSession{SubjectType: "group", CustomerName: "Cửa hàng X"}, "", msgs, loc)
	for _, want := range []string{
		`nhóm Zalo "Cửa hàng X"`,
		"(chưa có)",
		"[18/11 09:00] Khách (Anh Bình): còn mã A1 không",
		"Bot: Dạ còn 12 thùng",
		"Nhân viên: Em gọi lại anh nhé",
	} {
		if !strings.Contains(in, want) {
			t.Fatalf("input missing %q:\n%s", want, in)
		}
	}
	if customerTurns(msgs) != 1 {
		t.Fatalf("customerTurns = %d", customerTurns(msgs))
	}

	// A long transcript keeps the newest lines.
	var long []models.Message
	for i := 0; i < 100; i++ {
		long = append(long, models.Message{SenderType: "customer", Content: strings.Repeat("a", 300) + string(rune('A'+i%26)), SentAt: base.Add(time.Duration(i) * time.Minute)})
	}
	in = BuildSummaryInput(models.ChatSession{SubjectType: "user"}, `{"address_form":"chị"}`, long, loc)
	if len(in) > maxTranscriptChars+2000 {
		t.Fatalf("input too long: %d", len(in))
	}
	if !strings.Contains(in, base.Add(99*time.Minute).In(loc).Format("02/01 15:04")) {
		t.Fatal("newest message dropped")
	}
	if !strings.Contains(in, `{"address_form":"chị"}`) {
		t.Fatal("current profile missing")
	}
}

func TestSummaryIndexText(t *testing.T) {
	got := SummaryIndexText(models.ChatSession{Summary: "Tóm tắt", Intents: `["hỏi giá"]`, Products: `[]`, Outcome: "answered"})
	want := "Tóm tắt\nÝ định: hỏi giá\nKết quả: answered"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNewVectorIndexSelection(t *testing.T) {
	cfg := &config.Config{}
	if NewVectorIndex(cfg) != nil {
		t.Fatal("no credentials should mean no index")
	}
	cfg.AstraDBAPIEndpoint, cfg.AstraDBToken = "https://astra", "tok"
	if idx := NewVectorIndex(cfg); idx == nil || idx.Name() != "astra" {
		t.Fatal("astra credentials should select astra")
	}
	cfg.ChatMemory.VectorStore = "vectorize"
	if NewVectorIndex(cfg) != nil {
		t.Fatal("vectorize without credentials should be disabled")
	}
	cfg.ChatMemory.VectorizeAccountID, cfg.ChatMemory.VectorizeAPIToken, cfg.ChatMemory.VectorizeIndex = "acc", "tok", "idx"
	if idx := NewVectorIndex(cfg); idx == nil || idx.Name() != "vectorize" {
		t.Fatal("vectorize should be selected")
	}
	cfg.ChatMemory.VectorStore = "none"
	if NewVectorIndex(cfg) != nil {
		t.Fatal("none should disable the index")
	}
}

func TestVectorizeIndex(t *testing.T) {
	var gotPaths []string
	var upsertBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing auth header")
		}
		body, _ := io.ReadAll(r.Body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/upsert"):
			upsertBody = string(body)
			w.Write([]byte(`{"success":true,"errors":[],"result":{"mutationId":"x"}}`))
		case strings.HasSuffix(r.URL.Path, "/query"):
			var req map[string]interface{}
			json.Unmarshal(body, &req)
			if f, _ := req["filter"].(map[string]interface{}); f["subject_id"] != "u1" {
				t.Errorf("filter = %v", req["filter"])
			}
			w.Write([]byte(`{"success":true,"result":{"matches":[{"id":"s1","score":0.9,"metadata":{"text":"Tóm tắt","subject_id":"u1"}}]}}`))
		case strings.HasSuffix(r.URL.Path, "/delete_by_ids"):
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"success":false,"errors":[{"code":40001,"message":"bad ids"}]}`))
		}
	}))
	defer srv.Close()

	v := &VectorizeIndex{AccountID: "acc", APIToken: "tok", Index: "idx", BaseURL: srv.URL}
	ctx := context.Background()
	if err := v.Upsert(ctx, []VectorDoc{{ID: "s1", Vector: []float32{0.1, 0.2}, Text: "Tóm tắt", Metadata: map[string]string{"subject_id": "u1"}}}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(upsertBody, "\n") || !strings.Contains(upsertBody, `"id":"s1"`) || !strings.Contains(upsertBody, `"text":"Tóm tắt"`) {
		t.Fatalf("upsert body = %s", upsertBody)
	}
	hits, err := v.Query(ctx, []float32{0.1, 0.2}, map[string]string{"subject_id": "u1"}, 3)
	if err != nil || len(hits) != 1 || hits[0].Text != "Tóm tắt" || hits[0].Score != 0.9 {
		t.Fatalf("hits = %+v, err = %v", hits, err)
	}
	if err := v.Delete(ctx, []string{"s1"}); err == nil || !strings.Contains(err.Error(), "bad ids") {
		t.Fatalf("delete error = %v", err)
	}
	if gotPaths[0] != "/accounts/acc/vectorize/v2/indexes/idx/upsert" {
		t.Fatalf("path = %s", gotPaths[0])
	}
}

func TestAstraIndex(t *testing.T) {
	var cmds []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var cmd map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&cmd)
		for k := range cmd {
			cmds = append(cmds, k+" "+r.URL.Path)
		}
		switch {
		case cmd["createCollection"] != nil:
			w.Write([]byte(`{"status":{"ok":1}}`))
		case cmd["findOneAndReplace"] != nil:
			w.Write([]byte(`{"status":{"matchedCount":0,"upsertedId":"s1"}}`))
		case cmd["find"] != nil:
			if !strings.Contains(string(cmd["find"]), `"metadata.subject_id":"u1"`) {
				t.Errorf("find filter = %s", cmd["find"])
			}
			w.Write([]byte(`{"data":{"documents":[{"_id":"s1","text":"Tóm tắt","metadata":{"subject_id":"u1"},"$similarity":0.8}]}}`))
		case cmd["deleteMany"] != nil:
			w.Write([]byte(`{"errors":[{"message":"boom","errorCode":"X"}]}`))
		}
	}))
	defer srv.Close()

	a := &AstraIndex{Endpoint: srv.URL, Token: "tok", Collection: "c", Dimensions: 2}
	ctx := context.Background()
	if err := a.Upsert(ctx, []VectorDoc{{ID: "s1", Vector: []float32{1, 0}}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Upsert(ctx, []VectorDoc{{ID: "s2", Vector: []float32{0, 1}}}); err != nil {
		t.Fatal(err)
	}
	hits, err := a.Query(ctx, []float32{1, 0}, map[string]string{"subject_id": "u1"}, 2)
	if err != nil || len(hits) != 1 || hits[0].Score != 0.8 {
		t.Fatalf("hits = %+v, err = %v", hits, err)
	}
	if err := a.Delete(ctx, []string{"s1"}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("delete error = %v (Data API errors come back with HTTP 200)", err)
	}
	creates := 0
	for _, c := range cmds {
		if strings.HasPrefix(c, "createCollection") {
			creates++
			if c != "createCollection /api/json/v1/default_keyspace" {
				t.Fatalf("create call = %s", c)
			}
		}
	}
	if creates != 1 {
		t.Fatalf("createCollection called %d times", creates)
	}
}

func TestChannelSettingsFrom(t *testing.T) {
	if s := ChannelSettingsFrom(""); s.ProfileInPrompt || !s.IssueAlerts {
		t.Fatalf("defaults = %+v", s)
	}
	s := ChannelSettingsFrom(`{"chat_memory_profile_in_prompt":true,"chat_memory_issue_alerts":false}`)
	if !s.ProfileInPrompt || s.IssueAlerts {
		t.Fatalf("parsed = %+v", s)
	}
}
