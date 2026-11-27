package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/contracts"
	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
)

type blockingPersonalZaloImport struct {
	engine.ConversationIngester
	calls   atomic.Int32
	started chan struct{}
	resume  chan struct{}
}

func (s *blockingPersonalZaloImport) IngestConversationBatch(_, _ string, _ channels.SyncedConversation, _ []channels.SyncedMessage) (engine.IngestResult, error) {
	if s.calls.Add(1) == 1 {
		close(s.started)
		<-s.resume
	}
	return engine.IngestResult{}, nil
}

func TestPersonalZaloImportBindsAccountBeforeIngestion(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	createFacebookOAuthTenant(t, database, "tenant")
	channel := models.Channel{ID: "personal", TenantID: "tenant", ChannelType: "personal_zalo_import", Name: "New channel",
		IsActive: true, CredentialsEncrypted: []byte("test"), Metadata: "{}"}
	if err := database.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	ingester := &blockingPersonalZaloImport{started: make(chan struct{}), resume: make(chan struct{})}
	router := gin.New()
	router.POST("/import", NewPersonalZaloImportHandler(ingester))
	request := func(channelID, account string) int {
		now := time.Now().UTC()
		body, err := json.Marshal(contracts.PersonalZaloImportRequest{SchemaVersion: "1", RequestID: "req", TenantID: channel.TenantID,
			ChannelID: channelID, AccountExternalID: account, ImportedAt: now, Conversations: []contracts.ImportConversationBundle{{
				Conversation: contracts.ImportConversation{ExternalID: "peer", ThreadType: "user", LastMessageAt: now},
				Messages:     []contracts.ImportMessage{{ExternalID: "msg", SenderType: "customer", SentAt: now}},
			}}})
		if err != nil {
			panic(err)
		}
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/import", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		return w.Code
	}
	firstStatus := make(chan int, 1)
	go func() { firstStatus <- request(channel.ID, "101") }()
	select {
	case <-ingester.started:
	case status := <-firstStatus:
		t.Fatalf("first import exited before ingestion with %d", status)
	case <-time.After(5 * time.Second):
		t.Fatal("first import did not enter ingestion")
	}
	other := request(channel.ID, "202")
	close(ingester.resume)
	if first := <-firstStatus; first != http.StatusAccepted || other != http.StatusConflict || ingester.calls.Load() != 1 {
		t.Fatalf("overlapping first imports: first=%d other=%d ingest calls=%d", first, other, ingester.calls.Load())
	}
	if got := request(channel.ID, ""); got != http.StatusBadRequest {
		t.Fatalf("missing account on bound channel returned %d", got)
	}
	if got := request(channel.ID, "101"); got != http.StatusAccepted {
		t.Fatalf("same-account retry returned %d", got)
	}
	newChannel := channel
	newChannel.ID = "second"
	if err := database.Create(&newChannel).Error; err != nil {
		t.Fatal(err)
	}
	if got := request(newChannel.ID, "101"); got != http.StatusConflict || ingester.calls.Load() != 2 {
		t.Fatalf("duplicate channel reached ingestion: status=%d calls=%d", got, ingester.calls.Load())
	}
}

