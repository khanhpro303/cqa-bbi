package agents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EnabledSettingKey is the tenant switch that moves chat replies from the
// AI Engine prompts to agents. Off (or absent) keeps the old path, which
// still reads the old prompt settings untouched.
const EnabledSettingKey = "ai_agents_enabled"

// Legacy AI Engine prompt settings the migration copies from.
const (
	LegacyPublicPromptKey   = "ai_engine_system_prompt"
	LegacyInternalPromptKey = "ai_engine_system_prompt_internal"
)

// Keys of the agents created from the legacy prompts.
const (
	LegacyCustomerAgentKey = "tro-ly-khach-hang"
	LegacyStaffAgentKey    = "tro-ly-noi-bo"
)

// ChatChannelTypes are the channels the chatbot answers on, and so the
// channels a team can be given.
var ChatChannelTypes = []string{"zalo_oa", "personal_zalo_import"}

// ErrNotFound is returned for an agent or team outside the tenant.
var ErrNotFound = errors.New("not found")

// ErrInUse is returned when deleting an agent that is in a team, or a team
// that a channel still uses.
var ErrInUse = errors.New("in use")

func database(ctx context.Context) (*gorm.DB, error) {
	if db.DB == nil {
		return nil, errors.New("database unavailable")
	}
	return db.DB.WithContext(ctx), nil
}

// Enabled reports whether the tenant answers chats with agents.
func Enabled(ctx context.Context, tenantID string) (bool, error) {
	d, err := database(ctx)
	if err != nil {
		return false, err
	}
	var s models.AppSetting
	err = d.Select("value_plain").Where("tenant_id = ? AND setting_key = ?", tenantID, EnabledSettingKey).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(s.ValuePlain) == "true", nil
}

// SetEnabled turns agents on or off for the tenant. Turning on copies the
// AI Engine prompts into agents first when the tenant has none.
func SetEnabled(ctx context.Context, tenantID, actorID string, enabled bool) error {
	d, err := database(ctx)
	if err != nil {
		return err
	}
	if enabled {
		if _, err := MigrateLegacy(ctx, tenantID, actorID); err != nil {
			return err
		}
	}
	value := "false"
	if enabled {
		value = "true"
	}
	now := time.Now()
	row := models.AppSetting{ID: uuid.NewString(), TenantID: tenantID, SettingKey: EnabledSettingKey, ValuePlain: value, CreatedAt: now, UpdatedAt: now}
	return d.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "setting_key"}},
		DoUpdates: clause.Assignments(map[string]any{"value_plain": value, "updated_at": now}),
	}).Create(&row).Error
}

// TeamForChannel returns the team that answers through channelID: the
// channel's own team when it is set and active, else the tenant's default
// team. ok is false when the tenant has no usable team.
func TeamForChannel(ctx context.Context, tenantID, channelID string) (Team, bool, error) {
	d, err := database(ctx)
	if err != nil {
		return Team{}, false, err
	}
	var row models.AITeam
	found := false
	if channelID != "" {
		var ch models.Channel
		if err := d.Select("id", "ai_team_id").Where("id = ? AND tenant_id = ?", channelID, tenantID).First(&ch).Error; err == nil &&
			ch.AITeamID != nil && *ch.AITeamID != "" {
			err := d.Where("id = ? AND tenant_id = ? AND is_active = ?", *ch.AITeamID, tenantID, true).First(&row).Error
			switch {
			case err == nil:
				found = true
			case !errors.Is(err, gorm.ErrRecordNotFound):
				return Team{}, false, err
			}
		} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return Team{}, false, err
		}
	}
	if !found {
		err := d.Where("tenant_id = ? AND is_default = ? AND is_active = ?", tenantID, true, true).Order("created_at").First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Team{}, false, nil
		}
		if err != nil {
			return Team{}, false, err
		}
	}
	team := TeamFromModel(row)
	members, err := loadMembers(d, tenantID, team.ID, true)
	if err != nil {
		return Team{}, false, err
	}
	team.Members = members
	return team, true, nil
}

