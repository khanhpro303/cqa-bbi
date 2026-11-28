package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
)

func TestPersonalZaloComplaintsMySQL(t *testing.T) {
	redisAddr := os.Getenv("TEST_REDIS_ADDR")
	if redisAddr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(&models.AppSetting{}, &models.CRMGroup{}, &models.ZaloCustomer{},
		&models.ZaloWhitelist{}, &models.PersonalZaloIdentityLink{}, &models.Conversation{}, &models.CustomerCase{},
		&models.CustomerCaseEvent{}, &models.CustomerCaseAlert{}, &models.ERPAuditLog{}, &models.NotificationLog{}, &models.CRMGroupCustomerCode{},
		&models.CRMGroupCustomer{}); err != nil {
		t.Fatal(err)
	}
	previousRedis := db.RedisClient
	db.RedisClient = redis.NewClient(&redis.Options{Addr: redisAddr})
	t.Cleanup(func() { _ = db.RedisClient.Close(); db.RedisClient = previousRedis })
	if err := db.RedisClient.Ping(t.Context()).Err(); err != nil {
		t.Fatal(err)
	}
	const key = stockFixtureSigningKey
	const channelSecret = "fixture-complaints-channel-secret"
	const gatewaySecret = "fixture-complaints-gateway-signing-secret"
	t.Setenv("JWT_SECRET", key)
	t.Setenv("ENCRYPTION_KEY", key)
	t.Setenv("DB_PASSWORD", "fixture")

	tenant := "case-tenant"
	createFacebookOAuthTenant(t, database, tenant)
	credentials, err := pkg.Encrypt([]byte(`{"import_secret":"`+channelSecret+`"}`), key)
	if err != nil {
		t.Fatal(err)
	}
	oaCredentials, err := pkg.Encrypt([]byte(`{"access_token":"fixture-oa-token"}`), key)
	if err != nil {
		t.Fatal(err)
	}
	oa := models.Channel{ID: uuid.NewString(), TenantID: tenant, ChannelType: "zalo_oa", ExternalID: "999",
		Name: "Linked OA", IsActive: true, CredentialsEncrypted: oaCredentials, Metadata: "{}"}
	staff := models.CRMGroup{ID: uuid.NewString(), TenantID: tenant, ChannelID: oa.ID, ZaloGroupID: "654", Name: "Staff alerts"}
	if err := database.Create(&oa).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&staff).Error; err != nil {
		t.Fatal(err)
	}
	ch := models.Channel{ID: uuid.NewString(), TenantID: tenant, ChannelType: "personal_zalo_import", ExternalID: "123",
		Name: "Personal", CredentialsEncrypted: credentials, IsActive: true, PersonalZaloAutoReplyEnabled: true,
		Metadata: fmt.Sprintf(`{"complaint_cases":true,"personal_zalo_group_reply_enabled":true,"group_assistant_mode":true,"handover_alert_group_id":%q}`, staff.ID)}
	if err := database.Create(&ch).Error; err != nil {
		t.Fatal(err)
	}
	verifiedID, verifiedAt := "457", time.Now()
	customer := models.ZaloCustomer{ID: uuid.NewString(), TenantID: tenant, Name: "Verified customer", Status: "approved", CustomerCode: "S041"}
	if err := database.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	link := models.PersonalZaloIdentityLink{ID: uuid.NewString(), TenantID: tenant, ChannelID: ch.ID, AccountExternalID: ch.ExternalID,
		Kind: personalZaloCustomerIdentity, TargetID: customer.ID, SenderID: &verifiedID, VerifiedAt: &verifiedAt, ExpiresAt: verifiedAt}
	if err := database.Create(&link).Error; err != nil {
		t.Fatal(err)
	}
	settings := []models.AppSetting{
		{ID: uuid.NewString(), TenantID: tenant, SettingKey: "ai_engine_langflow_public_flow_id", ValuePlain: "public-flow"},
		{ID: uuid.NewString(), TenantID: tenant, SettingKey: "ai_engine_langflow_token", ValuePlain: "fixture-token"},
	}
	if err := database.Create(&settings).Error; err != nil {
		t.Fatal(err)
	}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() != "openapi.zalo.me" {
			return originalTransport.RoundTrip(r)
		}
		var payload struct {
			Recipient struct {
				GroupID string `json:"group_id"`
			} `json:"recipient"`
		}
		tokens := r.Header["access_token"]
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Recipient.GroupID != staff.ZaloGroupID ||
			len(tokens) != 1 || tokens[0] != "fixture-oa-token" {
			t.Error("case alert did not use the staff group and its linked OA")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"error":0,"data":{"message_id":"fixture-alert"}}`))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	gin.SetMode(gin.TestMode)
	mcp := gin.New()
	mcp.POST("/mcp/:tenantId", ERPMCP)
	flow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/responses" {
			_, _ = io.WriteString(w, `{"output_text":"{\"label\":\"HANDOVER\"}"}`)
			return
		}
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var data struct {
			Tweaks map[string]json.RawMessage `json:"tweaks"`
		}
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			t.Error(err)
		}
		promptFound := false
		for _, tweak := range data.Tweaks {
			if strings.Contains(string(tweak), complaintMCPToolName) && strings.Contains(string(tweak), "system_prompt") {
				promptFound = true
			}
		}
		if !promptFound {
			t.Error("Langflow did not get the complaint guide")
		}
		permission := r.Header.Get("X-Langflow-Global-Var-ERP_PERMISSION_TOKEN")
		p, err := engine.VerifyPermissionToken(permission, key)
		if err != nil || !p.PersonalZalo || !p.Complaints || p.ChannelID != ch.ID || p.ComplaintTurnID == "" || len(p.Groups) != 0 {
			t.Error("Langflow did not get a scoped complaint grant")
		}
		callTool := func(name string) string {
			body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":{"title":"Kính giao bị nứt","detail":"Kiểm tra đơn giao kính nứt","type":"product_defect"}}}`
			req := httptest.NewRequest(http.MethodPost, "/mcp/"+tenant, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Agent-Token", r.Header.Get("X-Langflow-Global-Var-CQA_ERP_AGENT_TOKEN"))
			req.Header.Set("X-Permission-Token", permission)
			res := httptest.NewRecorder()
			mcp.ServeHTTP(res, req)
			return res.Body.String()
		}
		if out := callTool("lookup_order"); !strings.Contains(out, "Tool unavailable in knowledge preview") {
			t.Errorf("complaint grant allowed an ERP lookup: %s", out)
		}
		if out := callTool(complaintMCPToolName); !strings.Contains(out, `"isError":false`) {
			t.Errorf("complaint tool rejected the signed turn: %s", out)
		} else if p != nil && p.ZaloGroupID != "" && (!strings.Contains(out, `\"action\":\"recorded\"`) || strings.Contains(out, `\"case\"`)) {
			t.Errorf("group tool result exposed case details: %s", out)
		}
		_, _ = fmt.Fprint(w, `{"outputs":[{"outputs":[{"results":{"message":{"text":"Dạ đã ghi nhận. Mã ticket: #66656"}}}]}]}`)
	}))
	defer flow.Close()
	classifierSettings := []models.AppSetting{
		{ID: uuid.NewString(), TenantID: tenant, SettingKey: "ai_provider", ValuePlain: "openai"},
		{ID: uuid.NewString(), TenantID: tenant, SettingKey: "ai_api_key", ValuePlain: "fixture-classifier-key"},
		{ID: uuid.NewString(), TenantID: tenant, SettingKey: "ai_base_url", ValuePlain: flow.URL},
	}
	if err := database.Create(&classifierSettings).Error; err != nil {
		t.Fatal(err)
	}

	const path = "/api/internal/chatbot/personal-zalo"
	router := gin.New()
	router.POST(path, middleware.InternalHMACAuth(middleware.PersonalZaloChatbotSecretResolver(key, gatewaySecret), 5*time.Minute),
		NewPersonalZaloChatbotHandler(&config.Config{EncryptionKey: key, LangflowAPIURL: flow.URL, LangflowFlowID: "private-flow"}))
	for _, tc := range []struct {
		name         string
		customerCode string
		turn         personalZaloChatbotRequest
	}{
		{name: "direct guest", turn: personalZaloChatbotRequest{SchemaVersion: personalZaloDirectProtocol, ThreadType: personalZaloThreadUser, SenderID: "456", ThreadID: "456"}},
		{name: "direct verified", customerCode: customer.CustomerCode, turn: personalZaloChatbotRequest{SchemaVersion: personalZaloDirectProtocol, ThreadType: personalZaloThreadUser, SenderID: verifiedID, ThreadID: verifiedID}},
		{name: "group mention", turn: personalZaloChatbotRequest{SchemaVersion: "personal-zalo-chatbot-v2", ThreadType: personalZaloThreadGroup, SenderID: "789", ThreadID: "987", BotMentioned: true}},
		{name: "group assistant without mention", turn: personalZaloChatbotRequest{SchemaVersion: "personal-zalo-chatbot-v2", ThreadType: personalZaloThreadGroup, SenderID: "790", ThreadID: "987"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			turn := tc.turn
			turn.TenantID, turn.ChannelID, turn.AccountExternalID = tenant, ch.ID, ch.ExternalID
			turn.SentAt, turn.MessageID, turn.Text = time.Now(), "same-message-id", "Kính vừa giao bị nứt rồi em"
			for range 2 {
				turn.RequestID = uuid.NewString()
				body, err := json.Marshal(turn)
				if err != nil {
					t.Fatal(err)
				}
				timestamp := fmt.Sprint(time.Now().Unix())
				sign := func(secret string) string {
					mac := hmac.New(sha256.New, []byte(secret))
					fmt.Fprintf(mac, "%s.%s.", timestamp, turn.RequestID)
					mac.Write(body)
					return hex.EncodeToString(mac.Sum(nil))
				}
				req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-CQA-Request-Id", turn.RequestID)
				req.Header.Set("X-CQA-Timestamp", timestamp)
				req.Header.Set("X-CQA-Signature", sign(channelSecret))
				req.Header.Set("X-CQA-Chatbot-Signature", sign(gatewaySecret))
				res := httptest.NewRecorder()
				router.ServeHTTP(res, req)
				if res.Code != http.StatusOK || strings.Contains(res.Body.String(), "66656") || !strings.Contains(res.Body.String(), "Dạ đã ghi nhận.") {
					t.Fatalf("reply status=%d body=%s", res.Code, res.Body.String())
				}
			}
			var cases []models.CustomerCase
			if err := database.Where("tenant_id = ? AND zalo_user_id = ?", tenant, turn.SenderID).Find(&cases).Error; err != nil {
				t.Fatal(err)
			}
			groupID := ""
			if turn.ThreadType == personalZaloThreadGroup {
				groupID = turn.ThreadID
			}
			if len(cases) != 1 || cases[0].ChannelID != ch.ID || cases[0].SubjectType != "user" ||
				cases[0].SubjectID != turn.SenderID || cases[0].ZaloGroupID != groupID || cases[0].FollowupCount != 0 ||
				cases[0].CustomerCode != tc.customerCode {
				t.Fatalf("wrong or duplicate cases: %+v", cases)
			}
		})
	}
	var alerts int64
	if err := database.Model(&models.NotificationLog{}).Where("tenant_id = ? AND recipient = ? AND status = ?", tenant, staff.ID, "sent").Count(&alerts).Error; err != nil {
		t.Fatal(err)
	}
	if alerts != 4 {
		t.Fatalf("expected one staff alert per complaint despite retries, got %d", alerts)
	}
	var history int64
	if err := database.Model(&models.CustomerCaseAlert{}).Where("tenant_id = ? AND group_id = ? AND group_name = ? AND status = ?", tenant, staff.ID, staff.Name, "sent").Count(&history).Error; err != nil || history != 4 {
		t.Fatalf("expected four linked alert outcomes, got %d (%v)", history, err)
	}
}
