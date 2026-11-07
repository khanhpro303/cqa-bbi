package pancakelabels

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/messengerlabels"
	"gorm.io/gorm"
)

// Connection is the non-secret view of the tenant's Pancake session.
type Connection struct {
	HasUserToken      bool       `json:"has_user_token"`
	SocketEnabled     bool       `json:"socket_enabled"`
	SocketStatus      string     `json:"socket_status"`
	SocketError       string     `json:"socket_error"`
	SocketConnectedAt *time.Time `json:"socket_connected_at"`
	SocketEventAt     *time.Time `json:"socket_event_at"`
}

// Report keeps the field names of messengerlabels.Report so the CQA label
// panel renders both sources with one component.
type Report struct {
	Source           string                         `json:"source"`
	GeneratedAt      time.Time                      `json:"generated_at"`
	Pages            []messengerlabels.Page         `json:"pages"`
	ChannelID        string                         `json:"channel_id"`
	Enabled          bool                           `json:"enabled"`
	MetaEnabled      bool                           `json:"meta_enabled"`
	HasPageToken     bool                           `json:"has_page_token"`
	Rules            []messengerlabels.Rule         `json:"rules"`
	Catalog          []channels.FacebookLabel       `json:"catalog"`
	IntakeLabelIDs   []string                       `json:"intake_label_ids"`
	CatalogSyncedAt  *time.Time                     `json:"catalog_synced_at"`
	ReconciledAt     *time.Time                     `json:"reconciled_at"`
	Sync             messengerlabels.SyncStatus     `json:"sync"`
	Intake           messengerlabels.IntakeProgress `json:"intake"`
	FreshnessMinutes int                            `json:"freshness_minutes"`
	Counts           *messengerlabels.Counts        `json:"counts"`
	Rows             []messengerlabels.Row          `json:"rows"`
	// Customers tagged in Pancake that have no CQA conversation yet.
	Unmatched  int        `json:"unmatched"`
	Connection Connection `json:"connection"`
}

func newReport(now time.Time) *Report {
	return &Report{Source: "pancake", GeneratedAt: now, Pages: []messengerlabels.Page{}, Rules: []messengerlabels.Rule{}, Catalog: []channels.FacebookLabel{}, IntakeLabelIDs: []string{}, FreshnessMinutes: int(ReadyWindow / time.Minute), Rows: []messengerlabels.Row{}, Sync: messengerlabels.SyncStatus{Status: "never"}}
}

func BuildReport(ctx context.Context, database *gorm.DB, tenantID, channelID string, socketEnabled bool, now time.Time) (*Report, error) {
	var report *Report
	err := database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		report, err = buildReport(tx, tenantID, channelID, socketEnabled, now)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return report, err
}