// TeamByID returns one of the tenant's teams, active or not, with its active
// members in order (the agents a turn could go to). The test console uses
// it, so a team can be tried before it is switched on.
func TeamByID(ctx context.Context, tenantID, teamID string) (Team, error) {
	d, err := database(ctx)
	if err != nil {
		return Team{}, err
	}
	var row models.AITeam
	if err := d.Where("id = ? AND tenant_id = ?", teamID, tenantID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Team{}, ErrNotFound
		}
		return Team{}, err
	}
	team := TeamFromModel(row)
	if team.Members, err = loadMembers(d, tenantID, team.ID, true); err != nil {
		return Team{}, err
	}
	return team, nil
}

// loadMembers returns a team's agents in position order. activeOnly drops
// disabled memberships and inactive agents.
func loadMembers(d *gorm.DB, tenantID, teamID string, activeOnly bool) ([]Agent, error) {
	var links []models.AITeamMember
	q := d.Where("tenant_id = ? AND team_id = ?", tenantID, teamID)
	if activeOnly {
		q = q.Where("enabled = ?", true)
	}
	if err := q.Order("position, agent_id").Find(&links).Error; err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return []Agent{}, nil
	}
	ids := make([]string, len(links))
	for i, l := range links {
		ids[i] = l.AgentID
	}
	var rows []models.AIAgent
	if err := d.Where("tenant_id = ? AND id IN ?", tenantID, ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	byID := make(map[string]models.AIAgent, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	out := make([]Agent, 0, len(links))
	for _, l := range links {
		r, ok := byID[l.AgentID]
		if !ok || (activeOnly && !r.IsActive) {
			continue
		}
		out = append(out, FromModel(r))
	}
	return out, nil
}

// ListAgents returns the tenant's agents without their prompts.
func ListAgents(ctx context.Context, tenantID string) ([]Agent, error) {
	d, err := database(ctx)
	if err != nil {
		return nil, err
	}
	var rows []models.AIAgent
	if err := d.Omit("system_prompt").Where("tenant_id = ?", tenantID).Order("created_at, `key`").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Agent, len(rows))
	for i, r := range rows {
		out[i] = FromModel(r)
	}
	return out, nil
}

// GetAgent returns one agent with its prompt.
func GetAgent(ctx context.Context, tenantID, id string) (Agent, error) {
	d, err := database(ctx)
	if err != nil {
		return Agent{}, err
	}
	var row models.AIAgent
	if err := d.Where("tenant_id = ? AND id = ?", tenantID, id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Agent{}, ErrNotFound
		}
		return Agent{}, err
	}
	return FromModel(row), nil
}

// SaveAgent creates (empty ID) or updates an agent and records a revision.
// The key of an existing agent never changes.
func SaveAgent(ctx context.Context, tenantID, actorID string, in Agent) (Agent, error) {
	d, err := database(ctx)
	if err != nil {
		return Agent{}, err
	}
	var saved models.AIAgent
	err = d.Transaction(func(tx *gorm.DB) error {
		if in.ID != "" {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND id = ?", tenantID, in.ID).First(&saved).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrNotFound
				}
				return err
			}
			in.Key = saved.Key
			if in.Options.LegacySource == "" {
				in.Options.LegacySource = FromModel(saved).Options.LegacySource
			}
		}
		a, err := Normalize(in)
		if err != nil {
			return err
		}
		now := time.Now()
		if saved.ID == "" {
			var count int64
			if err := tx.Model(&models.AIAgent{}).Where("tenant_id = ? AND `key` = ?", tenantID, a.Key).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return invalid("key", "mã agent %q đã tồn tại", a.Key)
			}
			saved = models.AIAgent{ID: uuid.NewString(), TenantID: tenantID, CreatedAt: now}
		}
		a.ApplyTo(&saved)
		saved.UpdatedBy = actorID
		saved.UpdatedAt = now
		// On insert GORM leaves out a false is_active and the column
		// default (true) wins; write it explicitly.
		active := saved.IsActive
		if err := tx.Save(&saved).Error; err != nil {
			return err
		}
		if saved.IsActive != active {
			if err := tx.Model(&saved).Update("is_active", active).Error; err != nil {
				return err
			}
			saved.IsActive = active
		}
		return recordRevision(tx, saved, actorID, now)
	})
	if err != nil {
		return Agent{}, err
	}
	return FromModel(saved), nil
}

