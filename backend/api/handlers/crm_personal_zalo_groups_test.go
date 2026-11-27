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
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
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
