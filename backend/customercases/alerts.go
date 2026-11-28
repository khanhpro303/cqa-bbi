package customercases

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/channels/zalotoken"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
)

const alertTimeout = 10 * time.Second

// MetaEnabled is the channel metadata switch for complaint cases: the bot
// gets the complaint guide and the customer's open cases, a HANDOVER turn
// goes to the bot so it can record the case, and staff are alerted about
// cases. Unset means off: turn it on once the Langflow MCP Tools node lists
// complaint_record, or complaints would reach a bot that cannot record them.
const MetaEnabled = "complaint_cases"

// EnabledFor reads the switch from channel metadata JSON.
func EnabledFor(metadata string) bool {
	var m map[string]interface{}
	if json.Unmarshal([]byte(metadata), &m) != nil {
		return false
	}
	v, _ := m[MetaEnabled].(bool)
	return v
}

// sendAlert delivers a case alert; replaced in tests.
var sendAlert = func(ctx context.Context, ch models.Channel, a engine.CaseAlert) bool {
	if ch.ChannelType != "personal_zalo_import" {
		adapter, err := channelAdapter(ch)
		if err != nil {
			log.Printf("[cases] alert: adapter for channel %s: %v", ch.ID, err)
			groupID := strings.TrimSpace(a.GroupID)
			if groupID == "" {
				groupID = engine.ParseHandoverAlertGroupID(ch.Metadata)
			}
			engine.RecordCaseAlertAttempt(a, groupID, "", "failed", "sender_unavailable")
			return false
		}
		return engine.NotifyCase(ctx, adapter, a)
	}
	destination, err := personalCaseAlertDestination(ctx, ch, a)
	if err != nil {
		log.Printf("[cases] alert: destination for personal channel %s: %v", ch.ID, err)
		groupID := strings.TrimSpace(a.GroupID)
		handoverID := engine.ParseHandoverAlertGroupID(ch.Metadata)
		if handoverID != "" && groupID != handoverID {
			groupID = handoverID
		}
		engine.RecordCaseAlertAttempt(a, groupID, "", "failed", "destination_unavailable")
		return false
	}
	adapter, err := channelAdapter(destination.oa)
	if err != nil {
		log.Printf("[cases] alert: adapter for channel %s: %v", destination.oa.ID, err)
		engine.RecordCaseAlertAttempt(a, destination.groupID, destination.zaloGroupID, "failed", "sender_unavailable")
		return false
	}
	return engine.NotifyCaseToGroup(ctx, adapter, a, destination.groupID, destination.zaloGroupID)
}

// alertStaff tells staff about a new case, a reopened case, or a customer
// chasing an open case (at most once per followupAlertGap). It reports
// whether staff know about this report: alerted now, or alerted about the
// case recently enough (follow-up) or before (summary link).
func alertStaff(ctx context.Context, out *Outcome, r Report) bool {
	c := out.Case
	if out.repeated {
		return out.linkedNotified
	}
	switch out.Action {
	case ActionCreated, ActionReopened:
	case ActionFollowup:
		if c.LastAlertAt != nil && time.Since(*c.LastAlertAt) < followupAlertGap {
			return true
		}
	case ActionLinked:
		return out.linkedNotified
	default:
		return false
	}
	if c.ChannelID == "" {
		engine.RecordCaseAlertAttempt(engine.CaseAlert{TenantID: c.TenantID, CaseID: c.ID, Kind: out.Action}, "", "", "failed", "case_channel_missing")
		return false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), alertTimeout)
	defer cancel()
	var ch models.Channel
	if err := db.DB.WithContext(ctx).Where("id = ? AND tenant_id = ?", c.ChannelID, c.TenantID).First(&ch).Error; err != nil {
		log.Printf("[cases] alert: load channel %s: %v", c.ChannelID, err)
		engine.RecordCaseAlertAttempt(engine.CaseAlert{TenantID: c.TenantID, CaseID: c.ID, Kind: out.Action}, "", "", "failed", "case_channel_unavailable")
		return false
	}
	if !EnabledFor(ch.Metadata) {
		engine.RecordCaseAlertAttempt(engine.CaseAlert{TenantID: c.TenantID, CaseID: c.ID, Kind: out.Action}, "", "", "failed", "complaint_alerts_disabled")
		return false
	}
	group := ""
	if c.SubjectType == "group" || ch.ChannelType == "personal_zalo_import" {
		group = r.ChatGroup
		if strings.TrimSpace(group) == "" {
			group = c.CustomerName
			if ch.ChannelType == "personal_zalo_import" {
				group = r.ZaloGroupID
			}
		}
	}
	a := engine.CaseAlert{
		TenantID: c.TenantID, CaseID: c.ID, Channel: ch, Kind: out.Action, ZaloUserID: c.ZaloUserID,
		ReporterName: r.ReporterName, CustomerName: c.CustomerName, CustomerCode: c.CustomerCode, ChatGroup: group,
		TypeLabel: TypeLabel(c.Type), Severity: c.Severity, Title: c.Title, StatusLabel: StatusLabel(c.Status),
		FollowupCount: c.FollowupCount, ImageCount: len(parseImages(c.Images)), FromSummary: r.Source == SourceSummary,
		SourceGroupID: r.ZaloGroupID,
	}
	if st, ok := ParseSettings(ch.Metadata).Stream(c.Stream); ok {
		a.StreamName, a.GroupID = st.Name, st.AlertGroupID
	}
	if out.Action == ActionCreated {
		a.Detail, a.Evidence = c.Detail, c.Evidence
	} else {
		// What the customer said this time.
		a.Detail, a.Evidence = r.Detail, r.Evidence
		if r.Title != c.Title {
			a.Detail = joinNonEmpty(r.Title, r.Detail)
		}
	}
	if !sendAlert(ctx, ch, a) {
		log.Printf("[cases] alert for case %s (%s) was not delivered", c.ID, out.Action)
		return false
	}
	now := time.Now()
	db.DB.WithContext(ctx).Model(&models.CustomerCase{}).Where("id = ?", c.ID).Update("last_alert_at", now)
	out.Case.LastAlertAt = &now
	return true
}