func recordRevision(tx *gorm.DB, a models.AIAgent, actorID string, at time.Time) error {
	rev := models.AIAgentRevision{
		ID: uuid.NewString(), TenantID: a.TenantID, AgentID: a.ID, Name: a.Name,
		Description: a.Description, SystemPrompt: a.SystemPrompt, Tools: a.Tools,
		EditedBy: actorID, CreatedAt: at,
	}
	if err := tx.Create(&rev).Error; err != nil {
		return err
	}
	// MySQL takes OFFSET only after a LIMIT.
	var stale []string
	if err := tx.Model(&models.AIAgentRevision{}).Where("agent_id = ?", a.ID).
		Order("created_at DESC, id").Limit(1000).Offset(maxRevisionsKept).Pluck("id", &stale).Error; err != nil {
		return err
	}
	if len(stale) > 0 {
		return tx.Where("id IN ?", stale).Delete(&models.AIAgentRevision{}).Error
	}
	return nil
}

// ListRevisions returns an agent's saved versions, newest first.
func ListRevisions(ctx context.Context, tenantID, agentID string) ([]models.AIAgentRevision, error) {
	d, err := database(ctx)
	if err != nil {
		return nil, err
	}
	var rows []models.AIAgentRevision
	err = d.Where("tenant_id = ? AND agent_id = ?", tenantID, agentID).Order("created_at DESC").Limit(maxRevisionsKept).Find(&rows).Error
	return rows, err
}

// DeleteAgent removes an agent that no team uses.
func DeleteAgent(ctx context.Context, tenantID, id string) error {
	d, err := database(ctx)
	if err != nil {
		return err
	}
	return d.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&models.AITeamMember{}).Where("tenant_id = ? AND agent_id = ?", tenantID, id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return ErrInUse
		}
		res := tx.Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&models.AIAgent{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return tx.Where("agent_id = ?", id).Delete(&models.AIAgentRevision{}).Error
	})
}

// TeamInput is what the UI saves for a team.
type TeamInput struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	PersonaPrompt  string        `json:"persona_prompt"`
	RouterPrompt   string        `json:"router_prompt"`
	DefaultAgentID string        `json:"default_agent_id"`
	IsActive       *bool         `json:"is_active"`
	Options        TeamOptions   `json:"options"`
	Members        []MemberInput `json:"members"`
}

// MemberInput is one team member as saved by the UI.
type MemberInput struct {
	AgentID  string `json:"agent_id"`
	Position int    `json:"position"`
	Enabled  bool   `json:"enabled"`
}

