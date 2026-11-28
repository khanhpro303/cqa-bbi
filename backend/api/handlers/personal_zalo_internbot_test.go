package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestPersonalZaloInternbotSignedDelivery(t *testing.T) {
	const key = "0123456789abcdef0123456789abcdef"
	const importSecret = "test-internbot-personal-zalo-channel-secret"
	const gatewaySecret = "test-internbot-deployment-gateway-signing-secret"
	const base = "/api/internal/chatbot/personal-zalo"
	credentials, err := pkg.Encrypt([]byte(`{"import_secret":"`+importSecret+`"}`), key)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	mode := personalZaloInternbotConfig{
		Mode: "internbot", Enabled: true, SupervisorGroupID: "777", SupervisorAccountID: "123",
		Version: uuid.NewString(), ActivatedAt: now.Add(-time.Second),
	}
	channel := models.Channel{
		ID: "channel", TenantID: "tenant", ExternalID: "123", ChannelType: "personal_zalo_import",
		IsActive: true, CredentialsEncrypted: credentials,
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
	redisAddr := os.Getenv("TEST_REDIS_ADDR")
	if redisAddr != "" {
		client := redis.NewClient(&redis.Options{Addr: redisAddr})
		originalRedis := db.RedisClient
		db.RedisClient = client
		t.Cleanup(func() { db.RedisClient = originalRedis; _ = client.Close() })
	}

	roster := `[{"id":"777","name":"Giám thị"}]`
	staff := false
	var changedDuringAI, changeOnNextCall atomic.Bool
	if err := database.Callback().Query().After("gorm:query").Register("test:internbot", func(q *gorm.DB) {
		switch row := q.Statement.Dest.(type) {
		case *models.Channel:
			if !slices.Contains(q.Statement.Vars, any(channel.ID)) || !slices.Contains(q.Statement.Vars, any(channel.TenantID)) {
				q.AddError(gorm.ErrRecordNotFound)
				return
			}
			if strings.Contains(q.Statement.SQL.String(), "personal_zalo_auto_reply_enabled = ?") && !channel.PersonalZaloAutoReplyEnabled {
				q.AddError(gorm.ErrRecordNotFound)
				return
			}
			*row = channel
			config := mode
			if changedDuringAI.Load() {
				config.Version = "changed-during-ai"
			}
			data, err := json.Marshal(config)
			if err != nil {
				q.AddError(err)
			}
			row.Metadata = string(data)
		case *models.PersonalZaloInternbotRoster:
			if !slices.Contains(q.Statement.Vars, any(channel.ID)) || !slices.Contains(q.Statement.Vars, any(channel.ExternalID)) {
				t.Error("supervisor membership query must be scoped to the channel and account")
			}
			*row = models.PersonalZaloInternbotRoster{ChannelID: channel.ID, AccountExternalID: channel.ExternalID, CapturedAt: now, Groups: roster}
		case *[]models.AppSetting:
			*row = []models.AppSetting{
				{SettingKey: "chatbot_active", ValuePlain: "true"},
				{SettingKey: "ai_engine_system_prompt", ValuePlain: "Hướng dẫn tiếng Việt của tenant"},
				{SettingKey: "ai_engine_langflow_public_flow_id", ValuePlain: "tenant-public"},
				{SettingKey: "ai_engine_langflow_token", ValuePlain: "tenant-flow-key"},
			}
		case *models.ZaloWhitelist:
			if !staff {
				q.AddError(gorm.ErrRecordNotFound)
				return
			}
			*row = models.ZaloWhitelist{ID: "staff", Status: "active"}
		case *models.PersonalZaloThreadApproval:
			t.Error("Internbot must not depend on Chatbot thread approval")
		}
	}); err != nil {
		t.Fatal(err)
	}

	var flowCalls atomic.Int32
	flow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = fmt.Fprint(w, `{"data":{"nodes":[{"id":"Agent-fixture","data":{"node":{"template":{"system_prompt":{}}}}}]}}`)
			return
		}
		flowCalls.Add(1)
		if changeOnNextCall.Swap(false) {
			changedDuringAI.Store(true)
		}
		if r.URL.Path != "/api/v1/run/tenant-public" || r.Header.Get("X-API-Key") != "tenant-flow-key" {
			t.Error("Internbot did not resolve the existing tenant Langflow configuration")
		}
		var requestBody struct {
			Tweaks map[string]any `json:"tweaks"`
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Error(err)
		}
		agent, _ := requestBody.Tweaks["Agent-fixture"].(map[string]any)
		prompt, _ := agent["system_prompt"].(string)
		if !strings.Contains(prompt, "Hướng dẫn tiếng Việt của tenant") || !strings.Contains(prompt, "Khách trong phiên này chưa xác thực") {
			t.Error("draft prompt omitted tenant instructions or guest restrictions")
		}
		token := r.Header.Get("X-LANGFLOW-GLOBAL-VAR-CQA_ERP_AGENT_TOKEN")
		if resolveKnowledgePreviewAgent(key, channel.TenantID, token) != "public" || r.Header.Get("X-LANGFLOW-GLOBAL-VAR-ERP_PERMISSION_TOKEN") != "" {
			t.Error("unverified customer did not get a knowledge-only credential")
		}
		_, _ = fmt.Fprint(w, `{"outputs":[{"outputs":[{"results":{"message":{"text":"Đây là phản hồi thử."}}}]}]}`)
	}))
	t.Cleanup(flow.Close)
	router := gin.New()
	auth := middleware.InternalHMACAuth(middleware.PersonalZaloChatbotSecretResolver(key, gatewaySecret), 5*time.Minute)
	cfg := &config.Config{EncryptionKey: key, LangflowAPIURL: flow.URL, LangflowFlowID: "internal-flow"}
	router.POST(base, auth, NewPersonalZaloChatbotHandler(cfg))
	router.POST(base+"/internbot/delivery", auth, NewPersonalZaloInternbotDeliveryHandler(cfg))

	post := func(path string, req personalZaloChatbotRequest, target string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req.RequestID = uuid.NewString()
		body, err := json.Marshal(struct {
			personalZaloChatbotRequest
			Target string `json:"supervisor_group_id,omitempty"`
		}{req, target})
		if err != nil {
			t.Fatal(err)
		}
		ts := fmt.Sprint(time.Now().Unix())
		sign := func(secret string) string {
			mac := hmac.New(sha256.New, []byte(secret))
			fmt.Fprintf(mac, "%s.%s.", ts, req.RequestID)
			mac.Write(body)
			return hex.EncodeToString(mac.Sum(nil))
		}
		request := httptest.NewRequest(http.MethodPost, base+path, bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CQA-Request-Id", req.RequestID)
		request.Header.Set("X-CQA-Timestamp", ts)
		request.Header.Set("X-CQA-Signature", sign(importSecret))
		request.Header.Set("X-CQA-Chatbot-Signature", sign(gatewaySecret))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s: status = %d, want %d: %s", path, response.Code, want, response.Body)
		}
		return response
	}
	decode := func(response *httptest.ResponseRecorder) personalZaloInternbotDelivery {
		t.Helper()
		var body struct {
			Internbot personalZaloInternbotDelivery `json:"internbot_delivery"`
			Reply     string                        `json:"reply"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Reply != "" ||
			body.Internbot.GroupID != mode.SupervisorGroupID || body.Internbot.Token == "" {
			t.Fatalf("response must use supervisor-only protocol: %s", response.Body)
		}
		return body.Internbot
	}
	checkDelivery := func(req personalZaloChatbotRequest, delivery personalZaloInternbotDelivery, target, digest string, want int) {
		t.Helper()
		req.DeliveryToken, req.DeliveryPart, req.DeliveryDigest = delivery.Token, "text", digest
		post("/internbot/delivery", req, target, want)
	}

	direct := personalZaloChatbotRequest{
		SchemaVersion: personalZaloDirectProtocol, SupportsInternbot: true,
		TenantID: channel.TenantID, ChannelID: channel.ID, AccountExternalID: channel.ExternalID,
		ThreadType: "user", ThreadID: "456", SenderID: "456", MessageID: "direct-1",
		SentAt: now, Text: "Giới thiệu mẫu mũ. OTP: 123456", SenderName: "Khách thử",
	}
	draft := decode(post("", direct, "", http.StatusOK))
	if !strings.Contains(draft.Text, "Đây là phản hồi thử.") || !strings.Contains(draft.Text, "Khách thử") || strings.Contains(draft.Text, "123456") {
		t.Fatalf("wrong draft or unredacted source text: %q", draft.Text)
	}
	digest := personalZaloIdentityDigest(draft.Text)
	checkDelivery(direct, draft, "777", digest, http.StatusOK)
	checkDelivery(direct, draft, "778", digest, http.StatusNoContent)
	checkDelivery(direct, draft, "777", personalZaloIdentityDigest("changed text"), http.StatusNoContent)
	differentSource := direct
	differentSource.ThreadID, differentSource.SenderID = "457", "457"
	checkDelivery(differentSource, draft, "777", digest, http.StatusNoContent)

	group := direct
	group.SchemaVersion, group.ThreadType, group.ThreadID, group.MessageID = "personal-zalo-chatbot-v2", "group", "789", "group-1"
	group.Text, group.SourceName = "Nhờ gặp nhân viên", "Nhóm có OA"
	groupDraft := decode(post("", group, "", http.StatusOK))
	if !strings.Contains(groupDraft.Text, "Nhóm có OA") || !strings.Contains(groupDraft.Text, "Cần nhân viên xử lý") {
		t.Fatalf("group handover simulation missing from supervisor: %q", groupDraft.Text)
	}
	checkDelivery(group, groupDraft, "777", personalZaloIdentityDigest(groupDraft.Text), http.StatusOK)
	wantFlowCalls := int32(2)
	if redisAddr != "" {
		groupQuestion := group
		groupQuestion.SchemaVersion = personalZaloInternbotProtocol
		groupQuestion.Text, groupQuestion.MessageID = "Giới thiệu mẫu mũ", "group-2"
		draft := decode(post("", groupQuestion, "", http.StatusOK))
		groupQuestion.SchemaVersion = "personal-zalo-chatbot-v2"
		checkDelivery(groupQuestion, draft, "777", personalZaloIdentityDigest(draft.Text), http.StatusOK)
		wantFlowCalls++
	}
	supervisor := group
	supervisor.ThreadID = "777"
	post("", supervisor, "", http.StatusNoContent)

	staff = true
	post("", direct, "", http.StatusNoContent)
	post("", group, "", http.StatusNoContent)
	checkDelivery(direct, draft, "777", digest, http.StatusNoContent)
	staff = false
	roster = `[]`
	checkDelivery(direct, draft, "777", digest, http.StatusNoContent)
	roster = `[{"id":"777","name":"Giám thị"}]`
	legacy := direct
	legacy.SupportsInternbot = false
	post("", legacy, "", http.StatusNoContent)
	tooOld := direct
	tooOld.SentAt = now.Add(-2 * time.Second)
	post("", tooOld, "", http.StatusNoContent)

	changeOnNextCall.Store(true)
	post("", direct, "", http.StatusNoContent)
	checkDelivery(direct, draft, "777", digest, http.StatusNoContent)
	changedDuringAI.Store(false)
	mode.Mode = personalZaloChatbotMode
	mode.Enabled = false
	channel.PersonalZaloAutoReplyEnabled = true
	manualOverlap := direct
	manualOverlap.SchemaVersion = personalZaloInternbotProtocol
	post("", manualOverlap, "", http.StatusNoContent)
	if calls := flowCalls.Load(); calls != wantFlowCalls {
		t.Fatalf("Langflow executions = %d, want %d", calls, wantFlowCalls)
	}
}

func TestPersonalZaloInternbotDraftFitsZaloTextLimit(t *testing.T) {
	draft := formatPersonalZaloInternbotDraft(personalZaloChatbotRequest{
		ThreadType: "group", ThreadID: "789", Text: strings.Repeat("😃", 400), SentAt: time.Now(),
	}, strings.Repeat("🛡", 2000), true)
	if personalZaloTextBoundary(draft, personalZaloTextLimit) != len(draft) || !strings.HasSuffix(draft, "…") {
		t.Fatal("draft exceeded the Zalo UTF-16 limit or lost its truncation marker")
	}
}

// The default frame in AI Engine reproduces the draft the code built before
// the frame became editable.
func TestPersonalZaloInternbotDefaultFrameMatchesTheOldDraft(t *testing.T) {
	sent := time.Date(2026, 11, 28, 2, 5, 0, 0, time.UTC)
	for _, handover := range []bool{false, true} {
		for _, req := range []personalZaloChatbotRequest{
			{ThreadType: "user", ThreadID: "456", SenderID: "456", SenderName: "Khách  A", Text: "còn {phan_hoi} FF399?\nsize L", SentAt: sent},
			{ThreadType: "group", ThreadID: "789", SenderID: "111", SourceName: "Đại lý B", Text: "hello", SentAt: sent},
			{ThreadType: "group", ThreadID: "789", SenderID: "111", Text: strings.Repeat("a", 500), SentAt: sent},
		} {
			reply := "Dạ còn ạ."
			status := ""
			if handover {
				status = "\nCần nhân viên xử lý"
			}
			source := map[string]string{"user": "Chat 1:1", "group": "Nhóm 789"}[req.ThreadType]
			if req.SourceName != "" {
				source = req.SourceName
			}
			sender := strings.Join(strings.Fields(req.SenderName), " ")
			if sender == "" {
				sender = req.SenderID
			}
			want := "Internbot · " + source + "\nKhách: " + sender + " · 09:05\nTin nhắn: " +
				clipPersonalZaloInternbotText(req.Text, 400) + status + "\n\nPhản hồi đề xuất:\n" + reply
			if got := formatPersonalZaloInternbotDraft(req, reply, handover); got != want {
				t.Fatalf("handover=%v draft:\n%q\nwant\n%q", handover, got, want)
			}
		}
	}
}

func TestPersonalZaloInternbotCustomFrame(t *testing.T) {
	req := personalZaloChatbotRequest{ThreadType: "user", ThreadID: "456", SenderID: "456", SenderName: "An", Text: "giá?",
		SentAt: time.Date(2026, 11, 28, 2, 5, 0, 0, time.UTC)}
	frame := "[{gio}] {khach}: {tin_nhan}\n⚠️ {neu_can_nhan_vien}Gọi khách ngay\n> {phan_hoi}\n({nhom})"
	if got := fillPersonalZaloInternbotDraft(frame, req, "Dạ 100k ạ.", false); got != "[09:05] An: giá?\n> Dạ 100k ạ.\n(Chat 1:1)" {
		t.Fatalf("draft = %q", got)
	}
	if got := fillPersonalZaloInternbotDraft(frame, req, "Nhờ nhân viên.", true); got != "[09:05] An: giá?\n⚠️ Gọi khách ngay\n> Nhờ nhân viên.\n(Chat 1:1)" {
		t.Fatalf("handover draft = %q", got)
	}
	// A reply placed mid-frame, or twice, still fits the Zalo limit and the
	// text after it survives.
	got := fillPersonalZaloInternbotDraft("{phan_hoi}\n---\n{phan_hoi}\nHết", req, strings.Repeat("🛡", 2000), false)
	if personalZaloTextBoundary(got, personalZaloTextLimit) != len(got) || !strings.HasSuffix(got, "…\nHết") {
		t.Fatalf("long draft does not fit or lost its ending: %d bytes, ends %q", len(got), got[len(got)-12:])
	}
}

func TestPersonalZaloInternbotSignsOnlyAnEffectiveERPGrant(t *testing.T) {
	const key = "fixture-internbot-signing-key"
	req := personalZaloChatbotRequest{ThreadType: "user", ThreadID: "456"}
	mode := personalZaloInternbotConfig{SupervisorGroupID: "777", Version: "version"}
	identity := personalZaloERPIdentity{AgentType: "public", ID: "customer"}
	delivery, err := buildPersonalZaloInternbotDelivery(key, req, mode, identity,
		personalZaloDirectGrant{grantHash: "hash-with-no-ERP-groups", agentToken: "preview-token"}, "Bản nháp", nil)
	if err != nil {
		t.Fatal(err)
	}
	var claims personalZaloInternbotClaims
	_, err = jwt.ParseWithClaims(delivery.Token, &claims, func(*jwt.Token) (any, error) {
		return personalZaloInternbotSigningKey(key), nil
	})
	if err != nil || claims.IdentityID != identity.ID || claims.Permissions != "" || claims.Credential != "" {
		t.Fatal("a verified customer without ERP access must still be allowed to receive a knowledge-only draft")
	}
}
