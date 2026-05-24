package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/contracts"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

type personalZaloGatewayAccountRecord struct {
	ID                string                 `json:"id"`
	TenantID          string                 `json:"tenantId"`
	ChannelID         string                 `json:"channelId"`
	ImportEndpoint    string                 `json:"importEndpoint,omitempty"`
	ImportSecret      string                 `json:"importSecret,omitempty"`
	AccountExternalID string                 `json:"accountExternalId,omitempty"`
	Status            string                 `json:"status"`
	DisplayName       string                 `json:"displayName,omitempty"`
	AvatarURL         string                 `json:"avatarUrl,omitempty"`
	ZaloUID           string                 `json:"zaloUid,omitempty"`
	QRImage           string                 `json:"qrImage,omitempty"`
	QRGeneratedAt     string                 `json:"qrGeneratedAt,omitempty"`
	SessionData       map[string]interface{} `json:"sessionData,omitempty"`
	LastError         string                 `json:"lastError,omitempty"`
	LastImportedAt    string                 `json:"lastImportedAt,omitempty"`
	CreatedAt         string                 `json:"createdAt,omitempty"`
	UpdatedAt         string                 `json:"updatedAt,omitempty"`
}

type personalZaloGatewayListResponse struct {
	Accounts []personalZaloGatewayAccountRecord `json:"accounts"`
}

type personalZaloGatewayConnectResponse struct {
	GatewayConfigured bool                              `json:"gateway_configured"`
	GatewayReachable  bool                              `json:"gateway_reachable"`
	AccountExists     bool                              `json:"account_exists"`
	NextAction        string                            `json:"next_action,omitempty"`
	Message           string                            `json:"message,omitempty"`
	Account           *personalZaloGatewayAccountClient `json:"account,omitempty"`
}

type personalZaloGatewayAccountClient struct {
	ID                string `json:"id"`
	AccountExternalID string `json:"account_external_id,omitempty"`
	Status            string `json:"status"`
	DisplayName       string `json:"display_name,omitempty"`
	AvatarURL         string `json:"avatar_url,omitempty"`
	ZaloUID           string `json:"zalo_uid,omitempty"`
	QRImage           string `json:"qr_image,omitempty"`
	QRGeneratedAt     string `json:"qr_generated_at,omitempty"`
	LastError         string `json:"last_error,omitempty"`
	LastImportedAt    string `json:"last_imported_at,omitempty"`
	CreatedAt         string `json:"created_at,omitempty"`
	UpdatedAt         string `json:"updated_at,omitempty"`
}

func GetPersonalZaloGatewayState(c *gin.Context) {
	channel, ok := getPersonalZaloImportChannel(c)
	if !ok {
		return
	}

	cfg, err := config.Load()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config_load_failed"})
		return
	}

	resp, statusCode, err := loadPersonalZaloGatewayState(cfg, channel)
	if err != nil {
		c.JSON(statusCode, gin.H{"error": "gateway_request_failed", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func ConnectPersonalZaloGateway(c *gin.Context) {
	channel, ok := getPersonalZaloImportChannel(c)
	if !ok {
		return
	}
	if !channel.IsActive {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "channel_is_inactive",
			"message": "Kênh chat đã tạm dừng hoạt động.",
		})
		return
	}

	cfg, err := config.Load()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config_load_failed"})
		return
	}
	if cfg.PersonalZaloGatewayBaseURL == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "gateway_not_configured"})
		return
	}

	account, err := ensurePersonalZaloGatewayAccount(c, cfg, channel)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "gateway_account_setup_failed", "details": err.Error()})
		return
	}

	switch determinePersonalZaloGatewayNextAction(account) {
	case "sync_now":
		// Already connected. Return current state.
	case "reconnect":
		if err := personalZaloGatewayAction(cfg.PersonalZaloGatewayBaseURL, account.ID, "reconnect"); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "gateway_reconnect_failed", "details": err.Error()})
			return
		}
	case "scan_qr", "create_account":
		if err := personalZaloGatewayAction(cfg.PersonalZaloGatewayBaseURL, account.ID, "login/qr"); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "gateway_qr_start_failed", "details": err.Error()})
			return
		}
	}

	latest, err := waitForPersonalZaloGatewayAccount(cfg.PersonalZaloGatewayBaseURL, account.ID, 6*time.Second)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "gateway_refresh_failed", "details": err.Error()})
		return
	}

	c.JSON(http.StatusAccepted, buildPersonalZaloGatewayStateResponse(true, true, latest))
}

