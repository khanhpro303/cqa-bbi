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
		&models.ZaloWhitelist{}, &models.PersonalZaloIdentityLink{}, &models.PersonalZaloThreadApproval{}, &models.Conversation{}, &models.CustomerCase{},
		&models.CustomerCaseEvent{}, &models.CustomerCaseAlert{}, &models.ERPAuditLog{}, &models.NotificationLog{}, &models.CRMGroupCustomerCode{},
		&models.CRMGroupCustomer{}, &models.CachedCustomer{}); err != nil {
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
	const staffSender, customerGroup, fallbackGroup, missingMappingGroup = "791", "988", "989", "990"
	const staffName, groupCustomerCode, groupCustomerName = "Nhân viên Ánh", "S042", "Khách của nhóm"
	const manualOverrideCode, fallbackCode, ambiguousCode = "S043", "S044", "S099"
	staffUser := models.User{ID: uuid.NewString(), Email: "staff-complaints@example.test", Name: staffName, PasswordHash: "fixture"}
	groupIdentity := models.CRMGroup{ID: uuid.NewString(), TenantID: tenant, ChannelID: ch.ID,
		ZaloGroupID: customerGroup, Name: "Nhóm khách kiểm thử", CustomerCode: groupCustomerCode}
	groupConversation := models.Conversation{ID: uuid.NewString(), TenantID: tenant, ChannelID: ch.ID,
		ExternalConversationID: composeImportConversationID(personalZaloThreadGroup, customerGroup), CustomerName: "Nhóm CRM", Metadata: "{}"}
	fallbackConversation := models.Conversation{ID: uuid.NewString(), TenantID: tenant, ChannelID: ch.ID,
		ExternalConversationID: composeImportConversationID(personalZaloThreadGroup, fallbackGroup), CustomerName: "Nhóm fallback", Metadata: "{}"}
	ambiguousConversation := models.Conversation{ID: uuid.NewString(), TenantID: tenant, ChannelID: ch.ID,
		ExternalConversationID: composeImportConversationID(personalZaloThreadGroup, "ambiguous"), CustomerName: "Nhóm ambiguous", Metadata: "{}"}
	foreignChannelConversation := models.Conversation{ID: uuid.NewString(), TenantID: tenant, ChannelID: oa.ID,
		ExternalConversationID: composeImportConversationID(personalZaloThreadGroup, "foreign"), CustomerName: "Nhóm OA", Metadata: "{}"}
	for _, row := range []any{
		&staffUser, &groupIdentity, &groupConversation, &fallbackConversation, &ambiguousConversation, &foreignChannelConversation,
		&models.CachedCustomer{ID: uuid.NewString(), TenantID: tenant, MA: groupCustomerCode, HO_VA_TEN: groupCustomerName},
		&models.CachedCustomer{ID: uuid.NewString(), TenantID: tenant, MA: manualOverrideCode, HO_VA_TEN: "Khách manual không được ưu tiên"},
		&models.CachedCustomer{ID: uuid.NewString(), TenantID: tenant, MA: fallbackCode, HO_VA_TEN: "Khách fallback"},
		&models.CachedCustomer{ID: uuid.NewString(), TenantID: tenant, MA: ambiguousCode, HO_VA_TEN: "Hồ sơ một", ERP_ID: 1},
		&models.CachedCustomer{ID: uuid.NewString(), TenantID: tenant, MA: ambiguousCode, HO_VA_TEN: "Hồ sơ hai", ERP_ID: 2},
		&models.CustomerCase{ID: uuid.NewString(), TenantID: tenant, ChannelID: ch.ID, SubjectType: "group",
			SubjectID: customerGroup, ZaloGroupID: customerGroup, ZaloUserID: "old-reporter", CustomerCode: "OLD",
			Status: "open", Title: "Vấn đề của khách trước đây", Images: "[]"},
	} {
		if err := database.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Exec("INSERT INTO user_tenants (tenant_id, user_id) VALUES (?, ?)", tenant, staffUser.ID).Error; err != nil {
		t.Fatal(err)
	}
	metadata, err := decodeMetadataObject(ch.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	metadata[personalZaloAccountOwnersKey] = []map[string]string{{"account_external_id": staffSender, "user_id": staffUser.ID}}
	mappingPatch := fmt.Sprintf(`{"complaint_group_customers":{%q:%q,%q:%q}}`,
		groupConversation.ID, manualOverrideCode, fallbackConversation.ID, fallbackCode)
	if err := validatePersonalZaloComplaintCustomers(t.Context(), ch, mappingPatch, metadata); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		fmt.Sprintf(`{"complaint_group_customers":{%q:%q}}`, foreignChannelConversation.ID, fallbackCode),
		fmt.Sprintf(`{"complaint_group_customers":{%q:%q}}`, ambiguousConversation.ID, ambiguousCode),
	} {
		if err := validatePersonalZaloComplaintCustomers(t.Context(), ch, invalid, map[string]interface{}{}); err == nil {
			t.Fatalf("accepted invalid fallback mapping: %s", invalid)
		}
	}
	saveMetadata := func() {
		t.Helper()
		raw, err := json.Marshal(metadata)
		if err != nil {
			t.Fatal(err)
		}
		ch.Metadata = string(raw)
		if err := database.Model(&ch).Update("metadata", ch.Metadata).Error; err != nil {
			t.Fatal(err)
		}
	}
	saveMetadata()
	approvals := []models.PersonalZaloThreadApproval{
		{ThreadType: personalZaloThreadUser, ThreadID: "456"},
		{ThreadType: personalZaloThreadUser, ThreadID: "457"},
		{ThreadType: personalZaloThreadGroup, ThreadID: "987"},
		{ThreadType: personalZaloThreadGroup, ThreadID: customerGroup},
		{ThreadType: personalZaloThreadGroup, ThreadID: fallbackGroup},
		{ThreadType: personalZaloThreadGroup, ThreadID: missingMappingGroup},
	}
	for i := range approvals {
		approvals[i].ChannelID, approvals[i].AccountExternalID = ch.ID, ch.ExternalID
		approvals[i].Present, approvals[i].Status = true, personalZaloApproved
		approvals[i].FirstSeenAt = time.Now().Add(-time.Minute)
	}
	if err := database.Create(&approvals).Error; err != nil {
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
			input, _ := io.ReadAll(r.Body)
			if strings.Contains(string(input), "Vấn đề của khách trước đây") {
				t.Error("complaint model received a case for the previous customer of this group")
			}
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
		if p == nil {
			return
		}
		isStaff := p.ZaloUserID == staffSender
		if p.ZaloGroupID != "" && p.GroupGuest == isStaff {
			t.Errorf("wrong group complaint grant for staff=%t", isStaff)
		}
		callTool := func(name string) string {
			body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":{"title":"Kính giao bị nứt","detail":"Kiểm tra đơn giao kính nứt","type":"product_defect","customer_code":"UNTRUSTED"}}}`
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
		out := callTool(complaintMCPToolName)
		if p.ZaloGroupID == missingMappingGroup {
			if !strings.Contains(out, "Chưa xác định được mã khách của nhóm") || !strings.Contains(out, `"isError":true`) {
				t.Errorf("missing mapping was not refused: %s", out)
			}
			_, _ = fmt.Fprint(w, `{"outputs":[{"outputs":[{"results":{"message":{"text":"[CQA_REQUEST_HUMAN_REVIEW]"}}}]}]}`)
			return
		}
		if !strings.Contains(out, `"isError":false`) {
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
		customerName string
		staffGroup   bool
		unmapped     bool
		silent       bool
		turn         personalZaloChatbotRequest
	}{
		{name: "direct guest", turn: personalZaloChatbotRequest{SchemaVersion: personalZaloDirectProtocol, ThreadType: personalZaloThreadUser, SenderID: "456", ThreadID: "456"}},
		{name: "direct verified", customerCode: customer.CustomerCode, turn: personalZaloChatbotRequest{SchemaVersion: personalZaloDirectProtocol, ThreadType: personalZaloThreadUser, SenderID: verifiedID, ThreadID: verifiedID}},
		{name: "group mention", turn: personalZaloChatbotRequest{SchemaVersion: "personal-zalo-chatbot-v2", ThreadType: personalZaloThreadGroup, SenderID: "789", ThreadID: "987", BotMentioned: true}},
		{name: "group assistant without mention", turn: personalZaloChatbotRequest{SchemaVersion: "personal-zalo-chatbot-v2", ThreadType: personalZaloThreadGroup, SenderID: "790", ThreadID: "987"}},
		{name: "ordinary staff message without calling bot", staffGroup: true, silent: true, turn: personalZaloChatbotRequest{SchemaVersion: "personal-zalo-chatbot-v2", ThreadType: personalZaloThreadGroup, SenderID: staffSender, ThreadID: customerGroup}},
		{name: "staff calls bot with group assistant off", customerCode: groupCustomerCode, customerName: groupCustomerName, staffGroup: true, turn: personalZaloChatbotRequest{SchemaVersion: "personal-zalo-chatbot-v2", ThreadType: personalZaloThreadGroup, SenderID: staffSender, ThreadID: customerGroup, BotMentioned: true}},
		{name: "staff fallback mapping without CRM identity", customerCode: fallbackCode, customerName: "Khách fallback", staffGroup: true, turn: personalZaloChatbotRequest{SchemaVersion: "personal-zalo-chatbot-v2", ThreadType: personalZaloThreadGroup, SenderID: staffSender, ThreadID: fallbackGroup, BotMentioned: true}},
		{name: "staff calls bot without a group mapping", staffGroup: true, unmapped: true, turn: personalZaloChatbotRequest{SchemaVersion: "personal-zalo-chatbot-v2", ThreadType: personalZaloThreadGroup, SenderID: staffSender, ThreadID: missingMappingGroup, BotMentioned: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.staffGroup {
				metadata["group_assistant_mode"] = false
				saveMetadata()
			}
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
				if tc.silent {
					if res.Code != http.StatusNoContent {
						t.Fatalf("unaddressed staff message returned status %d", res.Code)
					}
					continue
				}
				if tc.unmapped {
					if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "chưa ghi nhận được khiếu nại thay khách") {
						t.Fatalf("unmapped group reply status=%d body=%s", res.Code, res.Body.String())
					}
					continue
				}
				if res.Code != http.StatusOK || strings.Contains(res.Body.String(), "66656") || !strings.Contains(res.Body.String(), "Dạ đã ghi nhận.") {
					t.Fatalf("reply status=%d body=%s", res.Code, res.Body.String())
				}
			}
			var cases []models.CustomerCase
			groupID := ""
			if turn.ThreadType == personalZaloThreadGroup {
				groupID = turn.ThreadID
			}
			if err := database.Where("tenant_id = ? AND zalo_user_id = ? AND zalo_group_id = ?", tenant, turn.SenderID, groupID).Find(&cases).Error; err != nil {
				t.Fatal(err)
			}
			if tc.unmapped || tc.silent {
				if len(cases) != 0 {
					t.Fatalf("ineligible staff message produced cases: %+v", cases)
				}
				return
			}
			subjectType, subjectID := personalZaloThreadUser, turn.SenderID
			if tc.staffGroup {
				subjectType, subjectID = personalZaloThreadGroup, groupID
			}
			if len(cases) != 1 || cases[0].ChannelID != ch.ID || cases[0].SubjectType != subjectType ||
				cases[0].SubjectID != subjectID || cases[0].ZaloGroupID != groupID || cases[0].FollowupCount != 0 ||
				cases[0].CustomerCode != tc.customerCode {
				t.Fatalf("wrong or duplicate cases: %+v", cases)
			}
			if tc.staffGroup && (cases[0].ReporterName != staffName || cases[0].CustomerName != tc.customerName || cases[0].Private) {
				t.Fatalf("staff complaint has wrong reporter or customer: %+v", cases[0])
			}
			if tc.staffGroup {
				var event models.CustomerCaseEvent
				if err := database.Where("tenant_id = ? AND case_id = ? AND kind = ?", tenant, cases[0].ID, "opened").Take(&event).Error; err != nil || event.ActorName != staffName {
					t.Fatalf("complaint history has wrong reporter: %+v (%v)", event, err)
				}
			}
		})
	}
	var alerts int64
	if err := database.Model(&models.NotificationLog{}).Where("tenant_id = ? AND recipient = ? AND status = ?", tenant, staff.ID, "sent").Count(&alerts).Error; err != nil {
		t.Fatal(err)
	}
	if alerts != 6 {
		t.Fatalf("expected one staff alert per complaint despite retries, got %d", alerts)
	}
	var history int64
	if err := database.Model(&models.CustomerCaseAlert{}).Where("tenant_id = ? AND group_id = ? AND group_name = ? AND status = ?", tenant, staff.ID, staff.Name, "sent").Count(&history).Error; err != nil || history != 6 {
		t.Fatalf("expected six linked alert outcomes, got %d (%v)", history, err)
	}
}