// AlertStreamMoved tells a stream's group that staff moved an open case to
// it. Best effort: the move stands even if the alert fails.
func AlertStreamMoved(ctx context.Context, c models.CustomerCase, actor string) {
	if c.Stream == "" {
		return
	}
	baseAlert := engine.CaseAlert{TenantID: c.TenantID, CaseID: c.ID, Kind: "moved"}
	if c.ChannelID == "" {
		engine.RecordCaseAlertAttempt(baseAlert, "", "", "failed", "case_channel_missing")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), alertTimeout)
	defer cancel()
	var ch models.Channel
	if err := db.DB.WithContext(ctx).Where("id = ? AND tenant_id = ?", c.ChannelID, c.TenantID).First(&ch).Error; err != nil {
		engine.RecordCaseAlertAttempt(baseAlert, "", "", "failed", "case_channel_unavailable")
		return
	}
	if !EnabledFor(ch.Metadata) {
		engine.RecordCaseAlertAttempt(baseAlert, "", "", "failed", "complaint_alerts_disabled")
		return
	}
	st, ok := ParseSettings(ch.Metadata).Stream(c.Stream)
	if !ok {
		engine.RecordCaseAlertAttempt(baseAlert, "", "", "failed", "complaint_stream_unavailable")
		return
	}
	a := engine.CaseAlert{
		TenantID: c.TenantID, CaseID: c.ID, Channel: ch, Kind: "moved", ZaloUserID: c.ZaloUserID,
		ReporterName: c.ReporterName, CustomerName: c.CustomerName, CustomerCode: c.CustomerCode,
		TypeLabel: TypeLabel(c.Type), Severity: c.Severity, Title: c.Title, Detail: c.Detail, Evidence: c.Evidence,
		StatusLabel: StatusLabel(c.Status), ImageCount: len(parseImages(c.Images)),
		StreamName: st.Name, GroupID: st.AlertGroupID, MovedBy: actor,
		SourceGroupID: c.ZaloGroupID,
	}
	if c.SubjectType == "group" {
		a.ChatGroup = c.CustomerName
	}
	if !sendAlert(ctx, ch, a) {
		log.Printf("[cases] stream move alert for case %s was not delivered", c.ID)
	}
}

// CaseAlerts returns a case's alert attempts, newest first. Missing historical
// rows simply mean the case predates alert outcome tracking.
func CaseAlerts(ctx context.Context, tenantID, caseID string) ([]models.CustomerCaseAlert, error) {
	rows := make([]models.CustomerCaseAlert, 0)
	err := db.DB.WithContext(ctx).
		Where("tenant_id = ? AND case_id = ?", tenantID, caseID).
		Order("created_at DESC, id DESC").Limit(100).Find(&rows).Error
	return rows, err
}

