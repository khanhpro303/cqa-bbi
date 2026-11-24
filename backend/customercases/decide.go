package customercases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
)

const decideTimeout = 20 * time.Second

// callModel is the decision model call; replaced in tests.
var callModel = callTenantModel

// Decide asks the decision model whether r is one of candidates and, when
// the channel has complaint streams, which stream it belongs to. It returns
// the matching index (-1 for a new problem) and the stream key ("" when
// none). On error the index is -1; a stream read before the error is kept.
func Decide(ctx context.Context, r Report, candidates []models.CustomerCase, streams []Stream) (int, string, string, error) {
	loc := location(r.TenantID)
	system := engine.BuildTaskPrompt(r.TenantID, engine.PromptComplaintMatch, nil)
	input := BuildDecisionInput(r, candidates, streams, loc)
	content, err := callModel(ctx, r.TenantID, r.ChannelID, system, input)
	if err != nil {
		return -1, "", "", err
	}
	return ParseDecision(content, len(candidates), streams)
}

// BuildDecisionInput renders the new report, the earlier cases and the
// channel's complaint streams.
func BuildDecisionInput(r Report, candidates []models.CustomerCase, streams []Stream, loc *time.Location) string {
	var b strings.Builder
	b.WriteString("<van_de_moi>\n")
	fmt.Fprintf(&b, "Loại: %s\n", TypeLabel(r.Type))
	if r.Title != "" {
		fmt.Fprintf(&b, "Tóm tắt: %s\n", oneLine(r.Title))
	}
	if r.Detail != "" {
		fmt.Fprintf(&b, "Chi tiết: %s\n", oneLine(truncate(r.Detail, 1500)))
	}
	if r.Evidence != "" {
		fmt.Fprintf(&b, "Lời khách: \"%s\"\n", oneLine(truncate(r.Evidence, 500)))
	}
	for _, im := range r.Images {
		if im.Description != "" {
			fmt.Fprintf(&b, "Ảnh khách gửi: %s\n", oneLine(truncate(im.Description, 400)))
		}
	}
	b.WriteString("</van_de_moi>\n\n<van_de_cu>\n")
	for i, c := range candidates {
		fmt.Fprintf(&b, "[%d] %s · %s · báo lúc %s", i+1, StatusLabel(c.Status), TypeLabel(c.Type), c.CreatedAt.In(loc).Format("15:04 02/01/2006"))
		if c.LastFollowupAt != nil {
			fmt.Fprintf(&b, " · khách nhắc lại %d lần, gần nhất %s", c.FollowupCount, c.LastFollowupAt.In(loc).Format("15:04 02/01"))
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "    Tóm tắt: %s\n", oneLine(c.Title))
		if c.Detail != "" && c.Detail != c.Title {
			fmt.Fprintf(&b, "    Chi tiết: %s\n", oneLine(truncate(c.Detail, 600)))
		}
		if c.Resolution != "" {
			fmt.Fprintf(&b, "    Cách đã xử lý: %s\n", oneLine(truncate(c.Resolution, 300)))
		}
	}
	b.WriteString("</van_de_cu>")
	if len(streams) > 0 {
		b.WriteString("\n\n<luong_xu_ly>\n")
		for _, s := range streams {
			fmt.Fprintf(&b, "- %s: %s", s.Key, oneLine(s.Name))
			if s.Description != "" {
				fmt.Fprintf(&b, " (%s)", oneLine(s.Description))
			}
			b.WriteString("\n")
		}
		b.WriteString("</luong_xu_ly>")
	}
	return b.String()
}

