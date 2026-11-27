package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/realtime"
)

func TestPersonalZaloRosterSnapshotValidation(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name    string
		at      time.Time
		entries []personalZaloRosterEntry
		valid   bool
	}{
		{name: "missing roster", at: now},
		{name: "verified empty roster", at: now, entries: []personalZaloRosterEntry{}, valid: true},
		{name: "stale roster", at: now.Add(-3 * time.Minute), entries: []personalZaloRosterEntry{}},
		{name: "future roster", at: now.Add(time.Minute), entries: []personalZaloRosterEntry{}},
		{name: "self as friend", at: now, entries: []personalZaloRosterEntry{{ThreadType: "user", ThreadID: "111"}}},
		{name: "duplicate payload", at: now, entries: []personalZaloRosterEntry{
			{ThreadType: "user", ThreadID: "222"}, {ThreadType: "user", ThreadID: "222"},
		}},
		{name: "same numeric ID across types", at: now, entries: []personalZaloRosterEntry{
			{ThreadType: "user", ThreadID: "222"}, {ThreadType: "group", ThreadID: "222"},
		}, valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := personalZaloRosterSnapshot{AccountExternalID: "111", CapturedAt: tc.at, Entries: tc.entries}
			if snapshot.validate(now) != tc.valid {
				t.Fatalf("unexpected validity for %+v", snapshot)
			}
		})
	}
}

