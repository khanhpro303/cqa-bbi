package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/cloudifyorders"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm/clause"
)

// CloudifyOrderWebhookHandler receives a ping from Cloudify (an Odoo
// "Automated Action → Send Webhook Notification" on sale orders, or any HTTP
// caller) and makes CQA look at the order right away.
//
// POST /api/v1/webhooks/cloudify/:tenantId?secret=<secret>
//
// The body is only a hint: CQA never trusts it as order data. When it names an
// order code CQA re-reads that order from Cloudify; otherwise it asks the
// watcher to poll now. Odoo waits about one second for a webhook reply, so
// the work runs after the response.
func CloudifyOrderWebhookHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID := strings.TrimSpace(c.Param("tenantId"))
		secret := c.Query("secret")
		if secret == "" {
			secret = c.GetHeader("X-CQA-Webhook-Secret")
		}
		if tenantID == "" || !cloudifyWebhookSecretOK(tenantID, secret) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		body, _ := io.ReadAll(io.LimitReader(c.Request.Body, 64<<10))
		code := cloudifyOrderCodeFromBody(body)
		c.JSON(http.StatusAccepted, gin.H{"status": "accepted"})

		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			now := time.Now()
			if code != "" {
				fetcher, err := cloudifyorders.NewCloudifyFetcher(db.DB, tenantID, cfg.EncryptionKey)
				if err == nil {
					result, err := cloudifyorders.IngestByCode(ctx, db.DB, fetcher, tenantID, code, now)
					if err == nil && result.Fetched > 0 {
						log.Printf("[cloudify-orders] webhook tenant %s order %s: %d event(s)", tenantID, code, len(result.Events))
						return
					}
				}
			}
			if err := cloudifyorders.Nudge(db.DB, tenantID, now); err != nil {
				log.Printf("[cloudify-orders] webhook nudge failed for tenant %s: %v", tenantID, err)
			}
		}()
	}
}

func cloudifyWebhookSecretOK(tenantID, secret string) bool {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return false
	}
	var setting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, cloudifyorders.KeyWebhookSecret).First(&setting).Error; err != nil {
		return false
	}
	stored := strings.TrimSpace(setting.ValuePlain)
	if stored == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(hashWebhookSecret(secret))) == 1
}

func hashWebhookSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// cloudifyOrderCodeFromBody finds an order code in a webhook body. Odoo's
// webhook sends the record's selected fields at the top level; other callers
// may nest them under "record" or "data".
func cloudifyOrderCodeFromBody(body []byte) string {
	var payload map[string]any
	if len(body) == 0 || json.Unmarshal(body, &payload) != nil {
		return ""
	}
	candidates := []map[string]any{payload}
	for _, key := range []string{"record", "data", "order"} {
		if nested, ok := payload[key].(map[string]any); ok {
			candidates = append(candidates, nested)
		}
	}
	for _, m := range candidates {
		for _, key := range []string{"MA_HOA_DON", "SO_DON_HANG", "order_code", "code", "name"} {
			if v, ok := m[key].(string); ok {
				v = strings.TrimSpace(v)
				if v != "" && len(v) <= 64 {
					return v
				}
			}
		}
	}
	return ""
}

type cloudifyOrderSettingsBody struct {
	WatchEnabled  *bool    `json:"watch_enabled"`
	NotifyEnabled *bool    `json:"notify_enabled"`
	NotifyKinds   []string `json:"notify_kinds"`
	WindowDays    *int     `json:"window_days"`
	PollSeconds   *int     `json:"poll_seconds"`
	TplCreated    *string  `json:"template_created"`
	TplStatus     *string  `json:"template_status_changed"`
	TplUpdated    *string  `json:"template_updated"`
}

// GetCloudifyOrderWatch returns settings, watcher status and recent events.
func GetCloudifyOrderWatch(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	settings, err := cloudifyorders.LoadSettings(db.DB, tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot load settings"})
		return
	}
	var watch models.CloudifyOrderWatch
	db.DB.Where("tenant_id = ?", tenantID).First(&watch)
	var events []models.CloudifyOrderEvent
	db.DB.Where("tenant_id = ?", tenantID).Order("created_at DESC").Limit(50).Find(&events)
	kinds := []string{}
	for _, k := range []string{cloudifyorders.KindCreated, cloudifyorders.KindStatusChanged, cloudifyorders.KindUpdated} {
		if settings.NotifyKinds[k] {
			kinds = append(kinds, k)
		}
	}
	var secret models.AppSetting
	hasSecret := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, cloudifyorders.KeyWebhookSecret).First(&secret).Error == nil && secret.ValuePlain != ""
	c.JSON(http.StatusOK, gin.H{
		"settings": gin.H{
			"watch_enabled":           settings.WatchEnabled,
			"notify_enabled":          settings.NotifyEnabled,
			"notify_kinds":            kinds,
			"window_days":             settings.WindowDays,
			"poll_seconds":            int(settings.PollInterval / time.Second),
			"template_created":        settings.Templates[cloudifyorders.KindCreated],
			"template_status_changed": settings.Templates[cloudifyorders.KindStatusChanged],
			"template_updated":        settings.Templates[cloudifyorders.KindUpdated],
		},
		"status": gin.H{
			"seeded_at":    watch.SeededAt,
			"last_poll_at": watch.LastPollAt,
			"last_ok_at":   watch.LastOKAt,
			"last_error":   watch.LastError,
			"last_count":   watch.LastCount,
		},
		"webhook": gin.H{
			"has_secret": hasSecret,
			"path":       "/api/v1/webhooks/cloudify/" + tenantID,
		},
		"events": events,
	})
}

