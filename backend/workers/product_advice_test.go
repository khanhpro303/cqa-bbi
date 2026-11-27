package workers

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestProductAdviceSearchTerms(t *testing.T) {
	words, codes := adviceSearchTerms("Em tư vấn mũ LS2 FF399 đi đường dài, FF399 có chống nắng không?")
	for _, word := range []string{"mu", "duong", "chong", "nang"} {
		if !slices.Contains(words, word) {
			t.Errorf("missing %q in %v", word, words)
		}
	}
	if !slices.Equal(codes, []string{"ls2", "ff399"}) {
		t.Errorf("unexpected model codes %v", codes)
	}
	if !adviceIsFollowup("Cho em xem hình mẫu này") || adviceIsFollowup("Cho em xem hình FF399") || adviceIsFollowup("Anh hỏi công nợ") {
		t.Fatal("unexpected follow-up decision")
	}
}

func TestProductAdviceContextAndImageChoice(t *testing.T) {
	profile := productAdvice{
		ProductAdvisory: models.ProductAdvisory{ParentCode: "MODEL1", WebName: "Mũ bảo hiểm", Notes: "Kính trong\nKính màu", ScrapedContent: "Chi tiết từ website"},
		Images: []models.ProductAdvisoryImage{
			{ID: "front", ProductAdvisoryID: "model", Caption: "Mặt trước", URL: "https://example.com/front.png"},
			{ID: "side", ProductAdvisoryID: "model", Caption: "Nhìn ngang"},
		},
	}
	prompt, options := productAdviceContext([]productAdvice{profile}, true)
	for _, expected := range []string{"MODEL1", "Kính trong Kính màu", `Ảnh 2: "Nhìn ngang"`, "tra bằng ERP"} {
		if !strings.Contains(prompt, expected) {
			t.Errorf("prompt missing %q", expected)
		}
	}
	if strings.Contains(prompt, "example.com") {
		t.Fatal("image destination exposed to model")
	}
	if context, images := productAdviceContext([]productAdvice{profile}, false); strings.Contains(context, "Ảnh 1:") || len(images) != 0 {
		t.Fatal("turn without media capability got image choices")
	}
	for _, tt := range []struct {
		name, reply, wantText, wantImage string
	}{
		{"single listed option", "Dạ đây là góc ngang.\n[PRODUCT_IMAGE:2]", "Dạ đây là góc ngang.", "side"},
		{"unlisted option", "Dạ đây là ảnh.\n[PRODUCT_IMAGE:3]", "Dạ đây là ảnh.", ""},
		{"inline", "Dạ đây là ảnh. [PRODUCT_IMAGE:2]", "Dạ đây là ảnh.", ""},
		{"multiple", "Dạ đây là ảnh.\n[PRODUCT_IMAGE:1]\n[PRODUCT_IMAGE:2]", "Dạ đây là ảnh.", ""},
		{"no accompanying text", "[PRODUCT_IMAGE:2]", "", ""},
		{"ordinary reply", "Dạ, em gửi thông số.", "Dạ, em gửi thông số.", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			text, image := parseAdviceImage(tt.reply, options)
			if text != tt.wantText || image.ID != tt.wantImage {
				t.Fatalf("got (%q, %q), want (%q, %q)", text, image.ID, tt.wantText, tt.wantImage)
			}
		})
	}
}