func TestPersonalZaloFirstDirectTurnRequiresRosterBeforeSync(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(&models.AppSetting{}, &models.ZaloWhitelist{}, &models.PersonalZaloIdentityLink{}, &models.PersonalZaloThreadApproval{}); err != nil {
		t.Fatal(err)
	}
	createFacebookOAuthTenant(t, database, "tenant")
	channel := models.Channel{ID: "new", TenantID: "tenant", Name: "New", ChannelType: "personal_zalo_import",
		IsActive: true, PersonalZaloAutoReplyEnabled: true, CredentialsEncrypted: []byte("test"), Metadata: "{}"}
	if err := database.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	req := personalZaloChatbotRequest{SchemaVersion: personalZaloDirectProtocol, RequestID: "request", TenantID: channel.TenantID,
		ChannelID: channel.ID, AccountExternalID: "101", ThreadID: "345", ThreadType: "user", SenderID: "345", MessageID: "msg",
		SentAt: time.Now().UTC(), Text: "Tra công nợ"}
	router := gin.New()
	router.POST("/chat", func(c *gin.Context) { c.Set("internal_request_id", req.RequestID) }, NewPersonalZaloChatbotHandler(&config.Config{EncryptionKey: "fixture-key"}))
	check := func(turn personalZaloChatbotRequest, wantStatus int, wantAccount string) string {
		t.Helper()
		body, err := json.Marshal(turn)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		httpReq := httptest.NewRequest(http.MethodPost, "/chat", bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, httpReq)
		var current models.Channel
		if err := database.First(&current, "id = ?", channel.ID).Error; err != nil ||
			w.Code != wantStatus || current.ExternalID != wantAccount {
			t.Fatalf("first direct turn: status=%d want=%d account=%q want=%q err=%v", w.Code, wantStatus, current.ExternalID, wantAccount, err)
		}
		return w.Body.String()
	}
	preflight := req
	preflight.DeliveryToken, preflight.DeliveryPart, preflight.DeliveryDigest = "invalid", "text", strings.Repeat("a", 64)
	check(preflight, http.StatusNoContent, "")
	setting := models.AppSetting{ID: "master", TenantID: channel.TenantID, SettingKey: "chatbot_active", ValuePlain: "false"}
	if err := database.Create(&setting).Error; err != nil {
		t.Fatal(err)
	}
	check(req, http.StatusNoContent, "")
	if err := database.Model(&setting).Update("value_plain", "true").Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&channel).Update("metadata", `{"sync_scope":"group"}`).Error; err != nil {
		t.Fatal(err)
	}
	check(req, http.StatusNoContent, "")
	if err := database.Model(&channel).Update("metadata", "{}").Error; err != nil {
		t.Fatal(err)
	}
	check(req, http.StatusNoContent, "")
	if err := database.Model(&channel).Update("external_id", "101").Error; err != nil {
		t.Fatal(err)
	}
	check(req, http.StatusNoContent, "101")
	if err := database.Create(&models.PersonalZaloThreadApproval{
		ChannelID: channel.ID, AccountExternalID: "101", ThreadType: "user", ThreadID: "345",
		Present: true, Status: personalZaloApproved, FirstSeenAt: req.SentAt.Add(-time.Second),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if reply := check(req, http.StatusOK, "101"); !strings.Contains(reply, "cần xác thực tài khoản Zalo") {
		t.Fatal("a first-time unknown sender asking for ERP data should receive verification guidance before the first sync")
	}
	req.AccountExternalID = "202"
	check(req, http.StatusNoContent, "101")
}

func TestToSyncedConversation(t *testing.T) {
	now := time.Now().UTC()
	conv := contracts.ImportConversation{
		ExternalID:     "conv-1",
		ThreadType:     "group",
		ExternalUserID: "user-1",
		CustomerName:   "Khach A",
		LastMessageAt:  now,
		Metadata: map[string]interface{}{
			"source": "personal_zalo",
		},
	}

	synced := toSyncedConversation(conv, "account-1", nil)
	if synced.ExternalID != "group:"+conv.ExternalID {
		t.Fatalf("expected composite external id %s, got %s", "group:"+conv.ExternalID, synced.ExternalID)
	}
	if synced.Metadata["thread_type"] != "group" {
		t.Fatalf("expected metadata thread_type to be group, got %v", synced.Metadata["thread_type"])
	}
	if synced.Metadata["account_external_id"] != "account-1" {
		t.Fatalf("expected metadata account_external_id to be preserved")
	}
}

func TestToSyncedConversationWithOwner(t *testing.T) {
	now := time.Now().UTC()
	conv := contracts.ImportConversation{
		ExternalID:     "conv-1",
		ThreadType:     "user",
		ExternalUserID: "user-1",
		CustomerName:   "Khach A",
		LastMessageAt:  now,
	}

	synced := toSyncedConversation(conv, "account-1", &contracts.PersonalZaloAccountOwner{
		AccountExternalID: "account-1",
		UserID:            "user-123",
		UserName:          "Leader A",
		UserEmail:         "leader@example.com",
	})
	if synced.ExternalID != "user:"+conv.ExternalID {
		t.Fatalf("expected composite external id %s, got %s", "user:"+conv.ExternalID, synced.ExternalID)
	}
	if synced.Metadata["owner_user_id"] != "user-123" {
		t.Fatalf("expected owner_user_id to be preserved")
	}
	if synced.Metadata["owner_name"] != "Leader A" {
		t.Fatalf("expected owner_name to be preserved")
	}
	if synced.Metadata["owner_email"] != "leader@example.com" {
		t.Fatalf("expected owner_email to be preserved")
	}
}

func TestToSyncedMessages(t *testing.T) {
	now := time.Now().UTC()
	messages := []contracts.ImportMessage{
		{
			ExternalID:  "msg-1",
			SenderType:  "agent",
			SenderName:  "Admin 1",
			Content:     "Xin chao",
			ContentType: "text",
			SentAt:      now,
			Attachments: []contracts.ImportAttachment{
				{
					Type:      "image",
					URL:       "https://example.com/a.jpg",
					Name:      "a.jpg",
					LocalPath: "tenant/conv/a.jpg",
				},
			},
			RawData: map[string]interface{}{
				"source": "gateway",
			},
		},
	}

	synced := toSyncedMessages(messages)
	if len(synced) != 1 {
		t.Fatalf("expected 1 message, got %d", len(synced))
	}
	if synced[0].ExternalID != "msg-1" {
		t.Fatalf("expected external id msg-1, got %s", synced[0].ExternalID)
	}
	if len(synced[0].Attachments) != 1 || synced[0].Attachments[0].LocalPath != "tenant/conv/a.jpg" {
		t.Fatalf("expected attachment local path to be preserved")
	}
}
