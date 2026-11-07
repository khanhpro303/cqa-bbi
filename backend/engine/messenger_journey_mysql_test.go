package engine

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func journeyEngineTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("MESSENGER_LABELS_TEST_DSN")
	if dsn == "" {
		t.Skip("MySQL integration requires MESSENGER_LABELS_TEST_DSN")
	}
	cfg, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	cfg.DBName, cfg.ParseTime, cfg.Timeout = "", true, 5*time.Second
	admin, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("cannot connect to test MySQL")
	}
	name := "cqa_engine_journey_" + strings.ReplaceAll(pkg.NewUUID(), "-", "")
	if err := admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Exec("DROP DATABASE `" + name + "`").Error })
	cfg.DBName = name
	database, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(
		&models.Tenant{}, &models.Channel{}, &models.Conversation{}, &models.Message{}, &models.AppSetting{}, &models.MessengerLabelSnapshot{}, &models.MessengerIntakeAttribution{}, &models.Job{}, &models.JobRun{}, &models.JobResult{},
		&models.MessengerJourneySequence{}, &models.MessengerJourneyReceipt{}, &models.MessengerJourneyEvent{}, &models.MessengerJourneyCustomer{}, &models.MessengerJourneyOrder{}, &models.MessengerJourneyCoverage{}, &models.MessengerJourneyErasure{},
	); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestMySQLDurableOrderSurvivesDeletedJobResultAndDeduplicatesRerun(t *testing.T) {
	database := journeyEngineTestDatabase(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	tenant := models.Tenant{ID: pkg.NewUUID(), Name: "Test", Slug: pkg.NewUUID(), Settings: "{}"}
	channel := models.Channel{ID: pkg.NewUUID(), TenantID: tenant.ID, ChannelType: "facebook", ExternalID: "100", Name: "Page", IsActive: true, CredentialsEncrypted: []byte{}, Metadata: "{}"}
	conversation := models.Conversation{ID: pkg.NewUUID(), TenantID: tenant.ID, ChannelID: channel.ID, ExternalConversationID: "thread", Metadata: `{"participants":{"data":[{"id":"100"},{"id":"200"}]}}`}
	snapshot := models.MessengerLabelSnapshot{ConversationID: conversation.ID, TenantID: tenant.ID, ChannelID: channel.ID, PSID: "200", Labels: "[]", Status: "success", CheckedAt: &now, AttemptedAt: now}
	job := models.Job{ID: "job", TenantID: tenant.ID, Name: "Insights", JobType: "classification", InputChannelIDs: "[]", RulesConfig: "[]", Outputs: "[]", CreatedAt: now, UpdatedAt: now}
	run := models.JobRun{ID: "run", JobID: job.ID, TenantID: tenant.ID, StartedAt: now, Summary: "{}", CreatedAt: now}
	for _, row := range []interface{}{&tenant, &channel, &conversation, &snapshot, &job, &run} {
		if err := database.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	value := int64(450000)
	outcome := persistedOrderOutcome{OrderID: "order-1", OrderRef: "DH-1", ValueVND: &value, Status: "confirmed", OrderedAt: now.Add(-time.Minute).Format(time.RFC3339Nano), EvidenceMessageIDs: []string{"message-1"}, Confidence: .9}
	save := func(id string, value persistedOrderOutcome) {
		detail, _ := json.Marshal(map[string]interface{}{"order_outcomes": []persistedOrderOutcome{value}})
		result := models.JobResult{ID: id, JobRunID: "run", TenantID: tenant.ID, ConversationID: conversation.ID, ResultType: "conversation_insight", Detail: string(detail), CreatedAt: now}
		if err := saveConversationInsightWithJourney(database, result, []persistedOrderOutcome{value}, analysisSnapshotMetadata{SourceLastMessageAt: &now, SourceLastMessageID: "message-1", SourceMessageCount: 1}, true); err != nil {
			t.Fatal(err)
		}
	}
	save("result-1", outcome)
	if err := database.Where("id = ?", "result-1").Delete(&models.JobResult{}).Error; err != nil {
		t.Fatal(err)
	}
	durable, err := loadDurableOrderOutcomes(database, tenant.ID, conversation.ID)
	if err != nil || len(durable) != 1 || durable[0].Status != "confirmed" {
		t.Fatalf("durable order missing after result deletion: %+v %v", durable, err)
	}
	save("result-2", outcome)
	var count int64
	if err := database.Model(&models.MessengerJourneyEvent{}).Where("tenant_id = ? AND kind = ?", tenant.ID, "order").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("unchanged rerun created a business event: %d %v", count, err)
	}
	outcome.Status = "cancelled"
	save("result-3", outcome)
	if err := database.Model(&models.MessengerJourneyEvent{}).Where("tenant_id = ? AND kind = ?", tenant.ID, "order").Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("order revision missing: %d %v", count, err)
	}
	var latest models.MessengerJourneyOrder
	if err := database.First(&latest, "tenant_id = ? AND order_id = ?", tenant.ID, outcome.OrderID).Error; err != nil {
		t.Fatal(err)
	}
	var latestPayload map[string]interface{}
	if err := json.Unmarshal([]byte(latest.Payload), &latestPayload); err != nil {
		t.Fatalf("decode latest order payload: %v", err)
	}
	if latestPayload["status"] != "cancelled" || latestPayload["source_result_id"] != "result-3" || latestPayload["source_job_id"] != "job" {
		t.Fatalf("latest order lost business revision or provenance: %s", latest.Payload)
	}
	var coverage models.MessengerJourneyCoverage
	if err := database.First(&coverage, "tenant_id = ? AND conversation_id = ?", tenant.ID, conversation.ID).Error; err != nil {
		t.Fatal(err)
	}
	var coveragePayload map[string]interface{}
	if err := json.Unmarshal([]byte(coverage.Payload), &coveragePayload); err != nil {
		t.Fatal(err)
	}
	if coveragePayload["source_last_message_id"] != "message-1" || coveragePayload["source_message_count"] != float64(1) {
		t.Fatalf("coverage lost exact source cursor: %s", coverage.Payload)
	}
	if err := database.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("job_run_id = ?", run.ID).Delete(&models.JobResult{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", run.ID).Delete(&models.JobRun{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", job.ID).Delete(&models.Job{}).Error
	}); err != nil {
		t.Fatal(err)
	}
	var afterTaskDeletion models.MessengerJourneyOrder
	if err := database.First(&afterTaskDeletion, "tenant_id = ? AND order_id = ?", tenant.ID, outcome.OrderID).Error; err != nil {
		t.Fatalf("task deletion removed durable order: %v", err)
	}
	if afterTaskDeletion.Payload != latest.Payload {
		t.Fatalf("task deletion changed durable order: before=%s after=%s", latest.Payload, afterTaskDeletion.Payload)
	}
}
