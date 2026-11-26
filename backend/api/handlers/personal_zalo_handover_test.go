package handlers

import (
	"context"
	"testing"

	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestPersonalZaloHandoverRequest(t *testing.T) {
	for _, text := range []string{"Nhân viên!", "Bot ơi, cho mình gặp nhân viên", "chuyển tiếp cho nhân viên", "Cho tôi nói chuyện với nhân viên"} {
		if !personalZaloHandoverRequested(text) {
			t.Errorf("expected handover for %q", text)
		}
	}
	for _, text := range []string{"Mũ cho nhân viên", "gọi bot kiểm tra đơn của nhân viên", "nhân viên kho làm đến mấy giờ"} {
		if personalZaloHandoverRequested(text) {
			t.Errorf("unexpected handover for %q", text)
		}
	}
}

func TestPersonalZaloChatbotMetadataValidation(t *testing.T) {
	for _, settings := range []map[string]any{
		{"session_timeout_minutes": 5.5},
		{"session_timeout_minutes": float64(1441)},
		{"personal_zalo_group_reply_enabled": "true"},
		{"session_keyword": "Xin chào; Gọi bot", "session_end_keyword": "GỌI BOT"},
		{"handover_alert_group_id": "group", "personal_zalo_handover_staff_id": "staff"},
	} {
		if err := validatePersonalZaloChatbotMetadata(t.Context(), "tenant", settings); err == nil {
			t.Errorf("accepted invalid settings: %#v", settings)
		}
	}
	if err := validatePersonalZaloChatbotMetadata(t.Context(), "tenant", map[string]any{
		"sync_scope": "direct", "group_bot_names": "Bull;bot",
		"session_keyword": "Xin chào;Gọi bot", "session_end_keyword": "Tạm biệt",
		"session_timeout_minutes": float64(5), "personal_zalo_group_reply_enabled": false,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPersonalZaloHandoverDestinationsMySQL(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(&models.ZaloWhitelist{}, &models.ZaloCustomer{}, &models.CRMGroup{}, &models.CRMGroupCustomerCode{}); err != nil {
		t.Fatal(err)
	}
	createFacebookOAuthTenant(t, database, "source")
	createFacebookOAuthTenant(t, database, "different")
	oa := models.Channel{ID: "oa", TenantID: "source", Name: "OA", ChannelType: "zalo_oa",
		IsActive: true, CredentialsEncrypted: []byte("fixture"), Metadata: "{}"}
	if err := database.Create(&oa).Error; err != nil {
		t.Fatal(err)
	}
	groups := []models.CRMGroup{
		{ID: "staff-group", TenantID: "source", ChannelID: oa.ID, ZaloGroupID: "123", Name: "Staff"},
		{ID: "customer-group", TenantID: "source", ChannelID: oa.ID, ZaloGroupID: "456", Name: "Customer"},
	}
	if err := database.Create(&groups).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&models.CRMGroupCustomerCode{ID: "link", TenantID: "source", GroupID: "customer-group", CustomerCode: "KH-1"}).Error; err != nil {
		t.Fatal(err)
	}
	staff := models.ZaloWhitelist{ID: "staff", TenantID: "source", ChannelID: oa.ID, ZaloUserID: "789", Name: "Staff", Status: "active"}
	if err := database.Create(&staff).Error; err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	target, err := resolvePersonalZaloHandoverDestination(ctx, "source", "staff-group", "")
	if err != nil || !target.isGroup || target.id != "123" || target.channel.ID != oa.ID {
		t.Fatalf("unexpected group destination: %#v, %v", target, err)
	}
	target, err = resolvePersonalZaloHandoverDestination(ctx, "source", "", "staff")
	if err != nil || target.isGroup || target.id != "789" || target.channel.ID != oa.ID {
		t.Fatalf("unexpected staff destination: %#v, %v", target, err)
	}
	for _, tc := range []struct{ tenant, group, staff string }{
		{"different", "staff-group", ""}, {"source", "customer-group", ""}, {"different", "", "staff"},
	} {
		if _, err := resolvePersonalZaloHandoverDestination(ctx, tc.tenant, tc.group, tc.staff); err == nil {
			t.Errorf("accepted invalid recipient %#v", tc)
		}
	}
	if err := database.Delete(&groups[0]).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := resolvePersonalZaloHandoverDestination(ctx, "source", "staff-group", ""); err == nil {
		t.Fatal("accepted archived group")
	}
	if err := database.Model(&oa).Update("is_active", false).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := resolvePersonalZaloHandoverDestination(ctx, "source", "", "staff"); err == nil {
		t.Fatal("accepted staff on disabled OA")
	}
}
