package messengerlabels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func LoadState(database *gorm.DB, channel models.Channel) (models.MessengerLabelState, error) {
	state := models.MessengerLabelState{ChannelID: channel.ID, TenantID: channel.TenantID, Rules: "[]", Catalog: "[]", SyncStatus: "never"}
	err := database.Where("channel_id = ? AND tenant_id = ?", channel.ID, channel.TenantID).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return state, nil
	}
	return state, err
}

func ensureState(database *gorm.DB, channel models.Channel) error {
	return database.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.MessengerLabelState{ChannelID: channel.ID, TenantID: channel.TenantID, Rules: "[]", Catalog: "[]", SyncStatus: "never"}).Error
}

func SavePolicy(database *gorm.DB, channel models.Channel, policy Policy) error {
	return database.Transaction(func(tx *gorm.DB) error {
		state, err := lockMessengerLabelState(tx, channel)
		if err != nil {
			return err
		}
		var catalog []channels.FacebookLabel
		if json.Unmarshal([]byte(state.Catalog), &catalog) != nil {
			return ErrConfiguration
		}
		intakeLabelIDs, err := LoadIntakeLabelIDs(tx, channel)
		if err != nil {
			return err
		}
		if err = policy.ValidateTracking(catalog, intakeLabelIDs); err != nil {
			return err
		}
		rules, _ := json.Marshal(policy.Rules)
		if policy.Rules == nil {
			rules = []byte("[]")
		}
		return tx.Model(&state).Updates(map[string]interface{}{"enabled": policy.Enabled, "rules": string(rules)}).Error
	})
}

func lockMessengerLabelState(tx *gorm.DB, channel models.Channel) (models.MessengerLabelState, error) {
	if err := ensureState(tx, channel); err != nil {
		return models.MessengerLabelState{}, err
	}
	var state models.MessengerLabelState
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("channel_id = ? AND tenant_id = ?", channel.ID, channel.TenantID).First(&state).Error
	return state, err
}

func pruneNewIntakeRules(tx *gorm.DB, state *models.MessengerLabelState, snapshots []models.MessengerLabelSnapshot) error {
	if len(snapshots) == 0 {
		return nil
	}
	intakeLabelIDs, err := intakeLabelIDsFromSnapshots(snapshots)
	if err != nil {
		return err
	}
	if len(intakeLabelIDs) == 0 {
		return nil
	}
	var rules []Rule
	if json.Unmarshal([]byte(state.Rules), &rules) != nil {
		return ErrConfiguration
	}
	filtered, changed := filterIntakeRules(rules, intakeLabelIDs)
	if !changed {
		return nil
	}
	data, err := json.Marshal(filtered)
	if err != nil {
		return err
	}
	if err := tx.Model(state).Update("rules", string(data)).Error; err != nil {
		return err
	}
	state.Rules = string(data)
	return nil
}

func SaveCatalog(database *gorm.DB, channel models.Channel, catalog []channels.FacebookLabel, now time.Time) error {
	if err := ensureState(database, channel); err != nil {
		return err
	}
	if catalog == nil {
		catalog = []channels.FacebookLabel{}
	}
	data, err := json.Marshal(catalog)
	if err != nil {
		return err
	}
	return database.Model(&models.MessengerLabelState{}).Where("channel_id = ? AND tenant_id = ?", channel.ID, channel.TenantID).Updates(map[string]interface{}{"catalog": string(data), "catalog_synced_at": now}).Error
}

// ClaimSync serializes manual/scheduled work across replicas. A crashed worker's
// claim expires; the report treats an expired active claim as an error.
func ClaimSync(database *gorm.DB, channel models.Channel, now time.Time) (string, error) {
	state, err := LoadState(database, channel)
	if err != nil {
		return "", err
	}
	if !state.Enabled {
		return "", ErrDisabled
	}
	var policy Policy
	policy.Enabled = state.Enabled
	var catalog []channels.FacebookLabel
	if json.Unmarshal([]byte(state.Rules), &policy.Rules) != nil || json.Unmarshal([]byte(state.Catalog), &catalog) != nil || policy.Validate(catalog) != nil {
		return "", ErrConfiguration
	}
	token := pkg.NewUUID()
	// A short cooldown also applies after failures, preventing repeated manual
	// requests from immediately retrying a rate-limited Page. Keep its checkpoint.
	result := database.Model(&models.MessengerLabelState{}).Where("channel_id = ? AND tenant_id = ? AND enabled = ? AND (lease_until IS NULL OR lease_until < ?) AND (sync_finished_at IS NULL OR sync_finished_at <= ?)", channel.ID, channel.TenantID, true, now, now.Add(-time.Minute)).Updates(map[string]interface{}{"sync_status": "syncing", "sync_error": "", "sync_started_at": now, "lease_until": now.Add(syncLeaseDuration), "sync_token": token})
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected != 1 {
		return "", ErrBusy
	}
	return token, nil
}

