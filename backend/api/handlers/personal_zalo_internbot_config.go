package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	personalZaloChatbotMode   = "chatbot"
	personalZaloInternbotMode = "internbot"
	internbotRosterMaxAge     = 6 * time.Minute
)

var (
	errInvalidPersonalZaloInternbotSettings  = errors.New("invalid Internbot settings")
	errPersonalZaloInternbotEnableSeparately = errors.New("save the mode before enabling it")
	errPersonalZaloInternbotGroupUnavailable = errors.New("the supervisor group is unavailable")
	errPersonalZaloGatewayDisconnected       = errors.New("personal Zalo gateway account is disconnected")
)

type personalZaloInternbotConfig struct {
	Mode                string    `json:"chatbot_mode"`
	Enabled             bool      `json:"internbot_enabled"`
	SupervisorGroupID   string    `json:"supervisor_group_id"`
	Version             string    `json:"internbot_config_version"`
	ActivatedAt         time.Time `json:"internbot_activated_at"`
	SupervisorAccountID string    `json:"internbot_supervisor_account_id"`
}

func decodePersonalZaloInternbotConfig(raw string) (personalZaloInternbotConfig, error) {
	config := personalZaloInternbotConfig{Mode: personalZaloChatbotMode}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &config); err != nil {
			return config, err
		}
	}
	if config.Mode != personalZaloChatbotMode && config.Mode != personalZaloInternbotMode {
		return config, errInvalidPersonalZaloInternbotSettings
	}
	return config, nil
}

type personalZaloInternbotPatch struct {
	Mode              *string
	Enabled           *bool
	SupervisorGroupID *string
}

func (patch personalZaloInternbotPatch) present() bool {
	return patch.Mode != nil || patch.Enabled != nil || patch.SupervisorGroupID != nil
}

func internbotPatchFromMetadata(metadata map[string]any) (personalZaloInternbotPatch, error) {
	var patch personalZaloInternbotPatch
	for _, key := range []string{"internbot_config_version", "internbot_activated_at", "internbot_supervisor_account_id"} {
		if _, exists := metadata[key]; exists {
			return patch, errInvalidPersonalZaloInternbotSettings
		}
	}
	if value, ok := metadata["chatbot_mode"]; ok {
		mode, ok := value.(string)
		if !ok || (mode != personalZaloChatbotMode && mode != personalZaloInternbotMode) {
			return patch, errInvalidPersonalZaloInternbotSettings
		}
		patch.Mode = &mode
	}
	if value, ok := metadata["internbot_enabled"]; ok {
		enabled, ok := value.(bool)
		if !ok {
			return patch, errInvalidPersonalZaloInternbotSettings
		}
		patch.Enabled = &enabled
	}
	if value, ok := metadata["supervisor_group_id"]; ok {
		groupID, ok := value.(string)
		if !ok || (groupID != "" && !personalZaloUIDPattern.MatchString(groupID)) {
			return patch, errInvalidPersonalZaloInternbotSettings
		}
		patch.SupervisorGroupID = &groupID
	}
	return patch, nil
}

func canManagePersonalZaloBotMode(c *gin.Context) bool {
	role := middleware.GetTenantRole(c)
	return role == "owner" || role == "admin"
}

func writePersonalZaloInternbotConfig(metadata map[string]any, config personalZaloInternbotConfig) {
	metadata["chatbot_mode"] = config.Mode
	metadata["internbot_enabled"] = config.Enabled
	metadata["supervisor_group_id"] = config.SupervisorGroupID
	metadata["internbot_config_version"] = config.Version
	if config.Enabled {
		metadata["internbot_activated_at"] = config.ActivatedAt.Format(time.RFC3339Nano)
		metadata["internbot_supervisor_account_id"] = config.SupervisorAccountID
	} else {
		delete(metadata, "internbot_activated_at")
		delete(metadata, "internbot_supervisor_account_id")
	}
}

