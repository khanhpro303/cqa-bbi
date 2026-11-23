package customercases

import (
	"context"
	"encoding/json"
	"log"
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
	adapter, err := channelAdapter(ch)
	if err != nil {
		log.Printf("[cases] alert: adapter for channel %s: %v", ch.ID, err)
		return false
	}
	return engine.NotifyCase(ctx, adapter, a)
}

// alertStaff tells staff about a new case, a reopened case, or a customer
// chasing an open case (at most once per followupAlertGap). It reports
// whether staff know about this report: alerted now, or alerted about the
// case recently enough (follow-up) or before (summary link).
func alertStaff(ctx context.Context, out *Outcome, r Report) bool {
	c := out.Case
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
		return false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), alertTimeout)
	defer cancel()
	var ch models.Channel
	if err := db.DB.WithContext(ctx).Where("id = ? AND tenant_id = ?", c.ChannelID, c.TenantID).First(&ch).Error; err != nil {
		log.Printf("[cases] alert: load channel %s: %v", c.ChannelID, err)
		return false
	}
	if !EnabledFor(ch.Metadata) {
		return false
	}
	group := ""
	if c.SubjectType == "group" {
		group = r.ChatGroup
		if group == "" {
			group = c.CustomerName
		}
	}
	a := engine.CaseAlert{
		TenantID: c.TenantID, Channel: ch, Kind: out.Action, ZaloUserID: c.ZaloUserID,
		ReporterName: r.ReporterName, CustomerName: c.CustomerName, CustomerCode: c.CustomerCode, ChatGroup: group,
		TypeLabel: TypeLabel(c.Type), Severity: c.Severity, Title: c.Title, StatusLabel: StatusLabel(c.Status),
		FollowupCount: c.FollowupCount, ImageCount: len(parseImages(c.Images)), FromSummary: r.Source == SourceSummary,
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

func joinNonEmpty(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "" || b == a:
		return a
	}
	return a + ". " + b
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
