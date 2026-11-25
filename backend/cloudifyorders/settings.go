package cloudifyorders

import (
	"strconv"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

// Setting keys (app_settings, per tenant).
const (
	KeyWatchEnabled       = "cloudify_order_watch_enabled"
	KeyNotifyEnabled      = "cloudify_order_notify_enabled"
	KeyNotifyKinds        = "cloudify_order_notify_kinds"
	KeyWindowDays         = "cloudify_order_window_days"
	KeyPollSeconds        = "cloudify_order_poll_seconds"
	KeyTplCreated         = "cloudify_order_tpl_created"
	KeyTplStatus          = "cloudify_order_tpl_status_changed"
	KeyTplUpdated         = "cloudify_order_tpl_updated"
	KeyWebhookSecret      = "cloudify_order_webhook_secret"
	KeyPaymentBankBIN     = "cloudify_order_payment_bank_bin"
	KeyPaymentAccountNo   = "cloudify_order_payment_account_no"
	KeyPaymentAccountName = "cloudify_order_payment_account_name"
)

// AllKeys lists every setting this package reads.
var AllKeys = []string{
	KeyWatchEnabled, KeyNotifyEnabled, KeyNotifyKinds, KeyWindowDays, KeyPollSeconds,
	KeyTplCreated, KeyTplStatus, KeyTplUpdated, KeyPaymentBankBIN, KeyPaymentAccountNo, KeyPaymentAccountName,
}

// Default templates. Placeholders: {ma_don} {khach_hang} {trang_thai}
// {trang_thai_cu} {tong_tien} {ngay}.
const (
	DefaultTplCreated = "Đơn hàng {ma_don} của quý khách đã được tạo ngày {ngay}, tổng tiền {tong_tien}. Cảm ơn quý khách đã đặt hàng!"
	DefaultTplStatus  = "Đơn hàng {ma_don} của quý khách đã chuyển sang trạng thái: {trang_thai}."
	DefaultTplUpdated = "Đơn hàng {ma_don} của quý khách vừa được cập nhật. Tổng tiền hiện tại {tong_tien}, trạng thái: {trang_thai}."
)

// Limits.
const (
	DefaultWindowDays  = 7
	MaxWindowDays      = 60
	DefaultPollSeconds = 30
	MinPollSeconds     = 15
	MaxPollSeconds     = 3600
)

// Settings is the effective per-tenant configuration.
type Settings struct {
	WatchEnabled   bool
	NotifyEnabled  bool
	NotifyKinds    map[string]bool
	WindowDays     int
	PollInterval   time.Duration
	Templates      map[string]string
	PaymentAccount PaymentAccount
}

// ParseSettings builds Settings from raw plain values, applying defaults and
// limits. Customers are messaged only when both switches are on.
func ParseSettings(values map[string]string) Settings {
	s := Settings{
		WatchEnabled:  isTrue(values[KeyWatchEnabled]),
		NotifyEnabled: isTrue(values[KeyNotifyEnabled]),
		NotifyKinds:   map[string]bool{},
		WindowDays:    clampInt(values[KeyWindowDays], DefaultWindowDays, 1, MaxWindowDays),
		PollInterval:  time.Duration(clampInt(values[KeyPollSeconds], DefaultPollSeconds, MinPollSeconds, MaxPollSeconds)) * time.Second,
		PaymentAccount: PaymentAccount{
			BankBIN:     values[KeyPaymentBankBIN],
			AccountNo:   values[KeyPaymentAccountNo],
			AccountName: values[KeyPaymentAccountName],
		},
		Templates: map[string]string{
			KindCreated:       orDefault(values[KeyTplCreated], DefaultTplCreated),
			KindStatusChanged: orDefault(values[KeyTplStatus], DefaultTplStatus),
			KindUpdated:       orDefault(values[KeyTplUpdated], DefaultTplUpdated),
		},
	}
	kinds := values[KeyNotifyKinds]
	if strings.TrimSpace(kinds) == "" {
		kinds = KindCreated + "," + KindStatusChanged
	}
	for _, kind := range strings.Split(kinds, ",") {
		switch kind = strings.TrimSpace(kind); kind {
		case KindCreated, KindStatusChanged, KindUpdated:
			s.NotifyKinds[kind] = true
		}
	}
	return s
}

// LoadSettings reads the tenant's settings.
func LoadSettings(database *gorm.DB, tenantID string) (Settings, error) {
	values, err := loadPlain(database, tenantID, AllKeys)
	if err != nil {
		return Settings{}, err
	}
	return ParseSettings(values), nil
}

// EnabledTenants lists tenants that turned the watcher on.
func EnabledTenants(database *gorm.DB) ([]string, error) {
	var ids []string
	err := database.Model(&models.AppSetting{}).
		Where("setting_key = ? AND LOWER(value_plain) IN ?", KeyWatchEnabled, []string{"true", "1", "yes", "on"}).
		Pluck("tenant_id", &ids).Error
	return ids, err
}

func loadPlain(database *gorm.DB, tenantID string, keys []string) (map[string]string, error) {
	var rows []models.AppSetting
	if err := database.Where("tenant_id = ? AND setting_key IN ?", tenantID, keys).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.SettingKey] = row.ValuePlain
	}
	return out, nil
}

func isTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

func clampInt(raw string, def, min, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// Render fills a template for one event.
func Render(tpl string, event models.CloudifyOrderEvent) string {
	replacer := strings.NewReplacer(
		"{ma_don}", event.OrderCode,
		"{khach_hang}", event.CustomerName,
		"{trang_thai}", StatusName(event.Status),
		"{trang_thai_cu}", StatusName(event.PrevStatus),
		"{tong_tien}", FormatVND(event.Total),
		"{ngay}", FormatDate(event.OrderDate),
	)
	return strings.TrimSpace(replacer.Replace(tpl))
}

// FormatVND renders 1234567 as "1.234.567đ".
func FormatVND(amount float64) string {
	n := int64(amount)
	neg := n < 0
	if neg {
		n = -n
	}
	digits := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	out := b.String() + "đ"
	if neg {
		out = "-" + out
	}
	return out
}

// FormatDate turns Cloudify's "2006-01-02" into "02/01/2006".
func FormatDate(date string) string {
	date = strings.TrimSpace(date)
	if len(date) >= 10 {
		if t, err := time.Parse("2006-01-02", date[:10]); err == nil {
			return t.Format("02/01/2006")
		}
	}
	return date
}