func ReconnectPersonalZaloGateway(c *gin.Context) {
	channel, ok := getPersonalZaloImportChannel(c)
	if !ok {
		return
	}
	if !channel.IsActive {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "channel_is_inactive",
			"message": "Kênh chat đã tạm dừng hoạt động.",
		})
		return
	}

	cfg, err := config.Load()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config_load_failed"})
		return
	}

	resp, _, err := loadPersonalZaloGatewayState(cfg, channel)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "gateway_request_failed", "details": err.Error()})
		return
	}
	if resp.Account == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "gateway_account_not_found"})
		return
	}

	if err := personalZaloGatewayAction(cfg.PersonalZaloGatewayBaseURL, resp.Account.ID, "reconnect"); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "gateway_reconnect_failed", "details": err.Error()})
		return
	}

	latest, err := waitForPersonalZaloGatewayAccount(cfg.PersonalZaloGatewayBaseURL, resp.Account.ID, 5*time.Second)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "gateway_refresh_failed", "details": err.Error()})
		return
	}

	c.JSON(http.StatusAccepted, buildPersonalZaloGatewayStateResponse(true, true, latest))
}

func SyncPersonalZaloGateway(c *gin.Context) {
	channel, ok := getPersonalZaloImportChannel(c)
	if !ok {
		return
	}
	if !channel.IsActive {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "channel_is_inactive",
			"message": "Kênh chat đã tạm dừng hoạt động.",
		})
		return
	}

	cfg, err := config.Load()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config_load_failed"})
		return
	}

	resp, _, err := loadPersonalZaloGatewayState(cfg, channel)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "gateway_request_failed", "details": err.Error()})
		return
	}
	if resp.Account == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "gateway_account_not_found"})
		return
	}
	if resp.Account.Status != "connected" {
		c.JSON(http.StatusConflict, gin.H{"error": "gateway_account_not_connected", "message": "Hãy kết nối Zalo cá nhân trước khi đồng bộ."})
		return
	}

	if err := personalZaloGatewayAction(cfg.PersonalZaloGatewayBaseURL, resp.Account.ID, "sync"); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "gateway_sync_failed", "details": err.Error()})
		return
	}

	latest, err := waitForPersonalZaloGatewayAccount(cfg.PersonalZaloGatewayBaseURL, resp.Account.ID, 3*time.Second)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "gateway_refresh_failed", "details": err.Error()})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"message": "sync_queued",
		"state":   buildPersonalZaloGatewayStateResponse(true, true, latest),
	})
}

func loadPersonalZaloGatewayState(cfg *config.Config, channel models.Channel) (personalZaloGatewayConnectResponse, int, error) {
	if cfg.PersonalZaloGatewayBaseURL == "" {
		return personalZaloGatewayConnectResponse{
			GatewayConfigured: false,
			GatewayReachable:  false,
			AccountExists:     false,
			NextAction:        "configure_gateway",
			Message:           "Thiếu PERSONAL_ZALO_GATEWAY_BASE_URL trong backend.",
		}, http.StatusOK, nil
	}

	accounts, err := listPersonalZaloGatewayAccounts(cfg.PersonalZaloGatewayBaseURL)
	if err != nil {
		return personalZaloGatewayConnectResponse{
			GatewayConfigured: true,
			GatewayReachable:  false,
			AccountExists:     false,
			NextAction:        "fix_gateway",
			Message:           "Không kết nối được personal-zalo-gateway.",
		}, http.StatusBadGateway, err
	}

	account := findPersonalZaloGatewayAccount(accounts, channel.TenantID, channel.ID)
	return buildPersonalZaloGatewayStateResponse(true, true, account), http.StatusOK, nil
}

