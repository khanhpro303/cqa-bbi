package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestCustomerCasesKeepStreamsAndSourceConversationsScopedMySQL(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(&models.CustomerCase{}, &models.CustomerCaseEvent{}, &models.CRMGroup{}, &models.Conversation{}, &models.CustomerCaseAlert{}, &models.ActivityLog{}); err != nil {
		t.Fatal(err)
	}
	createFacebookOAuthTenant(t, database, "case-views")
	createFacebookOAuthTenant(t, database, "other-tenant")
	channels := []models.Channel{
		{ID: "oa", TenantID: "case-views", ChannelType: "zalo_oa", Name: "OA BBI", CredentialsEncrypted: []byte("test"),
			Metadata: `{"complaint_streams":[{"key":"warranty","name":"Bảo hành OA"},{"key":"oa_only","name":"Vận chuyển OA"}]}`},
		{ID: "personal", TenantID: "case-views", ChannelType: "personal_zalo_import", Name: "Zalo BBI", CredentialsEncrypted: []byte("test"),
			Metadata: `{"complaint_streams":[{"key":"warranty","name":"Bảo hành cá nhân"},{"key":"personal_only","name":"Đổi hàng"}]}`},
		{ID: "other-oa", TenantID: "other-tenant", ChannelType: "zalo_oa", Name: "Other OA", CredentialsEncrypted: []byte("test"), Metadata: `{}`},
	}
	fixtures := []any{
		&channels,
		&[]models.CustomerCase{
			{ID: "case-oa", TenantID: "case-views", ChannelID: "oa", SubjectType: "group", SubjectID: "group-id", Stream: "warranty", Images: "[]"},
			{ID: "case-personal", TenantID: "case-views", ChannelID: "personal", SubjectType: "user", SubjectID: "sender", ZaloGroupID: "group-id", Private: true, Stream: "warranty", Images: "[]"},
			{ID: "case-historical", TenantID: "case-views", ChannelID: "personal", SubjectType: "user", SubjectID: "sender", Stream: "retired", Images: "[]"},
			{ID: "case-other", TenantID: "other-tenant", ChannelID: "other-oa", Stream: "warranty", Images: "[]"},
		},
		&[]models.Conversation{
			{ID: "conv-oa", TenantID: "case-views", ChannelID: "oa", ExternalConversationID: "group-id", CustomerName: "Nhóm OA", Metadata: `{}`},
			{ID: "conv-personal", TenantID: "case-views", ChannelID: "personal", ExternalConversationID: "group:group-id", CustomerName: "Nhóm cá nhân", Metadata: `{}`},
			{ID: "conv-direct", TenantID: "case-views", ChannelID: "personal", ExternalConversationID: "user:sender", CustomerName: "Người báo", Metadata: `{}`},
			{ID: "conv-other", TenantID: "other-tenant", ChannelID: "other-oa", ExternalConversationID: "group:group-id", CustomerName: "Other group", Metadata: `{}`},
		},
		&models.CustomerCaseAlert{ID: "alert-personal", TenantID: "case-views", CaseID: "case-personal", Kind: "created", Status: "failed", GroupName: "Nhóm bảo hành", Error: "delivery_failed"},
	}
	for _, fixture := range fixtures {
		if err := database.Create(fixture).Error; err != nil {
			t.Fatal(err)
		}
	}

	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("tenant_id", "case-views") })
	router.GET("/cases", ListCustomerCases)
	router.GET("/cases/:caseId", GetCustomerCase)
	router.PATCH("/cases/:caseId", UpdateCustomerCase)
	request := func(method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != status {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, res.Code, status, res.Body.String())
		}
		return res
	}

	var list struct {
		Data     []customerCaseView
		Channels []complaintChannel
		Streams  []complaintStreamOption
	}
	response := request(http.MethodGet, "/cases?status=all", "", http.StatusOK)
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Channels) != 2 || len(list.Data) != 3 || len(list.Streams) != 0 {
		t.Fatalf("unexpected unfiltered channels/cases/streams: %+v", list)
	}
	byID := make(map[string]customerCaseView)
	for _, row := range list.Data {
		byID[row.ID] = row
	}
	if row := byID["case-oa"]; row.StreamName != "Bảo hành OA" || row.SourceGroupName != "Nhóm OA" || row.ZaloGroupID != "group-id" || row.ConversationID != "conv-oa" {
		t.Fatalf("wrong OA case source or stream: %+v", row)
	}
	if row := byID["case-personal"]; row.StreamName != "Bảo hành cá nhân" || row.SourceGroupName != "Nhóm cá nhân" || row.ConversationID != "conv-personal" || !row.Private {
		t.Fatalf("wrong personal group case source, privacy or stream: %+v", row)
	}
	if row := byID["case-historical"]; row.SourceGroupName != "" || row.ConversationID != "conv-direct" {
		t.Fatalf("wrong personal direct case source: %+v", row)
	}

	request(http.MethodGet, "/cases?stream=warranty", "", http.StatusBadRequest)
	response = request(http.MethodGet, "/cases?channel_id=personal&stream=warranty", "", http.StatusOK)
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 1 || list.Data[0].ID != "case-personal" || len(list.Streams) != 3 || list.Streams[2].Key != "retired" {
		t.Fatalf("unexpected cases or filters for personal channel: %+v", list)
	}
	for _, stream := range list.Streams {
		if stream.ChannelID != "personal" || stream.Key == "oa_only" {
			t.Fatalf("stream from another channel leaked into selector: %+v", stream)
		}
	}

	response = request(http.MethodGet, "/cases/case-personal", "", http.StatusOK)
	var detail struct {
		Case    customerCaseView
		Streams []complaintStreamOption
		Alerts  []models.CustomerCaseAlert
	}
	if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Case.ConversationID != "conv-personal" || len(detail.Streams) != 2 || detail.Streams[1].Key != "personal_only" {
		t.Fatalf("wrong conversation or editable streams: %+v", detail)
	}
	if len(detail.Alerts) != 1 || detail.Alerts[0].Status != "failed" || detail.Alerts[0].GroupName != "Nhóm bảo hành" {
		t.Fatalf("wrong case alert history: %+v", detail.Alerts)
	}
	request(http.MethodGet, "/cases/case-other", "", http.StatusNotFound)
	request(http.MethodPatch, "/cases/case-personal", `{"stream":"oa_only"}`, http.StatusBadRequest)
	request(http.MethodPatch, "/cases/case-personal", `{"stream":"personal_only"}`, http.StatusOK)
}