// ParseDecision reads the model's JSON answer. n is the number of
// candidates; a "same" answer must name one of them. The stream must be one
// of streams (matched by key or name), else it is "".
func ParseDecision(content string, n int, streams []Stream) (int, string, string, error) {
	body := strings.TrimSpace(content)
	if i := strings.Index(body, "{"); i >= 0 {
		if j := strings.LastIndex(body, "}"); j > i {
			body = body[i : j+1]
		}
	}
	var out struct {
		Decision string          `json:"decision"`
		Case     json.RawMessage `json:"case"`
		Reason   string          `json:"reason"`
		Stream   string          `json:"stream"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return -1, "", "", fmt.Errorf("decision is not valid JSON: %w", err)
	}
	reason := truncate(strings.TrimSpace(out.Reason), 300)
	stream := matchStream(streams, out.Stream)
	switch strings.ToLower(strings.TrimSpace(out.Decision)) {
	case engine.ComplaintMatchNew:
		return -1, stream, reason, nil
	case "same":
		var idx int
		if err := json.Unmarshal(out.Case, &idx); err != nil {
			var s string
			if json.Unmarshal(out.Case, &s) != nil {
				return -1, stream, reason, errors.New("decision names no case")
			}
			if _, err := fmt.Sscanf(strings.Trim(s, "[] "), "%d", &idx); err != nil {
				return -1, stream, reason, errors.New("decision names no case")
			}
		}
		if idx < 1 || idx > n {
			return -1, stream, reason, fmt.Errorf("decision names case %d of %d", idx, n)
		}
		return idx - 1, stream, reason, nil
	}
	return -1, stream, reason, fmt.Errorf("unknown decision %q", out.Decision)
}

func callTenantModel(ctx context.Context, tenantID, channelID, system, input string) (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	resolved, err := engine.ResolveTenantAIProvider(tenantID, cfg)
	if err != nil {
		return "", err
	}
	if resolved.Client == nil {
		return "", errors.New("no AI API key configured for tenant")
	}
	callCtx, cancel := context.WithTimeout(ctx, decideTimeout)
	defer cancel()
	var resp ai.AIResponse
	if jp, ok := resolved.Client.(ai.JSONProvider); ok {
		resp, err = jp.AnalyzeJSON(callCtx, system, input)
	} else {
		resp, err = resolved.Client.AnalyzeChat(callCtx, system, input)
	}
	if err != nil {
		return "", err
	}
	LogUsage(tenantID, channelID, resolved.Provider, resolved.Model, resp)
	return resp.Content, nil
}

// LogUsage records the AI spend of a chatbot side task (case decision,
// image reading) in the cost logs.
func LogUsage(tenantID, channelID, provider, model string, resp ai.AIResponse) {
	if db.DB == nil {
		return
	}
	if resp.Provider != "" {
		provider = resp.Provider
	}
	if resp.Model != "" {
		model = resp.Model
	}
	row := models.AIUsageLog{
		ID: pkg.NewUUID(), TenantID: tenantID, Provider: provider, Model: model,
		Source: "chatbot", ChannelID: channelID, InputTokens: resp.InputTokens, OutputTokens: resp.OutputTokens,
		CostUSD:   ai.CalculateCostUSD(provider, model, resp.InputTokens, resp.OutputTokens),
		CreatedAt: time.Now(),
	}
	if err := db.DB.Create(&row).Error; err != nil {
		log.Printf("[cases] usage log: %v", err)
	}
}

func location(tenantID string) *time.Location {
	if db.DB == nil {
		loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
		if err != nil {
			return time.Local
		}
		return loc
	}
	loc, err := time.LoadLocation(engine.TenantTimezone(tenantID))
	if err != nil || loc == nil {
		return time.Local
	}
	return loc
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// TypeLabel shows a case type in Vietnamese.
func TypeLabel(t string) string {
	switch t {
	case "complaint":
		return "Khiếu nại"
	case "delivery":
		return "Giao hàng"
	case "product_defect":
		return "Hàng lỗi"
	case "price":
		return "Giá"
	case "payment":
		return "Thanh toán, công nợ"
	case "staff_request":
		return "Khách cần nhân viên"
	default:
		return "Khác"
	}
}

// StatusLabel shows a case status in Vietnamese.
func StatusLabel(s string) string {
	switch s {
	case StatusOpen:
		return "Mới, chưa xử lý"
	case StatusInProgress:
		return "Đang xử lý"
	case StatusResolved:
		return "Đã xử lý xong"
	case StatusCancelled:
		return "Đã đóng"
	}
	return s
}
