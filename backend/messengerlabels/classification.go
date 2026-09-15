// Package messengerlabels observes labels assigned in Meta Inbox. It does not
// infer native Meta lead stages, AI classifications, or staff identity.
package messengerlabels

import (
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db/models"
)

const FreshnessMinutes = 30
const MaxConversations = 5000

var (
	ErrConfiguration = errors.New("chọn và lưu nhãn phân loại hợp lệ cho Fanpage trước khi đồng bộ")
	ErrIntakeLabel   = errors.New("nhãn mặc định đã có khi hội thoại được tiếp nhận không được dùng làm nhãn theo dõi")
	ErrDisabled      = errors.New("chưa bật theo dõi nhãn cho Fanpage này")
	ErrBusy          = errors.New("Fanpage đang đồng bộ nhãn hoặc vừa chạy xong; vui lòng chờ ít nhất 1 phút trước khi thử lại")
	ErrTooLarge      = errors.New("Fanpage vượt 5.000 hội thoại đã đồng bộ; chưa thể trả số đếm đầy đủ")
	digits           = regexp.MustCompile(`^[0-9]+$`)
)

type Rule struct {
	LabelID  string `json:"label_id"`
	Category string `json:"category"`
}

type Policy struct {
	Enabled bool   `json:"enabled"`
	Rules   []Rule `json:"rules"`
}

func (p Policy) Validate(catalog []channels.FacebookLabel) error {
	if p.Enabled && len(p.Rules) == 0 || len(p.Rules) > 100 {
		return ErrConfiguration
	}
	ids := map[string]bool{}
	for _, label := range catalog {
		ids[label.ID] = true
	}
	seen := map[string]bool{}
	for _, rule := range p.Rules {
		if !digits.MatchString(rule.LabelID) || seen[rule.LabelID] || (p.Enabled && !ids[rule.LabelID]) {
			return ErrConfiguration
		}
		if rule.Category != "qualified" && rule.Category != "unqualified" && rule.Category != "potential" {
			return ErrConfiguration
		}
		seen[rule.LabelID] = true
	}
	return nil
}

// ValidateTracking enforces the boundary between labels already present when a
// conversation entered CQA and labels intentionally added later for workflow
// tracking. Meta's label API does not expose assignment provenance.
func (p Policy) ValidateTracking(catalog []channels.FacebookLabel, intakeLabelIDs map[string]bool) error {
	if err := p.Validate(catalog); err != nil {
		return err
	}
	if !p.Enabled {
		return nil
	}
	for _, rule := range p.Rules {
		if intakeLabelIDs[rule.LabelID] {
			return ErrIntakeLabel
		}
	}
	return nil
}

type Counts struct {
	Total        int `json:"total"`
	Unclassified int `json:"unclassified"`
	Qualified    int `json:"qualified"`
	Unqualified  int `json:"unqualified"`
	Potential    int `json:"potential"`
	Conflict     int `json:"conflict"`
	Unknown      int `json:"unknown"`
}

func (c *Counts) Add(classification string) {
	c.Total++
	switch classification {
	case "unclassified":
		c.Unclassified++
	case "qualified":
		c.Qualified++
	case "unqualified":
		c.Unqualified++
	case "potential":
		c.Potential++
	case "conflict":
		c.Conflict++
	default:
		c.Unknown++
	}
}

// Classify requires evidence that the read completed recently. IDs observed at
// intake are excluded even if a stale or malicious policy maps them.
func Classify(labels []channels.FacebookLabel, intakeLabelIDs map[string]bool, status string, checkedAt *time.Time, rules []Rule, ready bool, now time.Time) string {
	if !ready || status != "success" || checkedAt == nil || checkedAt.After(now) || now.Sub(*checkedAt) > FreshnessMinutes*time.Minute {
		return "unknown"
	}
	categories := map[string]bool{}
	for _, label := range labels {
		if intakeLabelIDs[label.ID] {
			continue
		}
		for _, rule := range rules {
			if label.ID == rule.LabelID {
				categories[rule.Category] = true
			}
		}
	}
	if len(categories) > 1 {
		return "conflict"
	}
	for category := range categories {
		return category
	}
	return "unclassified"
}

func TrackingLabels(labels []channels.FacebookLabel, intakeLabelIDs map[string]bool) []channels.FacebookLabel {
	tracking := make([]channels.FacebookLabel, 0, len(labels))
	for _, label := range labels {
		if !intakeLabelIDs[label.ID] {
			tracking = append(tracking, label)
		}
	}
	return tracking
}

func filterIntakeRules(rules []Rule, intakeLabelIDs map[string]bool) ([]Rule, bool) {
	filtered := make([]Rule, 0, len(rules))
	changed := false
	for _, rule := range rules {
		if intakeLabelIDs[rule.LabelID] {
			changed = true
			continue
		}
		filtered = append(filtered, rule)
	}
	return filtered, changed
}

// Resolve participant identity from old sync metadata. The old Facebook adapter
// stored conversation ID as ExternalUserID, so that field is deliberately unused.
func ParticipantPSID(c models.Conversation, pageID string) string {
	var metadata struct {
		Participants struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			Paging struct {
				Next string `json:"next"`
			} `json:"paging"`
		} `json:"participants"`
	}
	if json.Unmarshal([]byte(c.Metadata), &metadata) != nil || metadata.Participants.Paging.Next != "" {
		return ""
	}
	ids := map[string]bool{}
	for _, participant := range metadata.Participants.Data {
		if participant.ID != pageID {
			if !digits.MatchString(participant.ID) {
				return ""
			}
			ids[participant.ID] = true
		}
	}
	if len(ids) != 1 {
		return ""
	}
	for id := range ids {
		return id
	}
	return ""
}
