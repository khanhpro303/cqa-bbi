package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/realtime"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	personalZaloPending  = "pending"
	personalZaloApproved = "approved"
	personalZaloRejected = "rejected"

	maxPersonalZaloRosterEntries = 25000
)

var errPersonalZaloRosterConflict = errors.New("personal Zalo roster changed concurrently")

type personalZaloRosterEntry struct {
	ThreadType  string `json:"thread_type"`
	ThreadID    string `json:"thread_id"`
	DisplayName string `json:"display_name"`
}

type personalZaloRosterSnapshot struct {
	AccountExternalID string                    `json:"account_external_id"`
	CapturedAt        time.Time                 `json:"captured_at"`
	Entries           []personalZaloRosterEntry `json:"entries"`
}

type personalZaloRosterEntryKey struct {
	threadType string
	threadID   string
}

func (snapshot *personalZaloRosterSnapshot) validate(now time.Time) bool {
	if !personalZaloUIDPattern.MatchString(snapshot.AccountExternalID) || snapshot.Entries == nil ||
		len(snapshot.Entries) > maxPersonalZaloRosterEntries ||
		now.Sub(snapshot.CapturedAt) > 2*time.Minute || snapshot.CapturedAt.After(now.Add(30*time.Second)) {
		return false
	}
	snapshot.CapturedAt = snapshot.CapturedAt.UTC().Truncate(time.Millisecond)
	seen := make(map[personalZaloRosterEntryKey]struct{}, len(snapshot.Entries))
	for i := range snapshot.Entries {
		entry := &snapshot.Entries[i]
		if !validPersonalZaloApprovalThread(entry.ThreadType, entry.ThreadID) ||
			(entry.ThreadType == personalZaloThreadUser && entry.ThreadID == snapshot.AccountExternalID) {
			return false
		}
		key := personalZaloRosterEntryKey{entry.ThreadType, entry.ThreadID}
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
		name := []rune(strings.TrimSpace(entry.DisplayName))
		if len(name) > 255 {
			name = name[:255]
		}
		entry.DisplayName = string(name)
	}
	return true
}

func validPersonalZaloApprovalThread(threadType, threadID string) bool {
	return (threadType == personalZaloThreadUser || threadType == personalZaloThreadGroup) &&
		personalZaloUIDPattern.MatchString(threadID)
}

func IngestPersonalZaloRoster(c *gin.Context) {
	var req struct {
		RequestID string `json:"request_id" binding:"required,max=64"`
		TenantID  string `json:"tenant_id" binding:"required,max=36"`
		ChannelID string `json:"channel_id" binding:"required,max=36"`
		CheckOnly bool   `json:"check_only"`
		personalZaloRosterSnapshot
	}
	if c.ShouldBindJSON(&req) != nil || !personalZaloUIDPattern.MatchString(req.AccountExternalID) ||
		(!req.CheckOnly && !req.validate(time.Now())) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_roster_snapshot"})
		return
	}
	if c.GetString("internal_request_id") != req.RequestID {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "request_id_mismatch"})
		return
	}

	ctx := c.Request.Context()
	if req.CheckOnly {
		var channel models.Channel
		err := db.DB.WithContext(ctx).Select("external_id", "is_active", "personal_zalo_auto_reply_enabled", "metadata").
			Where("id = ? AND tenant_id = ? AND channel_type = ?", req.ChannelID, req.TenantID, "personal_zalo_import").
			Take(&channel).Error
		bot, configErr := decodePersonalZaloInternbotConfig(channel.Metadata)
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			c.Status(http.StatusNoContent)
		case err != nil || configErr != nil:
			c.Status(http.StatusServiceUnavailable)
		case channel.IsActive && (channel.PersonalZaloAutoReplyEnabled || bot.Mode == personalZaloInternbotMode) &&
			(channel.ExternalID == "" || channel.ExternalID == req.AccountExternalID):
			c.Status(http.StatusOK)
		default:
			c.Status(http.StatusNoContent)
		}
		return
	}

	var changed bool
	err := db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		channel, err := lockPersonalZaloRosterChannel(tx, req.TenantID, req.ChannelID)
		if err != nil || !channel.IsActive ||
			(channel.ExternalID != "" && channel.ExternalID != req.AccountExternalID) {
			return err
		}
		bot, err := decodePersonalZaloInternbotConfig(channel.Metadata)
		if err != nil || (!channel.PersonalZaloAutoReplyEnabled && bot.Mode != personalZaloInternbotMode) {
			return err
		}
		if channel.ExternalID == "" {
			if err := tx.Model(&channel).UpdateColumn("external_id", req.AccountExternalID).Error; err != nil {
				return err
			}
			channel.ExternalID = req.AccountExternalID
		}
		if bot.Mode == personalZaloInternbotMode {
			snapshot, err := savePersonalZaloInternbotRoster(tx, channel, internbotGroupsFromRoster(req.personalZaloRosterSnapshot))
			if err != nil || !bot.Enabled || (bot.SupervisorAccountID == channel.ExternalID && snapshot.contains(bot.SupervisorGroupID)) {
				return err
			}
			metadata, err := decodeMetadataObject(channel.Metadata)
			if err != nil {
				return err
			}
			bot, err = planPersonalZaloInternbotUpdate(bot, personalZaloInternbotPatch{}, false, false)
			if err != nil {
				return err
			}
			writePersonalZaloInternbotConfig(metadata, bot)
			encoded, _ := json.Marshal(metadata)
			return tx.Model(&channel).Update("metadata", string(encoded)).Error
		}
		changed, err = reconcilePersonalZaloRoster(tx, channel.ID, req.personalZaloRosterSnapshot, false)
		return err
	})
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) && !errors.Is(err, errPersonalZaloRosterConflict) {
		log.Printf("[personal-zalo] failed to reconcile roster for channel %s: %v", req.ChannelID, err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "personal_zalo_roster_unavailable"})
		return
	}
	if err == nil && changed {
		realtime.Publish(ctx, req.TenantID, realtime.EventPersonalZaloApprovalsUpdated)
	}
	c.Status(http.StatusNoContent)
}

