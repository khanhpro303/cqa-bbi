package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

const (
	personalZaloStaffIdentity    = "staff"
	personalZaloCustomerIdentity = "customer"
	personalZaloPairingPrefix    = "pzl_"
	personalZaloPairingTTL       = 15 * time.Minute
	personalZaloPairingAttempts  = 5

	personalZaloInvalidPairingReply = "Dạ, mã xác thực không hợp lệ, đã được sử dụng hoặc đã hết hạn. Anh/chị vui lòng liên hệ nhân viên để được hỗ trợ ạ."
)

type personalZaloERPIdentity struct {
	AgentType string
	Customer  models.ZaloCustomer
	ID        string
}

func personalZaloIdentityAccountMatches(channel models.Channel, req personalZaloChatbotRequest) bool {
	return req.ThreadType == personalZaloThreadUser && req.SenderID == req.ThreadID &&
		channel.ChannelType == "personal_zalo_import" && channel.TenantID == req.TenantID && channel.ID == req.ChannelID &&
		personalZaloUIDPattern.MatchString(channel.ExternalID) && channel.ExternalID == req.AccountExternalID &&
		personalZaloUIDPattern.MatchString(req.SenderID) && req.SenderID != req.AccountExternalID
}

func resolvePersonalZaloERPIdentity(ctx context.Context, channel models.Channel, req personalZaloChatbotRequest) (personalZaloERPIdentity, error) {
	if !personalZaloIdentityAccountMatches(channel, req) {
		return personalZaloERPIdentity{}, nil
	}
	database := db.DB.WithContext(ctx)
	var staff models.ZaloWhitelist
	err := database.Where("tenant_id = ? AND channel_id = ? AND zalo_user_id = ? AND status = ?",
		channel.TenantID, channel.ID, req.SenderID, "active").Take(&staff).Error
	if err == nil {
		return personalZaloERPIdentity{AgentType: "private", ID: staff.ID}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return personalZaloERPIdentity{}, err
	}
	var links []models.PersonalZaloIdentityLink
	err = personalZaloIdentityLinks(database, channel).Where("sender_id = ? AND verified_at IS NOT NULL", req.SenderID).
		Order("CASE WHEN kind = 'staff' THEN 0 ELSE 1 END").Find(&links).Error
	if err != nil {
		return personalZaloERPIdentity{}, err
	}
	for _, link := range links {
		identity, err := loadPersonalZaloIdentitySource(database, channel, link.Kind, link.TargetID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue // Revoked CRM identities cannot grant access through an old link.
		}
		return identity, err
	}
	return personalZaloERPIdentity{}, nil
}

func loadPersonalZaloIdentitySource(database *gorm.DB, channel models.Channel, kind, targetID string) (personalZaloERPIdentity, error) {
	switch kind {
	case personalZaloStaffIdentity:
		var staff models.ZaloWhitelist
		err := personalZaloStaffOptions(database, channel).Where("zalo_whitelist.id = ?", targetID).Take(&staff).Error
		if err != nil {
			return personalZaloERPIdentity{}, err
		}
		return personalZaloERPIdentity{AgentType: "private", ID: staff.ID}, nil
	case personalZaloCustomerIdentity:
		var customer models.ZaloCustomer
		err := personalZaloCustomerOptions(database, channel.TenantID).Where("id = ?", targetID).Take(&customer).Error
		if err != nil {
			return personalZaloERPIdentity{}, err
		}
		return personalZaloERPIdentity{AgentType: "public", ID: customer.ID, Customer: customer}, nil
	default:
		return personalZaloERPIdentity{}, gorm.ErrRecordNotFound
	}
}

func personalZaloStaffOptions(database *gorm.DB, channel models.Channel) *gorm.DB {
	return database.Model(&models.ZaloWhitelist{}).
		Joins("JOIN channels AS source ON source.id = zalo_whitelist.channel_id AND source.tenant_id = zalo_whitelist.tenant_id").
		Where("zalo_whitelist.tenant_id = ? AND zalo_whitelist.status = ? AND zalo_whitelist.zalo_user_id <> '' AND (source.id = ? OR source.channel_type = ?)",
			channel.TenantID, "active", channel.ID, "zalo_oa")
}

func personalZaloCustomerOptions(database *gorm.DB, tenantID string) *gorm.DB {
	return database.Model(&models.ZaloCustomer{}).Where("tenant_id = ? AND status = ?", tenantID, "approved")
}

func personalZaloIdentityLinks(database *gorm.DB, channel models.Channel) *gorm.DB {
	return database.Model(&models.PersonalZaloIdentityLink{}).
		Where("tenant_id = ? AND channel_id = ? AND account_external_id = ?", channel.TenantID, channel.ID, channel.ExternalID)
}