func planPersonalZaloInternbotUpdate(current personalZaloInternbotConfig, patch personalZaloInternbotPatch, active, enableChatbot bool) (personalZaloInternbotConfig, error) {
	next := current
	if patch.Mode != nil {
		next.Mode = *patch.Mode
	}
	if patch.SupervisorGroupID != nil {
		next.SupervisorGroupID = *patch.SupervisorGroupID
	}
	modeChanged := next.Mode != current.Mode
	if (modeChanged || next.Mode == personalZaloInternbotMode) && enableChatbot {
		return next, errPersonalZaloInternbotEnableSeparately
	}
	if patch.Enabled != nil && *patch.Enabled {
		if modeChanged {
			return next, errPersonalZaloInternbotEnableSeparately
		}
		if !active || next.Mode != personalZaloInternbotMode || next.SupervisorGroupID == "" {
			return next, errInvalidPersonalZaloInternbotSettings
		}
	}
	if modeChanged || !active || next.Mode != personalZaloInternbotMode || next.SupervisorGroupID != current.SupervisorGroupID {
		next.Enabled = false
	}
	if patch.Enabled != nil && !modeChanged && active && next.Mode == personalZaloInternbotMode {
		next.Enabled = *patch.Enabled
	}
	if next.Mode != current.Mode || next.SupervisorGroupID != current.SupervisorGroupID || next.Enabled != current.Enabled {
		next.Version = pkg.NewUUID()
		next.ActivatedAt = time.Time{}
		next.SupervisorAccountID = ""
	}
	return next, nil
}

type personalZaloInternbotGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type personalZaloInternbotGroupSnapshot struct {
	AccountExternalID string
	CapturedAt        time.Time
	Groups            []personalZaloInternbotGroup
}

func (snapshot personalZaloInternbotGroupSnapshot) contains(groupID string) bool {
	if groupID == "" || groupID == snapshot.AccountExternalID {
		return false
	}
	index := sort.Search(len(snapshot.Groups), func(i int) bool { return snapshot.Groups[i].ID >= groupID })
	return index < len(snapshot.Groups) && snapshot.Groups[index].ID == groupID
}

func (snapshot personalZaloInternbotGroupSnapshot) fresh(now time.Time) bool {
	return !snapshot.CapturedAt.IsZero() && now.Sub(snapshot.CapturedAt) <= internbotRosterMaxAge &&
		!snapshot.CapturedAt.After(now.Add(30*time.Second))
}

func internbotGroupsFromRoster(roster personalZaloRosterSnapshot) personalZaloInternbotGroupSnapshot {
	result := personalZaloInternbotGroupSnapshot{
		AccountExternalID: roster.AccountExternalID, CapturedAt: roster.CapturedAt, Groups: []personalZaloInternbotGroup{},
	}
	for _, entry := range roster.Entries {
		if entry.ThreadType == personalZaloThreadGroup && entry.ThreadID != roster.AccountExternalID {
			result.Groups = append(result.Groups, personalZaloInternbotGroup{ID: entry.ThreadID, Name: entry.DisplayName})
		}
	}
	sort.Slice(result.Groups, func(i, j int) bool { return result.Groups[i].ID < result.Groups[j].ID })
	return result
}

func loadPersonalZaloInternbotRoster(query *gorm.DB, channel models.Channel) (personalZaloInternbotGroupSnapshot, error) {
	var snapshot personalZaloInternbotGroupSnapshot
	if !personalZaloUIDPattern.MatchString(channel.ExternalID) {
		return snapshot, nil
	}
	var record models.PersonalZaloInternbotRoster
	err := query.Where("channel_id = ? AND account_external_id = ?", channel.ID, channel.ExternalID).Take(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return snapshot, nil
	}
	if err != nil {
		return snapshot, err
	}
	snapshot.AccountExternalID, snapshot.CapturedAt = record.AccountExternalID, record.CapturedAt
	err = json.Unmarshal([]byte(record.Groups), &snapshot.Groups)
	return snapshot, err
}

// Caller holds the channel row lock, the same lock used for configuration changes.
func savePersonalZaloInternbotRoster(tx *gorm.DB, channel models.Channel, snapshot personalZaloInternbotGroupSnapshot) (personalZaloInternbotGroupSnapshot, error) {
	current, err := loadPersonalZaloInternbotRoster(tx, channel)
	if err != nil || !snapshot.CapturedAt.After(current.CapturedAt) {
		return current, err
	}
	groups, err := json.Marshal(snapshot.Groups)
	if err != nil {
		return current, err
	}
	row := models.PersonalZaloInternbotRoster{
		ChannelID: channel.ID, AccountExternalID: snapshot.AccountExternalID, CapturedAt: snapshot.CapturedAt, Groups: string(groups),
	}
	err = tx.Clauses(clause.OnConflict{DoUpdates: clause.AssignmentColumns([]string{"account_external_id", "captured_at", "groups"})}).Create(&row).Error
	return snapshot, err
}

