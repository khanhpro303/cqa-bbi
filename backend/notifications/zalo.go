package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// ZaloNotifier sends a notification into a Zalo GMF group via an OA adapter.
// Used as a job-output destination (parallel to telegram/email) so analysis
// results can be pushed straight into a GMF group, the same delivery channel
// CRM campaign alerts already use.
type ZaloNotifier struct {
	adapter     *channels.ZaloOAAdapter
	zaloGroupID string
}

// NewZaloNotifier wraps a ready adapter + resolved Zalo group id.
func NewZaloNotifier(adapter *channels.ZaloOAAdapter, zaloGroupID string) *ZaloNotifier {
	return &ZaloNotifier{adapter: adapter, zaloGroupID: zaloGroupID}
}

// Send delivers subject+body as one plain-text group message. Zalo group
// messages are plain text, so the caller's "default" body for type "zalo" is
// already built without HTML tags.
func (z *ZaloNotifier) Send(ctx context.Context, subject string, body string) error {
	msg := strings.TrimSpace(body)
	if s := strings.TrimSpace(subject); s != "" {
		msg = s + "\n\n" + msg
	}
	return z.adapter.SendGroupMessage(ctx, z.zaloGroupID, msg)
}

// HealthCheck is a no-op; the send call itself surfaces auth/permission errors.
func (z *ZaloNotifier) HealthCheck(_ context.Context) error { return nil }

// BuildZaloGroupNotifier builds a ZaloNotifier for the given OA channel + CRM
// group. It decrypts the channel credentials and wires the same token-refresh
// persistence callback used across the codebase, then resolves the CRM group's
// Zalo group id. Returns an error (never a partial notifier) on any misconfig so
// callers can log + skip safely.
func BuildZaloGroupNotifier(tenantID, channelID, groupID string) (*ZaloNotifier, error) {
	if strings.TrimSpace(channelID) == "" || strings.TrimSpace(groupID) == "" {
		return nil, fmt.Errorf("zalo output requires channel_id and group_id")
	}

	var channel models.Channel
	if err := db.DB.Where("id = ? AND tenant_id = ? AND channel_type = ?", channelID, tenantID, "zalo_oa").First(&channel).Error; err != nil {
		return nil, fmt.Errorf("zalo channel not found: %w", err)
	}

	var group models.CRMGroup
	if err := db.DB.Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
		return nil, fmt.Errorf("crm group not found: %w", err)
	}
	if strings.TrimSpace(group.ZaloGroupID) == "" {
		return nil, fmt.Errorf("crm group %s is not linked to a Zalo group", groupID)
	}

	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	credBytes, err := pkg.Decrypt(channel.CredentialsEncrypted, cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt channel credentials: %w", err)
	}
	var creds channels.ZaloOACredentials
	if err := json.Unmarshal(credBytes, &creds); err != nil {
		return nil, fmt.Errorf("parse channel credentials: %w", err)
	}

	adapter := channels.NewZaloOAAdapter(creds)
	adapter.SetTokenRefreshCallback(func(newAccess, newRefresh string) {
		credsMap := map[string]interface{}{
			"app_id":        creds.AppID,
			"app_secret":    creds.AppSecret,
			"access_token":  newAccess,
			"refresh_token": newRefresh,
			"oa_id":         creds.OAId,
		}
		newCredJSON, _ := json.Marshal(credsMap)
		if encrypted, e := pkg.Encrypt(newCredJSON, cfg.EncryptionKey); e == nil {
			db.DB.Model(&channel).Update("credentials_encrypted", encrypted)
		}
	})
	return NewZaloNotifier(adapter, group.ZaloGroupID), nil
}
