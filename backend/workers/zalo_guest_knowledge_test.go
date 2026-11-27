package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestZaloOAGuestDirectMessageUsesPublicKnowledgeOnly(t *testing.T) {
	const (
		key       = "0123456789abcdef0123456789abcdef"
		question  = "Chính sách đổi trả như thế nào?"
		answer    = "Dạ, sản phẩm được đổi theo chính sách dành cho khách hàng."
		channelID = "channel"
		tenantID  = "tenant"
		senderID  = "123456789012345678"
		oaID      = "oa"
	)
	database, err := gorm.Open(mysql.New(mysql.Config{
		DSN:                       "fixture@tcp(127.0.0.1:3306)/fixture",
		SkipInitializeWithVersion: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	previousDB, previousRedis := db.DB, db.RedisClient
	db.DB, db.RedisClient = database, nil
	t.Cleanup(func() {
		db.DB, db.RedisClient = previousDB, previousRedis
		_ = pool.Close()
	})

	credentials, err := pkg.Encrypt([]byte(`{"access_token":"fixture","oa_id":"oa"}`), key)
	if err != nil {
		t.Fatal(err)
	}
	langflowToken, err := pkg.Encrypt([]byte("tenant-key"), key)
	if err != nil {
		t.Fatal(err)
	}
	channel := models.Channel{
		ID: channelID, TenantID: tenantID, ChannelType: "zalo_oa", ExternalID: oaID,
		IsActive: true, AutoReplyEnabled: true, CredentialsEncrypted: credentials,
	}
	settings := []models.AppSetting{
		{TenantID: tenantID, SettingKey: "chatbot_active", ValuePlain: "true"},
		{TenantID: tenantID, SettingKey: "ai_engine_langflow_url", ValuePlain: "https://langflow.test"},
		{TenantID: tenantID, SettingKey: "ai_engine_langflow_token", ValueEncrypted: langflowToken},
		{TenantID: tenantID, SettingKey: "ai_engine_langflow_flow_id", ValuePlain: "private"},
		{TenantID: tenantID, SettingKey: "ai_engine_langflow_public_flow_id", ValuePlain: "public"},
	}
	err = database.Callback().Query().After("gorm:query").Register("test:oa-guest-knowledge", func(tx *gorm.DB) {
		switch row := tx.Statement.Dest.(type) {
		case *[]models.Channel:
			*row = []models.Channel{channel}
		case *models.CRMGroup, *models.ZaloWhitelist, *models.ZaloCustomer:
			_ = tx.AddError(gorm.ErrRecordNotFound)
		case *[]models.AppSetting:
			*row = settings
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	var replies []string
	flowCalls := 0
	transport := http.DefaultTransport
	http.DefaultTransport = stockTestTransport(func(req *http.Request) (*http.Response, error) {
		var body string
		switch req.URL.Host + req.URL.Path {
		case "langflow.test/api/v1/flows/public":
			body = `{"data":{"nodes":[{"id":"Agent-fixture","data":{"node":{"template":{"system_prompt":{}}}}}]}}`
		case "langflow.test/api/v1/run/public":
			flowCalls++
			if got := req.Header.Get("X-Langflow-Global-Var-ERP_PERMISSION_TOKEN"); got != "" {
				t.Errorf("guest request carried ERP permission token %q", got)
			}
			if got := req.Header.Get("X-Langflow-Global-Var-CQA_ERP_AGENT_TOKEN"); !strings.HasPrefix(got, "kb-preview:") {
				t.Errorf("guest agent token = %q, want public knowledge preview token", got)
			}
			var input struct {
				Text      string         `json:"input_value"`
				SessionID string         `json:"session_id"`
				Tweaks    map[string]any `json:"tweaks"`
			}
			if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
				return nil, err
			}
			if input.Text != question || input.Tweaks["customer_code"] != nil || input.Tweaks["permission_token"] != nil ||
				input.Tweaks["zalo_user_id"] != input.SessionID || !strings.HasPrefix(input.SessionID, "zalo-guest-") ||
				strings.Contains(input.SessionID, senderID) {
				t.Errorf("guest Langflow payload = %+v", input)
			}
			body = fmt.Sprintf(`{"provider":"OpenAI","outputs":[{"outputs":[{"results":{"message":{"text":%q,"properties":{"usage":{"input_tokens":9,"output_tokens":7,"model_name":"gpt-5.4"}}}}}]}]}`, answer)
		case "openapi.zalo.me/v3.0/oa/message/cs":
			var reply struct {
				Message struct{ Text string }
			}
			if err := json.NewDecoder(req.Body).Decode(&reply); err != nil {
				return nil, err
			}
			replies = append(replies, reply.Message.Text)
			body = `{"error":0}`
		default:
			return nil, fmt.Errorf("unexpected fixture request: %s %s", req.Method, req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = transport })

	cfg := &config.Config{EncryptionKey: key}
	handle := HandleZaloWebhookTask(cfg, engine.NewLangflowClient(cfg), nil)
	for _, tc := range []struct {
		text      string
		event     string
		signed    bool
		wantReply string
		wantCalls int
	}{
		{question, "user_send_text", true, answer, 1},
		{"FF901 còn size L không?", "user_send_text", true, engine.GuestKnowledgeVerificationReply, 0},
		{"Công nợ của tôi bao nhiêu?", "user_send_text", true, engine.GuestKnowledgeVerificationReply, 0},
		{question, "user_send_text", false, "", 0},
		{question, "user_send_group_text", true, "", 0},
	} {
		payload := ZaloWebhookPayload{EventName: tc.event, SignatureVerified: tc.signed}
		payload.Sender.ID, payload.Recipient.ID, payload.Message.Text = senderID, oaID, tc.text
		task, err := NewZaloWebhookTask(payload)
		if err != nil {
			t.Fatal(err)
		}
		replies = nil
		flowCalls = 0
		if err := handle(context.Background(), task); err != nil {
			t.Fatalf("guest message %q: %v", tc.text, err)
		}
		wantReplies := 0
		if tc.wantReply != "" {
			wantReplies = 1
		}
		if flowCalls != tc.wantCalls || len(replies) != wantReplies || (wantReplies == 1 && replies[0] != tc.wantReply) {
			t.Fatalf("guest message %q: replies=%v flowCalls=%d", tc.text, replies, flowCalls)
		}
	}
}
