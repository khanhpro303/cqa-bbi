package pancake

import (
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
)

var digits = regexp.MustCompile(`^[0-9]{1,64}$`)

// ConversationUpdate is the full current tag set of one inbox customer.
type ConversationUpdate struct {
	PageID                string
	PSID                  string
	PancakeConversationID string
	Tags                  []Tag
	// Timestamp is Pancake's microsecond clock for the change (0 = unknown).
	Timestamp int64
}

type rawConversation struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	PageID    string    `json:"page_id"`
	UpdatedAt string    `json:"updated_at"`
	Tags      *[]rawTag `json:"tags"`
	From      *struct {
		ID string `json:"id"`
	} `json:"from"`
	Customers []struct {
		FBID string `json:"fb_id"`
	} `json:"customers"`
	PageCustomer *struct {
		PSID string `json:"psid"`
	} `json:"page_customer"`
}

// update converts a conversation with a tags field into an update. It skips
// comments, livestreams and conversations whose customer PSID is unclear.
func (c rawConversation) update(pageID string, timestamp int64) (ConversationUpdate, bool) {
	if c.Tags == nil || !strings.EqualFold(strings.TrimSpace(c.Type), "INBOX") {
		return ConversationUpdate{}, false
	}
	if c.PageID != "" && pageID != "" && c.PageID != pageID {
		return ConversationUpdate{}, false
	}
	if pageID == "" {
		pageID = c.PageID
	}
	psid := inboxPSID(pageID, c)
	if psid == "" {
		return ConversationUpdate{}, false
	}
	tags := make([]Tag, 0, len(*c.Tags))
	seen := map[string]bool{}
	for _, raw := range *c.Tags {
		id := raw.id()
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		tags = append(tags, Tag{ID: id, Text: strings.TrimSpace(raw.Text), Color: raw.Color})
	}
	sortTags(tags)
	return ConversationUpdate{PageID: pageID, PSID: psid, PancakeConversationID: c.ID, Tags: tags, Timestamp: timestamp}, true
}

func inboxPSID(pageID string, c rawConversation) string {
	if pageID != "" && strings.HasPrefix(c.ID, pageID+"_") {
		if psid := strings.TrimPrefix(c.ID, pageID+"_"); digits.MatchString(psid) && psid != pageID {
			return psid
		}
	}
	if c.PageCustomer != nil && digits.MatchString(c.PageCustomer.PSID) && c.PageCustomer.PSID != pageID {
		return c.PageCustomer.PSID
	}
	ids := map[string]bool{}
	for _, customer := range c.Customers {
		if digits.MatchString(customer.FBID) && customer.FBID != pageID {
			ids[customer.FBID] = true
		}
	}
	if len(ids) == 1 {
		for id := range ids {
			return id
		}
	}
	if len(ids) == 0 && c.From != nil && digits.MatchString(c.From.ID) && c.From.ID != pageID {
		return c.From.ID
	}
	if len(ids) == 0 && digits.MatchString(c.ID) && c.ID != pageID {
		return c.ID
	}
	return ""
}

func sortTags(tags []Tag) {
	sort.Slice(tags, func(i, j int) bool {
		if len(tags[i].ID) != len(tags[j].ID) {
			return len(tags[i].ID) < len(tags[j].ID)
		}
		return tags[i].ID < tags[j].ID
	})
}

// Frame is one Phoenix Channels v2 message: [join_ref, ref, topic, event, payload].
type Frame struct {
	JoinRef *string
	Ref     *string
	Topic   string
	Event   string
	Payload json.RawMessage
}

var ErrBadFrame = errors.New("invalid phoenix frame")

func DecodeFrame(data []byte) (Frame, error) {
	var parts []json.RawMessage
	if json.Unmarshal(data, &parts) != nil || len(parts) != 5 {
		return Frame{}, ErrBadFrame
	}
	var f Frame
	if json.Unmarshal(parts[0], &f.JoinRef) != nil || json.Unmarshal(parts[1], &f.Ref) != nil ||
		json.Unmarshal(parts[2], &f.Topic) != nil || json.Unmarshal(parts[3], &f.Event) != nil {
		return Frame{}, ErrBadFrame
	}
	f.Payload = parts[4]
	return f, nil
}

