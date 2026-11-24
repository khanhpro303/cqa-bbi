package customercases

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// MetaStreams is the channel metadata list of complaint streams (luồng xử
// lý: bảo hành, khách hàng, sản phẩm, marketing…). The decision model files
// each new case under one stream, and the case alert goes to that stream's
// group; a stream without a group uses the channel's handover alert group.
const MetaStreams = "complaint_streams"

// maxStreams caps the list sent to the decision model.
const maxStreams = 20

// Stream is one complaint stream.
type Stream struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// AlertGroupID is the CRM group that receives this stream's alerts.
	AlertGroupID string `json:"alert_group_id"`
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// StreamKey turns a key or name into a stable slug ("Bảo hành" → "bao_hanh").
func StreamKey(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "đ", "d"), "Đ", "D")
	folded, _, err := transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC), s)
	if err == nil {
		s = folded
	}
	s = nonSlug.ReplaceAllString(strings.ToLower(s), "_")
	s = strings.Trim(s, "_")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "_")
	}
	return s
}

// StreamsFor reads the channel's complaint streams. A stream without a key
// takes one from its name; entries without a name, and repeated keys, are
// skipped. It returns nil when none are set.
func StreamsFor(metadata string) []Stream {
	var m struct {
		Streams []Stream `json:"complaint_streams"`
	}
	if json.Unmarshal([]byte(metadata), &m) != nil || len(m.Streams) == 0 {
		return nil
	}
	var out []Stream
	seen := map[string]bool{}
	for _, s := range m.Streams {
		s.Name = truncate(strings.TrimSpace(s.Name), 100)
		s.Key = StreamKey(s.Key)
		if s.Key == "" {
			s.Key = StreamKey(s.Name)
		}
		if s.Key == "" || s.Name == "" || seen[s.Key] {
			continue
		}
		seen[s.Key] = true
		s.Description = truncate(strings.TrimSpace(s.Description), 300)
		s.AlertGroupID = strings.TrimSpace(s.AlertGroupID)
		out = append(out, s)
		if len(out) == maxStreams {
			break
		}
	}
	return out
}

// matchStream returns the key of the stream the model named, by key or by
// name; "" when it names none.
func matchStream(streams []Stream, answer string) string {
	key := StreamKey(answer)
	if key == "" {
		return ""
	}
	for _, s := range streams {
		if s.Key == key || StreamKey(s.Name) == key {
			return s.Key
		}
	}
	return ""
}

// streamAlert returns the alert group and display name for a case's
// stream. A stream without its own group, or one since removed, returns no
// group, so the alert goes to the handover alert group.
func streamAlert(streams []Stream, key, storedName string) (groupID, name string) {
	if key == "" {
		return "", storedName
	}
	for _, s := range streams {
		if s.Key == key {
			return s.AlertGroupID, s.Name
		}
	}
	return "", storedName
}