// SaveTeam creates or updates a team and replaces its member list.
func SaveTeam(ctx context.Context, tenantID, actorID string, in TeamInput) (Team, error) {
	d, err := database(ctx)
	if err != nil {
		return Team{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 120 {
		return Team{}, invalid("name", "tên từ 1 đến 120 ký tự")
	}
	if len([]rune(in.PersonaPrompt)) > MaxPromptChars || len([]rune(in.RouterPrompt)) > MaxPromptChars {
		return Team{}, invalid("persona_prompt", "prompt tối đa %d ký tự", MaxPromptChars)
	}
	if in.Options.StickyMinutes < 0 || in.Options.StickyMinutes > 24*60 {
		return Team{}, invalid("options.sticky_minutes", "từ 0 đến 1440 phút")
	}
	var saved models.AITeam
	err = d.Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		if in.ID != "" {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND id = ?", tenantID, in.ID).First(&saved).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrNotFound
				}
				return err
			}
		} else {
			saved = models.AITeam{ID: uuid.NewString(), TenantID: tenantID, IsActive: true, CreatedAt: now}
		}
		memberIDs := map[string]bool{}
		for _, m := range in.Members {
			if m.AgentID == "" || memberIDs[m.AgentID] {
				return invalid("members", "danh sách agent bị trùng hoặc thiếu mã")
			}
			memberIDs[m.AgentID] = true
		}
		if len(memberIDs) > 0 {
			ids := make([]string, 0, len(memberIDs))
			for id := range memberIDs {
				ids = append(ids, id)
			}
			var count int64
			if err := tx.Model(&models.AIAgent{}).Where("tenant_id = ? AND id IN ?", tenantID, ids).Count(&count).Error; err != nil {
				return err
			}
			if int(count) != len(ids) {
				return invalid("members", "có agent không thuộc tenant này")
			}
		}
		if in.DefaultAgentID != "" && !memberIDs[in.DefaultAgentID] {
			return invalid("default_agent_id", "agent mặc định phải nằm trong danh sách")
		}
		if saved.IsDefault && in.IsActive != nil && !*in.IsActive {
			// Channels without their own team answer through it.
			return invalid("is_active", "không tắt được Bộ điều phối mặc định")
		}
		saved.Name = in.Name
		saved.PersonaPrompt = in.PersonaPrompt
		saved.RouterPrompt = in.RouterPrompt
		saved.DefaultAgentID = in.DefaultAgentID
		saved.Options = mustJSON(in.Options)
		if in.IsActive != nil {
			saved.IsActive = *in.IsActive
		}
		saved.UpdatedBy = actorID
		saved.UpdatedAt = now
		// On insert GORM leaves out a false is_active and the column
		// default (true) wins; write it explicitly.
		active := saved.IsActive
		if err := tx.Save(&saved).Error; err != nil {
			return err
		}
		if saved.IsActive != active {
			if err := tx.Model(&saved).Update("is_active", active).Error; err != nil {
				return err
			}
			saved.IsActive = active
		}
		if err := tx.Where("team_id = ?", saved.ID).Delete(&models.AITeamMember{}).Error; err != nil {
			return err
		}
		for _, m := range in.Members {
			link := models.AITeamMember{TeamID: saved.ID, AgentID: m.AgentID, TenantID: tenantID, Position: m.Position, Enabled: m.Enabled}
			if err := tx.Create(&link).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Team{}, err
	}
	team := TeamFromModel(saved)
	team.Members, err = loadMembers(d, tenantID, team.ID, false)
	return team, err
}

// TeamDetail is a team with every member (disabled ones too) and the
// channels that use it.
type TeamDetail struct {
	Team
	Memberships []models.AITeamMember `json:"memberships"`
	Channels    []TeamChannel         `json:"channels"`
	// Silent lists the audiences ("customer", "staff") no enabled, active
	// member serves: the bot stays silent for them on the team's channels.
	Silent []string `json:"silent"`
}

// silentAudiences lists the audiences no enabled, active member serves.
func silentAudiences(members []Agent, memberships []models.AITeamMember) []string {
	enabled := map[string]bool{}
	for _, m := range memberships {
		enabled[m.AgentID] = m.Enabled
	}
	out := []string{}
	for _, audience := range []string{AudienceCustomer, AudienceStaff} {
		served := false
		for _, a := range members {
			if enabled[a.ID] && a.IsActive && a.Serves(audience) {
				served = true
				break
			}
		}
		if !served {
			out = append(out, audience)
		}
	}
	return out
}

// TeamChannel is a channel answering through a team.
type TeamChannel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ChannelType string `json:"channel_type"`
	// AITeamID is the team the channel itself points at; empty when it
	// follows the tenant's default team.
	AITeamID string `json:"ai_team_id"`
}