func EncodeFrame(joinRef, ref *string, topic, event string, payload any) ([]byte, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	return json.Marshal([]any{joinRef, ref, topic, event, payload})
}

// PageIDFromTopic returns the Page ID of a "pages:<id>" topic.
func PageIDFromTopic(topic string) string {
	if !strings.HasPrefix(topic, "pages:") {
		return ""
	}
	id := strings.TrimPrefix(topic, "pages:")
	if !digits.MatchString(id) {
		return ""
	}
	return id
}

// ExtractUpdates reads every inbox tag set from a pages:<id> event payload.
// pages:tag_conversations carries {timestamp, conversations{id:[tag ids]},
// conversations_updated[...]}; other events may carry a "conversation" object
// with a "tags" field. Payloads without tag data produce no update.
func ExtractUpdates(pageID string, payload json.RawMessage) []ConversationUpdate {
	var body struct {
		Timestamp            json.Number                `json:"timestamp"`
		Conversation         *rawConversation           `json:"conversation"`
		ConversationsUpdated []rawConversation          `json:"conversations_updated"`
		Conversations        map[string]json.RawMessage `json:"conversations"`
	}
	if json.Unmarshal(payload, &body) != nil {
		return nil
	}
	timestamp, _ := body.Timestamp.Int64()
	byConv := map[string]ConversationUpdate{}
	order := []string{}
	add := func(update ConversationUpdate) {
		key := update.PSID
		if _, ok := byConv[key]; !ok {
			order = append(order, key)
		}
		byConv[key] = update
	}
	// The id → tag ids map is authoritative for the change; conversation
	// objects add type and customer details.
	mapped := map[string][]Tag{}
	for convID, raw := range body.Conversations {
		var ids []rawTag
		if json.Unmarshal(raw, &ids) != nil {
			continue
		}
		tags := make([]Tag, 0, len(ids))
		seen := map[string]bool{}
		for _, item := range ids {
			if id := item.id(); id != "" && !seen[id] {
				seen[id] = true
				tags = append(tags, Tag{ID: id})
			}
		}
		sortTags(tags)
		mapped[convID] = tags
	}
	conversations := append([]rawConversation(nil), body.ConversationsUpdated...)
	if body.Conversation != nil {
		conversations = append(conversations, *body.Conversation)
	}
	handled := map[string]bool{}
	for _, conv := range conversations {
		if tags, ok := mapped[conv.ID]; ok && conv.Tags == nil {
			raw := make([]rawTag, 0, len(tags))
			for _, tag := range tags {
				raw = append(raw, rawTag{bare: tag.ID})
			}
			conv.Tags = &raw
		}
		if conv.Type == "" {
			continue
		}
		handled[conv.ID] = true
		if update, ok := conv.update(pageID, timestamp); ok {
			if tags, ok := mapped[conv.ID]; ok {
				update.Tags = mergeTagText(tags, update.Tags)
			}
			add(update)
		}
	}
	// Map entries without a conversation object: only inbox ids
	// "<page_id>_<psid>" are safe to read; comment ids use a post prefix.
	for convID, tags := range mapped {
		if handled[convID] || pageID == "" || !strings.HasPrefix(convID, pageID+"_") {
			continue
		}
		psid := strings.TrimPrefix(convID, pageID+"_")
		if !digits.MatchString(psid) || psid == pageID {
			continue
		}
		add(ConversationUpdate{PageID: pageID, PSID: psid, PancakeConversationID: convID, Tags: tags, Timestamp: timestamp})
	}
	result := make([]ConversationUpdate, 0, len(order))
	for _, key := range order {
		result = append(result, byConv[key])
	}
	return result
}

func mergeTagText(ids []Tag, detailed []Tag) []Tag {
	text := map[string]Tag{}
	for _, tag := range detailed {
		text[tag.ID] = tag
	}
	out := make([]Tag, 0, len(ids))
	for _, tag := range ids {
		if d, ok := text[tag.ID]; ok {
			tag.Text, tag.Color = d.Text, d.Color
		}
		out = append(out, tag)
	}
	return out
}