type Page struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	IsActive bool   `json:"is_active"`
}

type SyncStatus struct {
	Status     string     `json:"status"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Error      string     `json:"error"`
}

type Row struct {
	ConversationID string                   `json:"conversation_id"`
	CustomerName   string                   `json:"customer_name"`
	ChannelID      string                   `json:"channel_id"`
	ChannelName    string                   `json:"channel_name"`
	Classification string                   `json:"classification"`
	Labels         []channels.FacebookLabel `json:"labels"`
	IntakeLabels   []channels.FacebookLabel `json:"intake_labels"`
	IntakeCaptured bool                     `json:"intake_captured"`
	IntakeError    string                   `json:"intake_error"`
	TrackingLabels []channels.FacebookLabel `json:"tracking_labels"`
	CheckedAt      *time.Time               `json:"checked_at"`
	Error          string                   `json:"error"`
}

type IntakeProgress struct {
	Total      int    `json:"total"`
	Captured   int    `json:"captured"`
	WithLabels int    `json:"with_labels"`
	Failed     int    `json:"failed"`
	Error      string `json:"error"`
}

type Report struct {
	GeneratedAt      time.Time                `json:"generated_at"`
	Pages            []Page                   `json:"pages"`
	ChannelID        string                   `json:"channel_id"`
	Enabled          bool                     `json:"enabled"`
	Rules            []Rule                   `json:"rules"`
	Catalog          []channels.FacebookLabel `json:"catalog"`
	IntakeLabelIDs   []string                 `json:"intake_label_ids"`
	CatalogSyncedAt  *time.Time               `json:"catalog_synced_at"`
	Sync             SyncStatus               `json:"sync"`
	Intake           IntakeProgress           `json:"intake"`
	FreshnessMinutes int                      `json:"freshness_minutes"`
	Counts           *Counts                  `json:"counts"`
	Rows             []Row                    `json:"rows"`
}

func BuildReport(ctx context.Context, database *gorm.DB, tenantID, channelID string, now time.Time) (*Report, error) {
	// Read status and snapshots from one database generation, even if a sweep
	// starts or commits a batch between these queries.
	var report *Report
	err := database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		report, err = buildReport(ctx, tx, tenantID, channelID, now)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return report, err
}

func buildReport(ctx context.Context, database *gorm.DB, tenantID, channelID string, now time.Time) (*Report, error) {
	database = database.WithContext(ctx)
	r := newReport(now)
	var pages []models.Channel
	if err := database.Where("tenant_id = ? AND channel_type = ?", tenantID, "facebook").Order("name ASC").Find(&pages).Error; err != nil {
		return nil, err
	}
	var selected *models.Channel
	for i := range pages {
		p := &pages[i]
		r.Pages = append(r.Pages, Page{p.ID, p.Name, p.IsActive})
		if p.ID == channelID {
			selected = p
		}
	}
	if channelID == "" {
		r.Counts = &Counts{}
		for _, page := range pages {
			pageReport, err := buildChannelReport(ctx, database, tenantID, page, now)
			if err != nil {
				return r, err
			}
			r.Rows = append(r.Rows, pageReport.Rows...)
			addCounts(r.Counts, pageReport.Counts)
			r.Intake.Total += pageReport.Intake.Total
			r.Intake.Captured += pageReport.Intake.Captured
			r.Intake.WithLabels += pageReport.Intake.WithLabels
			r.Intake.Failed += pageReport.Intake.Failed
			if r.Intake.Error == "" {
				r.Intake.Error = pageReport.Intake.Error
			}
		}
		sortRows(r.Rows)
		return r, nil
	}
	if selected == nil {
		return r, gorm.ErrRecordNotFound
	}
	r, err := buildChannelReport(ctx, database, tenantID, *selected, now)
	if err != nil {
		return r, err
	}
	r.Pages = make([]Page, 0, len(pages))
	for _, page := range pages {
		r.Pages = append(r.Pages, Page{page.ID, page.Name, page.IsActive})
	}
	return r, nil
}

func newReport(now time.Time) *Report {
	return &Report{GeneratedAt: now, Pages: []Page{}, Rules: []Rule{}, Catalog: []channels.FacebookLabel{}, IntakeLabelIDs: []string{}, FreshnessMinutes: FreshnessMinutes, Rows: []Row{}, Sync: SyncStatus{Status: "never"}}
}

func addCounts(total, added *Counts) {
	if total == nil || added == nil {
		return
	}
	total.Total += added.Total
	total.Unclassified += added.Unclassified
	total.Qualified += added.Qualified
	total.Unqualified += added.Unqualified
	total.Potential += added.Potential
	total.Conflict += added.Conflict
	total.Unknown += added.Unknown
}

func sortRows(rows []Row) {
	order := map[string]int{"unclassified": 0, "conflict": 1, "unknown": 2, "potential": 3, "qualified": 4, "unqualified": 5}
	sort.SliceStable(rows, func(i, j int) bool { return order[rows[i].Classification] < order[rows[j].Classification] })
}

func buildChannelReport(ctx context.Context, database *gorm.DB, tenantID string, selected models.Channel, now time.Time) (*Report, error) {
	r := newReport(now)
	channelID := selected.ID
	r.ChannelID = channelID
	state, err := LoadState(database, selected)
	if err != nil {
		return nil, err
	}
	r.Enabled = state.Enabled
	if json.Unmarshal([]byte(state.Rules), &r.Rules) != nil || json.Unmarshal([]byte(state.Catalog), &r.Catalog) != nil {
		return nil, ErrConfiguration
	}
	r.CatalogSyncedAt = state.CatalogSyncedAt
	r.Sync = SyncStatus{state.SyncStatus, state.SyncStartedAt, state.SyncFinishedAt, state.SyncError}
	if state.SyncStatus == "syncing" && (state.LeaseUntil == nil || !state.LeaseUntil.After(now)) {
		r.Sync.Status = "error"
		r.Sync.Error = "Lần đồng bộ bị gián đoạn; hãy đồng bộ lại."
	}
	ready := selected.IsActive && state.Enabled && (Policy{Enabled: state.Enabled, Rules: r.Rules}).Validate(r.Catalog) == nil && completedSync(r.Sync.Status) && state.CatalogSyncedAt != nil && !state.CatalogSyncedAt.After(now) && now.Sub(*state.CatalogSyncedAt) <= FreshnessMinutes*time.Minute
	var convs []models.Conversation
	if err := database.Select("id, customer_name").Where("tenant_id = ? AND channel_id = ?", tenantID, channelID).Order("id ASC").Limit(MaxConversations + 1).Find(&convs).Error; err != nil {
		return nil, err
	}
	if len(convs) > MaxConversations {
		return r, ErrTooLarge
	}
	var snapshots []models.MessengerLabelSnapshot
	if err := database.Where("tenant_id = ? AND channel_id = ?", tenantID, channelID).Find(&snapshots).Error; err != nil {
		return nil, err
	}
	byID := map[string]models.MessengerLabelSnapshot{}
	for _, snapshot := range snapshots {
		byID[snapshot.ConversationID] = snapshot
	}
	r.Counts = &Counts{}
	r.Intake.Total = len(convs)
	intakeLabelIDs, err := intakeLabelIDsFromSnapshots(snapshots)
	if err != nil {
		return nil, err
	}
	for id := range intakeLabelIDs {
		r.IntakeLabelIDs = append(r.IntakeLabelIDs, id)
	}
	sort.Strings(r.IntakeLabelIDs)
	for _, conv := range convs {
		snapshot := byID[conv.ID]
		labels := []channels.FacebookLabel{}
		valid := json.Unmarshal([]byte(snapshot.Labels), &labels) == nil && labels != nil
		if !valid {
			labels = []channels.FacebookLabel{}
		}
		intakeLabels := []channels.FacebookLabel{}
		intakeReady := snapshot.IntakeLabelsCapturedAt != nil && snapshot.IntakeLabels != nil && json.Unmarshal([]byte(*snapshot.IntakeLabels), &intakeLabels) == nil && intakeLabels != nil
		if !intakeReady {
			intakeLabels = []channels.FacebookLabel{}
			if snapshot.Status == "error" && snapshot.ErrorKind != "" {
				r.Intake.Failed++
				if r.Intake.Error == "" {
					r.Intake.Error = ErrorMessage(snapshot.ErrorKind)
				}
			}
		} else {
			r.Intake.Captured++
			if len(intakeLabels) > 0 {
				r.Intake.WithLabels++
			}
		}
		trackingLabels := TrackingLabels(labels, intakeLabelIDs)
		intakeError := ""
		if !intakeReady && snapshot.Status == "error" {
			intakeError = ErrorMessage(snapshot.ErrorKind)
		}
		classification := Classify(labels, intakeLabelIDs, snapshot.Status, snapshot.CheckedAt, r.Rules, ready && valid && intakeReady, now)
		errorText := ""
		if classification == "unknown" {
			switch {
			case !state.Enabled:
				errorText = "Chưa bật theo dõi nhãn."
			case !selected.IsActive:
				errorText = "Fanpage đang tạm dừng."
			case r.Sync.Status == "syncing":
				errorText = "Đang đồng bộ nhãn; số phân loại sẽ được tính khi lượt đọc hoàn tất."
			case !ready:
				errorText = "Cấu hình hoặc lần đồng bộ nhãn chưa sẵn sàng; hãy đồng bộ lại."
			case snapshot.Status == "error":
				errorText = ErrorMessage(snapshot.ErrorKind)
			case snapshot.CheckedAt == nil:
				errorText = "Chưa đọc nhãn thành công."
			case !intakeReady:
				errorText = "Chưa lưu được nhãn mặc định lúc tiếp nhận hội thoại."
			default:
				errorText = "Dữ liệu nhãn đã cũ; hãy đồng bộ lại."
			}
		}
		r.Rows = append(r.Rows, Row{
			ConversationID: conv.ID,
			CustomerName:   conv.CustomerName,
			ChannelID:      channelID,
			ChannelName:    selected.Name,
			Classification: classification,
			Labels:         labels,
			IntakeLabels:   intakeLabels,
			IntakeCaptured: intakeReady,
			IntakeError:    intakeError,
			TrackingLabels: trackingLabels,
			CheckedAt:      snapshot.CheckedAt,
			Error:          errorText,
		})
		r.Counts.Add(classification)
	}
	sortRows(r.Rows)
	return r, nil
}

func LoadIntakeLabelIDs(database *gorm.DB, channel models.Channel) (map[string]bool, error) {
	var snapshots []models.MessengerLabelSnapshot
	if err := database.Select("intake_labels,intake_labels_captured_at").Where("tenant_id = ? AND channel_id = ? AND intake_labels_captured_at IS NOT NULL", channel.TenantID, channel.ID).Find(&snapshots).Error; err != nil {
		return nil, err
	}
	return intakeLabelIDsFromSnapshots(snapshots)
}

func intakeLabelIDsFromSnapshots(snapshots []models.MessengerLabelSnapshot) (map[string]bool, error) {
	ids := map[string]bool{}
	for _, snapshot := range snapshots {
		if snapshot.IntakeLabelsCapturedAt == nil {
			continue
		}
		if snapshot.IntakeLabels == nil {
			return nil, ErrConfiguration
		}
		var labels []channels.FacebookLabel
		if json.Unmarshal([]byte(*snapshot.IntakeLabels), &labels) != nil || labels == nil {
			return nil, ErrConfiguration
		}
		for _, label := range labels {
			if strings.TrimSpace(label.ID) == "" {
				return nil, ErrConfiguration
			}
			ids[label.ID] = true
		}
	}
	return ids, nil
}
