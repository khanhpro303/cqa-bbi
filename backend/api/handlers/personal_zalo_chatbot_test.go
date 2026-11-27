package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestPersonalZaloChatbotSignedTurn(t *testing.T) {
	const key = "0123456789abcdef0123456789abcdef"
	const secret = "test-personal-zalo-channel-secret"
	const gatewaySecret = "test-deployment-gateway-signing-secret"
	const imageID = "cea6c2f1-40bc-4c42-a256-e2f676d24e9d"
	const gobyQuestion = "Bull ơi, cho tôi thông tin giày goby với"
	const gobyAnswer = "Dạ, LS2 Goby Man có 3 điểm nổi bật ạ.\n• CE EN 13634:2017, mức 2|2|2|2 WR.\n• Cổ cao bảo vệ mắt cá, gót và ngón chân.\n• Da bò và chi tiết phản quang."
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
	oaLinkedGroup := false
	publicFlow := "public-flow"
	var pauseDuringFlow atomic.Bool
	var denyApproval atomic.Bool
	var tenantCredentials atomic.Bool
	var requestHandover atomic.Bool
	var staffVerified atomic.Bool
	var customerVerified atomic.Bool
	var customerERP atomic.Bool
	var erpPaused atomic.Bool
	var disableERPDuringNextTurn atomic.Bool
	var revokeOnNextTurn atomic.Bool
	customerVerified.Store(true)
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
		case *models.PersonalZaloThreadApproval:
			if !slices.Contains(q.Statement.Vars, any(channel.ID)) ||
				!slices.Contains(q.Statement.Vars, any(channel.ExternalID)) ||
				!slices.Contains(q.Statement.Vars, any(personalZaloApproved)) {
				t.Error("approval lookup must pin the channel, account and approved status")
			}
			if denyApproval.Load() {
				q.AddError(gorm.ErrRecordNotFound)
			}
		case *[]models.AppSetting:
			if !slices.Contains(q.Statement.Vars, any(channel.TenantID)) {
				t.Error("settings query did not scope to tenant")
			}
			*row = []models.AppSetting{
				{SettingKey: "chatbot_active", ValuePlain: strconv.FormatBool(masterEnabled)},
				{SettingKey: "ai_engine_langflow_public_flow_id", ValuePlain: publicFlow},
			}
			for _, flag := range []string{"erp_integration_active", "erp_private_active"} {
				if slices.Contains(q.Statement.Vars, any(flag)) {
					*row = append(*row, models.AppSetting{SettingKey: flag, ValuePlain: strconv.FormatBool(!erpPaused.Load())})
				}
			}
			if tenantCredentials.Load() {
				*row = append(*row, models.AppSetting{SettingKey: "ai_engine_langflow_token", ValuePlain: "fixture-langflow-key"})
			}
			if requestHandover.Load() {
				*row = append(*row, models.AppSetting{SettingKey: "ai_engine_system_prompt", ValuePlain: "Hỗ trợ khách hàng."})
			}
			if staffVerified.Load() {
				*row = append(*row,
					models.AppSetting{SettingKey: "ai_agent_erp_token_private", ValuePlain: "fixture-private-erp"},
					models.AppSetting{SettingKey: "ai_engine_system_prompt_internal", ValuePlain: "Hỗ trợ nhân viên."},
				)
			}
			if customerERP.Load() {
				*row = append(*row,
					models.AppSetting{SettingKey: "ai_agent_erp_token_public", ValuePlain: "fixture-public-erp"},
					models.AppSetting{SettingKey: "ai_engine_system_prompt", ValuePlain: "Hỗ trợ khách hàng."},
				)
			}
		case *models.ZaloWhitelist:
			if !slices.Contains(q.Statement.Vars, any(channel.ID)) {
				t.Error("staff lookup must pin the personal channel")
			}
			if !staffVerified.Load() {
				q.AddError(gorm.ErrRecordNotFound)
				return
			}
			*row = models.ZaloWhitelist{ID: "staff", TenantID: channel.TenantID, ChannelID: channel.ID, ZaloUserID: "456", Status: "active"}
		case *[]models.PersonalZaloIdentityLink:
			if !customerVerified.Load() {
				return
			}
			sender, now := "456", time.Now()
			*row = []models.PersonalZaloIdentityLink{{ID: "link", Kind: "customer", TargetID: "customer", SenderID: &sender, VerifiedAt: &now}}
		case *models.ZaloCustomer:
			*row = models.ZaloCustomer{ID: "customer", TenantID: channel.TenantID, Status: "approved", CustomerCode: "S001"}
		case *[]models.CRMGroup:
			*row = []models.CRMGroup{{ID: "customer-group", Name: "Test customers"}}
		case *[]string:
			if oaLinkedGroup && strings.Contains(q.Statement.SQL.String(), "crm_groups.zalo_group_id") {
				*row = []string{"oa-linked-group"}
			}
		case *[]models.ERPEndpoint:
			ep := models.ERPEndpoint{Resource: "orders", ScopeType: "all", IsEnabled: true, GroupID: "customer-group"}
			if staffVerified.Load() {
				ep.GroupID = "private_bot"
			}
			*row = []models.ERPEndpoint{ep}
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
	var sessionDuringERP atomic.Value
	var pauseOnNextTurn atomic.Bool
	flow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = fmt.Fprint(w, `{"data":{"nodes":[{"id":"Agent-fixture","data":{"node":{"template":{"system_prompt":{}}}}}]}}`)
			return
		}
		flowCalls.Add(1)
		if pauseOnNextTurn.Swap(false) {
			pauseDuringFlow.Store(true)
		}
		staff := staffVerified.Load()
		if revokeOnNextTurn.Swap(false) {
			staffVerified.Store(false)
			customerVerified.Store(false)
		}
		var input struct {
			Text      string         `json:"input_value"`
			SessionID string         `json:"session_id"`
			Tweaks    map[string]any `json:"tweaks"`
		}
		wantFlow := "/api/v1/run/public-flow"
		if staff {
			wantFlow = "/api/v1/run/private-flow"
		}
		wantSessionPrefix := "personal-zalo-"
		guest := !staff && !customerVerified.Load()
		if guest {
			wantSessionPrefix = "zalo-guest-"
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || r.URL.Path != wantFlow ||
			!strings.HasPrefix(input.SessionID, wantSessionPrefix) || input.Tweaks["zalo_user_id"] != input.SessionID {
			t.Errorf("unexpected Langflow request: %s (want %s, decode err=%v)", r.URL.Path, wantFlow, err)
		}
		if r.Header.Get("X-API-Key") != "fixture-langflow-key" {
			t.Error("Langflow credentials were not passed")
		}
		reply := "Chào bạn!"
		token := r.Header.Get("X-LANGFLOW-GLOBAL-VAR-CQA_ERP_AGENT_TOKEN")
		if guest {
			tweak, _ := input.Tweaks["Agent-fixture"].(map[string]any)
			prompt, _ := tweak["system_prompt"].(string)
			if resolveKnowledgePreviewAgent(key, channel.TenantID, token) != "public" ||
				r.Header.Get("X-LANGFLOW-GLOBAL-VAR-ERP_PERMISSION_TOKEN") != "" ||
				input.Tweaks["permission_token"] != nil || input.Tweaks["customer_code"] != nil ||
				input.Text != gobyQuestion || !strings.Contains(prompt, "Khách trong phiên này chưa xác thực") ||
				!strings.Contains(prompt, "3–4 gạch đầu dòng thật ngắn") {
				t.Error("guest flow did not receive only the public knowledge credential and guest prompt")
			}
			reply = gobyAnswer
		} else if (staff || customerERP.Load()) && !erpPaused.Load() {
			sessionDuringERP.Store(input.SessionID)
			if disableERPDuringNextTurn.Swap(false) {
				erpPaused.Store(true)
			}
			signed, _ := input.Tweaks["permission_token"].(string)
			permissions, err := engine.VerifyPermissionToken(signed, key)
			wantAgent, wantScope := "public", "own"
			if staff {
				wantAgent, wantScope = "private", "all"
			}
			tweak, _ := input.Tweaks["Agent-fixture"].(map[string]any)
			prompt, _ := tweak["system_prompt"].(string)
			wantPrompt := "Hỗ trợ khách hàng."
			if staff {
				wantPrompt = "Hỗ trợ nhân viên."
			}
			if !strings.HasPrefix(prompt, wantPrompt) || !strings.Contains(prompt, "chào ngắn gọn") ||
				!strings.Contains(prompt, "3–4 gạch đầu dòng thật ngắn") {
				t.Errorf("%s flow received the wrong prompt", wantAgent)
			}
			if err != nil || permissions == nil || !permissions.PersonalZalo || permissions.AgentType != wantAgent ||
				permissions.TenantID != channel.TenantID || permissions.ChannelID != channel.ID || permissions.ZaloUserID != "456" ||
				permissions.PersonalZaloSessionID != input.SessionID ||
				!strings.HasPrefix(permissions.TurnID, "personal-zalo-erp:") || token != "fixture-"+wantAgent+"-erp" {
				t.Errorf("Langflow did not receive the signed %s personal ERP grant (err=%v)", wantAgent, err)
			} else if allowed, scope, _ := permissions.IsResourceAllowed("orders"); !allowed || scope != wantScope ||
				(!staff && !slices.Equal(permissions.CustomerCodes, []string{"S001"})) {
				t.Errorf("orders permission has scope %q (allowed=%t), want %q", scope, allowed, wantScope)
			}
		} else if input.Tweaks["permission_token"] != nil || input.Tweaks["customer_code"] != nil {
			t.Error("knowledge-only turn must have no ERP permission or customer code")
		} else if erpPaused.Load() {
			wantRole := "public"
			if staff {
				wantRole = "private"
			}
			if sessionDuringERP.Load() == input.SessionID || resolveKnowledgePreviewAgent(key, channel.TenantID, token) != wantRole {
				t.Error("ERP-disabled conversation reused ERP memory or credentials")
			}
		} else if tenantCredentials.Load() {
			if resolveKnowledgePreviewAgent(key, channel.TenantID, token) != "public" {
				t.Error("Langflow did not receive a public knowledge-only token")
			}
			if requestHandover.Load() {
				reply = personalZaloAIHandoverMarker
			} else if !strings.HasPrefix(input.Text, "Tra giúp sản phẩm\n\n") || !strings.Contains(input.Text, "search_knowledge") {
				t.Error("Langflow did not receive the knowledge-image instructions")
			}
			reply += "\n[KB_IMAGE:" + imageID + "]"
		} else if token != "" || input.Text != "Tra giúp sản phẩm" {
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
		SchemaVersion: personalZaloDirectProtocol, TenantID: channel.TenantID, ChannelID: channel.ID,
		AccountExternalID: "123", ThreadID: "456", MessageID: "msg-1", Text: " Tra giúp sản phẩm ", SentAt: time.Now(),
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
		{name: "legacy direct gateway cannot deliver protected replies", signingSecret: secret, want: http.StatusNoContent, setup: func(r *personalZaloChatbotRequest) func() { r.SchemaVersion = "personal-zalo-chatbot-v1"; return nil }},
		{name: "missing signature", want: http.StatusUnauthorized},
		{name: "wrong channel secret", signingSecret: "other-secret", want: http.StatusUnauthorized},
		{name: "different tenant", signingSecret: secret, want: http.StatusUnauthorized, setup: func(r *personalZaloChatbotRequest) func() { r.TenantID = "other-tenant"; return nil }},
		{name: "different account", signingSecret: secret, want: http.StatusNoContent, setup: func(r *personalZaloChatbotRequest) func() { r.AccountExternalID = "900"; return nil }},
		{name: "direct not approved", signingSecret: secret, want: http.StatusNoContent, setup: func(r *personalZaloChatbotRequest) func() {
			denyApproval.Store(true)
			return func() { denyApproval.Store(false) }
		}},
		{name: "mentioned group not approved", signingSecret: secret, want: http.StatusNoContent, setup: func(r *personalZaloChatbotRequest) func() {
			r.SchemaVersion, r.ThreadType, r.SenderID, r.BotMentioned = "personal-zalo-chatbot-v2", "group", "789", true
			channel.Metadata = `{"personal_zalo_group_reply_enabled":true}`
			denyApproval.Store(true)
			return func() { channel.Metadata = ""; denyApproval.Store(false) }
		}},
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
			req.SchemaVersion, req.ThreadType, req.SenderID, req.Text, req.BotMentioned = "personal-zalo-chatbot-v2", "group", "789", "", true
			return nil
		}},
		{name: "personal tag in OA-linked group", signingSecret: secret, want: http.StatusNoContent, setup: func(req *personalZaloChatbotRequest) func() {
			req.SchemaVersion, req.ThreadType, req.SenderID, req.Text, req.BotMentioned = "personal-zalo-chatbot-v2", "group", "789", "", true
			channel.Metadata = `{"personal_zalo_group_reply_enabled":true}`
			oaLinkedGroup = true
			return func() { channel.Metadata, oaLinkedGroup = "", false }
		}},
		{name: "direct chat cannot carry a group tag", signingSecret: secret, want: http.StatusNoContent, setup: func(req *personalZaloChatbotRequest) func() {
			req.BotMentioned = true
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
		{name: "missing public flow", signingSecret: secret, want: http.StatusOK, setup: func(_ *personalZaloChatbotRequest) func() {
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
	customerVerified.Store(false)
	tenantCredentials.Store(true)
	for i, question := range []string{"Báo giá LS2 FF901", "Còn hàng không?", "Đơn hàng của tôi tới đâu?", "Xem công nợ"} {
		fresh.RequestID, fresh.Text = fmt.Sprintf("guest-erp-%d", i), question
		if response := submit(fresh, secret, gatewaySecret); response.Code != http.StatusOK ||
			!strings.Contains(response.Body.String(), engine.GuestKnowledgeVerificationReply) || flowCalls.Load() != 0 {
			t.Fatalf("guest ERP request %q: status=%d calls=%d body=%s", question, response.Code, flowCalls.Load(), response.Body)
		}
	}
	channel.Metadata = `{"session_keyword":" ; \u00a0 ","session_timeout_minutes":7}`
	fresh.RequestID, fresh.Text = "guest-goby-knowledge", gobyQuestion
	guestResponse := submit(fresh, secret, gatewaySecret)
	var guestReply struct{ Reply string }
	if err := json.Unmarshal(guestResponse.Body.Bytes(), &guestReply); err != nil ||
		guestResponse.Code != http.StatusOK || guestReply.Reply != gobyAnswer ||
		!strings.Contains(guestResponse.Body.String(), "delivery_token") || flowCalls.Load() != 1 {
		t.Fatalf("guest knowledge: status=%d calls=%d body=%s", guestResponse.Code, flowCalls.Load(), guestResponse.Body)
	}
	channel.Metadata = ""
	tenantCredentials.Store(false)
	customerVerified.Store(true)
	fresh.RequestID, fresh.Text = "public-greeting", "Hello"
	if response := submit(fresh, secret, gatewaySecret); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), personalZaloDirectGreeting("public")) || flowCalls.Load() != 1 {
		t.Fatalf("a greeting must receive the public welcome without calling Langflow (status=%d, calls=%d)", response.Code, flowCalls.Load())
	}
	fresh.Text = "Tra giúp sản phẩm"
	fresh.RequestID = "valid-turn"
	if response := submit(fresh, secret, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("channel import secret alone returned %d", response.Code)
	}
	if response := submit(fresh, secret, gatewaySecret); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"reply":"Chào bạn!"`) || strings.Contains(response.Body.String(), `"images"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response := submit(fresh, secret, gatewaySecret); response.Code != http.StatusConflict || flowCalls.Load() != 2 {
		t.Fatalf("replay status=%d, Langflow calls=%d", response.Code, flowCalls.Load())
	}
	pauseOnNextTurn.Store(true)
	fresh.RequestID, fresh.MessageID = "paused-turn", "msg-2"
	if response := submit(fresh, secret, gatewaySecret); response.Code != http.StatusNoContent || flowCalls.Load() != 3 {
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

	// Simulate text and image leaving the durable gateway queue at different times.
	var queued struct {
		Token string `json:"delivery_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &queued); err != nil || queued.Token == "" {
		t.Fatal("protected reply missing delivery token")
	}
	pending := fresh
	pending.DeliveryToken = queued.Token
	pending.DeliveryPart = "text"
	pending.DeliveryDigest = personalZaloIdentityDigest(output.Reply)
	checkSend := func(request personalZaloChatbotRequest, id string, want int) {
		t.Helper()
		request.RequestID = id
		calls := flowCalls.Load()
		got := submit(request, secret, gatewaySecret)
		if got.Code != want || (want == http.StatusOK && got.Body.String() != `{"allowed":true}`) || flowCalls.Load() != calls {
			t.Fatalf("%s returned %d (expected %d), flow calls %d -> %d", id, got.Code, want, calls, flowCalls.Load())
		}
	}
	checkSend(pending, "queue-text", http.StatusOK)
	wrong := pending
	wrong.MessageID += "-other"
	checkSend(wrong, "queue-other-turn", http.StatusNoContent)
	wrong = pending
	wrong.DeliveryDigest = personalZaloIdentityDigest("other content")
	checkSend(wrong, "queue-other-content", http.StatusNoContent)
	customerVerified.Store(false)
	checkSend(pending, "queue-revoked", http.StatusNoContent)
	customerVerified.Store(true)
	masterEnabled = false
	checkSend(pending, "queue-disabled", http.StatusNoContent)
	masterEnabled = true
	pending.DeliveryPart = "image"
	pending.DeliveryDigest = personalZaloIdentityDigest(string(attachment.Data))
	checkSend(pending, "queue-image", http.StatusOK)
	imagePrivate = true
	checkSend(pending, "queue-image-now-private", http.StatusNoContent)
	fresh.RequestID, fresh.MessageID = "private-image", "msg-4"
	if response := submit(fresh, secret, gatewaySecret); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"reply":"Chào bạn!"`) || strings.Contains(response.Body.String(), `"images"`) {
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

	requestHandover.Store(false)
	customerERP.Store(true)
	fresh.RequestID = "verified-customer-erp"
	response = submit(fresh, secret, gatewaySecret)
	if response.Code != http.StatusOK {
		t.Fatalf("verified customer turn should use public ERP: %s", response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &queued); err != nil {
		t.Fatal(err)
	}
	pending = fresh
	pending.DeliveryToken, pending.DeliveryPart, pending.DeliveryDigest = queued.Token, "text", personalZaloIdentityDigest("Chào bạn!")
	checkSend(pending, "queue-erp-active", http.StatusOK)
	erpPaused.Store(true)
	checkSend(pending, "queue-public-integration-paused", http.StatusNoContent)
	fresh.RequestID = "customer-erp-paused"
	if res := submit(fresh, secret, gatewaySecret); res.Code != http.StatusOK {
		t.Errorf("knowledge conversation with ERP disabled: %d %s", res.Code, res.Body)
	}
	erpPaused.Store(false)
	checkSend(pending, "queue-erp-reenabled", http.StatusOK)
	customerERP.Store(false)
	checkSend(pending, "queue-erp-token-disabled", http.StatusNoContent)
	staffVerified.Store(true)
	fresh.RequestID = "verified-staff-erp"
	if response := submit(fresh, secret, gatewaySecret); response.Code != http.StatusOK {
		t.Fatalf("verified staff turn should use private ERP: %s", response.Body.String())
	}
	disableERPDuringNextTurn.Store(true)
	fresh.RequestID = "pause-staff-erp-during-flow"
	if res := submit(fresh, secret, gatewaySecret); res.Code != http.StatusNoContent {
		t.Errorf("in-flight response after disabling ERP: %d %s", res.Code, res.Body)
	}
	erpPaused.Store(false)
	revokeOnNextTurn.Store(true)
	fresh.RequestID = "revoked-staff-erp"
	if response := submit(fresh, secret, gatewaySecret); response.Code != http.StatusNoContent {
		t.Fatalf("an in-flight turn may not deliver after the sender loses access: %s", response.Body.String())
	}

	t.Run("configured session gates direct replies", func(t *testing.T) {
		addr := os.Getenv("TEST_REDIS_ADDR")
		if addr == "" {
			t.Skip("TEST_REDIS_ADDR not set")
		}
		client := redis.NewClient(&redis.Options{Addr: addr})
		if err := client.Ping(t.Context()).Err(); err != nil {
			t.Fatal(err)
		}
		originalRedis := db.RedisClient
		db.RedisClient = client
		t.Cleanup(func() { db.RedisClient = originalRedis; _ = client.Close() })
		customerVerified.Store(false)
		staffVerified.Store(false)
		req := fresh
		req.ThreadType, req.SenderID = personalZaloThreadUser, req.ThreadID
		sessionKey := personalZaloSessionKey(req)
		t.Cleanup(func() { _ = client.Del(context.WithoutCancel(t.Context()), sessionKey).Err() })

		send := func(id, message string, status int, reply string, flowCallsAdded int32) {
			t.Helper()
			req.RequestID, req.MessageID, req.Text = "direct-session-"+id, id, message
			calls := flowCalls.Load()
			response := submit(req, secret, gatewaySecret)
			if response.Code != status || flowCalls.Load()-calls != flowCallsAdded {
				t.Fatalf("%s: status=%d body=%s, flow calls=%d", id, response.Code, response.Body, flowCalls.Load()-calls)
			}
			if status == http.StatusOK {
				var output struct {
					Reply string `json:"reply"`
					Token string `json:"delivery_token"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &output); err != nil || output.Reply != reply || output.Token == "" {
					t.Fatalf("%s: unexpected reply or delivery token: %s", id, response.Body)
				}
			}
		}

		const handoverRequest = "Cho tôi gặp nhân viên"
		for _, sender := range []struct {
			name            string
			customer, staff bool
		}{
			{name: "guest"},
			{name: "customer", customer: true},
			{name: "staff", staff: true},
		} {
			customerVerified.Store(sender.customer)
			staffVerified.Store(sender.staff)
			channel.Metadata = `{"session_keyword":"chào bull","handover_alert_group_id":"unavailable-group"}`
			send(sender.name+"-handover-before-opening", handoverRequest, http.StatusNoContent, "", 0)
			channel.Metadata = `{"session_keyword":"chào bull","complaint_cases":true}`
			send(sender.name+"-complaint-before-opening", handoverRequest, http.StatusNoContent, "", 0)
		}
		customerVerified.Store(false)
		staffVerified.Store(false)
		channel.Metadata = `{"session_keyword":"chào bull; bắt đầu","session_end_keyword":"kết thúc","session_welcome_message":"Mời anh/chị đặt câu hỏi","session_goodbye_message":"Hẹn gặp lại"}`
		send("partial-keyword", "chào bull, tư vấn giúp em", http.StatusNoContent, "", 0)
		send("verify-before-opening", "verify", http.StatusOK, personalZaloInvalidPairingReply, 0)
		send("open", " CHÀO BULL ", http.StatusOK, "Mời anh/chị đặt câu hỏi", 0)
		send("follow-up", gobyQuestion, http.StatusOK, gobyAnswer, 1)
		send("close", "kết thúc", http.StatusOK, "Hẹn gặp lại", 0)
		send("closed-follow-up", gobyQuestion, http.StatusNoContent, "", 0)
		send("reopen", "bắt đầu", http.StatusOK, "Mời anh/chị đặt câu hỏi", 0)
		if err := client.Expire(t.Context(), sessionKey, -time.Second).Err(); err != nil {
			t.Fatal(err)
		}
		send("expired-follow-up", gobyQuestion, http.StatusNoContent, "", 0)

		customerVerified.Store(true)
		channel.Metadata = `{"session_keyword":"chào bull; nói chuyện với nhân viên","session_welcome_message":"Mời anh/chị đặt câu hỏi","handover_alert_group_id":"unavailable-group"}`
		send("open-with-handover-phrase", "nói chuyện với nhân viên", http.StatusOK, "Mời anh/chị đặt câu hỏi", 0)
		send("handover-in-open-session", handoverRequest, http.StatusOK, personalZaloHandoverUnavailable, 0)
		send("handover-after-session-closed", handoverRequest, http.StatusNoContent, "", 0)
	})
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
