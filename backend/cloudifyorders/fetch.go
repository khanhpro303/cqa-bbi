package cloudifyorders

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
)

// DefaultOrdersEndpoint is Cloudify's official order search endpoint.
const DefaultOrdersEndpoint = "saorders/search"

// ErrNotConfigured means the tenant has no Cloudify credentials.
var ErrNotConfigured = errors.New("cloudify credentials not configured")

// Fetcher reads order rows from Cloudify.
type Fetcher interface {
	// ByWindow returns every order whose order date is in [from, to].
	ByWindow(from, to time.Time) ([]map[string]any, error)
	// ByCode returns the order with this code (MA_HOA_DON / SO_DON_HANG).
	ByCode(code string) ([]map[string]any, error)
}

type cloudifyFetcher struct {
	client   *pkg.CloudifyClient
	endpoint string
}

func (f *cloudifyFetcher) ByWindow(from, to time.Time) ([]map[string]any, error) {
	return f.client.SearchCustomEndpointWithBody(f.endpoint, map[string]any{
		"TU_NGAY":  from.Format("02/01/2006"),
		"DEN_NGAY": to.Format("02/01/2006"),
	})
}

func (f *cloudifyFetcher) ByCode(code string) ([]map[string]any, error) {
	return f.client.SearchCustomEndpointWithBody(f.endpoint, map[string]any{"SO_DON_HANG": code})
}

// NewCloudifyFetcher builds a Fetcher from the tenant's ERP settings (the same
// erp_api_* keys the chatbot gateway uses) and honours a custom orders path
// configured in erp_global_method_permissions.
func NewCloudifyFetcher(database *gorm.DB, tenantID, encryptionKey string) (Fetcher, error) {
	var rows []models.AppSetting
	keys := []string{"erp_api_url", "erp_api_db", "erp_api_username", "erp_api_password", "erp_global_method_permissions"}
	if err := database.Where("tenant_id = ? AND setting_key IN ?", tenantID, keys).Find(&rows).Error; err != nil {
		return nil, err
	}
	values := map[string]models.AppSetting{}
	for _, row := range rows {
		values[row.SettingKey] = row
	}
	client := &pkg.CloudifyClient{
		BaseURL: strings.TrimSpace(values["erp_api_url"].ValuePlain),
		DB:      strings.TrimSpace(values["erp_api_db"].ValuePlain),
		Login:   strings.TrimSpace(values["erp_api_username"].ValuePlain),
	}
	if pw := values["erp_api_password"]; len(pw.ValueEncrypted) > 0 {
		plain, err := pkg.Decrypt(pw.ValueEncrypted, encryptionKey)
		if err != nil {
			return nil, errors.New("cannot decrypt ERP password")
		}
		client.Password = string(plain)
	} else {
		client.Password = pw.ValuePlain
	}
	if client.BaseURL == "" || client.Login == "" || client.Password == "" {
		return nil, ErrNotConfigured
	}
	return &cloudifyFetcher{client: client, endpoint: ordersEndpoint(values["erp_global_method_permissions"].ValuePlain)}, nil
}

// ordersEndpoint mirrors the chatbot gateway: a custom "orders" path in
// erp_global_method_permissions overrides saorders/search.
func ordersEndpoint(raw string) string {
	var perms map[string]struct {
		Path string `json:"path"`
	}
	if strings.TrimSpace(raw) == "" || json.Unmarshal([]byte(raw), &perms) != nil {
		return DefaultOrdersEndpoint
	}
	path := strings.TrimSpace(perms["orders"].Path)
	if path == "" || path == "orders" {
		return DefaultOrdersEndpoint
	}
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimPrefix(path, "rest_api/private/")
	return strings.TrimPrefix(path, "/")
}