func lockPersonalZaloRosterChannel(tx *gorm.DB, tenantID, channelID string) (models.Channel, error) {
	var channel models.Channel
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND tenant_id = ? AND channel_type = ?", channelID, tenantID, "personal_zalo_import").
		Take(&channel).Error
	return channel, err
}

func loadFreshPersonalZaloRoster(ctx context.Context, channel models.Channel) (personalZaloRosterSnapshot, error) {
	var snapshot personalZaloRosterSnapshot
	cfg, err := config.Load()
	if err != nil {
		return snapshot, err
	}
	if cfg.PersonalZaloGatewayBaseURL == "" {
		return snapshot, errors.New("personal Zalo gateway is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	accounts, err := listPersonalZaloGatewayAccountsContext(ctx, cfg.PersonalZaloGatewayBaseURL)
	if err != nil {
		return snapshot, err
	}
	account := findPersonalZaloGatewayAccount(accounts, channel.TenantID, channel.ID)
	if account == nil || account.ID == "" || account.Status != "connected" {
		return snapshot, errPersonalZaloGatewayDisconnected
	}

	path := "/api/v1/accounts/" + url.PathEscape(account.ID) + "/roster"
	if err := personalZaloGatewayJSONContext(ctx, cfg.PersonalZaloGatewayBaseURL, http.MethodGet, path, nil, &snapshot); err != nil {
		return snapshot, err
	}
	if !snapshot.validate(time.Now()) || snapshot.AccountExternalID != account.ZaloUID ||
		(account.AccountExternalID != "" && snapshot.AccountExternalID != account.AccountExternalID) ||
		(channel.ExternalID != "" && snapshot.AccountExternalID != channel.ExternalID) {
		return snapshot, errors.New("invalid personal Zalo gateway roster")
	}
	return snapshot, nil
}

// The channel row lock serializes snapshot commits with toggle and decision updates.
func reconcilePersonalZaloRoster(tx *gorm.DB, channelID string, snapshot personalZaloRosterSnapshot, baseline bool) (bool, error) {
	state := models.PersonalZaloRosterState{ChannelID: channelID, AccountExternalID: snapshot.AccountExternalID}
	err := tx.Where("channel_id = ? AND account_external_id = ?", state.ChannelID, state.AccountExternalID).Take(&state).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	if !snapshot.CapturedAt.After(state.CapturedAt) {
		return false, errPersonalZaloRosterConflict
	}

	var existing []models.PersonalZaloThreadApproval
	if err := tx.Where("channel_id = ? AND account_external_id = ?", channelID, snapshot.AccountExternalID).Find(&existing).Error; err != nil {
		return false, err
	}
	changes := personalZaloRosterChanges(channelID, snapshot, baseline, existing)
	if len(changes) > 0 {
		if err := tx.Clauses(clause.OnConflict{
			DoUpdates: clause.AssignmentColumns([]string{"display_name", "status", "present", "first_seen_at", "decided_at", "decided_by"}),
		}).CreateInBatches(&changes, 250).Error; err != nil {
			return false, err
		}
	}
	state.CapturedAt = snapshot.CapturedAt
	err = tx.Clauses(clause.OnConflict{DoUpdates: clause.AssignmentColumns([]string{"captured_at"})}).Create(&state).Error
	return len(changes) > 0, err
}

func personalZaloRosterChanges(channelID string, snapshot personalZaloRosterSnapshot, baseline bool, existing []models.PersonalZaloThreadApproval) []models.PersonalZaloThreadApproval {
	byKey := make(map[personalZaloRosterEntryKey]models.PersonalZaloThreadApproval, len(existing))
	for _, row := range existing {
		byKey[personalZaloRosterEntryKey{row.ThreadType, row.ThreadID}] = row
	}
	newStatus := personalZaloPending
	if baseline {
		newStatus = personalZaloApproved
	}
	var changes []models.PersonalZaloThreadApproval
	for _, entry := range snapshot.Entries {
		key := personalZaloRosterEntryKey{entry.ThreadType, entry.ThreadID}
		row, known := byKey[key]
		delete(byKey, key)
		if !known {
			row = models.PersonalZaloThreadApproval{
				ChannelID: channelID, AccountExternalID: snapshot.AccountExternalID,
				ThreadType: entry.ThreadType, ThreadID: entry.ThreadID, Status: newStatus,
				FirstSeenAt: snapshot.CapturedAt,
			}
		} else if !baseline && !row.Present && row.Status == personalZaloApproved {
			row.Status = personalZaloPending
			row.FirstSeenAt = snapshot.CapturedAt
			row.DecidedAt = nil
			row.DecidedBy = ""
		}
		if row.Present && (entry.DisplayName == "" || row.DisplayName == entry.DisplayName) {
			continue
		}
		row.Present = true
		if entry.DisplayName != "" {
			row.DisplayName = entry.DisplayName
		}
		changes = append(changes, row)
	}
	for _, row := range byKey {
		if row.Present {
			row.Present = false
			changes = append(changes, row)
		}
	}
	return changes
}

func updatePersonalZaloAutoReply(ctx context.Context, previous models.Channel, updates map[string]interface{}, patch personalZaloInternbotPatch) (bool, error) {
	enable, _ := updates["personal_zalo_auto_reply_enabled"].(bool)
	active := previous.IsActive
	if nextActive, exists := updates["is_active"].(bool); exists {
		active = nextActive
	}
	current, err := decodePersonalZaloInternbotConfig(previous.Metadata)
	if err != nil {
		return false, err
	}
	next, err := planPersonalZaloInternbotUpdate(current, patch, active, enable)
	if err != nil {
		return false, err
	}
	if next.Mode != current.Mode || next.Mode == personalZaloInternbotMode {
		updates["personal_zalo_auto_reply_enabled"] = false
	}
	verifyInternbot := next.SupervisorGroupID != "" &&
		(next.SupervisorGroupID != current.SupervisorGroupID || (next.Enabled && next.Version != current.Version))
	var baseline personalZaloRosterSnapshot
	if (enable && !previous.PersonalZaloAutoReplyEnabled) || verifyInternbot {
		baseline, err = loadFreshPersonalZaloRoster(ctx, previous)
		if err != nil {
			return false, err
		}
	}
	var changed bool
	err = db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		channel, err := lockPersonalZaloRosterChannel(tx, previous.TenantID, previous.ID)
		if err != nil {
			return err
		}
		if channel.PersonalZaloAutoReplyEnabled != previous.PersonalZaloAutoReplyEnabled || channel.ExternalID != previous.ExternalID ||
			channel.Metadata != previous.Metadata || channel.IsActive != previous.IsActive {
			return errPersonalZaloRosterConflict
		}
		if enable && !active {
			return errPersonalZaloRosterConflict
		}
		if verifyInternbot {
			if channel.ExternalID == "" {
				channel.ExternalID = baseline.AccountExternalID
				updates["external_id"] = channel.ExternalID
			}
			groups, err := savePersonalZaloInternbotRoster(tx, channel, internbotGroupsFromRoster(baseline))
			if err != nil {
				return err
			}
			if !groups.fresh(time.Now()) || !groups.contains(next.SupervisorGroupID) {
				return errPersonalZaloInternbotGroupUnavailable
			}
			if next.Enabled {
				next.ActivatedAt = time.Now().UTC()
				next.SupervisorAccountID = channel.ExternalID
			}
		}
		if patch.present() || next.Version != current.Version {
			raw := channel.Metadata
			if merged, ok := updates["metadata"].(string); ok {
				raw = merged
			}
			metadata, err := decodeMetadataObject(raw)
			if err != nil {
				return err
			}
			writePersonalZaloInternbotConfig(metadata, next)
			encoded, _ := json.Marshal(metadata)
			updates["metadata"] = string(encoded)
		}
		if enable && !channel.PersonalZaloAutoReplyEnabled {
			if channel.ExternalID != "" && channel.ExternalID != baseline.AccountExternalID {
				return errPersonalZaloRosterConflict
			}
			updates["external_id"] = baseline.AccountExternalID
			changed, err = reconcilePersonalZaloRoster(tx, channel.ID, baseline, true)
			if err != nil {
				return err
			}
		}
		return tx.Model(&channel).Updates(updates).Error
	})
	return changed, err
}