func personalZaloInternbotGroupPresent(ctx context.Context, channel models.Channel, groupID string) (bool, error) {
	snapshot, err := loadPersonalZaloInternbotRoster(db.DB.WithContext(ctx), channel)
	return err == nil && snapshot.fresh(time.Now()) && snapshot.contains(groupID), err
}

func GetPersonalZaloInternbotGroups(c *gin.Context) {
	channel, ok := getPersonalZaloImportChannel(c)
	if !ok {
		return
	}
	query := struct {
		Limit   int    `form:"limit" binding:"min=1,max=100"`
		Offset  int    `form:"offset" binding:"min=0"`
		Search  string `form:"q" binding:"max=255"`
		GroupID string `form:"group_id"`
		Refresh bool   `form:"refresh"`
	}{Limit: 50}
	if c.ShouldBindQuery(&query) != nil || (query.GroupID != "" && !personalZaloUIDPattern.MatchString(query.GroupID)) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_group_filter"})
		return
	}
	config, err := decodePersonalZaloInternbotConfig(channel.Metadata)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "personal_zalo_roster_unavailable"})
		return
	}
	response := gin.H{"items": []personalZaloInternbotGroup{}, "total": 0, "captured_at": nil, "supervisor_available": false}
	ctx := c.Request.Context()
	snapshot, err := loadPersonalZaloInternbotRoster(db.DB.WithContext(ctx), channel)
	if err == nil && (query.Refresh || !snapshot.fresh(time.Now())) {
		var live personalZaloRosterSnapshot
		live, err = loadFreshPersonalZaloRoster(ctx, channel)
		if err == nil {
			snapshot = internbotGroupsFromRoster(live)
			if channel.ExternalID == live.AccountExternalID {
				err = db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
					latest, err := lockPersonalZaloRosterChannel(tx, channel.TenantID, channel.ID)
					if err != nil {
						return err
					}
					if latest.ExternalID != live.AccountExternalID {
						return errPersonalZaloRosterConflict
					}
					snapshot, err = savePersonalZaloInternbotRoster(tx, latest, snapshot)
					return err
				})
			}
		}
	}
	if err != nil {
		response["status"] = "unavailable"
		if errors.Is(err, errPersonalZaloGatewayDisconnected) && config.SupervisorGroupID == "" {
			response["status"] = "waiting"
		}
		c.JSON(http.StatusOK, response)
		return
	}
	response["status"], response["captured_at"] = "ready", snapshot.CapturedAt
	response["supervisor_available"] = snapshot.contains(config.SupervisorGroupID) &&
		(config.SupervisorAccountID == "" || config.SupervisorAccountID == snapshot.AccountExternalID)
	search := strings.ToLower(strings.TrimSpace(query.Search))
	matched := make([]personalZaloInternbotGroup, 0, len(snapshot.Groups))
	for _, group := range snapshot.Groups {
		if (query.GroupID == "" || group.ID == query.GroupID) &&
			(strings.Contains(strings.ToLower(group.Name), search) || strings.Contains(group.ID, search)) {
			matched = append(matched, group)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool { return strings.ToLower(matched[i].Name) < strings.ToLower(matched[j].Name) })
	response["total"] = len(matched)
	if query.Offset < len(matched) {
		response["items"] = matched[query.Offset:min(query.Offset+query.Limit, len(matched))]
	}
	c.JSON(http.StatusOK, response)
}

func checkPersonalZaloInternbotMetadata(channelType string, metadata map[string]any) (personalZaloInternbotPatch, error) {
	patch, err := internbotPatchFromMetadata(metadata)
	if err == nil && channelType != "personal_zalo_import" && patch.present() {
		err = fmt.Errorf("%w: only personal Zalo channels support chatbot modes", errInvalidPersonalZaloInternbotSettings)
	}
	return patch, err
}