// ListTeams returns the tenant's teams with members and channels.
func ListTeams(ctx context.Context, tenantID string) ([]TeamDetail, error) {
	d, err := database(ctx)
	if err != nil {
		return nil, err
	}
	var rows []models.AITeam
	if err := d.Where("tenant_id = ?", tenantID).Order("is_default DESC, created_at").Find(&rows).Error; err != nil {
		return nil, err
	}
	var channelRows []models.Channel
	if err := d.Select("id", "name", "channel_type", "ai_team_id").
		Where("tenant_id = ? AND channel_type IN ?", tenantID, ChatChannelTypes).
		Order("name").Find(&channelRows).Error; err != nil {
		return nil, err
	}
	defaultTeam := ""
	for _, r := range rows {
		if r.IsDefault && r.IsActive {
			defaultTeam = r.ID
			break
		}
	}
	out := make([]TeamDetail, 0, len(rows))
	for _, r := range rows {
		detail := TeamDetail{Team: TeamFromModel(r), Channels: []TeamChannel{}}
		if err := d.Where("tenant_id = ? AND team_id = ?", tenantID, r.ID).Order("position, agent_id").Find(&detail.Memberships).Error; err != nil {
			return nil, err
		}
		members, err := loadMembers(d, tenantID, r.ID, false)
		if err != nil {
			return nil, err
		}
		for i := range members {
			members[i].SystemPrompt = ""
		}
		detail.Members = members
		detail.Silent = silentAudiences(members, detail.Memberships)
		for _, ch := range channelRows {
			own := ch.AITeamID != nil && *ch.AITeamID != ""
			if (own && *ch.AITeamID == r.ID) || (!own && r.ID == defaultTeam) {
				link := TeamChannel{ID: ch.ID, Name: ch.Name, ChannelType: ch.ChannelType}
				if own {
					link.AITeamID = *ch.AITeamID
				}
				detail.Channels = append(detail.Channels, link)
			}
		}
		out = append(out, detail)
	}
	return out, nil
}

// lockTenantForTeamChange gives default-team changes and migration the same
// lock order before any of them read or update a team.
func lockTenantForTeamChange(tx *gorm.DB, tenantID string) error {
	var tenant models.Tenant
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id = ?", tenantID).First(&tenant).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock tenant: %w", err)
	}
	return nil
}

// DeleteTeam removes a team no channel points at. The default team stays.
func DeleteTeam(ctx context.Context, tenantID, id string) error {
	d, err := database(ctx)
	if err != nil {
		return err
	}
	return d.Transaction(func(tx *gorm.DB) error {
		if err := lockTenantForTeamChange(tx, tenantID); err != nil {
			return err
		}
		var row models.AITeam
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND id = ?", tenantID, id).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if row.IsDefault {
			return ErrInUse
		}
		var count int64
		if err := tx.Model(&models.Channel{}).Where("tenant_id = ? AND ai_team_id = ?", tenantID, id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return ErrInUse
		}
		if err := tx.Where("team_id = ?", id).Delete(&models.AITeamMember{}).Error; err != nil {
			return err
		}
		return tx.Delete(&row).Error
	})
}

// SetDefaultTeam makes an active team the tenant's default: every channel
// without its own team answers through it. The previous default becomes a
// normal team that can be deleted once no channel points at it.
func SetDefaultTeam(ctx context.Context, tenantID, id string) error {
	d, err := database(ctx)
	if err != nil {
		return err
	}
	return d.Transaction(func(tx *gorm.DB) error {
		if err := lockTenantForTeamChange(tx, tenantID); err != nil {
			return err
		}
		var target models.AITeam
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND id = ?", tenantID, id).First(&target).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !target.IsActive {
			return invalid("is_active", "bật Bộ điều phối trước khi đặt làm mặc định")
		}
		if err := tx.Model(&models.AITeam{}).Where("tenant_id = ? AND id <> ? AND is_default = ?", tenantID, id, true).
			Update("is_default", false).Error; err != nil {
			return err
		}
		return tx.Model(&models.AITeam{}).Where("tenant_id = ? AND id = ?", tenantID, id).Update("is_default", true).Error
	})
}

