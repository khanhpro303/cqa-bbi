package chatmemory

import "encoding/json"

// ChannelSettings are the chat-memory switches stored in a Zalo OA channel's
// metadata and edited on the channel page.
type ChannelSettings struct {
	// ProfileInPrompt gives the chatbot the customer's profile card and last
	// session summary. Off unless the channel turns it on.
	ProfileInPrompt bool
	// IssueAlerts sends high-severity issues found in session summaries to
	// the channel's alert group. On unless the channel turns it off.
	IssueAlerts bool
}

// Channel metadata keys.
const (
	MetaProfileInPrompt = "chat_memory_profile_in_prompt"
	MetaIssueAlerts     = "chat_memory_issue_alerts"
)

// ChannelSettingsFrom reads the switches from channel metadata JSON.
func ChannelSettingsFrom(metadata string) ChannelSettings {
	out := ChannelSettings{IssueAlerts: true}
	var m map[string]interface{}
	if json.Unmarshal([]byte(metadata), &m) != nil {
		return out
	}
	if v, ok := m[MetaProfileInPrompt].(bool); ok {
		out.ProfileInPrompt = v
	}
	if v, ok := m[MetaIssueAlerts].(bool); ok {
		out.IssueAlerts = v
	}
	return out
}