func TestFindProductAdviceMySQL(t *testing.T) {
	dsn := os.Getenv("CQA_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("CQA_TEST_MYSQL_DSN not set")
	}
	testDB, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal("connect to test MySQL")
	}
	if err := testDB.AutoMigrate(&models.CachedProduct{}, &models.ProductAdvisory{}, &models.ProductAdvisoryImage{}); err != nil {
		t.Fatal(err)
	}
	previous := db.DB
	db.DB = testDB
	t.Cleanup(func() { db.DB = previous })
	tenant, otherTenant := pkg.NewUUID(), pkg.NewUUID()
	t.Cleanup(func() {
		for _, model := range []any{&models.ProductAdvisoryImage{}, &models.ProductAdvisory{}, &models.CachedProduct{}} {
			if err := testDB.Where("tenant_id IN ?", []string{tenant, otherTenant}).Delete(model).Error; err != nil {
				t.Error(err)
			}
		}
		if sqlDB, err := testDB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	good, hidden, unrelated, goby := pkg.NewUUID(), pkg.NewUUID(), pkg.NewUUID(), pkg.NewUUID()
	rows := []models.CachedProduct{
		{ID: pkg.NewUUID(), TenantID: tenant, MA: "SKU901", MA_CHA: "PARENT901", TEN_DONG_BO_WEB: "Mũ bảo hiểm LS2 FF399", NHAN_HIEU_NAME: "LS2", LIST_TEN_NHOM_VTHH: "Mũ"},
		{ID: pkg.NewUUID(), TenantID: tenant, MA: "SKU902", MA_CHA: "PARENT902", TEN_DONG_BO_WEB: "Mũ bảo hiểm MT FF399", NHAN_HIEU_NAME: "MT", LIST_TEN_NHOM_VTHH: "Mũ"},
		{ID: pkg.NewUUID(), TenantID: otherTenant, MA: "SKU901", MA_CHA: "PARENT901", TEN_DONG_BO_WEB: "Mũ bảo hiểm LS2 FF399", NHAN_HIEU_NAME: "LS2", LIST_TEN_NHOM_VTHH: "Mũ"},
		{ID: pkg.NewUUID(), TenantID: tenant, MA: "SP457662", MA_CHA: "GOBY", TEN_DONG_BO_WEB: "Giày bảo hộ LS2 Goby Man", NHAN_HIEU_NAME: "LS2", LIST_TEN_NHOM_VTHH: "Giày"},
	}
	profiles := []models.ProductAdvisory{
		{ID: good, TenantID: tenant, ParentCode: "PARENT901", WebName: rows[0].TEN_DONG_BO_WEB, Notes: "Vỏ nhẹ"},
		{ID: hidden, TenantID: tenant, ParentCode: "PARENT902", WebName: rows[1].TEN_DONG_BO_WEB, Notes: "Vỏ nhẹ"},
		{ID: unrelated, TenantID: otherTenant, ParentCode: "PARENT901", WebName: rows[2].TEN_DONG_BO_WEB, Notes: "Dữ liệu tenant khác"},
		{ID: goby, TenantID: tenant, ParentCode: "GOBY", WebName: rows[3].TEN_DONG_BO_WEB, ScrapedContent: gobyProductDetails},
	}
	photos := []models.ProductAdvisoryImage{
		{ID: pkg.NewUUID(), TenantID: tenant, ProductAdvisoryID: good, Enabled: true},
		{ID: pkg.NewUUID(), TenantID: tenant, ProductAdvisoryID: good, Enabled: true},
	}
	for _, seed := range []any{&rows, &profiles, &photos} {
		if err := testDB.Create(seed).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := testDB.Model(&photos[1]).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	perm := engine.GroupPermissionContext{TenantID: tenant}
	perm.Groups = []engine.GroupPermission{{Resources: []engine.ResourcePermission{{Resource: "products", IsEnabled: true, Brands: []string{"ls2"}, ProductGroups: []string{"mũ"}}}}}
	for _, query := range []string{"SKU901", "tư vấn mũ FF399"} {
		found, err := findProductAdvice(context.Background(), &perm, query)
		if err != nil {
			t.Fatal(err)
		}
		if len(found) != 1 || found[0].ID != good || len(found[0].Images) != 1 || found[0].Images[0].ID != photos[0].ID {
			t.Fatalf("query %q returned unexpected profiles: %+v", query, found)
		}
	}
	perm.Groups[0].Resources[0].ProductGroups = []string{"giày"}
	found, err := productAdviceForTurn(context.Background(), &perm, gobyQuestion, nil)
	if err != nil || len(found) != 1 || found[0].ID != goby {
		t.Fatalf("OA lookup for %q returned %d profiles, error: %v", gobyQuestion, len(found), err)
	}
	oaContext, _ := productAdviceContext(found, false)
	personalContext, err := PersonalZaloProductAdvice(context.Background(), &perm, gobyQuestion)
	if err != nil || oaContext != personalContext || !strings.Contains(oaContext, "CE EN 13634:2017 — 2|2|2|2 WR") {
		t.Fatalf("OA and Personal Zalo must get the same Goby source evidence (error: %v)", err)
	}
	perm.Groups[0].Resources[0].ProductGroups = []string{"mũ"}
	if denied, err := PersonalZaloProductAdvice(context.Background(), &perm, gobyQuestion); err != nil || denied != "" {
		t.Fatalf("Goby context outside the personal Zalo product scope: %q (%v)", denied, err)
	}
}