func ensurePersonalZaloGatewayAccount(c *gin.Context, cfg *config.Config, channel models.Channel) (*personalZaloGatewayAccountRecord, error) {
	accounts, err := listPersonalZaloGatewayAccounts(cfg.PersonalZaloGatewayBaseURL)
	if err != nil {
		return nil, err
	}

	creds, err := decodePersonalZaloImportCredentials(channel, cfg)
	if err != nil {
		return nil, err
	}

	importEndpoint := getInternalBaseURL(c, cfg) + "/api/internal/imports/personal-zalo"
	payload := map[string]string{
		"tenantId":       channel.TenantID,
		"channelId":      channel.ID,
		"importEndpoint": importEndpoint,
		"importSecret":   creds.ImportSecret,
		"displayName":    channel.Name,
	}
	if channel.ExternalID != "" {
		payload["accountExternalId"] = channel.ExternalID
	}

	account := findPersonalZaloGatewayAccount(accounts, channel.TenantID, channel.ID)
	if account == nil {
		var created personalZaloGatewayAccountRecord
		if err := personalZaloGatewayJSON(cfg.PersonalZaloGatewayBaseURL, http.MethodPost, "/api/v1/accounts", payload, &created); err != nil {
			return nil, err
		}
		return &created, nil
	}

	var updated personalZaloGatewayAccountRecord
	if err := personalZaloGatewayJSON(cfg.PersonalZaloGatewayBaseURL, http.MethodPut, "/api/v1/accounts/"+account.ID, payload, &updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

func decodePersonalZaloImportCredentials(channel models.Channel, cfg *config.Config) (*contracts.PersonalZaloImportCredentials, error) {
	credBytes, err := pkg.Decrypt(channel.CredentialsEncrypted, cfg.EncryptionKey)
	if err != nil {
		return nil, err
	}

	var creds contracts.PersonalZaloImportCredentials
	if err := json.Unmarshal(credBytes, &creds); err != nil {
		return nil, err
	}
	if strings.TrimSpace(creds.ImportSecret) == "" {
		return nil, errors.New("missing_import_secret")
	}
	return &creds, nil
}

func listPersonalZaloGatewayAccounts(baseURL string) ([]personalZaloGatewayAccountRecord, error) {
	var resp personalZaloGatewayListResponse
	if err := personalZaloGatewayJSON(baseURL, http.MethodGet, "/api/v1/accounts", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Accounts, nil
}

func fetchPersonalZaloGatewayAccount(baseURL, accountID string) (*personalZaloGatewayAccountRecord, error) {
	var account personalZaloGatewayAccountRecord
	if err := personalZaloGatewayJSON(baseURL, http.MethodGet, "/api/v1/accounts/"+accountID, nil, &account); err != nil {
		return nil, err
	}
	return &account, nil
}

func personalZaloGatewayAction(baseURL, accountID, action string) error {
	return personalZaloGatewayJSON(baseURL, http.MethodPost, "/api/v1/accounts/"+accountID+"/"+action, map[string]string{}, nil)
}

func personalZaloGatewayJSON(baseURL, method, path string, payload interface{}, out interface{}) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, strings.TrimRight(baseURL, "/")+path, body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClientWithTimeout.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("gateway %s %s failed: %s", method, path, strings.TrimSpace(string(raw)))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func findPersonalZaloGatewayAccount(accounts []personalZaloGatewayAccountRecord, tenantID, channelID string) *personalZaloGatewayAccountRecord {
	matches := make([]personalZaloGatewayAccountRecord, 0, 1)
	for _, account := range accounts {
		if account.TenantID == tenantID && account.ChannelID == channelID {
			matches = append(matches, account)
		}
	}
	if len(matches) == 0 {
		return nil
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].UpdatedAt > matches[j].UpdatedAt
	})
	return &matches[0]
}

func waitForPersonalZaloGatewayAccount(baseURL, accountID string, timeout time.Duration) (*personalZaloGatewayAccountRecord, error) {
	deadline := time.Now().Add(timeout)
	for {
		account, err := fetchPersonalZaloGatewayAccount(baseURL, accountID)
		if err != nil {
			return nil, err
		}
		if account.Status == "connected" || account.QRImage != "" || time.Now().After(deadline) {
			return account, nil
		}
		time.Sleep(600 * time.Millisecond)
	}
}

func buildPersonalZaloGatewayStateResponse(configured, reachable bool, account *personalZaloGatewayAccountRecord) personalZaloGatewayConnectResponse {
	resp := personalZaloGatewayConnectResponse{
		GatewayConfigured: configured,
		GatewayReachable:  reachable,
		AccountExists:     account != nil,
		NextAction:        determinePersonalZaloGatewayNextAction(account),
	}
	if account != nil {
		resp.Account = sanitizePersonalZaloGatewayAccount(account)
	}
	return resp
}

func sanitizePersonalZaloGatewayAccount(account *personalZaloGatewayAccountRecord) *personalZaloGatewayAccountClient {
	if account == nil {
		return nil
	}
	return &personalZaloGatewayAccountClient{
		ID:                account.ID,
		AccountExternalID: account.AccountExternalID,
		Status:            account.Status,
		DisplayName:       account.DisplayName,
		AvatarURL:         account.AvatarURL,
		ZaloUID:           account.ZaloUID,
		QRImage:           account.QRImage,
		QRGeneratedAt:     account.QRGeneratedAt,
		LastError:         account.LastError,
		LastImportedAt:    account.LastImportedAt,
		CreatedAt:         account.CreatedAt,
		UpdatedAt:         account.UpdatedAt,
	}
}

func determinePersonalZaloGatewayNextAction(account *personalZaloGatewayAccountRecord) string {
	if account == nil {
		return "create_account"
	}
	switch account.Status {
	case "connected":
		return "sync_now"
	case "connecting":
		return "wait_for_connection"
	case "qr_pending":
		return "scan_qr"
	case "disconnected":
		if len(account.SessionData) > 0 && account.LastError != "session_unstable_qr_required" {
			return "reconnect"
		}
		return "scan_qr"
	default:
		return "create_account"
	}
}
