package customercases

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

// Channel metadata keys for complaint cases (set on the channel's Zalo
// settings next to the complaint_cases switch).
const (
	// MetaResolvedDays: how many days after staff mark a case resolved the
	// bot still sees it and a "vẫn còn lỗi" report reopens it.
	MetaResolvedDays = "complaint_resolved_days"
	// MetaStreams: the complaint streams (bảo hành, sản phẩm, marketing...).
	// The decision model files each new case under one, and a stream's
	// alert group receives its alerts.
	MetaStreams = "complaint_streams"

	DefaultResolvedDays = 14
	MaxResolvedDays     = 90
	MaxStreams          = 12
)

// Stream is one complaint stream.
type Stream struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// AlertGroupID is the CRM group that gets this stream's alerts; empty
	// means the channel's handover alert group.
	AlertGroupID string `json:"alert_group_id,omitempty"`
}

// Settings are a channel's complaint case settings.
type Settings struct {
	ResolvedDays int
	Streams      []Stream
}

// ResolvedWindow is how long a resolved case stays visible to the bot and
// the decision model.
func (s Settings) ResolvedWindow() time.Duration {
	d := s.ResolvedDays
	if d <= 0 {
		d = DefaultResolvedDays
	}
	return time.Duration(d) * 24 * time.Hour
}

// Stream returns the stream with key k.
func (s Settings) Stream(k string) (Stream, bool) {
	for _, st := range s.Streams {
		if st.Key == k {
			return st, true
		}
	}
	return Stream{}, false
}

// ParseSettings reads the complaint settings from channel metadata JSON.
// Bad values fall back to the defaults.
func ParseSettings(metadata string) Settings {
	out := Settings{ResolvedDays: DefaultResolvedDays}
	var m map[string]interface{}
	if json.Unmarshal([]byte(metadata), &m) != nil {
		return out
	}
	if v, ok := m[MetaResolvedDays].(float64); ok && v >= 1 {
		out.ResolvedDays = int(v)
		if out.ResolvedDays > MaxResolvedDays {
			out.ResolvedDays = MaxResolvedDays
		}
	}
	if streams, err := NormalizeStreams(m[MetaStreams]); err == nil {
		out.Streams = streams
	}
	return out
}

// channelSettings loads a channel's complaint settings (defaults when the
// channel is unknown).
func channelSettings(ctx context.Context, tenantID, channelID string) Settings {
	if db.DB == nil || channelID == "" {
		return Settings{ResolvedDays: DefaultResolvedDays}
	}
	var ch models.Channel
	if err := db.DB.WithContext(ctx).Select("metadata").Where("id = ? AND tenant_id = ?", channelID, tenantID).First(&ch).Error; err != nil {
		return Settings{ResolvedDays: DefaultResolvedDays}
	}
	return ParseSettings(ch.Metadata)
}

var streamKeyRe = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)

// NormalizeStreams validates and trims a complaint_streams value (as decoded
// from JSON). nil means no streams.
func NormalizeStreams(raw interface{}) ([]Stream, error) {
	if raw == nil {
		return nil, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var in []Stream
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, fmt.Errorf("complaint_streams must be a list of {key, name, description, alert_group_id}")
	}
	if len(in) > MaxStreams {
		return nil, fmt.Errorf("at most %d complaint streams", MaxStreams)
	}
	seen := map[string]bool{}
	out := make([]Stream, 0, len(in))
	for _, s := range in {
		s.Key = strings.ToLower(strings.TrimSpace(s.Key))
		s.Name = truncate(strings.TrimSpace(s.Name), 60)
		s.Description = truncate(strings.TrimSpace(s.Description), 400)
		s.AlertGroupID = strings.TrimSpace(s.AlertGroupID)
		if s.Name == "" {
			return nil, fmt.Errorf("every complaint stream needs a name")
		}
		// "none" is the cases page's filter for cases without a stream.
		if !streamKeyRe.MatchString(s.Key) || s.Key == "none" {
			return nil, fmt.Errorf("invalid complaint stream key %q", s.Key)
		}
		if seen[s.Key] {
			return nil, fmt.Errorf("duplicate complaint stream key %q", s.Key)
		}
		seen[s.Key] = true
		out = append(out, s)
	}
	return out, nil
}

// StreamLabel is the stream's name for key in the given settings, or the
// key itself when the stream was removed.
func StreamLabel(s Settings, key string) string {
	if key == "" {
		return ""
	}
	if st, ok := s.Stream(key); ok {
		return st.Name
	}
	return key
}

// TenantStreams lists the streams of the tenant's channels that have
// complaint cases on, merged by key (first name wins), for the cases page.
func TenantStreams(ctx context.Context, tenantID string) []Stream {
	if db.DB == nil {
		return nil
	}
	var chans []models.Channel
	db.DB.WithContext(ctx).Select("id, metadata").
		Where("tenant_id = ? AND channel_type IN ?", tenantID, []string{"zalo_oa", "personal_zalo_import"}).
		Order("created_at").Find(&chans)
	seen := map[string]bool{}
	var out []Stream
	for _, ch := range chans {
		for _, st := range ParseSettings(ch.Metadata).Streams {
			if !seen[st.Key] {
				seen[st.Key] = true
				out = append(out, Stream{Key: st.Key, Name: st.Name})
			}
		}
	}
	return out
}
