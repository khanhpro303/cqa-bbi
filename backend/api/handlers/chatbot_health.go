package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"

	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// HealthCheck is one row of the chatbot health report.
type HealthCheck struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Status  string `json:"status"` // ok | warn | error
	Detail  string `json:"detail,omitempty"`
	Fix     string `json:"fix,omitempty"`
	Channel string `json:"channel,omitempty"`
}

// ChannelLangflowReport shows the EFFECTIVE Langflow config a channel uses at
// runtime (after AI Engine → legacy channel value → .env) and where each value
// came from — the exact thing the worker will call.
type ChannelLangflowReport struct {
	ChannelID   string                `json:"channel_id"`
	ChannelName string                `json:"channel_name"`
	AutoReply   bool                  `json:"auto_reply"`
	Effective   engine.LangflowConfig `json:"effective"`
	HasOverride bool                  `json:"has_override"`
}

type channelLangflowMeta struct {
	LangflowAPIURL       string `json:"langflow_api_url"`
	LangflowAPIKey       string `json:"langflow_api_key"`
	LangflowFlowID       string `json:"langflow_flow_id"`
	LangflowPublicFlowID string `json:"langflow_public_flow_id"`
}

// GetChatbotHealth runs every check the Zalo chatbot depends on and returns a
// report the admin UI renders as a checklist with a fix hint per failure.
// Read-only and cheap: Langflow is probed with GET /flows/{id} (no LLM run).
func GetChatbotHealth(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	cfg, _ := config.Load()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
	defer cancel()

	checks := []HealthCheck{}
	add := func(h HealthCheck) { checks = append(checks, h) }

	// 1. Master switch.
	var active models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "chatbot_active").First(&active).Error; err == nil && active.ValuePlain != "true" {
		add(HealthCheck{Key: "chatbot_active", Label: "Công tắc AI chatbot", Status: "error", Detail: "Đang tắt", Fix: "Bật công tắc AI chatbot ở trang Cài đặt."})
	} else {
		add(HealthCheck{Key: "chatbot_active", Label: "Công tắc AI chatbot", Status: "ok", Detail: "Đang bật"})
	}

	// 2. Redis + worker queue.
	if db.RedisClient == nil {
		add(HealthCheck{Key: "redis", Label: "Redis (hàng đợi tin nhắn)", Status: "error", Detail: "Chưa cấu hình REDIS_URL", Fix: "Đặt REDIS_URL trong .env; không có Redis thì tin Zalo bị bỏ."})
	} else if err := db.RedisClient.Ping(ctx).Err(); err != nil {
		add(HealthCheck{Key: "redis", Label: "Redis (hàng đợi tin nhắn)", Status: "error", Detail: err.Error(), Fix: "Kiểm tra container Redis."})
	} else {
		add(HealthCheck{Key: "redis", Label: "Redis (hàng đợi tin nhắn)", Status: "ok"})
		add(queueHealth(cfg))
	}

	// 3. Langflow per active Zalo OA channel, as the worker will resolve it.
	settings := engine.LoadLangflowTenantSettings(tenantID)
	tenantCfg := engine.ResolveTenantLangflowConfig(settings, cfg)
	if tenantCfg.APIKeyWarning != "" {
		add(HealthCheck{Key: "langflow_api_key", Label: "Langflow API key", Status: "warn",
			Detail: "Không giải mã được API key đã lưu.", Fix: tenantCfg.APIKeyWarning})
	}

	var channels []models.Channel
	db.DB.Where("tenant_id = ? AND channel_type = ? AND is_active = true", tenantID, "zalo_oa").Find(&channels)
	if len(channels) == 0 {
		add(HealthCheck{Key: "zalo_channel", Label: "Kênh Zalo OA", Status: "error", Detail: "Không có kênh Zalo OA nào đang hoạt động", Fix: "Kết nối kênh Zalo OA ở trang Kênh."})
	}

	probed := map[string]engine.FlowCheck{}
	probe := func(url, key, flow string) engine.FlowCheck {
		k := url + "|" + key + "|" + flow
		if r, ok := probed[k]; ok {
			return r
		}
		r := engine.CheckLangflowFlow(ctx, url, key, flow)
		probed[k] = r
		return r
	}

	reports := []ChannelLangflowReport{}
	for _, ch := range channels {
		var meta channelLangflowMeta
		if ch.Metadata != "" {
			_ = json.Unmarshal([]byte(ch.Metadata), &meta)
		}
		ov := engine.LangflowOverrides{APIURL: meta.LangflowAPIURL, APIKey: meta.LangflowAPIKey, FlowID: meta.LangflowFlowID, PublicFlowID: meta.LangflowPublicFlowID}
		eff := engine.ResolveLangflowConfig(ov, settings, cfg)
		reports = append(reports, ChannelLangflowReport{ChannelID: ch.ID, ChannelName: ch.Name, AutoReply: ch.AutoReplyEnabled, Effective: eff, HasOverride: !ov.IsEmpty()})

		if !ch.AutoReplyEnabled {
			add(HealthCheck{Key: "auto_reply", Channel: ch.Name, Label: "Tự động trả lời", Status: "warn", Detail: "Đang tắt trên kênh này", Fix: "Bật \"Tự động trả lời\" của kênh ở trang Kênh."})
		}

		for _, agent := range []struct{ typ, label string }{{"private", "Flow nội bộ (nhân viên)"}, {"public", "Flow khách hàng / nhóm"}} {
			flow, src := eff.FlowFor(agent.typ)
			r := probe(eff.APIURL, eff.APIKey, flow)
			h := HealthCheck{Key: "langflow_" + agent.typ, Channel: ch.Name, Label: agent.label}
			if r.OK {
				h.Status = "ok"
				h.Detail = fmt.Sprintf("%s (flow %s, nguồn: %s)", r.Name, flow, sourceLabel(src))
				add(h)
				continue
			}
			h.Status = "error"
			h.Detail = fmt.Sprintf("%s — flow %s (nguồn flow: %s, nguồn key: %s)", r.Detail, flow, sourceLabel(src), sourceLabel(eff.APIKeySource))
			h.Fix = langflowFixHint(r, eff, src)
			// The worker self-heals to the AI Engine config when a channel
			// override is rejected; report that as a warning, not an outage.
			if eff.UsesChannelOverride() && !eff.Equivalent(tenantCfg, agent.typ) {
				tFlow, _ := tenantCfg.FlowFor(agent.typ)
				if tr := probe(tenantCfg.APIURL, tenantCfg.APIKey, tFlow); tr.OK {
					h.Status = "warn"
					h.Detail += fmt.Sprintf(". Bot đang tự chuyển sang cấu hình AI Engine (%s) nên vẫn trả lời được.", tr.Name)
				}
			}
			add(h)
		}
		if !ov.IsEmpty() {
			add(HealthCheck{Key: "channel_override", Channel: ch.Name, Label: "Cấu hình Langflow cũ trên kênh", Status: "warn",
				Detail: "Kênh còn lưu Langflow riêng (kiểu cũ). Giá trị này chỉ được dùng khi AI Engine để trống.",
				Fix:    "Mở cài đặt kênh và bấm Lưu để xoá cấu hình cũ; chỉ cấu hình Langflow ở trang AI Engine."})
		}
	}

	// 4. ERP (Cloudify) — needed for stock/price/order/debt answers.
	add(erpHealth(tenantID, cfg))

	overall := "ok"
	for _, h := range checks {
		if h.Status == "error" {
			overall = "error"
			break
		}
		if h.Status == "warn" {
			overall = "warn"
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"status":   overall,
		"checks":   checks,
		"channels": reports,
		"checked":  time.Now().UTC().Format(time.RFC3339),
	})
}

