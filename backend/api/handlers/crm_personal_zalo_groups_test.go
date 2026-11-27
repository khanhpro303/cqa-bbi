package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/realtime"
	"gorm.io/gorm"
)

func TestCRMLinksPersonalZaloGroupOnceByZaloIDMySQL(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(&models.CRMGroup{}, &models.CRMGroupCustomerCode{}, &models.Conversation{}, &models.ActivityLog{}); err != nil {
		t.Fatal(err)
	}
	const tenant, other = "group-crm", "group-crm-other"
	createFacebookOAuthTenant(t, database, tenant)
	createFacebookOAuthTenant(t, database, other)
	fixtures := []any{
		&[]models.Channel{
			{ID: "personal-a", TenantID: tenant, ChannelType: "personal_zalo_import", ExternalID: "101", Name: "Zalo A", CredentialsEncrypted: []byte("fixture"), Metadata: "{}"},
			{ID: "personal-b", TenantID: tenant, ChannelType: "personal_zalo_import", ExternalID: "102", Name: "Zalo B", CredentialsEncrypted: []byte("fixture"), Metadata: "{}"},
			{ID: "personal-other", TenantID: other, ChannelType: "personal_zalo_import", ExternalID: "103", Name: "Other tenant", CredentialsEncrypted: []byte("fixture"), Metadata: "{}"},
			{ID: "group-oa", TenantID: tenant, ChannelType: "zalo_oa", ExternalID: "104", Name: "OA", CredentialsEncrypted: []byte("fixture"), Metadata: "{}"},
		},
		&[]models.Conversation{
			{ID: "chat-a", TenantID: tenant, ChannelID: "personal-a", CustomerName: "Khách A", ExternalConversationID: "group:9001", Metadata: "{}"},
			{ID: "chat-b", TenantID: tenant, ChannelID: "personal-b", CustomerName: "Khách A", ExternalConversationID: "group:9001", Metadata: "{}"},
			{ID: "chat-oa", TenantID: tenant, ChannelID: "personal-a", CustomerName: "Nhóm OA", ExternalConversationID: "group:9002", Metadata: "{}"},
			{ID: "chat-archive", TenantID: tenant, ChannelID: "personal-b", CustomerName: "Nhóm lưu trữ", ExternalConversationID: "group:9003", Metadata: "{}"},
			{ID: "chat-direct", TenantID: tenant, ChannelID: "personal-a", CustomerName: "Tin riêng", ExternalConversationID: "user:9001", Metadata: "{}"},
			{ID: "chat-other", TenantID: other, ChannelID: "personal-other", CustomerName: "Khách khác tenant", ExternalConversationID: "group:9001", Metadata: "{}"},
		},
		&[]models.CRMGroup{
			{ID: "already-oa", TenantID: tenant, ChannelID: "group-oa", ZaloGroupID: "9002", Name: "CRM OA", CustomerCode: "S084"},
			{ID: "archived", TenantID: tenant, ChannelID: "personal-b", ZaloGroupID: "9003", Name: "Đã lưu trữ", CustomerCode: "S085", ArchivedAt: gorm.DeletedAt{Time: time.Now(), Valid: true}},
		},
	}
	for _, rows := range fixtures {
		if err := database.Create(rows).Error; err != nil {
			t.Fatal(err)
		}
	}

	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("tenant_id", tenant) })
	router.GET("/groups/personal", ListUnlinkedPersonalZaloCRMConversations)
	router.POST("/groups", CreateCRMGroup)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	post := func(id string) *httptest.ResponseRecorder {
		return request(http.MethodPost, "/groups", fmt.Sprintf(`{"name":"Nhóm cửa hàng","customer_codes":["S042","S043"],"personal_conversation_id":%q}`, id))
	}
	res := request(http.MethodGet, "/groups/personal", "")
	var available []personalZaloCRMConversation
	if err := json.Unmarshal(res.Body.Bytes(), &available); res.Code != http.StatusOK || err != nil || len(available) != 2 || available[0].ID != "chat-a" || available[1].ID != "chat-b" {
		t.Fatalf("wrong unlinked group options: %d %s (%v)", res.Code, res.Body, err)
	}
	for id, want := range map[string]int{"chat-oa": http.StatusConflict, "chat-archive": http.StatusConflict, "chat-direct": http.StatusNotFound, "chat-other": http.StatusNotFound} {
		if res := post(id); res.Code != want {
			t.Fatalf("link %s returned %d, want %d: %s", id, res.Code, want, res.Body)
		}
	}

	var wg sync.WaitGroup
	results := make(chan int, 2)
	for _, id := range []string{"chat-a", "chat-b"} {
		wg.Go(func() { results <- post(id).Code })
	}
	wg.Wait()
	statuses := map[int]int{<-results: 1}
	statuses[<-results]++
	if statuses[http.StatusCreated] != 1 || statuses[http.StatusConflict] != 1 {
		t.Fatalf("duplicate link results: %v", statuses)
	}

	var group models.CRMGroup
	if err := database.Where("tenant_id = ? AND zalo_group_id = ?", tenant, "9001").Take(&group).Error; err != nil {
		t.Fatal(err)
	}
	if group.CustomerCode != "S042" || len(db.GetGroupCustomerCodes(tenant, []string{group.ID})) != 2 {
		t.Fatalf("wrong customer identity: %+v", group)
	}
	for _, test := range []struct {
		groupID string
		blocked bool
	}{{"9001", false}, {"9002", true}, {"9003", true}} {
		if blocked, err := personalZaloGroupBlocked(t.Context(), tenant, test.groupID); err != nil || blocked != test.blocked {
			t.Errorf("personal bot group %s: blocked=%t, err=%v", test.groupID, blocked, err)
		}
	}
	if res := request(http.MethodGet, "/groups/personal", ""); res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != "[]" {
		t.Fatalf("linked group is still selectable: %d %s", res.Code, res.Body)
	}
}

