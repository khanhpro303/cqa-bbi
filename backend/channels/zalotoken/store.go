// Package zalotoken coordinates Zalo OA token rotation across every adapter
// that serves a stored channel.
package zalotoken

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// ChannelStore persists rotated Zalo OA tokens on the channel row.
// Rotation runs inside a transaction holding a row lock, so concurrent
// adapters — even in different processes (API server vs webhook worker) —
// rotate one at a time, and the loser of the race reuses the winner's tokens
// instead of replaying the already consumed single-use refresh token.
type ChannelStore struct {
	ChannelID     string
	EncryptionKey string
}

// NewAdapter builds a Zalo OA adapter whose token rotation is
// persisted to (and coordinated through) the given channel row. Every adapter
// for a stored channel must be built here; a bare adapter would rotate tokens
// in memory only and silently invalidate the channel's stored refresh token.
func NewAdapter(channelID string, creds channels.ZaloOACredentials, encryptionKey string) *channels.ZaloOAAdapter {
	adapter := channels.NewZaloOAAdapter(creds)
	adapter.SetTokenStore(ChannelStore{ChannelID: channelID, EncryptionKey: encryptionKey})
	return adapter
}

// rotationTimeout bounds one rotation (row lock + Zalo OAuth call + commit).
// It stays below MySQL's default 50s innodb_lock_wait_timeout.
const rotationTimeout = 40 * time.Second

func (s ChannelStore) Rotate(ctx context.Context, staleAccessToken string, exchange channels.ZaloRefreshExchange) (channels.ZaloTokenPair, error) {
	// Detach from the caller's cancellation: once Zalo has consumed the old
	// refresh token, aborting before commit would lose the new pair for good.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rotationTimeout)
	defer cancel()

	var pair channels.ZaloTokenPair
	err := db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ch models.Channel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&ch, "id = ?", s.ChannelID).Error; err != nil {
			return fmt.Errorf("lock zalo channel %s: %w", s.ChannelID, err)
		}

		credJSON, err := pkg.Decrypt(ch.CredentialsEncrypted, s.EncryptionKey)
		if err != nil {
			return fmt.Errorf("decrypt zalo channel %s credentials: %w", s.ChannelID, err)
		}

		updatedJSON, rotated, changed, err := rotateCredentials(ctx, credJSON, staleAccessToken, exchange)
		if err != nil {
			return err
		}
		pair = rotated
		if !changed {
			log.Printf("[zalo-token] channel %s: tokens already rotated by another worker, reusing persisted pair", s.ChannelID)
			return nil
		}

		encrypted, err := pkg.Encrypt(updatedJSON, s.EncryptionKey)
		if err != nil {
			return fmt.Errorf("encrypt zalo channel %s credentials: %w", s.ChannelID, err)
		}
		if err := tx.Model(&models.Channel{}).Where("id = ?", s.ChannelID).Update("credentials_encrypted", encrypted).Error; err != nil {
			return fmt.Errorf("persist rotated zalo tokens for channel %s: %w", s.ChannelID, err)
		}
		return nil
	})
	if err != nil {
		log.Printf("[zalo-token] channel %s: token rotation failed: %v", s.ChannelID, err)
		return channels.ZaloTokenPair{}, err
	}
	log.Printf("[zalo-token] channel %s: token rotation ok", s.ChannelID)
	return pair, nil
}

// rotateCredentials decides how to rotate the stored credentials JSON.
// When the stored access token no longer equals staleAccessToken another
// adapter already rotated it: the stored pair is returned with changed=false
// and Zalo is not called. Otherwise the STORED refresh token (never a possibly
// outdated in-memory copy) is exchanged and the new pair is merged into the
// credentials, keeping unrelated fields such as oa_name.
func rotateCredentials(ctx context.Context, credJSON []byte, staleAccessToken string, exchange channels.ZaloRefreshExchange) ([]byte, channels.ZaloTokenPair, bool, error) {
	var creds map[string]interface{}
	if err := json.Unmarshal(credJSON, &creds); err != nil {
		return nil, channels.ZaloTokenPair{}, false, fmt.Errorf("parse zalo credentials: %w", err)
	}

	storedAccess, _ := creds["access_token"].(string)
	storedRefresh, _ := creds["refresh_token"].(string)
	if storedAccess != "" && storedAccess != staleAccessToken {
		return credJSON, channels.ZaloTokenPair{AccessToken: storedAccess, RefreshToken: storedRefresh}, false, nil
	}

	pair, err := exchange(ctx, storedRefresh)
	if err != nil {
		return nil, channels.ZaloTokenPair{}, false, err
	}

	updated := make(map[string]interface{}, len(creds))
	for k, v := range creds {
		updated[k] = v
	}
	updated["access_token"] = pair.AccessToken
	updated["refresh_token"] = pair.RefreshToken

	updatedJSON, err := json.Marshal(updated)
	if err != nil {
		return nil, channels.ZaloTokenPair{}, false, fmt.Errorf("encode zalo credentials: %w", err)
	}
	return updatedJSON, pair, true, nil
}