func personalZaloVerificationCommand(text string) (code string, isCommand bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 || !strings.EqualFold(fields[0], "verify") {
		return "", false
	}
	if len(fields) != 2 {
		return "", true
	}
	payload, ok := strings.CutPrefix(fields[1], personalZaloPairingPrefix)
	if !ok || len(payload) != base64.RawURLEncoding.EncodedLen(32) {
		return "", true
	}
	if _, err := base64.RawURLEncoding.Strict().DecodeString(payload); err != nil {
		return "", true
	}
	return fields[1], true
}

// A 32-byte base64url payload uses 43 characters. Its last character has two zero padding bits.
var personalZaloPairingCodePattern = regexp.MustCompile(personalZaloPairingPrefix + `[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]`)

func redactPersonalZaloPairingCodes(text string) string {
	return personalZaloPairingCodePattern.ReplaceAllString(text, "[Mã xác thực Zalo đã được ẩn]")
}

var personalZaloPairingRateLimit = redis.NewScript(`
local attempts = redis.call('INCR', KEYS[1])
if attempts == 1 then
    redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return attempts
`)

func handlePersonalZaloIdentityVerification(ctx context.Context, channel models.Channel, req personalZaloChatbotRequest) (string, bool, error) {
	code, isCommand := personalZaloVerificationCommand(req.Text)
	if !isCommand {
		return "", false, nil
	}
	if req.ThreadType != personalZaloThreadUser {
		return "", true, nil
	}
	if !personalZaloIdentityAccountMatches(channel, req) || code == "" {
		return personalZaloInvalidPairingReply, true, nil
	}
	if db.RedisClient == nil {
		return "", true, errors.New("personal Zalo verification rate limiter unavailable")
	}
	key := "personal_zalo_verify:" + personalZaloIdentityDigest(channel.TenantID+"\x00"+channel.ExternalID+"\x00"+req.SenderID)
	attempts, err := personalZaloPairingRateLimit.Run(ctx, db.RedisClient, []string{key}, int(time.Hour.Seconds())).Int()
	if err != nil {
		return "", true, fmt.Errorf("personal Zalo verification rate limiter: %w", err)
	}
	if attempts > personalZaloPairingAttempts {
		return "Dạ, anh/chị đã thử xác thực quá nhiều lần. Vui lòng thử lại sau một giờ hoặc liên hệ nhân viên để được hỗ trợ ạ.", true, nil
	}
	verified, err := redeemPersonalZaloInvite(ctx, channel, req.SenderID, code, time.Now().UTC())
	if err != nil {
		return "", true, err
	}
	if !verified {
		return personalZaloInvalidPairingReply, true, nil
	}
	return "Dạ, tài khoản Zalo của anh/chị đã được xác thực cho kênh này. Anh/chị có thể gửi câu hỏi để em hỗ trợ ạ.", true, nil
}

func redeemPersonalZaloInvite(ctx context.Context, channel models.Channel, senderID, code string, now time.Time) (bool, error) {
	digest := personalZaloIdentityDigest(code)
	verified := false
	err := db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var link models.PersonalZaloIdentityLink
		pending := func() *gorm.DB {
			return personalZaloIdentityLinks(tx, channel).
				Where("invite_digest = ? AND verified_at IS NULL AND expires_at > ?", digest, now)
		}
		if err := pending().Take(&link).Error; err != nil {
			return err
		}
		if _, err := loadPersonalZaloIdentitySource(tx, channel, link.Kind, link.TargetID); err != nil {
			return err
		}
		result := pending().Where("id = ?", link.ID).Updates(map[string]any{
			"sender_id": senderID, "verified_at": now, "invite_digest": nil,
		})
		verified = result.RowsAffected == 1
		return result.Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) || personalZaloDuplicateIdentity(err) {
		return false, nil
	}
	return verified, err
}

func personalZaloIdentityDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func personalZaloDuplicateIdentity(err error) bool {
	var mysqlError *mysql.MySQLError
	return errors.Is(err, gorm.ErrDuplicatedKey) || (errors.As(err, &mysqlError) && mysqlError.Number == 1062)
}

func personalZaloIdentityAdminChannel(c *gin.Context) (models.Channel, bool) {
	var channel models.Channel
	err := db.DB.WithContext(c.Request.Context()).Select("id", "tenant_id", "channel_type", "external_id", "is_active", "personal_zalo_auto_reply_enabled", "metadata").
		Where("id = ? AND tenant_id = ? AND channel_type = ?", c.Param("channelId"), middleware.GetTenantID(c), "personal_zalo_import").
		Take(&channel).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "channel_not_found"})
	} else if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "personal_zalo_identity_unavailable"})
	}
	return channel, err == nil
}

func ListPersonalZaloIdentityLinks(c *gin.Context) {
	channel, ok := personalZaloIdentityAdminChannel(c)
	if !ok {
		return
	}
	links := make([]models.PersonalZaloIdentityLink, 0)
	if err := personalZaloIdentityLinks(db.DB.WithContext(c.Request.Context()), channel).Order("created_at DESC").Find(&links).Error; err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "personal_zalo_identity_unavailable"})
		return
	}
	c.JSON(http.StatusOK, links)
}

