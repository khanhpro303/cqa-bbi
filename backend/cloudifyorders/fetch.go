package cloudifyorders

import (
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
// erp_api_* keys the chatbot gateway uses).
func NewCloudifyFetcher(database *gorm.DB, tenantID, encryptionKey string) (Fetcher, error) {
	var rows []models.AppSetting
	keys := []string{"erp_api_url", "erp_api_db", "erp_api_username", "erp_api_password"}
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
	return &cloudifyFetcher{client: client, endpoint: DefaultOrdersEndpoint}, nil
}
