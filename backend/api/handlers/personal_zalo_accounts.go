package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/contracts"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

const personalZaloAccountOwnersKey = "account_owners"

func GetPersonalZaloAccountOwners(c *gin.Context) {
	channel, ok := getPersonalZaloImportChannel(c)
	if !ok {
		return
	}

	usersByID, err := loadTenantUsersByID(channel.TenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_users_failed"})
		return
	}

	owners, err := decodePersonalZaloAccountOwners(channel.Metadata)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid_channel_metadata", "details": err.Error()})
		return
	}

	owners, err = normalizePersonalZaloAccountOwners(owners, usersByID)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "invalid_account_owners", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"account_owners": owners,
	})
}

func PutPersonalZaloAccountOwners(c *gin.Context) {
	channel, ok := getPersonalZaloImportChannel(c)
	if !ok {
		return
	}

	var req contracts.UpdatePersonalZaloAccountOwnersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	usersByID, err := loadTenantUsersByID(channel.TenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_users_failed"})
		return
	}

	owners, err := normalizePersonalZaloAccountOwners(req.AccountOwners, usersByID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_account_owners", "details": err.Error()})
		return
	}

	updatedMetadata, err := encodePersonalZaloAccountOwners(channel.Metadata, owners)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "encode_metadata_failed"})
		return
	}

	if err := db.DB.Model(&models.Channel{}).
		Where("id = ? AND tenant_id = ?", channel.ID, channel.TenantID).
		Updates(map[string]interface{}{
			"metadata":   updatedMetadata,
			"updated_at": time.Now(),
		}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update_channel_failed"})
		return
	}

	db.LogActivity(
		channel.TenantID,
		middleware.GetUserID(c),
		middleware.GetUserEmail(c),
		"channel.personal_zalo_account_owners.update",
		"channel",
		channel.ID,
		fmt.Sprintf("Updated personal Zalo account owners for channel: %s", channel.Name),
		"",
		c.ClientIP(),
	)

	c.JSON(http.StatusOK, gin.H{
		"message":        "updated",
		"account_owners": owners,
	})
}

func getPersonalZaloImportChannel(c *gin.Context) (models.Channel, bool) {
	tenantID := middleware.GetTenantID(c)
	channelID := c.Param("channelId")

	var channel models.Channel
	if err := db.DB.Where("id = ? AND tenant_id = ?", channelID, tenantID).First(&channel).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "channel_not_found"})
		return models.Channel{}, false
	}
	if channel.ChannelType != "personal_zalo_import" {
		c.JSON(http.StatusConflict, gin.H{"error": "channel_type_not_supported"})
		return models.Channel{}, false
	}

	return channel, true
}

func loadTenantUsersByID(tenantID string) (map[string]TenantUserResponse, error) {
	type tenantUserRow struct {
		UserID      string `json:"user_id"`
		Email       string `json:"email"`
		Name        string `json:"name"`
		Role        string `json:"role"`
		Permissions string `json:"permissions"`
	}

	var rows []tenantUserRow
	if err := db.DB.Table("user_tenants").
		Select("user_tenants.user_id, users.email, users.name, user_tenants.role, user_tenants.permissions").
		Joins("JOIN users ON users.id = user_tenants.user_id").
		Where("user_tenants.tenant_id = ?", tenantID).
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	usersByID := make(map[string]TenantUserResponse, len(rows))
	for _, row := range rows {
		usersByID[row.UserID] = TenantUserResponse{
			UserID:      row.UserID,
			Email:       row.Email,
			Name:        row.Name,
			Role:        row.Role,
			Permissions: row.Permissions,
		}
	}

	return usersByID, nil
}

func decodePersonalZaloAccountOwners(raw string) ([]contracts.PersonalZaloAccountOwner, error) {
	metadata, err := decodeMetadataObject(raw)
	if err != nil {
		return nil, err
	}

	value, ok := metadata[personalZaloAccountOwnersKey]
	if !ok || value == nil {
		return nil, nil
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}

	var owners []contracts.PersonalZaloAccountOwner
	if err := json.Unmarshal(encoded, &owners); err != nil {
		return nil, err
	}
	return owners, nil
}

