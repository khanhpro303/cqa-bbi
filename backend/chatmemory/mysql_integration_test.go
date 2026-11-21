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
	pc := PromptContext(context.Background(), tenantID, channelID, "user", userID, "")
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

// A recreated Zalo group for the same mã KH inherits the earlier group's
// memory; raw Zalo messages past retention are deleted, other channels kept.
func TestGroupMemoryByCustomerCodeAndMessageRetentionMySQL(t *testing.T) {
	connectTestMySQL(t)
	tenantID, zaloCh, fbCh := pkg.NewUUID(), pkg.NewUUID(), pkg.NewUUID()
	now := time.Now()
	code := "KH-" + tenantID[:6]
	rows := []any{
		&models.Tenant{ID: tenantID, Name: "t", Slug: "t-" + tenantID[:8], Settings: "{}", CreatedAt: now, UpdatedAt: now},
		&models.Channel{ID: zaloCh, TenantID: tenantID, ChannelType: "zalo_oa", Name: "OA", CredentialsEncrypted: []byte("x"), Metadata: "{}", CreatedAt: now, UpdatedAt: now},
		&models.Channel{ID: fbCh, TenantID: tenantID, ChannelType: "facebook", Name: "FB", CredentialsEncrypted: []byte("x"), Metadata: "{}", CreatedAt: now, UpdatedAt: now},
		&models.ChatCustomerProfile{ID: pkg.NewUUID(), TenantID: tenantID, ChannelID: zaloCh, SubjectType: "group", SubjectID: "zg-old",
			CustomerCode: code, Profile: "{}", Card: "Xưng chị Lan, hay hỏi hàng về", SessionsCount: 3, LastSessionAt: &now, CreatedAt: now, UpdatedAt: now},
		&models.ChatSession{ID: pkg.NewUUID(), TenantID: tenantID, ChannelID: zaloCh, SubjectType: "group", SubjectID: "zg-old", CustomerCode: code,
			StartedAt: now, LastActivityAt: now, SummarizedAt: &now, Status: "summarized", Summary: "Hỏi lô hàng tuần sau",
			Intents: "[]", Products: "[]", Issues: "[]", CreatedAt: now, UpdatedAt: now},
	}
	for _, r := range rows {
		if err := db.DB.Create(r).Error; err != nil {
			t.Fatalf("seed %T: %v", r, err)
		}
	}

	pc := PromptContext(context.Background(), tenantID, zaloCh, "group", "zg-new", code)
	if !strings.Contains(pc, "chị Lan") || !strings.Contains(pc, "lô hàng tuần sau") {
		t.Fatalf("new group did not inherit memory: %q", pc)
	}
	if pc := PromptContext(context.Background(), tenantID, zaloCh, "group", "zg-new", ""); pc != "" {
		t.Fatalf("no mã KH must not inherit: %q", pc)
	}
	if pc := PromptContext(context.Background(), tenantID, zaloCh, "user", "zu-1", code); pc != "" {
		t.Fatalf("1:1 chats must not inherit by mã KH: %q", pc)
	}
	if seed, ok := profileByCustomerCode(context.Background(), tenantID, "group", code, zaloCh, "zg-old"); ok {
		t.Fatalf("a chat must not inherit from itself: %+v", seed)
	}

	old := now.AddDate(0, 0, -800)
	convZ, convF := pkg.NewUUID(), pkg.NewUUID()
	msgs := map[string]struct {
		conv string
		at   time.Time
	}{"z-old": {convZ, old}, "z-new": {convZ, now}, "f-old": {convF, old}}
	for _, c := range []models.Conversation{
		{ID: convZ, TenantID: tenantID, ChannelID: zaloCh, ExternalConversationID: "zg-old", Metadata: "{}", CreatedAt: now, UpdatedAt: now},
		{ID: convF, TenantID: tenantID, ChannelID: fbCh, ExternalConversationID: "fb-1", Metadata: "{}", CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.DB.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
	}
	ids := map[string]string{}
	for name, m := range msgs {
		id := pkg.NewUUID()
		ids[name] = id
		if err := db.DB.Create(&models.Message{ID: id, TenantID: tenantID, ConversationID: m.conv, ExternalMessageID: name, SenderType: "customer",
			Content: name, Attachments: "[]", RawData: "{}", SentAt: m.at, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if n := ApplyMessageRetention(context.Background(), 0); n != 0 {
		t.Fatalf("retention 0 must keep everything, deleted %d", n)
	}
	ApplyMessageRetention(context.Background(), 730)
	for name, wantKept := range map[string]bool{"z-old": false, "z-new": true, "f-old": true} {
		var n int64
		db.DB.Model(&models.Message{}).Where("id = ?", ids[name]).Count(&n)
		if (n == 1) != wantKept {
			t.Errorf("message %s kept=%v, want %v", name, n == 1, wantKept)
		}
	}
	var sessions int64
	db.DB.Model(&models.ChatSession{}).Where("tenant_id = ?", tenantID).Count(&sessions)
	if sessions != 1 {
		t.Fatal("retention must keep session summaries")
	}
}

func TestRecentTurnsMySQL(t *testing.T) {
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
	userID := "zu-" + pkg.NewUUID()[:8]
	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	pause := func() { time.Sleep(5 * time.Millisecond) } // distinct created_at

	// An earlier session of the same chat must not leak into the new one.
	old := Turn{TenantID: tenantID, ChannelID: channelID, SessionID: pkg.NewUUID(), ZaloUserID: userID, AgentType: "public"}
	RecordInbound(context.Background(), old, Inbound{MessageID: "old-1", Text: "hỏi cũ", SentAt: start.Add(-30 * time.Second)})
	pause()

	turn := Turn{TenantID: tenantID, ChannelID: channelID, SessionID: pkg.NewUUID(), ZaloUserID: userID, AgentType: "public"}
	ctx := WithTurn(context.Background(), turn)
	RecordInbound(ctx, turn, Inbound{MessageID: "in-1", Text: "FF399 còn không", SentAt: start})
	pause()
	// Our clock runs ahead of Zalo's: the bot question carries a later
	// sent_at than the customer's answer. It is written first, so it counts.
	// Written before RecordOutbound returns (1:1 worker turn), so no wait.
	RecordOutbound(ctx, channelID, channels.SentMessage{UserID: userID, MessageID: "out-1",
		Text: "Anh/chị đang hỏi tồn kho của mẫu FF399 đúng không?", SentAt: start.Add(time.Minute)})
	pause()
	RecordInbound(ctx, turn, Inbound{MessageID: "in-2", Text: "đúng rồi", SentAt: start.Add(12 * time.Second)})
	pause()
	// Rows written after the current message (a retried task) are not read,
	// even when they would crowd out the turns before it.
	for i := 0; i < 8; i++ {
		RecordInbound(ctx, turn, Inbound{MessageID: "later-" + string(rune('a'+i)), Text: "sau", SentAt: start.Add(12 * time.Second)})
	}

	got := RecentTurns(context.Background(), turn.SessionID, "in-2", "đúng rồi", 6)
	want := []RecentTurn{
		{"Khách", "FF399 còn không"},
		{"Bot", "Anh/chị đang hỏi tồn kho của mẫu FF399 đúng không?"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v; want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("turn %d = %+v; want %+v", i, got[i], want[i])
		}
	}

	// The bot question and the answer written in the same millisecond: the
	// question is still kept.
	var cur models.Message
	if err := db.DB.Where("tenant_id = ? AND external_message_id = ?", tenantID, "in-2").Take(&cur).Error; err != nil {
		t.Fatal(err)
	}
	res := db.DB.Model(&models.Message{}).Where("tenant_id = ? AND external_message_id = ?", tenantID, "out-1").Update("created_at", cur.CreatedAt)
	if res.Error != nil || res.RowsAffected != 1 {
		t.Fatalf("set timestamp tie: err=%v rows=%d", res.Error, res.RowsAffected)
	}
	got = RecentTurns(context.Background(), turn.SessionID, "in-2", "đúng rồi", 6)
	if len(got) != 2 || got[1].Speaker != "Bot" {
		t.Fatalf("same-millisecond bot question lost: %+v", got)
	}
}

func TestEnsureConversationHonoursDeadlineMySQL(t *testing.T) {
	connectTestMySQL(t)
	turn := Turn{TenantID: pkg.NewUUID(), ChannelID: pkg.NewUUID(), SessionID: pkg.NewUUID(), ZaloUserID: "zu-lock"}
	key := turn.TenantID + "|" + turn.ChannelID + "|" + turn.ZaloUserID
	lock, _ := convLocks.LoadOrStore(key, make(chan struct{}, 1))
	lock.(chan struct{}) <- struct{}{} // another writer holds the chat
	defer func() { <-lock.(chan struct{}) }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	began := time.Now()
	if _, err := ensureConversation(db.DB.WithContext(ctx), turn, time.Now()); err == nil {
		t.Fatal("got no error while the chat lock was held")
	}
	if waited := time.Since(began); waited > time.Second {
		t.Fatalf("waited %v for a held lock; want the 50ms deadline", waited)
	}
}