// SetChannelTeam points a channel at a team; an empty teamID returns it to
// the tenant's default team.
func SetChannelTeam(ctx context.Context, tenantID, channelID, teamID string) error {
	d, err := database(ctx)
	if err != nil {
		return err
	}
	return d.Transaction(func(tx *gorm.DB) error {
		var value any
		if teamID != "" {
			var count int64
			if err := tx.Model(&models.AITeam{}).Where("tenant_id = ? AND id = ?", tenantID, teamID).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return ErrNotFound
			}
			value = teamID
		}
		var count int64
		if err := tx.Model(&models.Channel{}).Where("tenant_id = ? AND id = ? AND channel_type IN ?", tenantID, channelID, ChatChannelTypes).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return ErrNotFound
		}
		return tx.Model(&models.Channel{}).Where("tenant_id = ? AND id = ?", tenantID, channelID).Update("ai_team_id", value).Error
	})
}

// MigrateLegacy copies the AI Engine prompts into two agents (customers and
// staff) and a default team holding both, unless the tenant already has
// agents. Every channel without its own team answers through the default
// team, so turning agents on changes nothing a customer sees: same prompt,
// same Langflow flow (the agents keep following the AI Engine flows), same
// tools. created is false when the tenant already had agents.
func MigrateLegacy(ctx context.Context, tenantID, actorID string) (created bool, err error) {
	d, err := database(ctx)
	if err != nil {
		return false, err
	}
	err = d.Transaction(func(tx *gorm.DB) error {
		if err := lockTenantForTeamChange(tx, tenantID); err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&models.AIAgent{}).Where("tenant_id = ?", tenantID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		prompts := map[string]string{}
		var settings []models.AppSetting
		if err := tx.Where("tenant_id = ? AND setting_key IN ?", tenantID, []string{LegacyPublicPromptKey, LegacyInternalPromptKey}).Find(&settings).Error; err != nil {
			return err
		}
		for _, s := range settings {
			prompts[s.SettingKey] = s.ValuePlain
		}
		now := time.Now()
		legacy := []Agent{
			{
				Key: LegacyCustomerAgentKey, Name: "Trợ lý khách hàng",
				Description: "Trả lời khách hàng và mọi nhóm chat Zalo. Chuyển từ system prompt khách hàng của AI Engine.",
				Audience:    AudienceCustomer, Runtime: RuntimeLangflow, SystemPrompt: prompts[LegacyPublicPromptKey],
				Tools: AllToolNames(), IsActive: true, Options: Options{LegacySource: LegacyPublicPromptKey},
			},
			{
				Key: LegacyStaffAgentKey, Name: "Trợ lý nội bộ",
				Description: "Trả lời nhân viên nội bộ trong chat riêng. Chuyển từ system prompt nội bộ của AI Engine.",
				Audience:    AudienceStaff, Runtime: RuntimeLangflow, SystemPrompt: prompts[LegacyInternalPromptKey],
				Tools: AllToolNames(), IsActive: true, Options: Options{LegacySource: LegacyInternalPromptKey},
			},
		}
		ids := make([]string, len(legacy))
		for i, a := range legacy {
			a, err := Normalize(a)
			if err != nil {
				return err
			}
			row := models.AIAgent{ID: uuid.NewString(), TenantID: tenantID, UpdatedBy: actorID, CreatedAt: now, UpdatedAt: now}
			a.ApplyTo(&row)
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			if err := recordRevision(tx, row, actorID, now); err != nil {
				return err
			}
			ids[i] = row.ID
		}
		// The tenant may still have its default team from an earlier
		// migration (the agents were removed from it and deleted). Put the
		// new agents into that team instead of creating a second one, so
		// copying again never leaves an empty default team answering the
		// channels next to an undeletable duplicate.
		var team models.AITeam
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND is_default = ?", tenantID, true).Order("created_at").First(&team).Error
		switch {
		case err == nil:
			var existing []models.AITeamMember
			if err := tx.Where("tenant_id = ? AND team_id = ?", tenantID, team.ID).Find(&existing).Error; err != nil {
				return err
			}
			next, defaultIsMember := 0, false
			for _, m := range existing {
				if m.Position >= next {
					next = m.Position + 1
				}
				if m.AgentID == team.DefaultAgentID {
					defaultIsMember = true
				}
			}
			if team.DefaultAgentID == "" || !defaultIsMember {
				team.DefaultAgentID = ids[0]
			}
			team.IsActive = true
			team.UpdatedBy = actorID
			team.UpdatedAt = now
			if err := tx.Save(&team).Error; err != nil {
				return err
			}
			for i, id := range ids {
				if err := tx.Create(&models.AITeamMember{TeamID: team.ID, AgentID: id, TenantID: tenantID, Position: next + i, Enabled: true}).Error; err != nil {
					return err
				}
			}
		case errors.Is(err, gorm.ErrRecordNotFound):
			team = models.AITeam{
				ID: uuid.NewString(), TenantID: tenantID, Name: "Mặc định", DefaultAgentID: ids[0],
				Options: mustJSON(TeamOptions{}), IsDefault: true, IsActive: true,
				UpdatedBy: actorID, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Create(&team).Error; err != nil {
				return err
			}
			for i, id := range ids {
				if err := tx.Create(&models.AITeamMember{TeamID: team.ID, AgentID: id, TenantID: tenantID, Position: i, Enabled: true}).Error; err != nil {
					return err
				}
			}
		default:
			return err
		}
		created = true
		return nil
	})
	return created, err
}

// RefreshLegacyPrompts copies the current AI Engine prompts into the agents
// that were created from them. Use it when the AI Engine prompts were
// edited after the migration and before anyone edited the agents. It
// returns the keys of the agents it updated.
func RefreshLegacyPrompts(ctx context.Context, tenantID, actorID string) ([]string, error) {
	d, err := database(ctx)
	if err != nil {
		return nil, err
	}
	var updated []string
	err = d.Transaction(func(tx *gorm.DB) error {
		var settings []models.AppSetting
		if err := tx.Where("tenant_id = ? AND setting_key IN ?", tenantID, []string{LegacyPublicPromptKey, LegacyInternalPromptKey}).Find(&settings).Error; err != nil {
			return err
		}
		prompts := map[string]string{}
		for _, s := range settings {
			prompts[s.SettingKey] = s.ValuePlain
		}
		var rows []models.AIAgent
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ?", tenantID).Find(&rows).Error; err != nil {
			return err
		}
		now := time.Now()
		for _, row := range rows {
			source := FromModel(row).Options.LegacySource
			prompt, ok := prompts[source]
			if source == "" || !ok || prompt == row.SystemPrompt {
				continue
			}
			row.SystemPrompt = prompt
			row.UpdatedBy = actorID
			row.UpdatedAt = now
			if err := tx.Save(&row).Error; err != nil {
				return err
			}
			if err := recordRevision(tx, row, actorID, now); err != nil {
				return err
			}
			updated = append(updated, row.Key)
		}
		return nil
	})
	return updated, err
}

// LegacyDrift lists the agents whose prompt no longer matches the AI Engine
// prompt it was copied from.
func LegacyDrift(ctx context.Context, tenantID string) ([]string, error) {
	d, err := database(ctx)
	if err != nil {
		return nil, err
	}
	var settings []models.AppSetting
	if err := d.Where("tenant_id = ? AND setting_key IN ?", tenantID, []string{LegacyPublicPromptKey, LegacyInternalPromptKey}).Find(&settings).Error; err != nil {
		return nil, err
	}
	prompts := map[string]string{}
	for _, s := range settings {
		prompts[s.SettingKey] = s.ValuePlain
	}
	var rows []models.AIAgent
	if err := d.Where("tenant_id = ?", tenantID).Find(&rows).Error; err != nil {
		return nil, err
	}
	var drift []string
	for _, row := range rows {
		source := FromModel(row).Options.LegacySource
		if source != "" && prompts[source] != row.SystemPrompt {
			drift = append(drift, row.Key)
		}
	}
	return drift, nil
}
