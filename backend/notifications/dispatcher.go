package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// OutputConfig represents a single output destination from job.outputs JSON.
type OutputConfig struct {
	Type           string `json:"type"`            // "telegram" | "email"
	BotToken       string `json:"bot_token"`       // telegram
	ChatID         string `json:"chat_id"`         // telegram
	SMTPHost       string `json:"smtp_host"`       // email
	SMTPPort       int    `json:"smtp_port"`       // email
	SMTPUser       string `json:"smtp_user"`       // email
	SMTPPass       string `json:"smtp_pass"`       // email
	From           string `json:"from"`            // email
	To             string `json:"to"`              // email (comma-separated)
	Template       string `json:"template"`        // "default" | "custom"
	CustomTemplate string `json:"custom_template"` // user-defined template (HTML)
	ChannelID      string `json:"channel_id"`      // zalo: OA channel id
	GroupID        string `json:"group_id"`        // zalo: CRM group id (resolved to zalo_group_id)
}

// Dispatcher sends notifications for job results and logs every send.
type Dispatcher struct{}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{}
}

// SendJobResults sends notifications for a job run based on the job's output config.
func (d *Dispatcher) SendJobResults(ctx context.Context, job models.Job, run models.JobRun) error {
	outputs, err := parseOutputs(job.Outputs)
	if err != nil {
		return err
	}

	// Get unnotified results for this run
	var results []models.JobResult
	db.DB.Where("job_run_id = ? AND notified_at IS NULL", run.ID).Find(&results)

	if len(results) == 0 {
		return nil
	}

	// Count pass/fail from run summary
	var summary map[string]interface{}
	json.Unmarshal([]byte(run.Summary), &summary)
	total := int(getFloat(summary, "conversations_analyzed"))
	passed := int(getFloat(summary, "conversations_passed"))
	failed := total - passed
	issues := int(getFloat(summary, "issues_found"))

	subject := fmt.Sprintf("[CQA] %s - %d issues found", job.Name, issues)

	for _, output := range outputs {
		notifier, err := d.createNotifier(job.TenantID, output)
		if err != nil {
			log.Printf("[dispatcher] create notifier failed for %s: %v", output.Type, err)
			continue
		}

		// Build default body per output type (telegram uses plain link, email uses HTML <a>)
		defaultBody := d.buildNotificationBody(job, results, output.Type)

		// Use custom template if configured
		body := defaultBody
		link := fmt.Sprintf("%s/%s/jobs/%s", d.getBaseURL(job.TenantID), job.TenantID, job.ID)
		if output.Template == "custom" && output.CustomTemplate != "" {
			body = d.renderCustomTemplate(output.CustomTemplate, job.Name, total, passed, failed, issues, defaultBody, link)
		}

		sendErr := notifier.Send(ctx, subject, body)
		status := "sent"
		errMsg := ""
		if sendErr != nil {
			status = "failed"
			errMsg = sendErr.Error()
			log.Printf("[dispatcher] send failed for %s: %v", output.Type, sendErr)
		}

		d.writeNotificationLog(job, run.ID, output, subject, body, status, errMsg)
	}

	// Mark results as notified
	now := time.Now()
	db.DB.Model(&models.JobResult{}).Where("job_run_id = ? AND notified_at IS NULL", run.ID).
		Update("notified_at", &now)

	return nil
}

// SendJobError sends a failure alert for a job run that ended in error, to all
// of the job's configured outputs (telegram/email/zalo), and logs every send.
// Called for hard failures only (run.Status == "error"). No-ops when the job has
// no outputs configured.
func (d *Dispatcher) SendJobError(ctx context.Context, job models.Job, run models.JobRun) error {
	outputs, err := parseOutputs(job.Outputs)
	if err != nil {
		return err
	}
	if len(outputs) == 0 {
		return nil
	}

	subject := fmt.Sprintf("[CQA] %s - CHẠY LỖI", job.Name)
	link := fmt.Sprintf("%s/%s/jobs/%s", d.getBaseURL(job.TenantID), job.TenantID, job.ID)

	for _, output := range outputs {
		notifier, nErr := d.createNotifier(job.TenantID, output)
		if nErr != nil {
			log.Printf("[dispatcher] create notifier failed for %s: %v", output.Type, nErr)
			continue
		}

		defaultBody := d.buildErrorBody(job, run, output.Type)
		body := defaultBody
		if output.Template == "custom" && output.CustomTemplate != "" {
			// Reuse the same template renderer; counts are 0 for an errored run,
			// {{content}} carries the error detail.
			body = d.renderCustomTemplate(output.CustomTemplate, job.Name, 0, 0, 0, 0, defaultBody, link)
		}

		sendErr := notifier.Send(ctx, subject, body)
		status, errMsg := "sent", ""
		if sendErr != nil {
			status, errMsg = "failed", sendErr.Error()
			log.Printf("[dispatcher] error-alert send failed for %s: %v", output.Type, sendErr)
		}
		d.writeNotificationLog(job, run.ID, output, subject, body, status, errMsg)
	}
	return nil
}

// buildErrorBody renders the failure-alert body. Zalo gets plain text; telegram
// and email get HTML (<b>), matching buildNotificationBody.
func (d *Dispatcher) buildErrorBody(job models.Job, run models.JobRun, outputType string) string {
	msg := strings.TrimSpace(run.ErrorMessage)
	if msg == "" {
		msg = "Không có thông tin lỗi chi tiết."
	}
	body := htmlBold(outputType, "⚠️ Tác vụ chạy lỗi: "+job.Name) + "\n\n"
	body += "Lỗi: " + msg + "\n"
	if run.FinishedAt != nil {
		body += "Thời điểm: " + run.FinishedAt.Format("15:04 02/01/2006") + "\n"
	}
	return body
}

