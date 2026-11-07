package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/db/models"
)

// The status must count only conversations with stored messages whose
// coverage does not match them, and must not depend on the job having run after
// the latest 15-minute message sync. Labels are reported but do not block a
// Page: this one has no label sync at all.
func TestMySQLJourneyStatusCountsOnlyUncoveredMessages(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(
		&models.Conversation{}, &models.Message{}, &models.Job{}, &models.MessengerLabelState{}, &models.MessengerLabelSnapshot{},
		&models.MessengerJourneySequence{}, &models.MessengerJourneyEvent{}, &models.MessengerJourneyCoverage{},
		&models.MessengerJourneyCustomer{}, &models.MessengerJourneyOrder{},
	); err != nil {
		t.Fatal(err)
	}
	tenantID := "tenant-status"
	createFacebookOAuthTenant(t, database, tenantID)
	now := time.Now().UTC().Truncate(time.Millisecond)
	syncedAt := now.Add(-time.Minute)
	jobRunAt := now.Add(-10 * time.Minute) // before the latest sync
	channel := models.Channel{ID: "channel-status", TenantID: tenantID, ChannelType: "facebook", Name: "Page", ExternalID: "100", IsActive: true,
		CredentialsEncrypted: []byte("x"), Metadata: "{}", LastSyncAt: &syncedAt, LastSyncStatus: "success", CreatedAt: now, UpdatedAt: now}
	job := models.Job{ID: "job-status", TenantID: tenantID, Name: "Insights", JobType: "classification", InputChannelIDs: `["channel-status"]`,
		RulesConfig: `{"profile":"messenger_insights"}`, Outputs: "[]", IsActive: true, LastRunAt: &jobRunAt, LastRunStatus: "success", CreatedAt: now, UpdatedAt: now}
	for _, row := range []interface{}{&channel, &job} {
		if err := database.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	addConversation := func(id string, messages int) {
		if err := database.Create(&models.Conversation{ID: id, TenantID: tenantID, ChannelID: channel.ID, ExternalConversationID: "t_" + id, Metadata: "{}", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		for i := 0; i < messages; i++ {
			if err := database.Create(&models.Message{ID: id + "-m" + string(rune('a'+i)), TenantID: tenantID, ConversationID: id, ExternalMessageID: id + string(rune('a'+i)),
				SenderType: "customer", SentAt: now.Add(time.Duration(i) * time.Second), Attachments: "[]", RawData: "{}", CreatedAt: now}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	addConversation("covered", 1)
	addConversation("uncovered", 1)
	addConversation("empty", 0)
	payload, _ := json.Marshal(map[string]interface{}{"status": "complete", "source_last_message_at": now, "source_last_message_id": "covered-ma", "source_message_count": 1})
	if err := database.Create(&models.MessengerJourneyCoverage{SubjectKey: "subject-covered", TenantID: tenantID, PageID: "100", PSID: "201", ConversationID: "covered",
		Status: "complete", Payload: string(payload), ObservedAt: now, LastSequence: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}

	read := func() (map[string]interface{}, bool) {
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set("tenant_id", tenantID) })
		router.GET("/status", ListMessengerJourneyStatus)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/status?page_ids=100", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
		}
		var body struct {
			Pages []struct {
				Analysis map[string]interface{} `json:"analysis"`
				Complete bool                   `json:"complete"`
			} `json:"pages"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || len(body.Pages) != 1 {
			t.Fatalf("bad body %s: %v", recorder.Body.String(), err)
		}
		return body.Pages[0].Analysis, body.Pages[0].Complete
	}
	analysis, pageComplete := read()
	if analysis["pending"] != float64(1) || analysis["complete"] != false || pageComplete {
		t.Fatalf("want 1 pending conversation (empty one excluded), got %v", analysis)
	}
	if err := database.Where("conversation_id = ?", "uncovered").Delete(&models.Message{}).Error; err != nil {
		t.Fatal(err)
	}
	analysis, pageComplete = read()
	if analysis["pending"] != float64(0) || analysis["complete"] != true {
		t.Fatalf("job run before latest sync must not block complete coverage, got %v", analysis)
	}
	if !pageComplete {
		t.Fatal("a Page without label sync must be complete once messages and analysis are complete")
	}
}
