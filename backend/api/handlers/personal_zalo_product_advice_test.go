package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/agents"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestPersonalZaloGobyAdvisoryMySQL(t *testing.T) {
	dsn := os.Getenv("CQA_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("CQA_TEST_MYSQL_DSN not set")
	}
	database, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal("connect to test MySQL")
	}
	pool, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	previousDB := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previousDB; _ = pool.Close() })
	tenant := uuid.NewString()
	tables := []any{&models.ProductAdvisoryImage{}, &models.ProductAdvisory{}, &models.CachedProduct{}, &models.ERPEndpoint{}, &models.ZaloWhitelist{}, &models.Channel{}}
	if err := database.AutoMigrate(tables...); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, table := range tables {
			if err := database.Where("tenant_id = ?", tenant).Delete(table).Error; err != nil {
				t.Error(err)
			}
		}
		if err := database.Where("id = ?", tenant).Delete(&models.Tenant{}).Error; err != nil {
			t.Error(err)
		}
	})

	const name = "Giày bảo hộ LS2 Goby Man"
	const standard = "CE EN 13634:2017, mức 2|2|2|2 WR."
	questions := []string{"em ơi, giày goby sao em", "Bull ơi, cho tôi thông tin giày goby với"}
	const answer = "Dạ, LS2 Goby Man ạ.\n• " + standard + "\n• Cổ cao bảo vệ mắt cá.\n• Da bò có phản quang."
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 3, 4))); err != nil {
		t.Fatal(err)
	}
	profileID := uuid.NewString()
	frontID, detailID := uuid.NewString(), uuid.NewString()
	channel := models.Channel{ID: uuid.NewString(), TenantID: tenant, Name: "Goby test", ExternalID: "123",
		ChannelType: "personal_zalo_import", Metadata: "{}", CredentialsEncrypted: []byte("fixture")}
	staff := models.ZaloWhitelist{ID: uuid.NewString(), TenantID: tenant, ChannelID: channel.ID, ZaloUserID: "456", Status: "active"}
	endpoint := models.ERPEndpoint{ID: uuid.NewString(), TenantID: tenant, GroupID: "private_bot", Resource: "products", IsEnabled: true, ProductGroups: "Giày"}
	for _, row := range []any{
		&models.Tenant{ID: tenant, Name: "Goby test", Slug: tenant, Settings: "{}"}, &channel, &endpoint, &staff,
		&models.CachedProduct{ID: uuid.NewString(), TenantID: tenant, MA: "SP457662", MA_CHA: "GOBY", TEN_DONG_BO_WEB: name, NHAN_HIEU_NAME: "LS2", LIST_TEN_NHOM_VTHH: "Giày"},
		&models.ProductAdvisory{ID: profileID, TenantID: tenant, ParentCode: "GOBY", WebName: name,
			ScrapedContent: strings.Repeat("Giới thiệu sản phẩm. ", 130) + "\n\n## Thông số\n" + standard + "\nCổ cao, da bò có phản quang."},
		&models.ProductAdvisoryImage{ID: frontID, TenantID: tenant, ProductAdvisoryID: profileID, Source: "upload", Caption: "Ảnh đại diện Goby", Enabled: true, Data: pngData.Bytes()},
		&models.ProductAdvisoryImage{ID: detailID, TenantID: tenant, ProductAdvisoryID: profileID, Source: "upload", Caption: "Chi tiết cổ cao Goby", Enabled: true, Data: pngData.Bytes(), SortOrder: 1},
		&models.ProductAdvisoryImage{ID: uuid.NewString(), TenantID: tenant, ProductAdvisoryID: profileID, Source: "upload", Caption: "Ảnh Goby chưa duyệt", Data: pngData.Bytes(), SortOrder: 2},
	} {
		if err := database.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = fmt.Fprint(w, `{"data":{"nodes":[{"id":"Agent-fixture","data":{"node":{"template":{"system_prompt":{}}}}}]}}`)
			return
		}
		var data struct {
			Text   string                     `json:"input_value"`
			Tweaks map[string]json.RawMessage `json:"tweaks"`
		}
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			t.Error(err)
			return
		}
		var agent struct {
			Prompt string `json:"system_prompt"`
		}
		if err := json.Unmarshal(data.Tweaks["Agent-fixture"], &agent); err != nil ||
			(data.Text != questions[0] && data.Text != questions[1]) || !strings.Contains(agent.Prompt, "3–4 gạch đầu dòng thật ngắn") ||
			!strings.Contains(agent.Prompt, "Ảnh đại diện Goby") || !strings.Contains(agent.Prompt, "Chi tiết cổ cao Goby") ||
			strings.Contains(agent.Prompt, "Ảnh Goby chưa duyệt") ||
			!strings.Contains(agent.Prompt, standard) || !strings.Contains(agent.Prompt, "Cổ cao, da bò có phản quang") {
			t.Errorf("wrong Goby request sent to Langflow: %q / %q (%v)", data.Text, agent.Prompt, err)
		}
		_, _ = fmt.Fprintf(w, `{"outputs":[{"outputs":[{"results":{"message":{"text":%q}}}]}]}`, answer+"\n[PRODUCT_IMAGE:1]\n[PRODUCT_IMAGE:2]")
	}))
	defer server.Close()

	ctx := context.Background()
	cfg := &config.Config{EncryptionKey: "0123456789abcdef0123456789abcdef"}
	req := personalZaloChatbotRequest{
		TenantID: tenant, ChannelID: channel.ID, SenderID: staff.ZaloUserID,
		AccountExternalID: channel.ExternalID, ThreadID: staff.ZaloUserID, ThreadType: personalZaloThreadUser,
		MessageID: "goby-message", SentAt: time.Now().UTC(), MultipleImages: true,
	}
	identity := personalZaloERPIdentity{ID: staff.ID, AgentType: "private"}
	settings := map[string]models.AppSetting{
		"erp_private_active":               {ValuePlain: "true"},
		"ai_agent_erp_token_private":       {ValuePlain: "fixture-private-erp"},
		"ai_engine_system_prompt_internal": {ValuePlain: "Hỗ trợ nhân viên."},
	}
	flow := engine.LangflowConfig{APIURL: server.URL, APIKey: "fixture-key", FlowID: "private", PublicFlowID: "public"}
	grant, err := preparePersonalZaloDirectGrant(ctx, cfg, req, identity, flow, "fixture-session", settings, agents.Plan{})
	if err != nil {
		t.Fatal(err)
	}
	var images []personalZaloKnowledgeImage
	for _, question := range questions {
		req.Text = question
		reply, chosen, err := grant.run(ctx, engine.NewLangflowClient(cfg), channel, req)
		if err != nil || reply != answer {
			t.Fatalf("Goby reply = %q (%v)", reply, err)
		}
		images = personalZaloProductReplyImages(ctx, grant.advicePermissions, chosen.products)
		if len(images) != 2 || images[0].ID != frontID || images[1].ID != detailID ||
			!images[0].product || !images[1].product || images[0].Width != 3 || images[1].Height != 4 {
			t.Fatalf("Goby reply did not attach the selected approved images in order: %+v", images)
		}
	}

	for _, capable := range []bool{true, false} {
		req.MultipleImages = capable
		recorder := httptest.NewRecorder()
		writer, _ := gin.CreateTestContext(recorder)
		writePersonalZaloDirectReply(writer, cfg.EncryptionKey, req, gin.H{"reply": answer, "images": images}, identity, grant)
		var payload struct {
			DeliveryToken string `json:"delivery_token"`
			Images        []personalZaloKnowledgeImage
		}
		wantCount := 1
		if capable {
			wantCount = len(images)
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil || recorder.Code != http.StatusOK ||
			len(payload.Images) != wantCount || payload.Images[0].ID != frontID {
			t.Fatalf("capable=%t: wrong API response, status=%d, images=%d (%v)", capable, recorder.Code, len(payload.Images), err)
		}
		for _, image := range payload.Images {
			req.DeliveryPart, req.DeliveryDigest = "image", personalZaloIdentityDigest(string(image.Data))
			req.DeliveryToken = image.DeliveryToken
			if !capable {
				req.DeliveryToken = payload.DeliveryToken
			}
			assertDelivery := func(want int) {
				t.Helper()
				recorder := httptest.NewRecorder()
				preflight, _ := gin.CreateTestContext(recorder)
				checkPersonalZaloDelivery(preflight, ctx, cfg.EncryptionKey, channel, req, settings)
				preflight.Writer.WriteHeaderNow()
				if recorder.Code != want {
					t.Fatalf("image %s preflight status=%d, want %d", image.ID, recorder.Code, want)
				}
			}
			assertDelivery(http.StatusOK)
			if image.ID == detailID {
				if err := database.Model(&models.ProductAdvisoryImage{}).Where("id = ?", detailID).Update("enabled", false).Error; err != nil {
					t.Fatal(err)
				}
				assertDelivery(http.StatusNoContent)
			}
		}
	}
}