func TestPersonalZaloRosterApprovalLifecycleMySQL(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(&models.PersonalZaloRosterState{}, &models.PersonalZaloThreadApproval{}, &models.PersonalZaloInternbotRoster{}, &models.AppSetting{}, &models.CRMGroup{}); err != nil {
		t.Fatal(err)
	}
	const tenant, channelID, account = "zalo-approvals-tenant", "zalo-approvals-channel", "111"
	createFacebookOAuthTenant(t, database, tenant)
	channel := models.Channel{ID: channelID, TenantID: tenant, ChannelType: "personal_zalo_import", ExternalID: account,
		Name: "Bound Zalo", Metadata: `{"personal_zalo_group_reply_enabled":true}`, CredentialsEncrypted: []byte("fixture")}
	if err := database.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Millisecond).Add(-2 * time.Second)
	oldFriend := personalZaloRosterEntry{"user", "222", "Existing friend"}
	newFriend := personalZaloRosterEntry{"user", "333", "New friend"}
	newGroup := personalZaloRosterEntry{"group", "333", "New group"}
	offFriend := personalZaloRosterEntry{"user", "444", "Added while off"}
	var liveRoster atomic.Pointer[personalZaloRosterSnapshot]
	setRoster := func(at time.Time, entries ...personalZaloRosterEntry) {
		liveRoster.Store(&personalZaloRosterSnapshot{AccountExternalID: account, CapturedAt: at, Entries: entries})
	}
	setRoster(start, oldFriend)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/accounts":
			_ = json.NewEncoder(w).Encode(personalZaloGatewayListResponse{Accounts: []personalZaloGatewayAccountRecord{{
				ID: "gateway-account", TenantID: tenant, ChannelID: channelID, Status: "connected", ZaloUID: account,
			}}})
		case "/api/v1/accounts/gateway-account/roster":
			_ = json.NewEncoder(w).Encode(liveRoster.Load())
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer gateway.Close()
	t.Setenv("PERSONAL_ZALO_GATEWAY_BASE_URL", gateway.URL)
	t.Setenv("PERSONAL_ZALO_GATEWAY_ADMIN_SECRET", "test-gateway-admin-secret-fixture-111")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("DB_PASSWORD", "fixture")

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("tenant_id", c.GetHeader("Test-Tenant"))
		c.Set("tenant_role", c.GetHeader("Test-Role"))
		c.Set("internal_request_id", "fixture-request")
	})
	router.PUT("/channels/:channelId", UpdateChannel)
	router.GET("/channels/:channelId/approvals", GetPersonalZaloApprovals)
	router.PUT("/channels/:channelId/approvals/:threadType/:threadId", DecidePersonalZaloApproval)
	router.POST("/roster", IngestPersonalZaloRoster)
	router.POST("/thread-access", CheckPersonalZaloGroupThreadAccess)

	send := func(method, path, tenantID, role string, payload any, want int) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Test-Tenant", tenantID)
		req.Header.Set("Test-Role", role)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s returned %d, want %d: %s", method, path, w.Code, want, w.Body)
		}
		return w
	}
	path := "/channels/" + channelID
	toggle := func(enabled bool, role string, want int) {
		t.Helper()
		send(http.MethodPut, path, tenant, role, gin.H{"auto_reply_enabled": enabled}, want)
	}
	probe := func(want int) {
		t.Helper()
		send(http.MethodPost, "/roster", tenant, "admin", gin.H{
			"request_id": "fixture-request", "tenant_id": tenant, "channel_id": channelID,
			"account_external_id": account, "check_only": true,
		}, want)
	}
	push := func(snapshot personalZaloRosterSnapshot) {
		t.Helper()
		payload := struct {
			RequestID string `json:"request_id"`
			TenantID  string `json:"tenant_id"`
			ChannelID string `json:"channel_id"`
			personalZaloRosterSnapshot
		}{"fixture-request", tenant, channelID, snapshot}
		send(http.MethodPost, "/roster", tenant, "admin", payload, http.StatusNoContent)
	}
	list := func(status string, wantTotal int) {
		t.Helper()
		w := send(http.MethodGet, path+"/approvals?status="+status, tenant, "admin", nil, http.StatusOK)
		var page struct {
			Items []models.PersonalZaloThreadApproval `json:"items"`
			Total int                                 `json:"total"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || page.Items == nil ||
			page.Total != wantTotal || len(page.Items) != wantTotal {
			t.Fatalf("%s: got %s, want %d items: %v", status, w.Body, wantTotal, err)
		}
	}
	decision := func(kind, id, status, expected string, want int) {
		t.Helper()
		send(http.MethodPut, path+"/approvals/"+kind+"/"+id, tenant, "admin",
			gin.H{"status": status, "expected_status": expected}, want)
	}
	groupAccess := func(sentAt time.Time, want int) {
		t.Helper()
		req := personalZaloChatbotRequest{RequestID: "fixture-request", TenantID: tenant, ChannelID: channelID,
			AccountExternalID: account, ThreadID: newGroup.ThreadID, ThreadType: "group", SentAt: sentAt}
		send(http.MethodPost, "/thread-access", tenant, "admin", req, want)
	}
	events, err := realtime.Subscribe(t.Context(), tenant)
	if err != nil {
		t.Fatal(err)
	}
	expectEvent := func() {
		t.Helper()
		timeout := time.After(time.Second)
		for {
			select {
			case event := <-events:
				if event == realtime.EventPersonalZaloApprovalsUpdated {
					return
				}
				if event != realtime.EventCRMPersonalZaloGroupsUpdated {
					t.Fatalf("unexpected SSE event: %s", event)
				}
			case <-timeout:
				t.Fatal("missing approval update SSE event")
			}
		}
	}

	probe(http.StatusOK)
	toggle(true, "member", http.StatusForbidden)
	toggle(true, "admin", http.StatusOK)
	expectEvent()
	probe(http.StatusOK)
	list(personalZaloApproved, 1)
	list(personalZaloPending, 0)

	setRoster(start.Add(time.Millisecond), oldFriend, newFriend, newGroup)
	push(*liveRoster.Load())
	expectEvent()
	list(personalZaloPending, 2)
	groupAccess(time.Now(), http.StatusNoContent)
	send(http.MethodPut, path+"/approvals/group/333", "foreign-tenant", "admin",
		gin.H{"status": "approved", "expected_status": "pending"}, http.StatusNotFound)
	decision("user", newFriend.ThreadID, personalZaloRejected, personalZaloPending, http.StatusNoContent)
	expectEvent()
	decision("user", newFriend.ThreadID, personalZaloApproved, personalZaloPending, http.StatusConflict)
	oldMessage := time.Now().Add(-time.Second)
	decision("group", newGroup.ThreadID, personalZaloApproved, personalZaloPending, http.StatusNoContent)
	expectEvent()
	groupAccess(oldMessage, http.StatusNoContent)
	groupAccess(time.Now(), http.StatusOK)

	setRoster(start.Add(2*time.Millisecond), oldFriend, newFriend)
	push(*liveRoster.Load())
	expectEvent()
	groupAccess(time.Now(), http.StatusNoContent)
	push(personalZaloRosterSnapshot{AccountExternalID: account, CapturedAt: start, Entries: []personalZaloRosterEntry{oldFriend, newGroup}})
	groupAccess(time.Now(), http.StatusNoContent)
	setRoster(start.Add(3*time.Millisecond), oldFriend, newFriend, newGroup)
	push(*liveRoster.Load())
	expectEvent()
	list(personalZaloPending, 1)
	groupAccess(time.Now(), http.StatusNoContent)

	toggle(false, "owner", http.StatusOK)
	probe(http.StatusOK)
	setRoster(start.Add(4*time.Millisecond), oldFriend, newFriend, newGroup, offFriend)
	push(*liveRoster.Load())
	list(personalZaloApproved, 1)
	toggle(true, "admin", http.StatusOK)
	expectEvent()
	list(personalZaloApproved, 2)
	list(personalZaloRejected, 1)
	list(personalZaloPending, 1)
}
