package engine

import (
	"context"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/messengerjourney"
	"github.com/vietbui/chat-quality-agent/pkg"
)

func TestMySQLReconcileInsightCoverageReusesProvenInsights(t *testing.T) {
	database := journeyEngineTestDatabase(t)
	if err := database.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	tenant := models.Tenant{ID: pkg.NewUUID(), Name: "Test", Slug: pkg.NewUUID(), Settings: "{}"}
	channel := models.Channel{ID: pkg.NewUUID(), TenantID: tenant.ID, ChannelType: "facebook", ExternalID: "100", Name: "Page", IsActive: true, CredentialsEncrypted: []byte{}, Metadata: "{}"}
	job := models.Job{ID: pkg.NewUUID(), TenantID: tenant.ID, Name: "Insights", JobType: "classification", InputChannelIDs: `["` + channel.ID + `"]`, RulesConfig: "[]", Outputs: "[]", CreatedAt: base, UpdatedAt: base}
	run := models.JobRun{ID: pkg.NewUUID(), JobID: job.ID, TenantID: tenant.ID, StartedAt: base, Summary: "{}", CreatedAt: base}
	for _, row := range []interface{}{&tenant, &channel, &job, &run} {
		if err := database.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	// conversation creates one Messenger conversation with messages stored at
	// storedAt and an insight saved at insightAt that read through readThrough.
	conversation := func(psid string, sent []time.Time, storedAt, insightAt time.Time, readThrough *time.Time) string {
		conv := models.Conversation{ID: pkg.NewUUID(), TenantID: tenant.ID, ChannelID: channel.ID, ExternalConversationID: "t_" + psid,
			Metadata: `{"participants":{"data":[{"id":"100"},{"id":"` + psid + `"}]}}`, CreatedAt: base, UpdatedAt: base}
		if len(sent) > 0 {
			last := sent[len(sent)-1]
			conv.LastMessageAt = &last
		}
		if err := database.Create(&conv).Error; err != nil {
			t.Fatal(err)
		}
		for i, at := range sent {
			msg := models.Message{ID: pkg.NewUUID(), TenantID: tenant.ID, ConversationID: conv.ID, ExternalMessageID: pkg.NewUUID(), SenderType: "customer", Content: "x", SentAt: at, Attachments: "[]", RawData: "{}", CreatedAt: storedAt.Add(time.Duration(i) * time.Millisecond)}
			if err := database.Create(&msg).Error; err != nil {
				t.Fatal(err)
			}
		}
		detail := `{}`
		if readThrough != nil {
			detail = `{"source_last_message_at":"` + readThrough.UTC().Format(time.RFC3339Nano) + `"}`
		}
		result := models.JobResult{ID: pkg.NewUUID(), JobRunID: run.ID, TenantID: tenant.ID, ConversationID: conv.ID, ResultType: "conversation_insight", Detail: detail, CreatedAt: insightAt}
		if err := database.Create(&result).Error; err != nil {
			t.Fatal(err)
		}
		return conv.ID
	}
	sent := []time.Time{base, base.Add(time.Minute)}
	last := sent[1]
	proven := conversation("201", sent, base.Add(2*time.Minute), base.Add(10*time.Minute), &last)
	lateMessage := conversation("202", sent, base.Add(20*time.Minute), base.Add(10*time.Minute), &last)
	noCursor := conversation("203", sent, base.Add(2*time.Minute), base.Add(10*time.Minute), nil)

	now := base.Add(time.Hour)
	result, err := ReconcileInsightCoverage(ctx, database, tenant.ID, []string{channel.ID}, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Written != 1 {
		t.Fatalf("written = %d, want 1", result.Written)
	}
	needs := map[string]bool{}
	for _, id := range result.NeedsAnalysis {
		needs[id] = true
	}
	if len(needs) != 2 || !needs[lateMessage] || !needs[noCursor] || needs[proven] {
		t.Fatalf("needs analysis = %v", result.NeedsAnalysis)
	}
	cursors, err := messengerjourney.LoadMessageCursors(ctx, database, tenant.ID, []string{proven})
	if err != nil {
		t.Fatal(err)
	}
	var row models.MessengerJourneyCoverage
	if err := database.First(&row, "tenant_id = ? AND conversation_id = ?", tenant.ID, proven).Error; err != nil {
		t.Fatal(err)
	}
	if !messengerjourney.CoverageComplete(row, true, cursors[proven]) || row.PageID != "100" || row.PSID != "201" {
		t.Fatalf("reconciled coverage does not match current messages: %+v", row)
	}

	// A second pass is idempotent: no new journal event, nothing more to write.
	var before, after int64
	database.Model(&models.MessengerJourneyEvent{}).Where("tenant_id = ?", tenant.ID).Count(&before)
	again, err := ReconcileInsightCoverage(ctx, database, tenant.ID, []string{channel.ID}, 0, now.Add(time.Minute))
	if err != nil || again.Written != 0 {
		t.Fatalf("second pass wrote %d, err %v", again.Written, err)
	}
	database.Model(&models.MessengerJourneyEvent{}).Where("tenant_id = ?", tenant.ID).Count(&after)
	if before != after {
		t.Fatalf("second pass added journal events: %d -> %d", before, after)
	}

	// A new message makes the reconciled coverage stale until the AI runs again.
	msg := models.Message{ID: pkg.NewUUID(), TenantID: tenant.ID, ConversationID: proven, ExternalMessageID: pkg.NewUUID(), SenderType: "customer", Content: "x", SentAt: base.Add(30 * time.Minute), Attachments: "[]", RawData: "{}", CreatedAt: now}
	if err := database.Create(&msg).Error; err != nil {
		t.Fatal(err)
	}
	third, err := ReconcileInsightCoverage(ctx, database, tenant.ID, []string{channel.ID}, 0, now.Add(2*time.Minute))
	if err != nil || third.Written != 0 {
		t.Fatalf("stale insight was reused: written %d err %v", third.Written, err)
	}
	found := false
	for _, id := range third.NeedsAnalysis {
		found = found || id == proven
	}
	if !found {
		t.Fatalf("conversation with a new message was not sent to analysis: %v", third.NeedsAnalysis)
	}
}
