package workers

import (
	"context"
	"errors"
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

func TestPersonalZaloProductAdviceImageChoice(t *testing.T) {
	images := []models.ProductAdvisoryImage{
		{ID: "front", ProductAdvisoryID: "goby", Caption: "Ảnh đại diện"},
		{ID: "sole", ProductAdvisoryID: "goby", Caption: "Đế giày"},
		{ID: "reflective", ProductAdvisoryID: "goby", Caption: "Chi tiết phản quang"},
	}
	profile := productAdvice{
		ProductAdvisory: models.ProductAdvisory{ID: "goby", ParentCode: "GOBY", WebName: "Giày bảo hộ LS2 Goby Man"},
		Brand:           "LS2",
		VariantCodes:    []string{"SP457662"},
		Images:          images,
	}
	prompt, candidates, err := personalZaloProductAdviceContext([]productAdvice{profile}, gobyQuestion)
	if err != nil || !slices.EqualFunc(candidates, images, func(a, b models.ProductAdvisoryImage) bool { return a.ID == b.ID }) {
		t.Fatalf("unexpected candidates: %+v (%v)", candidates, err)
	}
	for _, want := range []string{"chọn ít nhất ảnh đại diện 1", `Ảnh 1 (ảnh đại diện đã duyệt)`, `Ảnh 2: "Đế giày"`, "Có thể ghi nhiều dòng", "không gửi nhiều ảnh có chú thích chung chung hoặc trùng ý"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("personal Zalo prompt missing %q", want)
		}
	}

	text, chosen := ParsePersonalZaloProductAdviceImages("Dạ, Goby có phản quang.\n[PRODUCT_IMAGE:3]\n[PRODUCT_IMAGE:1]\n[PRODUCT_IMAGE:3]\n[PRODUCT_IMAGE:99]", candidates)
	if text != "Dạ, Goby có phản quang." || !slices.EqualFunc(chosen, []models.ProductAdvisoryImage{images[2], images[0]}, func(a, b models.ProductAdvisoryImage) bool { return a.ID == b.ID }) {
		t.Fatalf("got (%q, %+v)", text, chosen)
	}
	text, chosen = ParsePersonalZaloProductAdviceImages("Dạ, Goby có phản quang [PRODUCT_IMAGE:2]", candidates)
	if text != "Dạ, Goby có phản quang" || len(chosen) != 0 {
		t.Fatalf("inline marker selected an image: (%q, %+v)", text, chosen)
	}
	text, chosen = ParsePersonalZaloProductAdviceImages("Dạ, Goby có phản quang.\n[PRODUCT_IMAGE:x]\n[PRODUCT_IMAGE:2]", candidates)
	if text != "Dạ, Goby có phản quang." || len(chosen) != 1 || chosen[0].ID != "sole" {
		t.Fatalf("invalid trailing choice blocked valid choices: (%q, %+v)", text, chosen)
	}

	text, chosen = ParsePersonalZaloProductAdviceImages("Dạ đây là ảnh.\n[PRODUCT_IMAGE:1]\n[KB_IMAGE:cea6c2f1-40bc-4c42-a256-e2f676d24e9d]", candidates)
	if !strings.Contains(text, "Dạ đây là ảnh.") || len(chosen) != 1 || chosen[0].ID != "front" {
		t.Fatalf("knowledge marker blocked catalog image: (%q, %+v)", text, chosen)
	}

	other := productAdvice{
		ProductAdvisory: models.ProductAdvisory{ID: "other", ParentCode: "OTHER", WebName: "Mẫu khác"}, Brand: "LS2", Images: images,
	}
	_, ambiguous, err := personalZaloProductAdviceContext([]productAdvice{profile, other}, "Tư vấn sản phẩm")
	if err != nil || len(ambiguous) != 0 {
		t.Fatalf("ambiguous products exposed image choices: %+v (%v)", ambiguous, err)
	}
	resolvedPrompt, resolved, err := personalZaloProductAdviceContext([]productAdvice{other, profile}, "Giày Goby sao em")
	if err != nil || len(resolved) != len(images) || !strings.Contains(resolvedPrompt, `Dòng "Giày bảo hộ LS2 Goby Man"`) || strings.Contains(resolvedPrompt, `Dòng "Mẫu khác"`) {
		t.Fatalf("Goby name did not resolve the correct photo profile: %q %+v (%v)", resolvedPrompt, resolved, err)
	}
	_, wrongModel, err := personalZaloProductAdviceContext([]productAdvice{profile}, "Giày LS2 X khác sao em")
	if err != nil || len(wrongModel) != 0 {
		t.Fatalf("generic category or brand matched the wrong sole profile: %+v (%v)", wrongModel, err)
	}
	_, exactSKU, err := personalZaloProductAdviceContext([]productAdvice{profile}, "Cho tôi xem SP457662")
	if err != nil || len(exactSKU) != len(images) {
		t.Fatalf("exact SKU did not retain the product photos: %+v (%v)", exactSKU, err)
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
	good, hidden, unrelated, goby, otherShoe := pkg.NewUUID(), pkg.NewUUID(), pkg.NewUUID(), pkg.NewUUID(), pkg.NewUUID()
	rows := []models.CachedProduct{
		{ID: pkg.NewUUID(), TenantID: tenant, MA: "SKU901", MA_CHA: "PARENT901", TEN_DONG_BO_WEB: "Mũ bảo hiểm LS2 FF399", NHAN_HIEU_NAME: "LS2", LIST_TEN_NHOM_VTHH: "Mũ"},
		{ID: pkg.NewUUID(), TenantID: tenant, MA: "SKU902", MA_CHA: "PARENT902", TEN_DONG_BO_WEB: "Mũ bảo hiểm MT FF399", NHAN_HIEU_NAME: "MT", LIST_TEN_NHOM_VTHH: "Mũ"},
		{ID: pkg.NewUUID(), TenantID: otherTenant, MA: "SKU901", MA_CHA: "PARENT901", TEN_DONG_BO_WEB: "Mũ bảo hiểm LS2 FF399", NHAN_HIEU_NAME: "LS2", LIST_TEN_NHOM_VTHH: "Mũ"},
		{ID: pkg.NewUUID(), TenantID: tenant, MA: "SP457662", MA_CHA: "GOBY", TEN_DONG_BO_WEB: "Giày bảo hộ LS2 Goby Man", NHAN_HIEU_NAME: "LS2", LIST_TEN_NHOM_VTHH: "Giày"},
		{ID: pkg.NewUUID(), TenantID: tenant, MA: "SP457663", MA_CHA: "DARDO", TEN_DONG_BO_WEB: "Giày bảo hộ LS2 Dardo Man", NHAN_HIEU_NAME: "LS2", LIST_TEN_NHOM_VTHH: "Giày"},
	}
	profiles := []models.ProductAdvisory{
		{ID: good, TenantID: tenant, ParentCode: "PARENT901", WebName: rows[0].TEN_DONG_BO_WEB, Notes: "Vỏ nhẹ"},
		{ID: hidden, TenantID: tenant, ParentCode: "PARENT902", WebName: rows[1].TEN_DONG_BO_WEB, Notes: "Vỏ nhẹ"},
		{ID: unrelated, TenantID: otherTenant, ParentCode: "PARENT901", WebName: rows[2].TEN_DONG_BO_WEB, Notes: "Dữ liệu tenant khác"},
		{ID: goby, TenantID: tenant, ParentCode: "GOBY", WebName: rows[3].TEN_DONG_BO_WEB, ScrapedContent: gobyProductDetails},
		{ID: otherShoe, TenantID: tenant, ParentCode: "DARDO", WebName: rows[4].TEN_DONG_BO_WEB, ScrapedContent: "Giày Dardo có phần bảo vệ mắt cá."},
	}
	photos := []models.ProductAdvisoryImage{
		{ID: pkg.NewUUID(), TenantID: tenant, ProductAdvisoryID: good, Enabled: true},
		{ID: pkg.NewUUID(), TenantID: tenant, ProductAdvisoryID: good, Enabled: true},
		{ID: pkg.NewUUID(), TenantID: tenant, ProductAdvisoryID: goby, Source: "upload", Caption: "Ảnh đại diện", Data: []byte("front"), Enabled: true, SortOrder: 0},
		{ID: pkg.NewUUID(), TenantID: tenant, ProductAdvisoryID: goby, Source: "scraped", Caption: "Chi tiết phản quang", URL: "https://example.com/reflective.png", Enabled: true, SortOrder: 1},
		{ID: pkg.NewUUID(), TenantID: tenant, ProductAdvisoryID: goby, Source: "upload", Caption: "Chưa duyệt", Enabled: false, SortOrder: 2},
		{ID: pkg.NewUUID(), TenantID: tenant, ProductAdvisoryID: otherShoe, Source: "upload", Caption: "Ảnh Dardo", Data: []byte("dardo"), Enabled: true, SortOrder: 0},
	}
	for _, seed := range []any{&rows, &profiles, &photos} {
		if err := testDB.Create(seed).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.MigrateProductAdvisoryGroups(testDB); err != nil {
		t.Fatal(err)
	}
	if err := testDB.Model(&photos[1]).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	perm := engine.GroupPermissionContext{TenantID: tenant}
	perm.Groups = []engine.GroupPermission{{Resources: []engine.ResourcePermission{{Resource: "products", IsEnabled: true, Brands: []string{"ls2"}, ProductGroups: []string{"mũ"}}}}}
	for _, query := range []string{"SKU901", "tư vấn mũ FF399"} {
		found, err := findProductAdvice(context.Background(), &perm, query, adviceImageLimit)
		if err != nil {
			t.Fatal(err)
		}
		if len(found) != 1 || found[0].ID != good || len(found[0].Images) != 1 || found[0].Images[0].ID != photos[0].ID {
			t.Fatalf("query %q returned unexpected profiles: %+v", query, found)
		}
	}
	perm.Groups[0].Resources[0].ProductGroups = []string{"giày"}
	found, err := productAdviceForTurn(context.Background(), &perm, gobyQuestion, nil)
	if err != nil || len(found) < 1 || found[0].ID != goby {
		t.Fatalf("OA lookup for %q returned %d profiles, error: %v", gobyQuestion, len(found), err)
	}
	personalContext, personalImages, err := PersonalZaloProductAdvice(context.Background(), &perm, gobyQuestion)
	if err != nil || !strings.Contains(personalContext, "CE EN 13634:2017 — 2|2|2|2 WR") ||
		len(personalImages) != 2 || personalImages[0].ID != photos[2].ID || personalImages[1].ID != photos[3].ID {
		t.Fatalf("Personal Zalo must get the Goby source evidence and approved photos (error: %v)", err)
	}
	if strings.Contains(personalContext, "Dardo") || slices.ContainsFunc(personalImages, func(image models.ProductAdvisoryImage) bool { return image.ProductAdvisoryID == otherShoe }) {
		t.Fatalf("adjacent shoe leaked into Goby advice: %q %+v", personalContext, personalImages)
	}
	for _, question := range []string{gobyQuestion, "Em ơi, giày Goby sao em"} {
		_, selected, err := PersonalZaloProductAdvice(context.Background(), &perm, question)
		if err != nil || len(selected) != 2 || selected[0].ProductAdvisoryID != goby || selected[1].ProductAdvisoryID != goby {
			t.Fatalf("question %q selected wrong photos: %+v (%v)", question, selected, err)
		}
	}
	loaded, err := LoadPersonalZaloProductAdviceImage(context.Background(), &perm, photos[3].ID)
	if err != nil || loaded.ID != photos[3].ID || loaded.URL != photos[3].URL {
		t.Fatalf("failed to reload approved Goby image: %+v (%v)", loaded, err)
	}
	if _, err := LoadPersonalZaloProductAdviceImage(context.Background(), &perm, photos[4].ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("unapproved image reload error = %v", err)
	}
	perm.Groups[0].Resources[0].ProductGroups = []string{"mũ"}
	if denied, deniedImages, err := PersonalZaloProductAdvice(context.Background(), &perm, gobyQuestion); err != nil || denied != "" || len(deniedImages) != 0 {
		t.Fatalf("Goby context outside the personal Zalo product scope: %q (%v)", denied, err)
	}
	if _, err := LoadPersonalZaloProductAdviceImage(context.Background(), &perm, photos[2].ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("image escaped product scope: %v", err)
	}

	accessory := models.CachedProduct{ID: pkg.NewUUID(), TenantID: tenant, MA: "SKU903", MA_CHA: "PARENT901", TEN_DONG_BO_WEB: "Phụ kiện LS2", NHAN_HIEU_NAME: "LS2", LIST_TEN_NHOM_VTHH: "Phụ kiện"}
	accessoryProfile := models.ProductAdvisory{ID: pkg.NewUUID(), TenantID: tenant, ParentCode: "PARENT901", WebName: accessory.TEN_DONG_BO_WEB, Notes: "Phụ tùng thay thế cho SKU901"}
	accessoryImage := models.ProductAdvisoryImage{ID: pkg.NewUUID(), TenantID: tenant, ProductAdvisoryID: accessoryProfile.ID, Enabled: true}
	for _, seed := range []any{&accessory, &accessoryProfile, &accessoryImage} {
		if err := testDB.Create(seed).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.MigrateProductAdvisoryGroups(testDB); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPersonalZaloProductAdviceImage(context.Background(), &perm, photos[0].ID); err != nil {
		t.Fatalf("helmet photo was blocked by the accessory: %v", err)
	}
	if _, err := LoadPersonalZaloProductAdviceImage(context.Background(), &perm, accessoryImage.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("accessory photo escaped the helmet-only scope: %v", err)
	}
	perm.Groups[0].Resources[0].ProductGroups = []string{"mũ", "phụ kiện"}
	for sku, wanted := range map[string]string{"SKU901": good, "SKU903": accessoryProfile.ID} {
		profiles, err := findProductAdvice(context.Background(), &perm, sku, adviceImageLimit)
		if err != nil || len(profiles) != 1 || profiles[0].ID != wanted || len(profiles[0].VariantCodes) != 1 || profiles[0].VariantCodes[0] != sku {
			t.Fatalf("SKU %s matched the wrong product line: %+v (%v)", sku, profiles, err)
		}
	}
}