func sourceLabel(src string) string {
	switch src {
	case engine.LangflowSourceChannel:
		return "cài đặt kênh"
	case engine.LangflowSourceTenant:
		return "AI Engine"
	case engine.LangflowSourceEnv:
		return ".env"
	default:
		return "chưa cấu hình"
	}
}

func langflowFixHint(r engine.FlowCheck, eff engine.LangflowConfig, flowSrc string) string {
	where := func(src string) string {
		switch src {
		case engine.LangflowSourceChannel:
			return "trang AI Engine (giá trị hiện lấy từ cấu hình cũ của kênh; mở kênh và bấm Lưu để xoá)"
		case engine.LangflowSourceEnv:
			return "trang AI Engine (giá trị hiện lấy từ .env)"
		default:
			return "trang AI Engine"
		}
	}
	switch r.Problem {
	case "bad_key":
		return "Sửa Langflow API key ở " + where(eff.APIKeySource) + "."
	case "flow_missing":
		return "Flow ID không còn tồn tại. Chọn lại Flow ID ở " + where(flowSrc) + "."
	case "unreachable":
		return "Không kết nối được Langflow. Kiểm tra Langflow URL ở " + where(eff.APIURLSource) + " và server Langflow."
	case "not_configured":
		return "Nhập Langflow URL và Flow ID ở trang AI Engine."
	default:
		return "Kiểm tra cấu hình Langflow ở trang AI Engine."
	}
}

