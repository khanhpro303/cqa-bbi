package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/realtime"
	"gorm.io/gorm"
)

func crmLifecycleCall(t *testing.T, h gin.HandlerFunc, method, target, groupID string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	c.Params = gin.Params{{Key: "id", Value: groupID}}
	c.Set("tenant_id", "tenant-a")
	h(c)
	return w
}

func countRows(t *testing.T, q *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := q.Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// Archive hides the group from every normal lookup but keeps its links and
// history; permanent delete removes only the group's config rows.
func TestMySQLCRMGroupArchiveRestoreDelete(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(
		&models.ZaloWhitelist{}, &models.ZaloCustomer{}, &models.CRMGroup{}, &models.CRMGroupCustomerCode{},
		&models.ERPEndpoint{}, &models.ERPGroupRateLimit{}, &models.Campaign{}, &models.CampaignSegment{},
		&models.OrderCart{}, &models.OrderAction{}, &models.ChatSession{}, &models.ActivityLog{},
	); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	createFacebookOAuthTenant(t, database, "tenant-a")
	openKey := "work-1"
	seed := []any{
		&models.Channel{ID: "ch-1", TenantID: "tenant-a", ChannelType: "zalo_oa", Name: "OA chính", ExternalID: "oa-1", CredentialsEncrypted: []byte("x"),
			Metadata: `{"handover_alert_group_id":"grp-1"}`, CreatedAt: now, UpdatedAt: now},
		&models.ZaloWhitelist{ID: "wl-1", TenantID: "tenant-a", ChannelID: "ch-1", ZaloUserID: "staff-1", Status: "active", CreatedAt: now, UpdatedAt: now},
		&models.ZaloCustomer{ID: "cus-1", TenantID: "tenant-a", ZaloUserID: "cust-1", Name: "Khách", Status: "approved", CustomerCode: "KH01", CreatedAt: now, UpdatedAt: now},
		&models.CRMGroup{ID: "grp-1", TenantID: "tenant-a", Name: "Shop KH01", ZaloGroupID: "zg-1", ChannelID: "ch-1", CustomerCode: "KH01", CreatedAt: now, UpdatedAt: now},
		&models.CRMGroupEmployee{GroupID: "grp-1", ZaloWhitelistID: "wl-1"},
		&models.CRMGroupCustomer{GroupID: "grp-1", ZaloCustomerID: "cus-1"},
		&models.CRMGroupCustomerCode{ID: "gcc-1", TenantID: "tenant-a", GroupID: "grp-1", CustomerCode: "KH01", IsPrimary: true, CreatedAt: now, UpdatedAt: now},
		&models.ERPEndpoint{ID: "ep-1", TenantID: "tenant-a", GroupID: "grp-1", Resource: "orders", IsEnabled: true, CreatedAt: now, UpdatedAt: now},
		&models.ERPGroupRateLimit{ID: "rl-1", TenantID: "tenant-a", GroupID: "grp-1", DateKey: "2026-11-19", CallCount: 3, UpdatedAt: now},
		&models.Campaign{ID: "camp-1", TenantID: "tenant-a", Name: "Nhắc nợ", ChannelID: "ch-1", Status: "active", CreatedAt: now, UpdatedAt: now},
		&models.CampaignSegment{ID: "seg-1", CampaignID: "camp-1", GroupID: "grp-1"},
		&models.OrderCart{ID: "cart-1", TenantID: "tenant-a", OpenKey: &openKey, Status: "open", ZaloGroupID: "zg-1", CustomerCode: "KH01", ExpiresAt: now.Add(time.Hour)},
		&models.OrderAction{ID: "act-1", TenantID: "tenant-a", Kind: "create", Status: "pending", ZaloGroupID: "zg-1", CustomerCode: "KH01", ExpiresAt: now.Add(time.Hour)},
		&models.ChatSession{ID: "sess-1", TenantID: "tenant-a", ChannelID: "ch-1", SubjectType: "group", SubjectID: "zg-1", ZaloGroupID: "zg-1",
			CustomerCode: "KH01", StartedAt: now, LastActivityAt: now, Status: "summarized", Summary: "Khách hỏi giá", Intents: "[]", Products: "[]", Issues: "[]", CreatedAt: now, UpdatedAt: now},
	}
	for _, row := range seed {
		if err := database.Create(row).Error; err != nil {
			t.Fatalf("seed %T: %v", row, err)
		}
	}

	// The group receives the channel's handover alerts: archiving is refused.
	if w := crmLifecycleCall(t, ArchiveCRMGroup, http.MethodPost, "/archive", "grp-1"); w.Code != http.StatusConflict {
		t.Fatalf("archive alert target: code=%d body=%s", w.Code, w.Body.String())
	}
	if err := database.Model(&models.Channel{}).Where("id = ?", "ch-1").Update("metadata", "{}").Error; err != nil {
		t.Fatal(err)
	}
	// Permanent delete needs an archived group.
	if w := crmLifecycleCall(t, DeleteCRMGroup, http.MethodDelete, "/", "grp-1"); w.Code != http.StatusConflict {
		t.Fatalf("delete active group: code=%d body=%s", w.Code, w.Body.String())
	}

	if w := crmLifecycleCall(t, ArchiveCRMGroup, http.MethodPost, "/archive", "grp-1"); w.Code != http.StatusOK {
		t.Fatalf("archive: code=%d body=%s", w.Code, w.Body.String())
	}
	if n := countRows(t, database.Model(&models.CRMGroup{}).Where("id = ?", "grp-1")); n != 0 {
		t.Fatal("archived group still visible to normal queries")
	}
	if n := countRows(t, database.Model(&models.CRMGroupCustomer{}).Where("zalo_customer_id = ? AND group_id IN (?)", "cus-1", db.ActiveCRMGroupIDs())); n != 0 {
		t.Fatal("archived group's member link still resolves as active")
	}
	if n := countRows(t, database.Model(&models.CRMGroupCustomer{}).Where("group_id = ?", "grp-1")); n != 1 {
		t.Fatal("archive must keep member links for restore")
	}
	var cart models.OrderCart
	database.First(&cart, "id = ?", "cart-1")
	if cart.Status != "cancelled" || cart.OpenKey != nil {
		t.Fatalf("open cart not cancelled: %+v", cart)
	}
	var action models.OrderAction
	database.First(&action, "id = ?", "act-1")
	if action.Status != "declined" {
		t.Fatalf("pending action not declined: %s", action.Status)
	}

	w := crmLifecycleCall(t, ListCRMGroups, http.MethodGet, "/?archived=1", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"id":"grp-1"`) {
		t.Fatalf("archive list missing group: %s", w.Body.String())
	}
	if w := crmLifecycleCall(t, ListCRMGroups, http.MethodGet, "/", ""); strings.Contains(w.Body.String(), `"id":"grp-1"`) {
		t.Fatal("active list shows archived group")
	}

	if w := crmLifecycleCall(t, RestoreCRMGroup, http.MethodPost, "/restore", "grp-1"); w.Code != http.StatusOK {
		t.Fatalf("restore: code=%d body=%s", w.Code, w.Body.String())
	}
	if n := countRows(t, database.Model(&models.CRMGroup{}).Where("id = ?", "grp-1")); n != 1 {
		t.Fatal("restored group not visible")
	}

	if w := crmLifecycleCall(t, ArchiveCRMGroup, http.MethodPost, "/archive", "grp-1"); w.Code != http.StatusOK {
		t.Fatalf("re-archive: code=%d body=%s", w.Code, w.Body.String())
	}
	if w := crmLifecycleCall(t, DeleteCRMGroup, http.MethodDelete, "/", "grp-1"); w.Code != http.StatusOK {
		t.Fatalf("delete: code=%d body=%s", w.Code, w.Body.String())
	}
	for _, q := range []*gorm.DB{
		database.Unscoped().Model(&models.CRMGroup{}).Where("id = ?", "grp-1"),
		database.Model(&models.CRMGroupEmployee{}).Where("group_id = ?", "grp-1"),
		database.Model(&models.CRMGroupCustomer{}).Where("group_id = ?", "grp-1"),
		database.Model(&models.CRMGroupCustomerCode{}).Where("group_id = ?", "grp-1"),
		database.Model(&models.ERPEndpoint{}).Where("group_id = ?", "grp-1"),
		database.Model(&models.ERPGroupRateLimit{}).Where("group_id = ?", "grp-1"),
		database.Model(&models.CampaignSegment{}).Where("group_id = ?", "grp-1"),
	} {
		if n := countRows(t, q); n != 0 {
			t.Fatalf("config row left after permanent delete: %v", q.Statement.Table)
		}
	}
	// History stays.
	for _, q := range []*gorm.DB{
		database.Model(&models.ChatSession{}).Where("id = ?", "sess-1"),
		database.Model(&models.OrderCart{}).Where("id = ?", "cart-1"),
		database.Model(&models.OrderAction{}).Where("id = ?", "act-1"),
		database.Model(&models.Campaign{}).Where("id = ?", "camp-1"),
		database.Model(&models.ZaloCustomer{}).Where("id = ?", "cus-1"),
	} {
		if n := countRows(t, q); n != 1 {
			t.Fatalf("history row removed by permanent delete: %v", q.Statement.Table)
		}
	}
}

func TestMySQLAddGroupCustomersPublishesCommittedMembership(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(&models.ZaloCustomer{}, &models.CRMGroup{}, &models.CRMGroupCustomerCode{}); err != nil {
		t.Fatal(err)
	}
	createFacebookOAuthTenant(t, database, "tenant-a")
	channel := models.Channel{
		ID: "ch-1", TenantID: "tenant-a", ChannelType: "zalo_oa", Name: "OA thử",
		ExternalID: "oa-1", CredentialsEncrypted: []byte("fixture"), Metadata: "{}",
	}
	if err := database.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&models.CRMGroup{ID: "grp-1", TenantID: "tenant-a", Name: "Nhóm thử", ChannelID: channel.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&models.ZaloCustomer{ID: "cus-1", TenantID: "tenant-a", Name: "Khách thử", Status: "approved"}).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates, err := realtime.Subscribe(ctx, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	otherTenant, err := realtime.Subscribe(ctx, "tenant-b")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPost, "/groups/grp-1/members", strings.NewReader(`{"customer_ids":["cus-1"]}`))
	c.Params = gin.Params{{Key: "id", Value: "grp-1"}}
	c.Set("tenant_id", "tenant-a")
	AddGroupMembers(c)
	if response.Code != http.StatusOK {
		t.Fatalf("add members: %d %s", response.Code, response.Body)
	}
	select {
	case event := <-updates:
		if event != realtime.EventCRMGroupMembersUpdated {
			t.Fatalf("unexpected event %q", event)
		}
	default:
		t.Fatal("adding a CRM member must publish a tenant update")
	}
	select {
	case event := <-otherTenant:
		t.Fatalf("another tenant received %q", event)
	default:
	}
	if n := countRows(t, database.Model(&models.CRMGroupCustomer{}).Where("group_id = ? AND zalo_customer_id = ?", "grp-1", "cus-1")); n != 1 {
		t.Fatalf("expected one committed group member, got %d", n)
	}
}

func TestChannelAlertsToGroup(t *testing.T) {
	cases := []struct {
		meta string
		want bool
	}{
		{`{"handover_alert_group_id":"g1"}`, true},
		{`{"handover_alert_group_id":"g2"}`, false},
		{`{"campaign_alerts_enabled":true,"campaign_alert_group_id":"g1"}`, true},
		{`{"campaign_alerts_enabled":false,"campaign_alert_group_id":"g1"}`, false},
		{`{}`, false},
		{`not json`, false},
		{``, false},
	}
	for _, tc := range cases {
		if got := channelAlertsToGroup(tc.meta, "g1"); got != tc.want {
			t.Errorf("channelAlertsToGroup(%q) = %v, want %v", tc.meta, got, tc.want)
		}
	}
}