func TestCRMDiscoversPersonalZaloGroupsWithoutMessagesMySQL(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(&models.CRMGroup{}, &models.CRMGroupCustomerCode{}, &models.PersonalZaloInternbotRoster{},
		&models.PersonalZaloThreadApproval{}, &models.Conversation{}, &models.ActivityLog{}); err != nil {
		t.Fatal(err)
	}
	const tenant, other = "roster-crm", "roster-other"
	createFacebookOAuthTenant(t, database, tenant)
	createFacebookOAuthTenant(t, database, other)
	channels := []models.Channel{
		{ID: "personal-a", TenantID: tenant, ChannelType: "personal_zalo_import", ExternalID: "101", Name: "Zalo A", Metadata: `{"sync_scope":"group"}`},
		{ID: "personal-b", TenantID: tenant, ChannelType: "personal_zalo_import", ExternalID: "102", Name: "Zalo B", Metadata: "{}"},
		{ID: "personal-other", TenantID: other, ChannelType: "personal_zalo_import", ExternalID: "103", Name: "Other", Metadata: "{}"},
		{ID: "personal-direct", TenantID: tenant, ChannelType: "personal_zalo_import", ExternalID: "104", Name: "1:1", Metadata: `{"sync_scope":"direct"}`},
		{ID: "oa", TenantID: tenant, ChannelType: "zalo_oa", ExternalID: "105", Name: "OA", Metadata: "{}"},
	}
	for i := range channels {
		channels[i].CredentialsEncrypted = []byte("fixture")
	}
	if err := database.Create(&channels).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&models.CRMGroup{ID: "already-oa", TenantID: tenant, ChannelID: "oa", ZaloGroupID: "9002", Name: "OA", CustomerCode: "S084"}).Error; err != nil {
		t.Fatal(err)
	}
	events, err := realtime.Subscribe(t.Context(), tenant)
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("tenant_id", tenant)
		c.Set("tenant_role", c.GetHeader("Test-Role"))
		c.Set("internal_request_id", "fixture")
	})
	router.POST("/roster", IngestPersonalZaloRoster)
	router.GET("/groups/personal", middleware.RequireRole("owner", "admin"), ListUnlinkedPersonalZaloCRMConversations)
	router.POST("/groups", middleware.RequireRole("owner", "admin"), CreateCRMGroup)
	request := func(method, path, role, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Test-Role", role)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != want {
			t.Fatalf("%s %s got %d, want %d: %s", method, path, res.Code, want, res.Body)
		}
		return res
	}
	start := time.Now().UTC().Truncate(time.Millisecond).Add(-time.Second)
	push := func(channel models.Channel, at time.Time, entries ...personalZaloRosterEntry) {
		t.Helper()
		body, err := json.Marshal(gin.H{
			"request_id": "fixture", "tenant_id": channel.TenantID, "channel_id": channel.ID,
			"account_external_id": channel.ExternalID, "captured_at": at, "entries": append([]personalZaloRosterEntry{}, entries...),
		})
		if err != nil {
			t.Fatal(err)
		}
		request(http.MethodPost, "/roster", "", string(body), http.StatusNoContent)
	}
	link := func(channelID, groupID string, want int) {
		t.Helper()
		body := fmt.Sprintf(`{"name":"Nhóm mới","customer_codes":["S084"],"channel_id":%q,"personal_zalo_group_id":%q}`, channelID, groupID)
		request(http.MethodPost, "/groups", "admin", body, want)
	}
	list := func(query string) []personalZaloCRMRosterGroup {
		t.Helper()
		res := request(http.MethodGet, "/groups/personal?roster=1"+query, "admin", "", http.StatusOK)
		var groups []personalZaloCRMRosterGroup
		if err := json.Unmarshal(res.Body.Bytes(), &groups); err != nil {
			t.Fatalf("invalid groups payload: %s (%v)", res.Body, err)
		}
		return groups
	}
	request(http.MethodGet, "/groups/personal?roster=1", "member", "", http.StatusForbidden)
	request(http.MethodPost, "/roster", "", `{"request_id":"fixture","tenant_id":"roster-crm","channel_id":"personal-a","account_external_id":"101","check_only":true}`, http.StatusOK)
	newGroup := personalZaloRosterEntry{"group", "9001", "Nhóm mới chưa nhắn"}
	oaGroup := personalZaloRosterEntry{"group", "9002", "S084"}
	push(channels[0], start, newGroup, oaGroup, personalZaloRosterEntry{"user", "9008", "Bạn bè"})
	select {
	case evt := <-events:
		if evt != realtime.EventCRMPersonalZaloGroupsUpdated {
			t.Fatalf("unexpected SSE event: %s", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("missing CRM roster SSE event")
	}
	push(channels[1], start, personalZaloRosterEntry{"group", "9001", ""}, personalZaloRosterEntry{"group", "9003", "Nhóm sẽ rời"})
	push(channels[2], start, personalZaloRosterEntry{"group", "9004", "Nhóm tenant khác"})
	push(channels[3], start, personalZaloRosterEntry{"group", "9005", "Nhóm chỉ 1:1"})
	if groups := list(""); len(groups) != 2 {
		t.Fatalf("expected two unique unlinked groups: %+v", groups)
	}
	if groups := list("&search=9001"); len(groups) != 1 || groups[0] != (personalZaloCRMRosterGroup{"9001", newGroup.DisplayName, "personal-a", "Zalo A"}) {
		t.Fatalf("unexpected group search result: %+v", groups)
	}
	link("personal-a", "9002", http.StatusConflict)
	link("personal-other", "9004", http.StatusNotFound)
	link("personal-direct", "9005", http.StatusNotFound)
	link("personal-a", "9999", http.StatusNotFound)
	push(channels[1], start.Add(time.Millisecond), personalZaloRosterEntry{"group", "9001", ""})
	push(channels[1], start, personalZaloRosterEntry{"group", "9003", "Stale roster"})
	link("personal-b", "9003", http.StatusNotFound)
	link("personal-a", "9001", http.StatusCreated)
	link("personal-b", "9001", http.StatusConflict)
	if groups := list(""); len(groups) != 0 {
		t.Fatalf("linked or departed groups were listed: %+v", groups)
	}
	var group models.CRMGroup
	if err := database.Where("tenant_id = ? AND zalo_group_id = ?", tenant, "9001").Take(&group).Error; err != nil || group.ChannelID != "personal-a" || group.CustomerCode != "S084" {
		t.Fatalf("group did not save the customer: %+v, %v", group, err)
	}
	oaCreate := models.CRMGroup{ID: "inflight-oa", TenantID: tenant, ChannelID: "oa", ZaloGroupID: "9001", Name: "OA",
		ZaloAssetID: "fixture-asset", ZaloGroupLink: "fixture-link", CustomerCode: "S089"}
	if err := saveZaloOACRMGroup(t.Context(), &oaCreate, []string{"S089"}); err != nil {
		t.Fatal(err)
	}
	var sameGroups []models.CRMGroup
	if err := database.Where("tenant_id = ? AND zalo_group_id = ?", tenant, "9001").Find(&sameGroups).Error; err != nil ||
		len(sameGroups) != 1 || sameGroups[0].ID != group.ID || sameGroups[0].CustomerCode != "S084" ||
		sameGroups[0].ChannelID != "oa" || sameGroups[0].ZaloAssetID != "fixture-asset" || oaCreate.ChannelID != "oa" {
		t.Fatalf("delayed OA creation did not keep the same CRM customer and take ownership: %+v, %v", sameGroups, err)
	}
	if blocked, err := personalZaloGroupBlocked(t.Context(), tenant, "9001"); err != nil || !blocked {
		t.Fatalf("personal bot should defer to OA after ownership is recorded: blocked=%t, %v", blocked, err)
	}
	for _, model := range []any{&models.Conversation{}, &models.PersonalZaloThreadApproval{}} {
		var count int64
		if err := database.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("discovery created %d records in %T: %v", count, model, err)
		}
	}
}