// CaseFallbackObserver associates an actual handover result with the cases
// whose original alert failed on this turn.
func CaseFallbackObserver(ctx context.Context, tenantID, turnID string) engine.HandoverDeliveryObserver {
	if turnID == "" {
		return nil
	}
	return func(groupID, zaloGroupID, kind, status, errorCode string) {
		traceCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		var events []models.CustomerCaseEvent
		if err := db.DB.WithContext(traceCtx).Select("id, case_id").Where("tenant_id = ? AND turn_id = ? AND actor = ? AND staff_notified = ?", tenantID, turnID, SourceBot, false).
			Find(&events).Error; err != nil {
			log.Printf("[cases] load fallback trace events: %v", err)
			return
		}
		seen := make(map[string]bool, len(events))
		for _, event := range events {
			if !seen[event.CaseID] {
				engine.RecordCaseAlertAttempt(engine.CaseAlert{TenantID: tenantID, CaseID: event.CaseID, Kind: kind}, groupID, zaloGroupID, status, errorCode)
				seen[event.CaseID] = true
			}
		}
	}
}

func joinNonEmpty(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "" || b == a:
		return a
	}
	return a + ". " + b
}

// The recipient and credentials are resolved together so a fallback OA can
// only send to the matching fallback group.
type personalCaseDestination struct {
	oa          models.Channel
	groupID     string
	zaloGroupID string
}

func personalCaseAlertDestination(ctx context.Context, ch models.Channel, a engine.CaseAlert) (personalCaseDestination, error) {
	groupID := strings.TrimSpace(a.GroupID)
	handoverID := engine.ParseHandoverAlertGroupID(ch.Metadata)
	if groupID == "" {
		groupID = handoverID
	}
	destination, err := loadPersonalCaseDestination(ctx, ch.TenantID, groupID, a.SourceGroupID)
	if err != nil && groupID != handoverID && handoverID != "" {
		destination, err = loadPersonalCaseDestination(ctx, ch.TenantID, handoverID, a.SourceGroupID)
	}
	if err != nil {
		return personalCaseDestination{}, err
	}
	return destination, nil
}

func loadPersonalCaseDestination(ctx context.Context, tenantID, groupID, sourceGroupID string) (personalCaseDestination, error) {
	if groupID == "" {
		return personalCaseDestination{}, fmt.Errorf("personal Zalo channel has no complaint alert group")
	}
	var group models.CRMGroup
	err := db.DB.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", groupID, tenantID).
		Where("COALESCE(TRIM(zalo_group_id), '') <> '' AND COALESCE(TRIM(channel_id), '') <> ''").
		Where("COALESCE(TRIM(customer_code), '') = ''").
		Where("NOT EXISTS (SELECT 1 FROM crm_group_customer_codes WHERE group_id = crm_groups.id)").
		Where("NOT EXISTS (SELECT 1 FROM crm_group_customers WHERE group_id = crm_groups.id)").
		First(&group).Error
	if err != nil {
		return personalCaseDestination{}, err
	}
	if strings.TrimSpace(sourceGroupID) != "" && strings.TrimSpace(sourceGroupID) == strings.TrimSpace(group.ZaloGroupID) {
		return personalCaseDestination{}, fmt.Errorf("complaint alert group is the source customer chat")
	}
	var oa models.Channel
	err = db.DB.WithContext(ctx).
		Where("id = ? AND tenant_id = ? AND channel_type = ? AND is_active = ?", group.ChannelID, tenantID, "zalo_oa", true).
		First(&oa).Error
	if err != nil {
		return personalCaseDestination{}, err
	}
	return personalCaseDestination{oa: oa, groupID: group.ID, zaloGroupID: strings.TrimSpace(group.ZaloGroupID)}, nil
}

func channelAdapter(ch models.Channel) (*channels.ZaloOAAdapter, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	credBytes, err := pkg.Decrypt(ch.CredentialsEncrypted, cfg.EncryptionKey)
	if err != nil {
		return nil, err
	}
	var creds channels.ZaloOACredentials
	if err := json.Unmarshal(credBytes, &creds); err != nil {
		return nil, err
	}
	return zalotoken.NewAdapter(ch.ID, creds, cfg.EncryptionKey), nil
}
