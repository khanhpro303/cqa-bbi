package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
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
	tables := []any{&models.ProductAdvisoryImage{}, &models.ProductAdvisory{}, &models.CachedProduct{}, &models.ERPEndpoint{}}
	if err := database.AutoMigrate(tables...); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, table := range tables {
			if err := database.Where("tenant_id = ?", tenant).Delete(table).Error; err != nil {
				t.Error(err)
			}
		}
	})

	const name = "Giày bảo hộ LS2 Goby Man"
	const standard = "CE EN 13634:2017, mức 2|2|2|2 WR."
	const question = "Bull ơi, cho tôi thông tin giày goby với"
	const answer = "Dạ, LS2 Goby Man ạ.\n• " + standard + "\n• Cổ cao bảo vệ mắt cá.\n• Da bò có phản quang."
	endpoint := models.ERPEndpoint{ID: uuid.NewString(), TenantID: tenant, GroupID: "private_bot", Resource: "products", IsEnabled: true, ProductGroups: "Giày"}
	for _, row := range []any{
		&endpoint,
		&models.CachedProduct{ID: uuid.NewString(), TenantID: tenant, MA: "SP457662", MA_CHA: "GOBY", TEN_DONG_BO_WEB: name, NHAN_HIEU_NAME: "LS2", LIST_TEN_NHOM_VTHH: "Giày"},
		&models.ProductAdvisory{ID: uuid.NewString(), TenantID: tenant, ParentCode: "GOBY", WebName: name,
			ScrapedContent: strings.Repeat("Giới thiệu sản phẩm. ", 130) + "\n\n## Thông số\n" + standard + "\nCổ cao, da bò có phản quang."},
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
			data.Text != question || !strings.Contains(agent.Prompt, "3–4 gạch đầu dòng thật ngắn") ||
			!strings.Contains(agent.Prompt, standard) || !strings.Contains(agent.Prompt, "Cổ cao, da bò có phản quang") {
			t.Errorf("wrong Goby request sent to Langflow: %q / %q (%v)", data.Text, agent.Prompt, err)
		}
		_, _ = fmt.Fprintf(w, `{"outputs":[{"outputs":[{"results":{"message":{"text":%q}}}]}]}`, answer)
	}))
	defer server.Close()

	ctx := context.Background()
	cfg := &config.Config{EncryptionKey: "0123456789abcdef0123456789abcdef"}
	req := personalZaloChatbotRequest{TenantID: tenant, ChannelID: "channel", SenderID: "staff", Text: question}
	settings := map[string]models.AppSetting{
		"erp_private_active":               {ValuePlain: "true"},
		"ai_agent_erp_token_private":       {ValuePlain: "fixture-private-erp"},
		"ai_engine_system_prompt_internal": {ValuePlain: "Hỗ trợ nhân viên."},
	}
	flow := engine.LangflowConfig{APIURL: server.URL, APIKey: "fixture-key", FlowID: "private", PublicFlowID: "public"}
	grant, err := preparePersonalZaloDirectGrant(ctx, cfg, req, personalZaloERPIdentity{ID: "staff", AgentType: "private"}, flow, "fixture-session", settings)
	if err != nil {
		t.Fatal(err)
	}
	reply, _, err := grant.run(ctx, engine.NewLangflowClient(cfg), models.Channel{ID: req.ChannelID}, req)
	if err != nil || reply != answer {
		t.Fatalf("Goby reply = %q (%v)", reply, err)
	}
}
