package customercases

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

// RenderForBot lists cases for the bot's system prompt. It shows no ids, and
// ticket numbers written into the text are stripped (StripRefs).
func RenderForBot(cases []models.CustomerCase, loc *time.Location) string {
	if len(cases) == 0 {
		return "(không có)"
	}
	var b strings.Builder
	for i, c := range cases {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "- %s: %s (báo lúc %s; %s", TypeLabel(c.Type), oneLine(StripRefs(c.Title)), c.CreatedAt.In(loc).Format("15:04 02/01"), StatusLabel(c.Status))
		if c.FollowupCount > 0 {
			fmt.Fprintf(&b, "; khách đã hỏi lại %d lần", c.FollowupCount)
		}
		b.WriteString(")")
		if u := StripRefs(strings.TrimSpace(c.CustomerUpdate)); u != "" {
			fmt.Fprintf(&b, "\n  Nhân viên cho phép báo khách: %s", oneLine(truncate(u, 400)))
		}
	}
	return b.String()
}

// BotContext returns the complaint guide (task prompt complaint_bot_guide)
// with the chat's open cases, for the bot's system prompt.
func BotContext(ctx context.Context, tenantID, subjectType, subjectID, customerCode string, build func(cases string) string) string {
	cases := OpenCases(ctx, tenantID, subjectType, subjectID, customerCode)
	return build(RenderForBot(cases, location(tenantID)))
}

// BotView is what the complaint_record tool tells the bot about a case.
type BotView struct {
	Title          string `json:"title"`
	Type           string `json:"type"`
	Status         string `json:"status"`
	ReportedAt     string `json:"reported_at"`
	FollowupCount  int    `json:"followup_count"`
	CustomerUpdate string `json:"customer_update,omitempty"`
	Images         int    `json:"images_attached"`
}

// ViewForBot renders c for the bot. No id: the bot never needs one, and the
// customer must never be given one.
func ViewForBot(c models.CustomerCase) BotView {
	return BotView{
		Title: StripRefs(c.Title), Type: TypeLabel(c.Type), Status: StatusLabel(c.Status),
		ReportedAt:    c.CreatedAt.In(location(c.TenantID)).Format("15:04 02/01/2006"),
		FollowupCount: c.FollowupCount, CustomerUpdate: StripRefs(strings.TrimSpace(c.CustomerUpdate)),
		Images: len(parseImages(c.Images)),
	}
}

// imageHostSuffixes are the hosts Zalo serves customer images from. Only
// images on these hosts are downloaded, stored on messages and cases, and
// shown to staff: the URL comes from the webhook.
var imageHostSuffixes = []string{"zdn.vn", "zadn.vn", "zalo.me", "zaloapp.com", "zalo.vn"}

// AllowedImageURL reports whether u is an https URL on a Zalo image host.
func AllowedImageURL(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil {
		return false
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	for _, suffix := range imageHostSuffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

// ImageAttachment is how an inbound image is stored in messages.attachments.
type ImageAttachment struct {
	Type        string `json:"type"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// RecentImages returns the images the customer sent in a chat since since,
// oldest first, with what the AI read in each.
// In a group chat only the reporter's own images are taken.
func RecentImages(ctx context.Context, tenantID, channelID, conversationExtID, senderID string, since time.Time) []models.CaseImage {
	if db.DB == nil || conversationExtID == "" {
		return nil
	}
	var conv models.Conversation
	if err := db.DB.WithContext(ctx).Select("id").
		Where("tenant_id = ? AND channel_id = ? AND external_conversation_id = ?", tenantID, channelID, conversationExtID).
		First(&conv).Error; err != nil {
		return nil
	}
	return conversationImages(ctx, tenantID, conv.ID, senderID, since, time.Now().Add(time.Minute))
}

// ConversationImages returns the customer images of a conversation sent
// between from and to, oldest first.
func ConversationImages(ctx context.Context, tenantID, conversationID string, from, to time.Time) []models.CaseImage {
	return conversationImages(ctx, tenantID, conversationID, "", from, to)
}

func conversationImages(ctx context.Context, tenantID, conversationID, senderID string, from, to time.Time) []models.CaseImage {
	if db.DB == nil || conversationID == "" {
		return nil
	}
	q := db.DB.WithContext(ctx).Select("id, attachments, sent_at").
		Where("tenant_id = ? AND conversation_id = ? AND sender_type = 'customer' AND content_type = 'image' AND sent_at BETWEEN ? AND ?",
			tenantID, conversationID, from, to)
	if senderID != "" {
		q = q.Where("sender_external_id = ?", senderID)
	}
	var msgs []models.Message
	q.Order("sent_at ASC").Limit(maxImagesPerCase).Find(&msgs)
	var out []models.CaseImage
	for _, m := range msgs {
		var atts []ImageAttachment
		if json.Unmarshal([]byte(m.Attachments), &atts) != nil {
			continue
		}
		for _, a := range atts {
			if a.Type == "image" && AllowedImageURL(a.URL) {
				out = append(out, models.CaseImage{URL: a.URL, Description: a.Description, At: m.SentAt})
			}
		}
	}
	return out
}

// LastBotReportAt is when the bot last recorded a report for this chat (zero
// when never), so images already attached are not attached again elsewhere.
func LastBotReportAt(ctx context.Context, tenantID, subjectType, subjectID string) time.Time {
	if db.DB == nil {
		return time.Time{}
	}
	var row struct{ At *time.Time }
	db.DB.WithContext(ctx).Table("customer_case_events AS e").Select("MAX(e.created_at) AS at").
		Joins("JOIN customer_cases AS c ON c.id = e.case_id").
		Where("c.tenant_id = ? AND c.subject_type = ? AND c.subject_id = ? AND e.actor = ?", tenantID, subjectType, subjectID, SourceBot).
		Scan(&row)
	if row.At == nil {
		return time.Time{}
	}
	return *row.At
}
