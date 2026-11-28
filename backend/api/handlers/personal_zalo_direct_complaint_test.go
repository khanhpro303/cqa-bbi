package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
)

func TestPersonalZaloComplaintCodes(t *testing.T) {
	for _, tc := range []struct {
		text string
		want []string
	}{
		{"Ghi nhận khách s056, sản phẩm S084 bị nứt. Mã khách S056.", []string{"S056"}},
		{"Khách S056 / KH012: phản ánh đơn DH123 bị giao thiếu.", []string{"S056", "KH012"}},
		{"Đơn DH123 cho mã KH: CN_A42, sản phẩm FF800.", []string{"CN_A42"}},
		{"Sản phẩm FF800 đơn DH123 bị giao sai", nil},
	} {
		if got, _ := personalZaloComplaintCodes(tc.text); !slices.Equal(got, tc.want) {
			t.Errorf("codes for %q: got %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestPersonalZaloDirectStaffComplaintsMySQL(t *testing.T) {
	redisAddr := os.Getenv("TEST_REDIS_ADDR")
	if redisAddr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(&models.AppSetting{}, &models.CachedCustomer{}, &models.CRMGroup{},
		&models.ZaloWhitelist{}, &models.PersonalZaloIdentityLink{}, &models.PersonalZaloThreadApproval{}, &models.Conversation{},
		&models.CustomerCase{}, &models.CustomerCaseEvent{}, &models.CustomerCaseAlert{}, &models.ERPAuditLog{}); err != nil {
		t.Fatal(err)
	}
	previousRedis := db.RedisClient
	client := redis.NewClient(&redis.Options{Addr: redisAddr})
	db.RedisClient = client
	t.Cleanup(func() { _ = client.Close(); db.RedisClient = previousRedis })
	if err := client.Ping(t.Context()).Err(); err != nil {
		t.Fatal(err)
	}
	const key = stockFixtureSigningKey
	t.Setenv("JWT_SECRET", key)
	t.Setenv("ENCRYPTION_KEY", key)
	t.Setenv("DB_PASSWORD", "fixture")

	const tenant, otherTenant, sender, staffName = "staff-case-tenant", "other-case-tenant", "456", "Nhân viên Ánh"
	createFacebookOAuthTenant(t, database, tenant)
	createFacebookOAuthTenant(t, database, otherTenant)
	ch := models.Channel{ID: uuid.NewString(), TenantID: tenant, ChannelType: "personal_zalo_import", ExternalID: "123",
		Name: "Personal", CredentialsEncrypted: []byte("fixture"), IsActive: true, PersonalZaloAutoReplyEnabled: true, Metadata: `{"complaint_cases":true}`}
	oa := models.Channel{ID: uuid.NewString(), TenantID: tenant, ChannelType: "zalo_oa", ExternalID: "999", Name: "OA", CredentialsEncrypted: []byte("fixture"), Metadata: "{}"}
	staff := models.ZaloWhitelist{ID: uuid.NewString(), TenantID: tenant, ChannelID: oa.ID, ZaloUserID: "567", Name: staffName, Status: "active"}
	senderID, now := sender, time.Now()
	link := models.PersonalZaloIdentityLink{ID: uuid.NewString(), TenantID: tenant, ChannelID: ch.ID, AccountExternalID: ch.ExternalID,
		Kind: personalZaloStaffIdentity, TargetID: staff.ID, SenderID: &senderID, VerifiedAt: &now, ExpiresAt: now}
	approval := models.PersonalZaloThreadApproval{ChannelID: ch.ID, AccountExternalID: ch.ExternalID,
		ThreadType: personalZaloThreadUser, ThreadID: sender, Present: true, Status: personalZaloApproved, FirstSeenAt: now.Add(-time.Minute)}
	const unrelatedTitle = "Phiếu cũ dành cho khách khác"
	for _, row := range []any{
		&ch, &oa, &staff, &link, &approval,
		&models.CachedCustomer{ID: uuid.NewString(), TenantID: tenant, MA: "S056 - Khách Một", HO_VA_TEN: "Khách Một"},
		&models.CachedCustomer{ID: uuid.NewString(), TenantID: tenant, MA: "S057", HO_VA_TEN: "Khách Hai"},
		&models.CachedCustomer{ID: uuid.NewString(), TenantID: tenant, MA: "S058", HO_VA_TEN: "Khách Ba"},
		&models.CachedCustomer{ID: uuid.NewString(), TenantID: tenant, MA: "BBI001 - Khách BBI", HO_VA_TEN: "Khách BBI"},
		&models.CachedCustomer{ID: uuid.NewString(), TenantID: tenant, MA: "S059", HO_VA_TEN: "Trùng mã một", ERP_ID: 91},
		&models.CachedCustomer{ID: uuid.NewString(), TenantID: tenant, MA: "S059 - Trùng mã hai", HO_VA_TEN: "Trùng mã hai", ERP_ID: 92},
		&models.CachedCustomer{ID: uuid.NewString(), TenantID: tenant, MA: "S060", HO_VA_TEN: "Khách Bốn"},
		&models.CachedCustomer{ID: uuid.NewString(), TenantID: otherTenant, MA: "S099", HO_VA_TEN: "Khách tenant khác"},
		&models.CustomerCase{ID: uuid.NewString(), TenantID: tenant, ChannelID: ch.ID, SubjectType: "user",
			SubjectID: sender, ZaloUserID: sender, CustomerCode: "S000", Status: "open", Title: unrelatedTitle, Images: "[]"},
	} {
		if err := database.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}

	gin.SetMode(gin.TestMode)
	mcp := gin.New()
	mcp.POST("/mcp/:tenantId", ERPMCP)
	var skippedConfirmation atomic.Bool
	flow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), unrelatedTitle) {
			t.Error("AI received an unrelated customer's earlier complaint")
		}
		if r.URL.Path == "/responses" {
			// Prove the tool refusal drives the reply even if the classifier misses it.
			_, _ = io.WriteString(w, `{"output_text":"{\"label\":\"IN_SCOPE\"}"}`)
			return
		}
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		respond := func(reply string) {
			_, _ = fmt.Fprintf(w, `{"outputs":[{"outputs":[{"results":{"message":{"text":%q}}}]}]}`, reply)
		}
		var input struct {
			Text string `json:"input_value"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			t.Error(err)
			return
		}
		if strings.HasPrefix(input.Text, "Tra đơn") {
			respond("Đang tra đơn cho anh/chị.")
			return
		}
		permission := r.Header.Get("X-Langflow-Global-Var-ERP_PERMISSION_TOKEN")
		p, err := engine.VerifyPermissionToken(permission, key)
		if err != nil || p.ComplaintStaffID != staff.ID || p.CustomerCode != "" {
			t.Errorf("invalid staff complaint grant: %+v (%v)", p, err)
			return
		}
		if p.ComplaintCustomerCode == "S057" && !strings.Contains(input.Text, "kính giao sai màu") {
			t.Error("confirmation lost the original complaint")
		}
		if p.ComplaintCustomerCode == "S057" && !skippedConfirmation.Swap(true) {
			respond("Dạ, em đã ghi nhận vấn đề ạ.")
			return
		}
		// Omit the customer argument for S056. For the remaining turns ask
		// for the wrong customer to prove its value cannot override the grant.
		extra := `,"customer_code":"S059"`
		if p.ComplaintCustomerCode == "S056" {
			extra = ""
		}
		payload := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"complaint_record","arguments":{"title":"Sản phẩm lỗi","detail":"Nhân viên báo lỗi sản phẩm","type":"product_defect"` + extra + `}}}`
		req := httptest.NewRequest(http.MethodPost, "/mcp/"+tenant, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Agent-Token", r.Header.Get("X-Langflow-Global-Var-CQA_ERP_AGENT_TOKEN"))
		req.Header.Set("X-Permission-Token", permission)
		res := httptest.NewRecorder()
		mcp.ServeHTTP(res, req)
		if got := strings.Contains(res.Body.String(), `"isError":true`); got != (p.ComplaintCustomerCode == "") {
			t.Errorf("tool response for selected code %q: %s", p.ComplaintCustomerCode, res.Body)
		}
		respond("Dạ, em đã ghi nhận vấn đề ạ.")
	}))
	defer flow.Close()
	for name, value := range map[string]string{
		"ai_engine_langflow_url": flow.URL, "ai_engine_langflow_flow_id": "private-fixture", "ai_engine_langflow_token": "fixture-token",
		"ai_provider": "openai", "ai_api_key": "fixture-decision-key", "ai_base_url": flow.URL,
	} {
		if err := database.Create(&models.AppSetting{
			ID: uuid.NewString(), TenantID: tenant, SettingKey: name, ValuePlain: value,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	router := gin.New()
	router.POST("/chatbot", func(c *gin.Context) { c.Set("internal_request_id", "fixture"); c.Next() },
		NewPersonalZaloChatbotHandler(&config.Config{EncryptionKey: key, LangflowAPIURL: flow.URL}))
	send := func(id, message, expected string, count int64) {
		t.Helper()
		turn := personalZaloChatbotRequest{SchemaVersion: personalZaloDirectProtocol, RequestID: "fixture",
			TenantID: tenant, ChannelID: ch.ID, AccountExternalID: ch.ExternalID, ThreadType: personalZaloThreadUser,
			SenderID: sender, ThreadID: sender, MessageID: id, SentAt: time.Now(), Text: message}
		body, _ := json.Marshal(turn)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/chatbot", bytes.NewReader(body)))
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), expected) {
			t.Fatalf("%q: wanted %q, status=%d, body=%s", message, expected, res.Code, res.Body)
		}
		var got int64
		if err := database.Model(&models.CustomerCase{}).Count(&got).Error; err != nil || got != count {
			t.Fatalf("%q: cases=%d, want %d (%v)", message, got, count, err)
		}
	}

	send("single", "Khách s056 báo kính FF800 bị nứt, ghi nhận giúp tôi", "đã ghi nhận", 2)
	send("normal", "Tra đơn khách S056 và S057", "Đang tra đơn", 2)
	send("handover", "Cho gặp nhân viên phụ trách", "chưa chuyển được tin này", 2)
	send("other-code-format", "Ghi nhận giúp khách S056 hay BBI001, kính giao sai màu", "S056, BBI001", 2)
	send("ambiguous", "Ghi nhận giúp khách S056 hay S057, kính giao sai màu", "xác nhận một mã", 2)
	send("not-offered", "S058", "xác nhận một mã", 2)
	send("first-confirmation", "Xác nhận S057", "chưa ghi nhận được khiếu nại", 2)
	send("confirmation", "Khách S057 ạ", "đã ghi nhận", 3)
	send("confirmation", "Khách S057 ạ", "đã ghi nhận", 3)
	send("no-pending", "S057", "cùng nội dung khiếu nại", 3)
	send("unknown", "Ghi khiếu nại khách S099, hàng bị lỗi", "không tìm thấy mã khách hàng S099", 3)
	send("correction", "S058", "đã ghi nhận", 4)
	send("duplicate", "Ghi nhận khách S059 giao sai món", "khớp nhiều hồ sơ", 4)
	send("missing", "Ghi khiếu nại hộ khách, sản phẩm FF800 bị vỡ", "xin mã khách", 4)
	send("full-correction", "Ghi khiếu nại hộ khách S060, sản phẩm FF800 bị vỡ", "đã ghi nhận", 5)
	send("stale-confirmation", "S057", "cùng nội dung khiếu nại", 5)

	var cases []models.CustomerCase
	if err := database.Where("customer_code IN ?", []string{"S056", "S057", "S058", "S060"}).Order("customer_code").Find(&cases).Error; err != nil {
		t.Fatal(err)
	}
	names := []string{"Khách Một", "Khách Hai", "Khách Ba", "Khách Bốn"}
	messages := []string{"single", "ambiguous", "unknown", "full-correction"}
	if len(cases) != len(names) {
		t.Fatalf("wrong saved customers: %+v", cases)
	}
	for i, c := range cases {
		if c.ReporterName != staffName || c.CustomerName != names[i] || c.ZaloUserID != sender || c.ChannelID != ch.ID || c.ZaloGroupID != "" {
			t.Errorf("wrong complaint identity: %+v", c)
		}
		var event models.CustomerCaseEvent
		turnID := personalZaloComplaintTurnID(personalZaloChatbotRequest{TenantID: tenant, ChannelID: ch.ID,
			AccountExternalID: ch.ExternalID, ThreadType: personalZaloThreadUser, ThreadID: sender, SenderID: sender, MessageID: messages[i]})
		if err := database.Where("case_id = ? AND kind = ?", c.ID, "opened").Take(&event).Error; err != nil || event.ActorName != staffName || event.TurnID != turnID {
			t.Errorf("wrong reporter or original turn: %+v (%v)", event, err)
		}
	}
}
