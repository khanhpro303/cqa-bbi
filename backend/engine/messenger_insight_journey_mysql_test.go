package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/messengerjourney"
	"github.com/vietbui/chat-quality-agent/pkg"
)

type insightEventBody struct {
	Schema               int      `json:"schema"`
	LeadLevel            string   `json:"lead_level"`
	Need                 string   `json:"need"`
	NeedSource           string   `json:"need_source"`
	Products             []string `json:"products"`
	NegativeFeedback     []string `json:"negative_feedback"`
	Messages             int      `json:"messages"`
	CustomerMessages     int      `json:"customer_messages"`
	FirstReplyStatus     string   `json:"first_reply_status"`
	FirstResponseSeconds *int64   `json:"first_response_seconds"`
	RepliedWithinTarget  *bool    `json:"replied_within_target"`
}

func TestMySQLInsightEventsBackfillAndNewAnalysis(t *testing.T) {
	database := journeyEngineTestDatabase(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC) // 10:00 in Vietnam, inside work hours
	tenant := models.Tenant{ID: pkg.NewUUID(), Name: "Test", Slug: pkg.NewUUID(), Settings: "{}"}
	channel := models.Channel{ID: pkg.NewUUID(), TenantID: tenant.ID, ChannelType: "facebook", ExternalID: "100", Name: "Page", IsActive: true, CredentialsEncrypted: []byte{}, Metadata: "{}"}
	job := models.Job{ID: pkg.NewUUID(), TenantID: tenant.ID, Name: "Insights", JobType: "classification", InputChannelIDs: `["` + channel.ID + `"]`, RulesConfig: "[]", Outputs: "[]", CreatedAt: base, UpdatedAt: base}
	run := models.JobRun{ID: pkg.NewUUID(), JobID: job.ID, TenantID: tenant.ID, StartedAt: base, Summary: "{}", CreatedAt: base}
	for _, row := range []interface{}{&tenant, &channel, &job, &run} {
		if err := database.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	type line struct {
		sender string
		at     time.Duration
	}
	conversation := func(psid string, lines []line) string {
		conv := models.Conversation{ID: pkg.NewUUID(), TenantID: tenant.ID, ChannelID: channel.ID, ExternalConversationID: "t_" + psid,
			Metadata: `{"participants":{"data":[{"id":"100"},{"id":"` + psid + `"}]}}`, CreatedAt: base, UpdatedAt: base}
		last := base.Add(lines[len(lines)-1].at)
		conv.LastMessageAt = &last
		if err := database.Create(&conv).Error; err != nil {
			t.Fatal(err)
		}
		for _, l := range lines {
			msg := models.Message{ID: pkg.NewUUID(), TenantID: tenant.ID, ConversationID: conv.ID, ExternalMessageID: pkg.NewUUID(), SenderType: l.sender, Content: "secret customer text", SentAt: base.Add(l.at), Attachments: "[]", RawData: "{}", CreatedAt: base.Add(l.at)}
			if err := database.Create(&msg).Error; err != nil {
				t.Fatal(err)
			}
		}
		return conv.ID
	}
	insight := func(conversationID, detail string, at time.Time) models.JobResult {
		result := models.JobResult{ID: pkg.NewUUID(), JobRunID: run.ID, TenantID: tenant.ID, ConversationID: conversationID, ResultType: "conversation_insight", Detail: detail, CreatedAt: at}
		if err := database.Create(&result).Error; err != nil {
			t.Fatal(err)
		}
		return result
	}
	readThrough := base.Add(4 * time.Minute).Format(time.RFC3339Nano)
	// Old insight (no need field): replied after 3 minutes, a later message is ignored.
	fast := conversation("201", []line{{"customer", 0}, {"customer", time.Minute}, {"agent", 3 * time.Minute}, {"customer", 4 * time.Minute}, {"agent", 30 * time.Minute}})
	insight(fast, `{"summary":"khách nói secret customer text","intents":["Hỏi giá mũ E-24"],"products":[{"name":"E-24","evidence":"secret"},{"name":"e-24","evidence":"x"},{"name":"FF818","evidence":"y"}],
		"feedback":[{"category":"giao hàng","sentiment":"negative","evidence":"chậm quá"},{"category":"giá","sentiment":"positive","evidence":"rẻ"}],
		"lead_quality":{"level":"HIGH","evidence":"lấy 2 cái","reason":"muốn mua"},"source_last_message_at":"`+readThrough+`"}`, base.Add(5*time.Minute))
	// Unanswered customer and an unreadable old detail.
	waiting := conversation("202", []line{{"customer", 0}})
	insight(waiting, `{"intents":[],"lead_quality":{"level":"unknown"}}`, base.Add(time.Minute))
	broken := conversation("203", []line{{"customer", 0}, {"agent", 20 * time.Minute}})
	insight(broken, `{"lead_quality":"not an object"}`, base.Add(time.Hour))

	now := base.Add(2 * time.Hour)
	written, err := BackfillInsightEvents(ctx, database, tenant.ID, []string{channel.ID}, 0, now)
	if err != nil || written != 2 {
		t.Fatalf("backfill wrote %d err %v", written, err)
	}
	var events []models.MessengerJourneyEvent
	if err := database.Where("tenant_id = ? AND kind = ?", tenant.ID, messengerjourney.KindInsight).Order("sequence").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("insight events = %d", len(events))
	}
	bodies := map[string]insightEventBody{}
	for _, event := range events {
		if strings.Contains(event.Payload, "secret") || strings.Contains(event.Payload, "evidence") || strings.Contains(event.Payload, "muốn mua") || strings.Contains(event.Payload, "chậm") {
			t.Fatalf("insight event leaked text: %s", event.Payload)
		}
		if event.Source != insightEventSourceBackfill || event.PageID != "100" || event.EntityID == "" || event.OccurredAt.After(event.ObservedAt) {
			t.Fatalf("bad event envelope: %+v", event)
		}
		var body insightEventBody
		if err := json.Unmarshal([]byte(event.Payload), &body); err != nil {
			t.Fatal(err)
		}
		bodies[event.PSID] = body
	}
	got := bodies["201"]
	if got.Schema != 1 || got.LeadLevel != "high" || got.Need != "price" || got.NeedSource != "keyword" || strings.Join(got.Products, ",") != "E-24,FF818" ||
		strings.Join(got.NegativeFeedback, ",") != "delivery" || got.Messages != 4 || got.CustomerMessages != 3 || got.FirstReplyStatus != "answered" ||
		got.FirstResponseSeconds == nil || *got.FirstResponseSeconds != 180 || got.RepliedWithinTarget == nil || !*got.RepliedWithinTarget {
		t.Fatalf("fast payload = %+v", got)
	}
	got = bodies["202"]
	if got.Need != "unknown" || got.LeadLevel != "unknown" || got.FirstReplyStatus != "waiting" || got.FirstResponseSeconds != nil || got.RepliedWithinTarget != nil || len(got.Products) != 0 {
		t.Fatalf("waiting payload = %+v", got)
	}

	// A second pass writes nothing: receipts prove each insight is exported.
	again, err := BackfillInsightEvents(ctx, database, tenant.ID, []string{channel.ID}, 0, now.Add(time.Minute))
	if err != nil || again != 0 {
		t.Fatalf("second pass wrote %d err %v", again, err)
	}

	// A new analysis writes its insight event in the same transaction, with the
	// AI need code and a slow first reply (20 minutes > 5 minute target).
	slow := conversation("204", []line{{"customer", 0}, {"agent", 20 * time.Minute}})
	last := base.Add(20 * time.Minute)
	result := models.JobResult{ID: pkg.NewUUID(), JobRunID: run.ID, TenantID: tenant.ID, ConversationID: slow, ResultType: "conversation_insight",
		Detail: `{"intents":["Hỏi giá"],"need":"advice","lead_quality":{"level":"medium","evidence":"x"},"source_last_message_at":"` + last.Format(time.RFC3339Nano) + `"}`, CreatedAt: base.Add(time.Hour)}
	if err := saveConversationInsightWithJourney(database, result, nil, analysisSnapshotMetadata{SourceLastMessageAt: &last, SourceLastMessageID: "m", SourceMessageCount: 2}, true); err != nil {
		t.Fatal(err)
	}
	var fresh models.MessengerJourneyEvent
	if err := database.Where("tenant_id = ? AND kind = ? AND psid = ?", tenant.ID, messengerjourney.KindInsight, "204").First(&fresh).Error; err != nil {
		t.Fatal(err)
	}
	var body insightEventBody
	_ = json.Unmarshal([]byte(fresh.Payload), &body)
	if fresh.Source != insightEventSourceAnalysis || body.Need != "advice" || body.NeedSource != "ai" || body.LeadLevel != "medium" ||
		body.FirstResponseSeconds == nil || *body.FirstResponseSeconds != 1200 || body.RepliedWithinTarget == nil || *body.RepliedWithinTarget {
		t.Fatalf("new analysis event = %s / %+v", fresh.Payload, body)
	}
	if n, err := BackfillInsightEvents(ctx, database, tenant.ID, []string{channel.ID}, 0, now.Add(2*time.Minute)); err != nil || n != 0 {
		t.Fatalf("backfill re-exported a new analysis: %d %v", n, err)
	}

	// Erasure blanks insight payloads like every other event of the subject.
	if _, err := messengerjourney.EraseSubject(ctx, database, messengerjourney.EraseInput{EventID: "erase-201", TenantID: tenant.ID, PageID: "100", PSID: "201", Source: "test", ObservedAt: now}); err != nil {
		t.Fatal(err)
	}
	var erased models.MessengerJourneyEvent
	if err := database.Where("tenant_id = ? AND kind = ? AND subject_id = ?", tenant.ID, messengerjourney.KindInsight, messengerjourney.SubjectKey(tenant.ID, "100", "201")).First(&erased).Error; err != nil {
		t.Fatal(err)
	}
	if erased.PSID != "" || erased.Payload != "{}" {
		t.Fatalf("erasure kept insight data: %+v", erased)
	}
}