func buildReport(database *gorm.DB, tenantID, channelID string, socketEnabled bool, now time.Time) (*Report, error) {
	r := newReport(now)
	conn, err := LoadConnection(database, tenantID)
	if err != nil {
		return nil, err
	}
	r.Connection = Connection{HasUserToken: len(conn.UserTokenEncrypted) > 0, SocketEnabled: socketEnabled, SocketStatus: conn.SocketStatus, SocketError: conn.SocketError, SocketConnectedAt: conn.SocketConnectedAt, SocketEventAt: conn.SocketEventAt}
	if conn.SocketStatus == "connected" && (conn.LeaseUntil == nil || conn.LeaseUntil.Before(now)) {
		r.Connection.SocketStatus = "disconnected"
	}
	var pages []models.Channel
	if err := database.Where("tenant_id = ? AND channel_type = ?", tenantID, "facebook").Order("name ASC").Find(&pages).Error; err != nil {
		return nil, err
	}
	var selected *models.Channel
	for i := range pages {
		r.Pages = append(r.Pages, messengerlabels.Page{ID: pages[i].ID, Name: pages[i].Name, IsActive: pages[i].IsActive})
		if pages[i].ID == channelID {
			selected = &pages[i]
		}
	}
	if channelID == "" {
		r.Counts = &messengerlabels.Counts{}
		return r, nil
	}
	if selected == nil {
		return r, gorm.ErrRecordNotFound
	}
	r.ChannelID = selected.ID
	state, err := LoadState(database, *selected)
	if err != nil {
		return nil, err
	}
	var meta models.MessengerLabelState
	if database.Select("enabled").Where("channel_id = ? AND tenant_id = ?", selected.ID, tenantID).First(&meta).Error == nil {
		r.MetaEnabled = meta.Enabled
	}
	r.Enabled, r.HasPageToken = state.Enabled, len(state.PageTokenEncrypted) > 0
	if json.Unmarshal([]byte(state.Rules), &r.Rules) != nil || json.Unmarshal([]byte(state.Catalog), &r.Catalog) != nil {
		return nil, ErrConfiguration
	}
	r.CatalogSyncedAt, r.ReconciledAt = state.CatalogSyncedAt, state.ReconciledAt
	r.Sync = messengerlabels.SyncStatus{Status: state.SyncStatus, StartedAt: state.SyncStartedAt, FinishedAt: state.SyncFinishedAt, Error: state.SyncError}
	if state.SyncStatus == "syncing" && (state.LeaseUntil == nil || !state.LeaseUntil.After(now)) {
		r.Sync.Status, r.Sync.Error = "error", "Lần đối soát bị gián đoạn; hãy đồng bộ lại."
	}
	ready := Ready(*selected, state, now)

	var convs []models.Conversation
	if err := database.Select("id,tenant_id,channel_id,customer_name,metadata").Where("tenant_id = ? AND channel_id = ?", tenantID, selected.ID).Order("id ASC").Limit(messengerlabels.MaxConversations + 1).Find(&convs).Error; err != nil {
		return nil, err
	}
	if len(convs) > messengerlabels.MaxConversations {
		return r, messengerlabels.ErrTooLarge
	}
	var snapshots []models.PancakeLabelSnapshot
	if err := database.Where("tenant_id = ? AND channel_id = ?", tenantID, selected.ID).Find(&snapshots).Error; err != nil {
		return nil, err
	}
	byPSID := map[string]models.PancakeLabelSnapshot{}
	byConv := map[string]models.PancakeLabelSnapshot{}
	for _, snapshot := range snapshots {
		byPSID[snapshot.PSID] = snapshot
		if snapshot.ConversationID != nil {
			byConv[*snapshot.ConversationID] = snapshot
		}
	}
	var metaSnapshots []models.MessengerLabelSnapshot
	if err := database.Select("conversation_id,psid").Where("tenant_id = ? AND channel_id = ? AND psid <> ''", tenantID, selected.ID).Find(&metaSnapshots).Error; err != nil {
		return nil, err
	}
	metaPSID := map[string]string{}
	for _, snapshot := range metaSnapshots {
		metaPSID[snapshot.ConversationID] = snapshot.PSID
	}
	matched := map[string]bool{}
	r.Counts = &messengerlabels.Counts{}
	for _, conv := range convs {
		snapshot, found := byConv[conv.ID]
		if !found {
			psid := messengerlabels.ParticipantPSID(conv, selected.ExternalID)
			if psid == "" {
				psid = metaPSID[conv.ID]
			}
			if psid != "" {
				snapshot, found = byPSID[psid]
			}
		}
		labels := []channels.FacebookLabel{}
		var checkedAt *time.Time
		if found {
			matched[snapshot.PSID] = true
			if json.Unmarshal([]byte(snapshot.Labels), &labels) != nil || labels == nil {
				labels = []channels.FacebookLabel{}
			}
			at := snapshot.CheckedAt
			checkedAt = &at
		}
		classification := "unknown"
		errorText := ""
		switch {
		case !state.Enabled:
			errorText = "Chưa bật phân loại theo nhãn Pancake."
		case !selected.IsActive:
			errorText = "Fanpage đang tạm dừng."
		case !ready:
			errorText = "Chưa đối soát nhãn Pancake gần đây; hãy đồng bộ lại."
		case !found:
			errorText = "Pancake chưa có hội thoại inbox của khách này."
		default:
			classification = Classification(labels, r.Rules, true, now)
		}
		if classification != "unknown" {
			errorText = ""
		}
		r.Rows = append(r.Rows, messengerlabels.Row{
			ConversationID: conv.ID, CustomerName: conv.CustomerName, ChannelID: selected.ID, ChannelName: selected.Name,
			Classification: classification, Labels: labels, IntakeLabels: []channels.FacebookLabel{}, IntakeCaptured: found,
			TrackingLabels: labels, CheckedAt: checkedAt, Error: errorText,
		})
		r.Counts.Add(classification)
	}
	for psid := range byPSID {
		if !matched[psid] {
			r.Unmatched++
		}
	}
	order := map[string]int{"unclassified": 0, "conflict": 1, "unknown": 2, "potential": 3, "qualified": 4, "unqualified": 5}
	sort.SliceStable(r.Rows, func(i, j int) bool { return order[r.Rows[i].Classification] < order[r.Rows[j].Classification] })
	return r, nil
}
