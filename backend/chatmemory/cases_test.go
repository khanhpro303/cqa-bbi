package chatmemory

import (
	"context"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/customercases"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

func TestSessionAlertIssuesSkipsCaseIssues(t *testing.T) {
	r := SummaryResult{Issues: []SummaryIssue{
		{Type: "product_defect", Severity: "high", Detail: "kính nứt", NeedsCase: true},
		{Type: "bot_failure", Severity: "high", Detail: "bot trả lời sai giá", NeedsCase: true},
		{Type: "price", Severity: "medium", Detail: "hỏi giá"},
	}}
	// The defect's case reached staff: it is not alerted again.
	got := sessionAlertIssues(r, map[int]bool{0: true})
	if len(got) != 1 || got[0].Type != "bot_failure" {
		t.Fatalf("case notified: %+v", got)
	}
	// Its case was written but the alert failed (or the write failed): the
	// session alert still carries it.
	if got := sessionAlertIssues(r, map[int]bool{0: false}); len(got) != 2 {
		t.Fatalf("case not notified, issue must stay in the session alert: %+v", got)
	}
	if got := sessionAlertIssues(r, nil); len(got) != 2 {
		t.Fatalf("with cases off every high issue is a session alert: %+v", got)
	}
}

func TestParseSummaryNeedsCase(t *testing.T) {
	r, err := ParseSummaryResult(`{"summary":"x","issues":[{"type":"delivery","severity":"HIGH","detail":"giao thiếu","needs_case":true}]}`)
	if err != nil || len(r.Issues) != 1 || !r.Issues[0].NeedsCase || r.Issues[0].Severity != "high" {
		t.Fatalf("%+v %v", r, err)
	}
}

// A summarized session turns its needs_case issues into cases, with the
// images the customer sent during the session.
func TestSessionIssuesBecomeCasesMySQL(t *testing.T) {
	connectTestMySQL(t)
	SetEnabled(true)
	defer SetEnabled(false)
	tenantID, channelID := pkg.NewUUID(), pkg.NewUUID()
	now := time.Now()
	if err := db.DB.Create(&models.Tenant{ID: tenantID, Name: "t", Slug: "t-" + tenantID[:8], Settings: "{}", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&models.Channel{ID: channelID, TenantID: tenantID, ChannelType: "zalo_oa", Name: "OA", CredentialsEncrypted: []byte("x"), Metadata: `{"complaint_cases":true}`, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	userID, sessionID := "zu-"+pkg.NewUUID()[:8], pkg.NewUUID()
	turn := Turn{TenantID: tenantID, ChannelID: channelID, SessionID: sessionID, ZaloUserID: userID, CustomerName: "Anh Bình", CustomerCode: "KH01", AgentType: "public"}
	start := now.Add(-2 * time.Hour).Truncate(time.Second)
	RecordInbound(context.Background(), turn, Inbound{MessageID: "c1", Text: "kính em nhận bị nứt", SentAt: start})
	RecordInbound(context.Background(), turn, Inbound{MessageID: "c2", Text: "[Ảnh khách gửi: kính nứt]", ContentType: "image",
		Attachments: []customercases.ImageAttachment{{Type: "image", URL: "https://f1.zdn.vn/k.jpg", Description: "kính nứt"}}, SentAt: start.Add(time.Minute)})

	var reports []customercases.Report
	cfg := &config.Config{ChatbotSessionTimeout: 30}
	cfg.ChatMemory = config.ChatMemoryConfig{Enabled: true}
	s := &Service{cfg: cfg,
		summarize: func(context.Context, string, string, string) (ai.AIResponse, string, string, error) {
			return ai.AIResponse{Content: `{"summary":"Khách báo kính nứt","outcome":"handover","sentiment":"negative","issues":[{"type":"product_defect","severity":"high","detail":"Kính FF800 nứt khi nhận","evidence":"kính em nhận bị nứt","needs_case":true},{"type":"price","severity":"low","detail":"hỏi giá","needs_case":false}],"profile":{},"card":""}`}, "openai", "m", nil
		},
		embed: func(context.Context, string, string) ([]float32, error) { return nil, nil },
		recordCase: func(_ context.Context, r customercases.Report) (customercases.Outcome, error) {
			if r.TenantID != tenantID {
				// Sessions other tests left in the shared database.
				return customercases.Outcome{}, nil
			}
			reports = append(reports, r)
			return customercases.Outcome{Action: customercases.ActionCreated, Case: models.CustomerCase{ID: "c"}}, nil
		},
	}
	s.RunOnce(context.Background())

	if len(reports) != 1 {
		t.Fatalf("reports = %+v", reports)
	}
	r := reports[0]
	if r.Source != customercases.SourceSummary || r.SessionID != sessionID || r.Title != "Kính FF800 nứt khi nhận" || r.CustomerCode != "KH01" || r.SubjectID != userID {
		t.Fatalf("report = %+v", r)
	}
	if len(r.Images) != 1 || r.Images[0].URL != "https://f1.zdn.vn/k.jpg" || r.Images[0].Description != "kính nứt" {
		t.Fatalf("images = %+v", r.Images)
	}

	// Switching complaint cases off on the channel stops it.
	reports = nil
	db.DB.Model(&models.Channel{}).Where("id = ?", channelID).Update("metadata", `{"complaint_cases":false}`)
	db.DB.Model(&models.ChatSession{}).Where("id = ?", sessionID).Updates(map[string]interface{}{"status": "open", "attempts": 0})
	s.RunOnce(context.Background())
	if len(reports) != 0 {
		t.Fatalf("cases off but reported: %+v", reports)
	}
}