func encodePersonalZaloAccountOwners(raw string, owners []contracts.PersonalZaloAccountOwner) (string, error) {
	metadata, err := decodeMetadataObject(raw)
	if err != nil {
		return "", err
	}

	if len(owners) == 0 {
		delete(metadata, personalZaloAccountOwnersKey)
	} else {
		metadata[personalZaloAccountOwnersKey] = owners
	}

	encoded, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func resolvePersonalZaloAccountOwner(tenantID string, channel *models.Channel, accountExternalID string) (*contracts.PersonalZaloAccountOwner, string, error) {
	if channel == nil || channel.ChannelType != "personal_zalo_import" {
		return nil, "", nil
	}

	owners, err := decodePersonalZaloAccountOwners(channel.Metadata)
	if err != nil {
		return nil, "", err
	}
	if len(owners) == 0 {
		return nil, "", nil
	}

	usersByID, err := loadTenantUsersByID(tenantID)
	if err != nil {
		return nil, "", err
	}

	owners, err = normalizePersonalZaloAccountOwners(owners, usersByID)
	if err != nil {
		return nil, "", err
	}

	accountExternalID = strings.TrimSpace(accountExternalID)
	exactIdx := -1
	bootstrapIdx := -1
	for i, owner := range owners {
		switch {
		case owner.AccountExternalID == accountExternalID && accountExternalID != "":
			exactIdx = i
		case owner.AccountExternalID == "":
			bootstrapIdx = i
		}
	}

	if exactIdx >= 0 {
		resolved := owners[exactIdx]
		return &resolved, "", nil
	}
	if bootstrapIdx < 0 || accountExternalID == "" {
		return nil, "", nil
	}

	owners[bootstrapIdx].AccountExternalID = accountExternalID
	updatedMetadata, err := encodePersonalZaloAccountOwners(channel.Metadata, owners)
	if err != nil {
		return nil, "", err
	}

	resolved := owners[bootstrapIdx]
	return &resolved, updatedMetadata, nil
}

func normalizePersonalZaloAccountOwners(owners []contracts.PersonalZaloAccountOwner, usersByID map[string]TenantUserResponse) ([]contracts.PersonalZaloAccountOwner, error) {
	if len(owners) == 0 {
		return nil, nil
	}

	normalized := make([]contracts.PersonalZaloAccountOwner, 0, len(owners))
	seenAccounts := make(map[string]struct{}, len(owners))
	blankAccounts := 0

	for _, owner := range owners {
		owner.UserID = strings.TrimSpace(owner.UserID)
		owner.AccountExternalID = strings.TrimSpace(owner.AccountExternalID)
		if owner.UserID == "" {
			return nil, errors.New("user_id is required")
		}

		user, ok := usersByID[owner.UserID]
		if !ok {
			return nil, fmt.Errorf("user %s is not in tenant", owner.UserID)
		}

		if owner.AccountExternalID == "" {
			blankAccounts++
			if blankAccounts > 1 {
				return nil, errors.New("only one bootstrap mapping without account_external_id is allowed")
			}
		} else {
			accountKey := strings.ToLower(owner.AccountExternalID)
			if _, exists := seenAccounts[accountKey]; exists {
				return nil, fmt.Errorf("duplicate account_external_id %s", owner.AccountExternalID)
			}
			seenAccounts[accountKey] = struct{}{}
		}

		owner.UserName = user.Name
		owner.UserEmail = user.Email
		normalized = append(normalized, owner)
	}

	return normalized, nil
}

func decodeMetadataObject(raw string) (map[string]interface{}, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]interface{}{}, nil
	}

	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return nil, err
	}
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	return metadata, nil
}