// parseOutputs unmarshals job.Outputs, tolerating a double-encoded (string-wrapped)
// JSON array.
func parseOutputs(raw string) ([]OutputConfig, error) {
	var outputs []OutputConfig
	if err := json.Unmarshal([]byte(raw), &outputs); err != nil {
		var s string
		if err2 := json.Unmarshal([]byte(raw), &s); err2 == nil {
			if err3 := json.Unmarshal([]byte(s), &outputs); err3 != nil {
				return nil, fmt.Errorf("invalid outputs config: %w", err)
			}
		} else {
			return nil, fmt.Errorf("invalid outputs config: %w", err)
		}
	}
	return outputs, nil
}

// recipientFor returns the human-facing destination for a NotificationLog row.
func recipientFor(cfg OutputConfig) string {
	switch cfg.Type {
	case "email":
		return cfg.To
	case "zalo":
		return cfg.GroupID
	default:
		return cfg.ChatID
	}
}

// writeNotificationLog persists a single send outcome (best-effort).
func (d *Dispatcher) writeNotificationLog(job models.Job, runID string, output OutputConfig, subject, body, status, errMsg string) {
	now := time.Now()
	db.DB.Create(&models.NotificationLog{
		ID:           pkg.NewUUID(),
		TenantID:     job.TenantID,
		JobID:        job.ID,
		JobRunID:     runID,
		ChannelType:  output.Type,
		Recipient:    recipientFor(output),
		Subject:      subject,
		Body:         body,
		Status:       status,
		ErrorMessage: errMsg,
		SentAt:       now,
		CreatedAt:    now,
	})
}

func (d *Dispatcher) createNotifier(tenantID string, cfg OutputConfig) (Notifier, error) {
	switch cfg.Type {
	case "telegram":
		return NewTelegramNotifier(cfg.BotToken, cfg.ChatID), nil
	case "email":
		return NewEmailNotifier(
			cfg.SMTPHost, cfg.SMTPPort,
			cfg.SMTPUser, cfg.SMTPPass,
			cfg.From, splitComma(cfg.To),
		), nil
	case "zalo":
		return BuildZaloGroupNotifier(tenantID, cfg.ChannelID, cfg.GroupID)
	default:
		return nil, fmt.Errorf("unsupported output type: %s", cfg.Type)
	}
}

// htmlBold wraps s in <b> for telegram (HTML parse mode) and email (HTML), but
// returns plain text for zalo group messages (no HTML rendering).
func htmlBold(outputType, s string) string {
	if outputType == "zalo" {
		return s
	}
	return "<b>" + s + "</b>"
}

func (d *Dispatcher) buildNotificationBody(job models.Job, results []models.JobResult, outputType string) string {
	body := htmlBold(outputType, "Kết quả phân tích: "+job.Name) + "\n\n"

	for i, r := range results {
		if i >= 10 {
			body += fmt.Sprintf("\n... và %d vấn đề khác\n", len(results)-10)
			break
		}
		switch r.ResultType {
		case "qc_violation":
			emoji := "⚠️"
			if r.Severity == "NGHIEM_TRONG" {
				emoji = "🔴"
			}
			body += fmt.Sprintf("%s %s — %s\n📌 %s\n\n", emoji, htmlBold(outputType, r.Severity), r.RuleName, r.Evidence)
		case "classification_tag":
			body += fmt.Sprintf("🏷 %s (%.0f%%)\n📌 %s\n\n", htmlBold(outputType, r.RuleName), r.Confidence*100, r.Evidence)
		}
	}

	return body
}

func splitComma(s string) []string {
	var result []string
	for _, p := range splitBy(s, ',') {
		p = trimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func splitBy(s string, sep byte) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

func (d *Dispatcher) getBaseURL(tenantID string) string {
	// Priority 1: tenant setting from DB
	var setting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "app_url").First(&setting).Error; err == nil && setting.ValuePlain != "" {
		return setting.ValuePlain
	}
	// Priority 2: environment variable
	if u := os.Getenv("APP_URL"); u != "" {
		return u
	}
	// Priority 3: fallback
	return "http://localhost:8080"
}

func (d *Dispatcher) renderCustomTemplate(tmpl, jobName string, total, passed, failed, issues int, content, link string) string {
	result := tmpl
	replacements := map[string]string{
		"{{job_name}}": jobName,
		"{{total}}":    fmt.Sprintf("%d", total),
		"{{passed}}":   fmt.Sprintf("%d", passed),
		"{{failed}}":   fmt.Sprintf("%d", failed),
		"{{issues}}":   fmt.Sprintf("%d", issues),
		"{{content}}":  content,
		"{{link}}":     link,
	}
	for k, v := range replacements {
		for {
			idx := -1
			for i := 0; i <= len(result)-len(k); i++ {
				if result[i:i+len(k)] == k {
					idx = i
					break
				}
			}
			if idx < 0 {
				break
			}
			result = result[:idx] + v + result[idx+len(k):]
		}
	}
	return result
}

func getFloat(m map[string]interface{}, key string) float64 {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	f, ok := v.(float64)
	if !ok {
		return 0
	}
	return f
}
