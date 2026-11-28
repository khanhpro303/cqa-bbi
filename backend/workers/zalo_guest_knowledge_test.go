package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/agents"
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
		question  = gobyQuestion
		answer    = "Dạ, LS2 Goby Man có 3 điểm nổi bật ạ.\n• CE EN 13634:2017, mức 2|2|2|2 WR.\n• Cổ cao bảo vệ mắt cá, gót và ngón chân.\n• Da bò và chi tiết phản quang."
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
	agentsOn := false
	audience := agents.AudienceCustomer
	var agentTools []string
	err = database.Callback().Query().After("gorm:query").Register("test:oa-guest-knowledge", func(tx *gorm.DB) {
		switch row := tx.Statement.Dest.(type) {
		case *[]models.Channel:
			*row = []models.Channel{channel}
		case *models.CRMGroup, *models.ZaloWhitelist, *models.ZaloCustomer:
			_ = tx.AddError(gorm.ErrRecordNotFound)
		case *[]models.AppSetting:
			*row = settings
		case *models.AppSetting:
			for _, value := range tx.Statement.Vars {
				if value == agents.EnabledSettingKey && agentsOn {
					row.ValuePlain = "true"
				}
			}
		case *models.AITeam:
			*row = models.AITeam{ID: "team", IsActive: true, IsDefault: true}
		case *[]models.AITeamMember:
			*row = []models.AITeamMember{{TeamID: "team", AgentID: "agent", Enabled: true}}
		case *[]models.AIAgent:
			var agent models.AIAgent
			agents.Agent{Key: "bao-hanh", Audience: audience, IsActive: true,
				SystemPrompt: "Tư vấn bảo hành.", Tools: agentTools, KnowledgeTags: []string{"bảo hành"}}.ApplyTo(&agent)
			agent.ID, agent.TenantID = "agent", tenantID
			*row = []models.AIAgent{agent}
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
			permission := req.Header.Get("X-Langflow-Global-Var-ERP_PERMISSION_TOKEN")
			if agentsOn {
				scope, err := engine.VerifyPermissionToken(permission, key)
				if err != nil || scope.TenantID != tenantID || scope.AgentType != agents.TierPublic || scope.AgentKey != "bao-hanh" ||
					!scope.DryRun || len(scope.Groups) != 0 || strings.Join(scope.KnowledgeTags, ",") != "bảo hành" ||
					!scope.ToolAllowed("search_knowledge") || scope.ToolAllowed("inventory_by_sku") {
					t.Errorf("guest specialist permission scope = %+v, err = %v", scope, err)
				}
			} else if permission != "" {
				t.Error("legacy guest carried a permission token")
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
			if input.Text != question || input.Tweaks["customer_code"] != nil ||
				input.Tweaks["zalo_user_id"] != input.SessionID || !strings.HasPrefix(input.SessionID, "zalo-guest-") ||
				strings.Contains(input.SessionID, senderID) {
				t.Errorf("guest Langflow payload = %+v", input)
			}
			agent, _ := input.Tweaks["Agent-fixture"].(map[string]any)
			prompt, _ := agent["system_prompt"].(string)
			hasProductAdvice := strings.Contains(prompt, "3–4 gạch đầu dòng thật ngắn") && strings.Contains(prompt, "Giữ nguyên mã tiêu chuẩn")
			if hasProductAdvice == agentsOn {
				t.Errorf("product advice in guest prompt = %t, specialist = %t", hasProductAdvice, agentsOn)
			}
			if !strings.Contains(prompt, engine.KnowledgeImageMarker) {
				t.Error("guest prompt lacks knowledge image rules")
			}
			if agentsOn && !strings.Contains(prompt, "Tư vấn bảo hành.") {
				t.Error("guest specialist prompt missing")
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
		agentsOn  bool
		audience  string
		tools     []string
	}{
		{question, "user_send_text", true, answer, 1, false, "", nil},
		{"FF901 còn size L không?", "user_send_text", true, engine.GuestKnowledgeVerificationReply, 0, false, "", nil},
		{"Công nợ của tôi bao nhiêu?", "user_send_text", true, engine.GuestKnowledgeVerificationReply, 0, false, "", nil},
		{question, "user_send_text", false, "", 0, false, "", nil},
		{question, "user_send_group_text", true, "", 0, false, "", nil},
		{question, "user_send_text", true, answer, 1, true, agents.AudienceCustomer, []string{"search_knowledge"}},
		{question, "user_send_text", true, engine.GuestKnowledgeUnavailableReply, 0, true, agents.AudienceCustomer, []string{"lookup_debt"}},
		{"FF901 còn size L không?", "user_send_text", true, engine.GuestKnowledgeVerificationReply, 0, true, agents.AudienceCustomer, []string{"lookup_debt"}},
		{question, "user_send_text", true, "", 0, true, agents.AudienceStaff, nil},
	} {
		agentsOn, audience, agentTools = tc.agentsOn, tc.audience, tc.tools
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