func ListPersonalZaloIdentityOptions(c *gin.Context) {
	channel, ok := personalZaloIdentityAdminChannel(c)
	if !ok {
		return
	}
	enabled, err := personalZaloIdentityPairingEnabled(c.Request.Context(), channel)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "personal_zalo_identity_unavailable"})
		return
	}
	type option struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		ChannelName  string `json:"channel_name,omitempty"`
		CustomerCode string `json:"customer_code,omitempty"`
	}
	database := db.DB.WithContext(c.Request.Context())
	staff := make([]option, 0)
	customers := make([]option, 0)
	if err := personalZaloStaffOptions(database, channel).Select("zalo_whitelist.id, zalo_whitelist.name, source.name AS channel_name").
		Order("zalo_whitelist.name, zalo_whitelist.id").Scan(&staff).Error; err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "personal_zalo_identity_unavailable"})
		return
	}
	if err := personalZaloCustomerOptions(database, channel.TenantID).Select("id, name, customer_code").Order("name, id").Scan(&customers).Error; err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "personal_zalo_identity_unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"staff": staff, "customers": customers, "pairing_enabled": enabled})
}

func personalZaloIdentityPairingEnabled(ctx context.Context, channel models.Channel) (bool, error) {
	if !channel.IsActive || !channel.PersonalZaloAutoReplyEnabled || !personalZaloUIDPattern.MatchString(channel.ExternalID) {
		return false, nil
	}
	metadata, err := decodeMetadataObject(channel.Metadata)
	if err != nil {
		return false, err
	}
	if scope, _ := metadata["sync_scope"].(string); scope == "group" {
		return false, nil
	}
	settings, err := loadPersonalZaloChatbotSettings(ctx, channel.TenantID, "chatbot_active")
	return err == nil && personalZaloChatbotMasterEnabled(settings), err
}

func InvitePersonalZaloIdentity(c *gin.Context) {
	var req struct {
		Kind     string `json:"kind" binding:"required,oneof=staff customer"`
		TargetID string `json:"target_id" binding:"required,max=36"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	channel, ok := personalZaloIdentityAdminChannel(c)
	if !ok {
		return
	}
	if !personalZaloUIDPattern.MatchString(channel.ExternalID) {
		c.JSON(http.StatusConflict, gin.H{"error": "personal_zalo_account_not_bound"})
		return
	}
	enabled, err := personalZaloIdentityPairingEnabled(c.Request.Context(), channel)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "personal_zalo_identity_unavailable"})
		return
	}
	if !enabled {
		c.JSON(http.StatusConflict, gin.H{"error": "personal_zalo_pairing_disabled"})
		return
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "personal_zalo_identity_unavailable"})
		return
	}
	code := personalZaloPairingPrefix + base64.RawURLEncoding.EncodeToString(secret[:])
	digest := personalZaloIdentityDigest(code)
	now := time.Now().UTC()
	link := models.PersonalZaloIdentityLink{
		ID: uuid.NewString(), TenantID: channel.TenantID, ChannelID: channel.ID, AccountExternalID: channel.ExternalID,
		Kind: req.Kind, TargetID: req.TargetID, InviteDigest: &digest, ExpiresAt: now.Add(personalZaloPairingTTL), CreatedAt: now,
	}
	err = db.DB.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if _, err := loadPersonalZaloIdentitySource(tx, channel, req.Kind, req.TargetID); err != nil {
			return err
		}
		if err := personalZaloIdentityLinks(tx, channel).Where("kind = ? AND target_id = ? AND verified_at IS NULL AND expires_at <= ?",
			req.Kind, req.TargetID, now).Delete(&models.PersonalZaloIdentityLink{}).Error; err != nil {
			return err
		}
		return tx.Create(&link).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "personal_zalo_identity_target_not_found"})
		return
	}
	if personalZaloDuplicateIdentity(err) {
		c.JSON(http.StatusConflict, gin.H{"error": "personal_zalo_identity_link_exists"})
		return
	}
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "personal_zalo_identity_unavailable"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, struct {
		models.PersonalZaloIdentityLink
		VerificationCommand string `json:"verification_command"`
	}{link, "verify " + code})
}

func DeletePersonalZaloIdentityLink(c *gin.Context) {
	channel, ok := personalZaloIdentityAdminChannel(c)
	if !ok {
		return
	}
	result := personalZaloIdentityLinks(db.DB.WithContext(c.Request.Context()), channel).Where("id = ?", c.Param("linkId")).
		Delete(&models.PersonalZaloIdentityLink{})
	if result.Error != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "personal_zalo_identity_unavailable"})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "personal_zalo_identity_link_not_found"})
		return
	}
	c.Status(http.StatusNoContent)
}
