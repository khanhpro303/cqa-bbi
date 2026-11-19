package chatmemory

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// The pipeline test needs MySQL 8 (JSON functions, GREATEST/LEAST), e.g.
//
//	docker run -d -e MYSQL_ROOT_PASSWORD=root -e MYSQL_DATABASE=cqa -p 3307:3306 mysql:8.0
//	CQA_TEST_MYSQL_DSN='root:root@tcp(127.0.0.1:3307)/cqa?parseTime=true&loc=UTC&charset=utf8mb4' go test ./chatmemory
func connectTestMySQL(t *testing.T) {
	t.Helper()
	dsn := os.Getenv("CQA_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("CQA_TEST_MYSQL_DSN not set")
	}
	if db.DB == nil {
		if err := db.Connect(dsn, false); err != nil {
			t.Fatal(err)
		}
		if err := db.AutoMigrate(); err != nil {
			t.Fatal(err)
		}
	}
}

type fakeIndex struct {
	mu      sync.Mutex
	docs    map[string]VectorDoc
	deleted []string
}

func (f *fakeIndex) Name() string { return "fake" }
func (f *fakeIndex) Upsert(_ context.Context, docs []VectorDoc) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range docs {
		f.docs[d.ID] = d
	}
	return nil
}
func (f *fakeIndex) Query(context.Context, []float32, map[string]string, int) ([]VectorHit, error) {
	return nil, nil
}
func (f *fakeIndex) Delete(_ context.Context, ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, ids...)
	return nil
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestPipelineMySQL(t *testing.T) {
	connectTestMySQL(t)
	SetEnabled(true)
	defer SetEnabled(false)

	tenantID, channelID := pkg.NewUUID(), pkg.NewUUID()
	now := time.Now()
	if err := db.DB.Create(&models.Tenant{ID: tenantID, Name: "t", Slug: "t-" + tenantID[:8], Settings: "{}", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&models.Channel{ID: channelID, TenantID: tenantID, ChannelType: "zalo_oa", Name: "OA", CredentialsEncrypted: []byte("x"), Metadata: "{}", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	userID, sessionID := "zu-"+pkg.NewUUID()[:8], pkg.NewUUID()
	turn := Turn{TenantID: tenantID, ChannelID: channelID, SessionID: sessionID, ZaloUserID: userID,
		CustomerName: "Anh Bình", CustomerCode: "KH01", AgentType: "public"}
	start := time.Now().Add(-2 * time.Hour).Truncate(time.Second)

	// Customer trigger, bot welcome, a question, the bot answer.
	RecordInbound(context.Background(), turn, Inbound{MessageID: "in-1", Text: "bot", SentAt: start})
	ctx := WithTurn(context.Background(), turn)
	RecordOutbound(ctx, channelID, channels.SentMessage{UserID: userID, MessageID: "out-1", Text: "Chào anh", SentAt: start.Add(time.Second)})
	RecordInbound(ctx, turn, Inbound{MessageID: "in-2", Text: "còn mã A1 không em", SentAt: start.Add(time.Minute)})
	RecordOutbound(ctx, channelID, channels.SentMessage{UserID: userID, MessageID: "out-2", Text: "Dạ còn 12 thùng ạ", SentAt: start.Add(2 * time.Minute)})
	// A reply to another chat in the same turn (e.g. a staff alert) is not part of the session.
	RecordOutbound(ctx, channelID, channels.SentMessage{GroupID: "staff-group", MessageID: "out-x", Text: "alert", SentAt: start.Add(2 * time.Minute)})
	// The same inbound delivered twice is stored once.
	RecordInbound(ctx, turn, Inbound{MessageID: "in-2", Text: "còn mã A1 không em", SentAt: start.Add(time.Minute)})

	var conv models.Conversation
	if err := db.DB.Where("tenant_id = ? AND channel_id = ? AND external_conversation_id = ?", tenantID, channelID, userID).First(&conv).Error; err != nil {
		t.Fatal(err)
	}
	countMsgs := func() int64 {
		var n int64
		db.DB.Model(&models.Message{}).Where("conversation_id = ?", conv.ID).Count(&n)
		return n
	}
	waitFor(t, "4 recorded messages", func() bool { return countMsgs() == 4 })
	time.Sleep(200 * time.Millisecond)
	if n := countMsgs(); n != 4 {
		t.Fatalf("messages = %d, want 4", n)
	}

	// The Zalo sync later fetches the same messages: one with the same id,
	// one under a different id (twin by content and time). Neither is stored again.
	ingest := engine.NewIngestService()
	res, err := ingest.IngestMessages(tenantID, conv.ID, []channels.SyncedMessage{
		{ExternalID: "out-2", SenderType: "agent", Content: "Dạ còn 12 thùng ạ", SentAt: start.Add(2 * time.Minute)},
		{ExternalID: "sync-77", SenderType: "customer", Content: "còn mã A1 không em", SentAt: start.Add(time.Minute + 20*time.Second)},
		{ExternalID: "sync-78", SenderType: "customer", Content: "tin nhắn ngoài phiên", SentAt: start.Add(3 * time.Hour)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.MessagesInserted != 1 || countMsgs() != 5 {
		t.Fatalf("sync inserted %d (total %d), want 1 (total 5)", res.MessagesInserted, countMsgs())
	}

	var sess models.ChatSession
	if err := db.DB.Where("id = ?", sessionID).First(&sess).Error; err != nil {
		t.Fatal(err)
	}
	if sess.InboundCount != 2 || sess.OutboundCount != 2 || sess.SubjectType != "user" || sess.SubjectID != userID || sess.Status != "open" {
		t.Fatalf("session = %+v", sess)
	}
	if !sess.StartedAt.Equal(start) || !sess.LastActivityAt.Equal(start.Add(2*time.Minute)) {
		t.Fatalf("session window %v..%v", sess.StartedAt, sess.LastActivityAt)
	}

	// Summarize with a fake model and index with a fake store.
	idx := &fakeIndex{docs: map[string]VectorDoc{}}
	cfg := &config.Config{ChatbotSessionTimeout: 30}
	cfg.ChatMemory = config.ChatMemoryConfig{Enabled: true, VectorRetentionDays: 365}
	var gotInput string
	s := &Service{cfg: cfg, index: idx,
		summarize: func(_ context.Context, _, _, input string) (ai.AIResponse, string, string, error) {
			gotInput = input
			return ai.AIResponse{Content: `{"summary":"Khách hỏi tồn kho A1, bot báo còn 12 thùng.","intents":["hỏi tồn kho"],"products":["A1"],"outcome":"answered","sentiment":"neutral","issues":[],"profile":{"address_form":"anh"},"card":"Xưng anh, hay hỏi tồn kho."}`, InputTokens: 10, OutputTokens: 5}, "openai", "test-model", nil
		},
		embed: func(context.Context, string, string) ([]float32, error) { return []float32{0.1, 0.2}, nil },
	}
	s.RunOnce(context.Background())

	if err := db.DB.Where("id = ?", sessionID).First(&sess).Error; err != nil {
		t.Fatal(err)
	}
	if sess.Status != "summarized" || sess.IndexedAt == nil || sess.Outcome != "answered" {
		t.Fatalf("after run: status=%s indexed=%v outcome=%s err=%s", sess.Status, sess.IndexedAt, sess.Outcome, sess.SummaryError)
	}
	if strings.Contains(gotInput, "tin nhắn ngoài phiên") || !strings.Contains(gotInput, "Bot: Dạ còn 12 thùng ạ") {
		t.Fatalf("transcript:\n%s", gotInput)
	}
	if _, ok := idx.docs[sessionID]; !ok {
		t.Fatal("summary not indexed")
	}
	var prof models.ChatCustomerProfile
	if err := db.DB.Where("tenant_id = ? AND subject_id = ?", tenantID, userID).First(&prof).Error; err != nil {
		t.Fatal(err)
	}
	var p map[string]interface{}
	if json.Unmarshal([]byte(prof.Profile), &p); p["address_form"] != "anh" || prof.SessionsCount != 1 {
		t.Fatalf("profile = %+v", prof)
	}
	pc := PromptContext(context.Background(), tenantID, channelID, "user", userID)
	if !strings.Contains(pc, "Xưng anh") || !strings.Contains(pc, "12 thùng") || !strings.Contains(pc, "<ho_so_khach>") {
		t.Fatalf("prompt context = %q", pc)
	}

	// Running again does nothing new.
	s.RunOnce(context.Background())
	db.DB.Where("tenant_id = ? AND subject_id = ?", tenantID, userID).First(&prof)
	if prof.SessionsCount != 1 {
		t.Fatalf("profile counted twice: %d", prof.SessionsCount)
	}

	// Retention removes old vectors but keeps the summary.
	db.DB.Model(&models.ChatSession{}).Where("id = ?", sessionID).Update("indexed_at", time.Now().AddDate(-2, 0, 0))
	s.ApplyVectorRetention(context.Background())
	db.DB.Where("id = ?", sessionID).First(&sess)
	if len(idx.deleted) != 1 || sess.VectorDeletedAt == nil || sess.Summary == "" {
		t.Fatalf("retention: deleted=%v vector_deleted_at=%v", idx.deleted, sess.VectorDeletedAt)
	}

	// A session with only the trigger word is skipped without a model call.
	short := Turn{TenantID: tenantID, ChannelID: channelID, SessionID: pkg.NewUUID(), ZaloUserID: userID}
	RecordInbound(context.Background(), short, Inbound{MessageID: "in-9", Text: "bot", SentAt: start.Add(-time.Hour)})
	gotInput = ""
	s.RunOnce(context.Background())
	var shortSess models.ChatSession
	db.DB.Where("id = ?", short.SessionID).First(&shortSess)
	if shortSess.Status != "skipped" || gotInput != "" {
		t.Fatalf("short session status = %s", shortSess.Status)
	}
}