// SaveCloudifyOrderWatch updates the watcher settings. Omitted fields keep
// their value; an empty template resets it to the default.
func SaveCloudifyOrderWatch(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var body cloudifyOrderSettingsBody
	if c.ShouldBindJSON(&body) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "dữ liệu không hợp lệ"})
		return
	}
	values := map[string]string{}
	if body.WatchEnabled != nil {
		values[cloudifyorders.KeyWatchEnabled] = strconv.FormatBool(*body.WatchEnabled)
	}
	if body.NotifyEnabled != nil {
		values[cloudifyorders.KeyNotifyEnabled] = strconv.FormatBool(*body.NotifyEnabled)
	}
	if body.NotifyKinds != nil {
		kinds := []string{}
		for _, k := range body.NotifyKinds {
			switch k {
			case cloudifyorders.KindCreated, cloudifyorders.KindStatusChanged, cloudifyorders.KindUpdated:
				kinds = append(kinds, k)
			default:
				c.JSON(http.StatusBadRequest, gin.H{"error": "loại sự kiện không hợp lệ: " + k})
				return
			}
		}
		if len(kinds) == 0 {
			// An empty list would fall back to the default; store a marker
			// that matches no kind so "notify nothing" sticks.
			kinds = []string{"none"}
		}
		values[cloudifyorders.KeyNotifyKinds] = strings.Join(kinds, ",")
	}
	if body.WindowDays != nil {
		if *body.WindowDays < 1 || *body.WindowDays > cloudifyorders.MaxWindowDays {
			c.JSON(http.StatusBadRequest, gin.H{"error": "số ngày quét phải từ 1 đến 60"})
			return
		}
		values[cloudifyorders.KeyWindowDays] = strconv.Itoa(*body.WindowDays)
	}
	if body.PollSeconds != nil {
		if *body.PollSeconds < cloudifyorders.MinPollSeconds || *body.PollSeconds > cloudifyorders.MaxPollSeconds {
			c.JSON(http.StatusBadRequest, gin.H{"error": "chu kỳ quét phải từ 15 đến 3600 giây"})
			return
		}
		values[cloudifyorders.KeyPollSeconds] = strconv.Itoa(*body.PollSeconds)
	}
	for key, tpl := range map[string]*string{
		cloudifyorders.KeyTplCreated: body.TplCreated,
		cloudifyorders.KeyTplStatus:  body.TplStatus,
		cloudifyorders.KeyTplUpdated: body.TplUpdated,
	} {
		if tpl != nil {
			if len([]rune(*tpl)) > 1000 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "mẫu tin quá dài (tối đa 1000 ký tự)"})
				return
			}
			values[key] = strings.TrimSpace(*tpl)
		}
	}
	for key, value := range values {
		if err := upsertPlainSetting(tenantID, key, value); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "không lưu được cài đặt"})
			return
		}
	}
	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "cloudify_orders.settings_saved", "tenant", tenantID, "", "", c.ClientIP())
	GetCloudifyOrderWatch(c)
}

// RotateCloudifyOrderWebhookSecret creates a new webhook secret and returns it
// once, inside the full webhook URL path. Only its hash is stored.
func RotateCloudifyOrderWebhookSecret(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot generate secret"})
		return
	}
	secret := hex.EncodeToString(buf)
	if err := upsertPlainSetting(tenantID, cloudifyorders.KeyWebhookSecret, hashWebhookSecret(secret)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "không lưu được secret"})
		return
	}
	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "cloudify_orders.webhook_secret_rotated", "tenant", tenantID, "", "", c.ClientIP())
	c.JSON(http.StatusOK, gin.H{
		"secret": secret,
		"path":   "/api/v1/webhooks/cloudify/" + tenantID + "?secret=" + secret,
	})
}

// PollCloudifyOrdersNow asks the watcher to poll on its next tick.
func PollCloudifyOrdersNow(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	if err := cloudifyorders.Nudge(db.DB, tenantID, time.Now()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "không gửi được yêu cầu quét"})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "queued"})
}

// upsertPlainSetting writes a plain setting, including empty values (the
// shared upsertSetting ignores empty strings).
func upsertPlainSetting(tenantID, key, value string) error {
	now := time.Now()
	row := models.AppSetting{ID: pkg.NewUUID(), TenantID: tenantID, SettingKey: key, ValuePlain: value, CreatedAt: now, UpdatedAt: now}
	return db.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "setting_key"}},
		DoUpdates: clause.Assignments(map[string]any{"value_plain": value, "value_encrypted": nil, "updated_at": now}),
	}).Create(&row).Error
}