func queueHealth(cfg *config.Config) HealthCheck {
	h := HealthCheck{Key: "queue", Label: "Hàng đợi worker"}
	opt, err := asynq.ParseRedisURI(cfg.RedisURL)
	if err != nil {
		h.Status, h.Detail = "warn", "Không đọc được REDIS_URL: "+err.Error()
		return h
	}
	insp := asynq.NewInspector(opt)
	defer insp.Close()
	servers, err := insp.Servers()
	if err != nil || len(servers) == 0 {
		h.Status, h.Detail = "error", "Không thấy worker nào đang chạy"
		h.Fix = "Khởi động lại app/worker; khi worker dừng, tin nhắn chỉ nằm chờ trong hàng đợi."
		return h
	}
	info, err := insp.GetQueueInfo("default")
	if err != nil {
		h.Status, h.Detail = "ok", fmt.Sprintf("%d worker đang chạy", len(servers))
		return h
	}
	h.Detail = fmt.Sprintf("%d worker · chờ %d · đang retry %d · thất bại %d", len(servers), info.Pending, info.Retry, info.Archived)
	switch {
	case info.Pending > 50:
		h.Status, h.Fix = "warn", "Hàng đợi đang dồn; kiểm tra Langflow có chậm không."
	case info.Retry > 0:
		h.Status, h.Fix = "warn", "Có tin đang retry; xem log [zalo-drop]/[worker] để biết lý do."
	default:
		h.Status = "ok"
	}
	return h
}

func erpHealth(tenantID string, cfg *config.Config) HealthCheck {
	h := HealthCheck{Key: "erp", Label: "ERP Cloudify (tồn kho, giá, đơn, công nợ)"}
	url, dbName, login, password, err := loadCloudifyCredentials(tenantID, cfg)
	if err != nil || url == "" || login == "" || password == "" {
		h.Status, h.Detail, h.Fix = "error", "Chưa cấu hình đủ ERP", "Nhập URL, tài khoản, mật khẩu Cloudify ở trang ERP."
		return h
	}
	client := &pkg.CloudifyClient{BaseURL: url, DB: dbName, Login: login, Password: password}
	if err := client.TestConnection(); err != nil {
		h.Status, h.Detail, h.Fix = "error", err.Error(), "Kiểm tra tài khoản Cloudify ở trang ERP."
		return h
	}
	h.Status, h.Detail = "ok", url
	return h
}
