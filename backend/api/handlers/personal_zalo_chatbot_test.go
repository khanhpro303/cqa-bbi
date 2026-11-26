package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestPersonalZaloChatbotSignedTurn(t *testing.T) {
	const key = "0123456789abcdef0123456789abcdef"
	const secret = "test-personal-zalo-channel-secret"
	const gatewaySecret = "test-deployment-gateway-signing-secret"
	const imageID = "cea6c2f1-40bc-4c42-a256-e2f676d24e9d"
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 3, 4))); err != nil {
		t.Fatal(err)
	}
	credentials, err := pkg.Encrypt([]byte(`{"import_secret":"`+secret+`"}`), key)
	if err != nil {
		t.Fatal(err)
	}
	channel := models.Channel{
		ID: "channel", TenantID: "tenant", ChannelType: "personal_zalo_import",
		ExternalID: "123", CredentialsEncrypted: credentials,
		AutoReplyEnabled: true, IsActive: true, PersonalZaloAutoReplyEnabled: true,
	}
	database, err := gorm.Open(mysql.New(mysql.Config{
		DSN: "fixture@tcp(127.0.0.1:3306)/fixture", SkipInitializeWithVersion: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	originalDB := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = originalDB; _ = pool.Close() })

	masterEnabled := true
	publicFlow := "public-flow"
	var pauseDuringFlow atomic.Bool
	var tenantCredentials atomic.Bool
	var requestHandover atomic.Bool
	imagePrivate := false
	imageReads := 0
	if err := database.Callback().Query().After("gorm:query").Register("test:personal_zalo_chatbot", func(q *gorm.DB) {
		switch row := q.Statement.Dest.(type) {
		case *models.Channel:
			tenantMatches := slices.Contains(q.Statement.Vars, any(channel.TenantID))
			channelMatches := slices.Contains(q.Statement.Vars, any(channel.ID))
			eligible := channel.IsActive && channel.PersonalZaloAutoReplyEnabled && !pauseDuringFlow.Load()
			if !tenantMatches || !channelMatches ||
				(strings.Contains(q.Statement.SQL.String(), "personal_zalo_auto_reply_enabled = ?") && !eligible) {
				q.AddError(gorm.ErrRecordNotFound)
				return
			}
			*row = channel
		case *[]models.AppSetting:
			if !slices.Contains(q.Statement.Vars, any(channel.TenantID)) {
				t.Error("settings query did not scope to tenant")
			}
			*row = []models.AppSetting{
				{SettingKey: "chatbot_active", ValuePlain: strconv.FormatBool(masterEnabled)},
				{SettingKey: "ai_engine_langflow_public_flow_id", ValuePlain: publicFlow},
			}
			if tenantCredentials.Load() {
				*row = append(*row, models.AppSetting{SettingKey: "ai_engine_langflow_token", ValuePlain: "fixture-langflow-key"})
			}
			if requestHandover.Load() {
				*row = append(*row, models.AppSetting{SettingKey: "ai_engine_system_prompt", ValuePlain: "Hỗ trợ khách hàng."})
			}
		case *models.CRMGroup:
			q.AddError(gorm.ErrRecordNotFound)
		case *[]models.KnowledgeDocumentImage:
			imageReads++
			if !strings.Contains(q.Statement.SQL.String(), "d.audience = ?") ||
				!slices.Contains(q.Statement.Vars, any("public")) ||
				!slices.Contains(q.Statement.Vars, any(imageID)) ||
				!slices.Contains(q.Statement.Vars, any(channel.TenantID)) {
				t.Error("image lookup did not enforce selected ID, tenant, and customer visibility")
			}
			if !imagePrivate {
				*row = []models.KnowledgeDocumentImage{{ID: imageID, Data: pngData.Bytes()}}
			}
		}
	}); err != nil {
		t.Fatal(err)
	}

	var flowCalls atomic.Int32
	var pauseOnNextTurn atomic.Bool
	flow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.NotFound(w, r)
			return
		}
		flowCalls.Add(1)
		if pauseOnNextTurn.Swap(false) {
			pauseDuringFlow.Store(true)
		}
		var input struct {
			Text      string         `json:"input_value"`
			SessionID string         `json:"session_id"`
			Tweaks    map[string]any `json:"tweaks"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil ||
			r.URL.Path != "/api/v1/run/public-flow" ||
			!strings.HasPrefix(input.SessionID, "personal-zalo-") ||
			input.Tweaks["permission_token"] != nil || input.Tweaks["customer_code"] != nil ||
			r.Header.Get("X-LANGFLOW-GLOBAL-VAR-ERP_AGENT_TOKEN") != "" ||
			r.Header.Get("X-LANGFLOW-GLOBAL-VAR-ERP_PERMISSION_TOKEN") != "" {
			t.Errorf("unexpected public Langflow request: %s %+v (%v)", r.URL.Path, input, err)
		}
		if r.Header.Get("X-API-Key") != "fixture-langflow-key" {
			t.Error("Langflow credentials were not passed")
		}
		reply := "Chào bạn!"
		if token := r.Header.Get("X-LANGFLOW-GLOBAL-VAR-CQA_ERP_AGENT_TOKEN"); tenantCredentials.Load() {
			if resolveKnowledgePreviewAgent(key, channel.TenantID, token) != "public" {
				t.Error("Langflow did not receive a public knowledge-only token")
			}
			if requestHandover.Load() {
				reply = personalZaloAIHandoverMarker
			} else if !strings.HasPrefix(input.Text, "Xin chào\n\n") || !strings.Contains(input.Text, "search_knowledge") {
				t.Error("Langflow did not receive the knowledge-image instructions")
			}
			reply += "\n[KB_IMAGE:" + imageID + "]"
		} else if token != "" || input.Text != "Xin chào" {
			t.Error("legacy Langflow turn unexpectedly changed")
		}
		_, _ = fmt.Fprintf(w, `{"outputs":[{"outputs":[{"results":{"message":{"text":%q}}}]}]}`, reply)
	}))
	defer flow.Close()

	cfg := &config.Config{EncryptionKey: key, LangflowAPIURL: flow.URL, LangflowAPIKey: "fixture-langflow-key", LangflowFlowID: "private-flow"}
	router := gin.New()
	const path = "/api/internal/chatbot/personal-zalo"
	router.POST(path, middleware.InternalHMACAuth(middleware.PersonalZaloChatbotSecretResolver(key, gatewaySecret), 5*time.Minute), NewPersonalZaloChatbotHandler(cfg))

	fresh := personalZaloChatbotRequest{
		SchemaVersion: "personal-zalo-chatbot-v1", TenantID: channel.TenantID, ChannelID: channel.ID,
		AccountExternalID: "123", ThreadID: "456", MessageID: "msg-1", Text: " Xin chào ", SentAt: time.Now(),
	}
	submit := func(req personalZaloChatbotRequest, signingSecret, serviceSecret string) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		httpReq := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/json")
		if signingSecret != "" {
			ts := strconv.FormatInt(time.Now().Unix(), 10)
			sign := func(secret string) string {
				mac := hmac.New(sha256.New, []byte(secret))
				fmt.Fprintf(mac, "%s.%s.", ts, req.RequestID)
				mac.Write(body)
				return hex.EncodeToString(mac.Sum(nil))
			}
			httpReq.Header.Set("X-CQA-Request-Id", req.RequestID)
			httpReq.Header.Set("X-CQA-Timestamp", ts)
			httpReq.Header.Set("X-CQA-Signature", sign(signingSecret))
			if serviceSecret != "" {
				httpReq.Header.Set("X-CQA-Chatbot-Signature", sign(serviceSecret))
			}
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httpReq)
		return response
	}
	for _, tc := range []struct {
		name, signingSecret string
		want                int
		setup               func(*personalZaloChatbotRequest) func()
	}{
		{name: "missing signature", want: http.StatusUnauthorized},
		{name: "wrong channel secret", signingSecret: "other-secret", want: http.StatusUnauthorized},
		{name: "different tenant", signingSecret: secret, want: http.StatusUnauthorized, setup: func(r *personalZaloChatbotRequest) func() { r.TenantID = "other-tenant"; return nil }},
		{name: "different account", signingSecret: secret, want: http.StatusNoContent, setup: func(r *personalZaloChatbotRequest) func() { r.AccountExternalID = "900"; return nil }},
		{name: "old message", signingSecret: secret, want: http.StatusNoContent, setup: func(r *personalZaloChatbotRequest) func() { r.SentAt = time.Now().Add(-3 * time.Minute); return nil }},
		{name: "self conversation", signingSecret: secret, want: http.StatusNoContent, setup: func(r *personalZaloChatbotRequest) func() { r.ThreadID = r.AccountExternalID; return nil }},
		{name: "legacy opt-in off", signingSecret: secret, want: http.StatusNoContent, setup: func(_ *personalZaloChatbotRequest) func() {
			channel.PersonalZaloAutoReplyEnabled = false
			return func() { channel.PersonalZaloAutoReplyEnabled = true }
		}},
		{name: "group-only channel", signingSecret: secret, want: http.StatusNoContent, setup: func(_ *personalZaloChatbotRequest) func() {
			channel.Metadata = `{"sync_scope":"group"}`
			return func() { channel.Metadata = "" }
		}},
		{name: "group replies default off", signingSecret: secret, want: http.StatusNoContent, setup: func(req *personalZaloChatbotRequest) func() {
			req.SchemaVersion, req.ThreadType, req.SenderID, req.Text = "personal-zalo-chatbot-v2", "group", "789", "Bot ơi"
			return nil
		}},
		{name: "group with direct sync scope", signingSecret: secret, want: http.StatusNoContent, setup: func(req *personalZaloChatbotRequest) func() {
			req.SchemaVersion, req.ThreadType, req.SenderID, req.Text = "personal-zalo-chatbot-v2", "group", "789", "Bot ơi"
			channel.Metadata = `{"personal_zalo_group_reply_enabled":true,"sync_scope":"direct"}`
			return func() { channel.Metadata = "" }
		}},
		{name: "ambient group talk", signingSecret: secret, want: http.StatusNoContent, setup: func(req *personalZaloChatbotRequest) func() {
			req.SchemaVersion, req.ThreadType, req.SenderID, req.Text = "personal-zalo-chatbot-v2", "group", "789", "Cho tôi gặp nhân viên"
			channel.Metadata = `{"personal_zalo_group_reply_enabled":true}`
			return func() { channel.Metadata = "" }
		}},
		{name: "master disabled", signingSecret: secret, want: http.StatusNoContent, setup: func(_ *personalZaloChatbotRequest) func() {
			masterEnabled = false
			return func() { masterEnabled = true }
		}},
		{name: "missing public flow", signingSecret: secret, want: http.StatusNoContent, setup: func(_ *personalZaloChatbotRequest) func() {
			publicFlow = ""
			return func() { publicFlow = "public-flow" }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := fresh
			req.RequestID = tc.name
			if tc.setup != nil {
				if cleanup := tc.setup(&req); cleanup != nil {
					defer cleanup()
				}
			}
			if response := submit(req, tc.signingSecret, gatewaySecret); response.Code != tc.want {
				t.Fatalf("status=%d, want %d, body=%s", response.Code, tc.want, response.Body.String())
			}
		})
	}
	if calls := flowCalls.Load(); calls != 0 {
		t.Fatalf("ineligible requests called Langflow %d times", calls)
	}
	fresh.RequestID = "valid-turn"
	if response := submit(fresh, secret, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("channel import secret alone returned %d", response.Code)
	}
	if response := submit(fresh, secret, gatewaySecret); response.Code != http.StatusOK || response.Body.String() != `{"reply":"Chào bạn!"}` {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response := submit(fresh, secret, gatewaySecret); response.Code != http.StatusConflict || flowCalls.Load() != 1 {
		t.Fatalf("replay status=%d, Langflow calls=%d", response.Code, flowCalls.Load())
	}
	pauseOnNextTurn.Store(true)
	fresh.RequestID, fresh.MessageID = "paused-turn", "msg-2"
	if response := submit(fresh, secret, gatewaySecret); response.Code != http.StatusNoContent || flowCalls.Load() != 2 {
		t.Fatalf("paused status=%d, Langflow calls=%d", response.Code, flowCalls.Load())
	}

	pauseDuringFlow.Store(false)
	tenantCredentials.Store(true)
	fresh.RequestID, fresh.MessageID = "public-image", "msg-3"
	response := submit(fresh, secret, gatewaySecret)
	var output struct {
		Reply  string                       `json:"reply"`
		Images []personalZaloKnowledgeImage `json:"images"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &output); err != nil || response.Code != http.StatusOK ||
		output.Reply != "Chào bạn!" || len(output.Images) != 1 {
		t.Fatalf("public image response status=%d body=%s err=%v", response.Code, response.Body.String(), err)
	}
	attachment := output.Images[0]
	if attachment.ID != imageID || attachment.Width != 3 || attachment.Height != 4 ||
		attachment.MediaType != "image/png" || attachment.FileName != "image.png" ||
		!bytes.Equal(attachment.Data, pngData.Bytes()) || imageReads != 2 {
		t.Fatalf("unexpected image metadata or visibility checks: %+v (reads: %d)", attachment, imageReads)
	}

	imagePrivate = true
	fresh.RequestID, fresh.MessageID = "private-image", "msg-4"
	if response := submit(fresh, secret, gatewaySecret); response.Code != http.StatusOK || response.Body.String() != `{"reply":"Chào bạn!"}` {
		t.Fatalf("private image response status=%d body=%s", response.Code, response.Body.String())
	}

	imagePrivate = false
	requestHandover.Store(true)
	channel.Metadata = `{"handover_alert_group_id":"unavailable-group"}`
	fresh.RequestID, fresh.MessageID = "handover-with-image", "msg-5"
	readsBeforeHandover := imageReads
	if response := submit(fresh, secret, gatewaySecret); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), personalZaloHandoverUnavailable) || strings.Contains(response.Body.String(), imageID) ||
		imageReads != readsBeforeHandover {
		t.Fatalf("handover must suppress image. status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestPersonalZaloTextBoundaryCountsUTF16(t *testing.T) {
	text := strings.Repeat("ắ", 1998) + "🙂x"
	fit := text[:personalZaloTextBoundary(text, personalZaloTextLimit)]
	if fit != strings.TrimSuffix(text, "x") {
		t.Fatal("2000 UTF-16 units should include the whole emoji")
	}
	shorter := text[:personalZaloTextBoundary(text, personalZaloTextLimit-1)]
	if shorter != strings.TrimSuffix(text, "🙂x") {
		t.Fatal("a two-unit emoji should never be partially included")
	}
}
