// Package customercases keeps the problems customers report (complaints,
// defects, wrong deliveries, payment mistakes...) as cases staff follow up
// until they are done.
//
// Cases are opened by the chatbot (MCP tool complaint_record) and by the
// chat-memory summarizer (a session issue marked needs_case). Each report is
// first compared with the customer's open and recently resolved cases by the
// tenant's AI model (task prompt complaint_case_match): the same problem is
// added to the existing case as a follow-up, a new problem opens a new case.
// Staff manage cases on the Insights khách page.
package customercases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// Case statuses.
const (
	StatusOpen       = "open"
	StatusInProgress = "in_progress"
	StatusResolved   = "resolved"
	StatusCancelled  = "cancelled"
)

// Report sources (also event actors).
const (
	SourceBot     = "bot"
	SourceSummary = "summary"
	SourceStaff   = "staff"
)

// Report outcomes.
const (
	ActionCreated  = "created"
	ActionFollowup = "followup"
	ActionReopened = "reopened"
	// ActionLinked: the summarizer found a problem the case already holds
	// (usually one the bot recorded during the same session).
	ActionLinked = "linked"
)

// Types are the problem types a case can have (the chat-memory issue types
// without bot_failure, which is about the bot, not the customer).
var Types = []string{"complaint", "delivery", "product_defect", "price", "payment", "staff_request", "other"}

// Statuses lists every case status.
var Statuses = []string{StatusOpen, StatusInProgress, StatusResolved, StatusCancelled}

const (
	// recentResolvedWindow: a case resolved this recently is still offered
	// to the decision model, so "vẫn còn lỗi" reopens it.
	recentResolvedWindow = 14 * 24 * time.Hour
	maxCandidates        = 8
	// followupAlertGap: a customer chasing the same case is reported to
	// staff at most once in this window.
	followupAlertGap = 2 * time.Hour
	maxImagesPerCase = 20
)

// Report is one problem report from the bot, the summarizer or staff.
type Report struct {
	TenantID     string
	ChannelID    string
	SubjectType  string // user | group
	SubjectID    string
	ZaloUserID   string
	ZaloGroupID  string
	ReporterName string
	CustomerName string
	CustomerCode string
	// ChatGroup is the Zalo group's name for a group chat (alerts only).
	ChatGroup string
	SessionID string
	Source    string
	Type      string
	Severity  string
	Title     string
	Detail    string
	Evidence  string
	Images    []models.CaseImage
}

// Outcome is what Report did.
type Outcome struct {
	Action string
	Case   models.CustomerCase
	// Reason is the decision model's reason (empty for a first case).
	Reason string
}

// IsOpen reports whether a status still needs staff.
func IsOpen(status string) bool { return status == StatusOpen || status == StatusInProgress }

// ValidType returns t when it is a known type, else "other".
func ValidType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	for _, known := range Types {
		if t == known {
			return t
		}
	}
	return "other"
}

// ValidSeverity returns s when it is low, medium or high, else medium.
func ValidSeverity(s string) string {
	switch s = strings.ToLower(strings.TrimSpace(s)); s {
	case "low", "medium", "high":
		return s
	}
	return "medium"
}

func severityRank(s string) int {
	switch s {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	}
	return 0
}

// subjectLocks serialises reports for one customer inside this process, so
// two reports arriving together (the bot and the summarizer, or two images)
// cannot both open a case for the same problem.
var subjectLocks sync.Map

func lockSubject(ctx context.Context, key string) (func(), error) {
	l, _ := subjectLocks.LoadOrStore(key, make(chan struct{}, 1))
	ch := l.(chan struct{})
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ErrInvalid is returned for a report without the fields a case needs.
var ErrInvalid = errors.New("report needs a tenant, a chat and a description")

// Record records a problem: it joins the matching earlier case or opens a
// new one, and alerts staff. Alert failures never fail the report.
func Record(ctx context.Context, r Report) (Outcome, error) {
	r.Type = ValidType(r.Type)
	r.Severity = ValidSeverity(r.Severity)
	r.Title = truncate(strings.TrimSpace(r.Title), 200)
	r.Detail = truncate(strings.TrimSpace(r.Detail), 4000)
	r.Evidence = truncate(strings.TrimSpace(r.Evidence), 1000)
	r.CustomerCode = strings.TrimSpace(r.CustomerCode)
	if r.Source == "" {
		r.Source = SourceBot
	}
	if r.Title == "" {
		r.Title = truncate(firstLine(r.Detail), 200)
	}
	if r.TenantID == "" || r.SubjectID == "" || (r.Title == "" && r.Detail == "") {
		return Outcome{}, ErrInvalid
	}
	if db.DB == nil {
		return Outcome{}, errors.New("database not initialised")
	}
	unlock, err := lockSubject(ctx, r.TenantID+"|"+r.SubjectType+"|"+r.SubjectID)
	if err != nil {
		return Outcome{}, err
	}
	defer unlock()

	candidates, err := Candidates(ctx, r.TenantID, r.SubjectType, r.SubjectID, r.CustomerCode)
	if err != nil {
		return Outcome{}, err
	}
	var out Outcome
	matched := -1
	if len(candidates) > 0 {
		idx, reason, err := Decide(ctx, r, candidates)
		if err != nil {
			// A duplicate case is easier to fix than a lost complaint.
			log.Printf("[cases] decision model failed for %s/%s (%v); opening a new case", r.SubjectType, r.SubjectID, err)
		}
		matched, out.Reason = idx, reason
	}
	if matched < 0 {
		c, err := create(ctx, r)
		if err != nil {
			return Outcome{}, err
		}
		out.Action, out.Case = ActionCreated, c
	} else {
		c, action, err := join(ctx, candidates[matched], r)
		if err != nil {
			return Outcome{}, err
		}
		out.Action, out.Case = action, c
	}
	alertStaff(ctx, &out, r)
	return out, nil
}

// Candidates returns the cases a new report may belong to: the chat's (or
// the customer code's) open cases and recently resolved ones, newest first.
func Candidates(ctx context.Context, tenantID, subjectType, subjectID, customerCode string) ([]models.CustomerCase, error) {
	q := db.DB.WithContext(ctx).Where("tenant_id = ?", tenantID)
	if customerCode != "" {
		q = q.Where("((subject_type = ? AND subject_id = ?) OR customer_code = ?)", subjectType, subjectID, customerCode)
	} else {
		q = q.Where("subject_type = ? AND subject_id = ?", subjectType, subjectID)
	}
	q = q.Where("(status IN ? OR (status = ? AND resolved_at > ?))",
		[]string{StatusOpen, StatusInProgress}, StatusResolved, time.Now().Add(-recentResolvedWindow))
	var rows []models.CustomerCase
	err := q.Order("updated_at DESC").Limit(maxCandidates).Find(&rows).Error
	return rows, err
}

// OpenCases returns the chat's (or the customer code's) cases that still
// need staff, newest first.
func OpenCases(ctx context.Context, tenantID, subjectType, subjectID, customerCode string) []models.CustomerCase {
	if db.DB == nil || tenantID == "" || subjectID == "" {
		return nil
	}
	q := db.DB.WithContext(ctx).Where("tenant_id = ? AND status IN ?", tenantID, []string{StatusOpen, StatusInProgress})
	if customerCode != "" {
		q = q.Where("((subject_type = ? AND subject_id = ?) OR customer_code = ?)", subjectType, subjectID, customerCode)
	} else {
		q = q.Where("subject_type = ? AND subject_id = ?", subjectType, subjectID)
	}
	var rows []models.CustomerCase
	if err := q.Order("updated_at DESC").Limit(maxCandidates).Find(&rows).Error; err != nil {
		log.Printf("[cases] open cases for %s/%s: %v", subjectType, subjectID, err)
		return nil
	}
	return rows
}

// TouchedSince reports whether the bot recorded or followed up a case for
// this chat at or after since (used to tell whether a complaint turn reached
// staff through a case).
func TouchedSince(ctx context.Context, tenantID, subjectType, subjectID string, since time.Time) bool {
	if db.DB == nil {
		return false
	}
	var n int64
	db.DB.WithContext(ctx).Table("customer_case_events AS e").
		Joins("JOIN customer_cases AS c ON c.id = e.case_id").
		Where("c.tenant_id = ? AND c.subject_type = ? AND c.subject_id = ? AND e.actor = ? AND e.created_at >= ?",
			tenantID, subjectType, subjectID, SourceBot, since).
		Count(&n)
	return n > 0
}

func create(ctx context.Context, r Report) (models.CustomerCase, error) {
	now := time.Now()
	images := mergeImages(nil, r.Images)
	imagesJSON, _ := json.Marshal(images)
	c := models.CustomerCase{
		ID: pkg.NewUUID(), TenantID: r.TenantID, ChannelID: r.ChannelID,
		SubjectType: r.SubjectType, SubjectID: r.SubjectID, ZaloUserID: r.ZaloUserID, ZaloGroupID: r.ZaloGroupID,
		ReporterName: truncate(r.ReporterName, 250), CustomerName: truncate(r.CustomerName, 250), CustomerCode: truncate(r.CustomerCode, 60),
		Type: r.Type, Severity: r.Severity, Title: r.Title, Detail: r.Detail, Evidence: r.Evidence,
		Images: string(imagesJSON), Status: StatusOpen, Source: r.Source, OpenedSessionID: r.SessionID,
		CreatedAt: now, UpdatedAt: now,
	}
	err := db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&c).Error; err != nil {
			return err
		}
		return tx.Create(newEvent(c, "opened", r.Source, r.ReporterName, r.SessionID, eventText(r), r.Images)).Error
	})
	return c, err
}

// join adds a report to an earlier case.
func join(ctx context.Context, c models.CustomerCase, r Report) (models.CustomerCase, string, error) {
	now := time.Now()
	updates := map[string]interface{}{"updated_at": now}
	images := mergeImages(parseImages(c.Images), r.Images)
	if imagesJSON, _ := json.Marshal(images); string(imagesJSON) != c.Images {
		updates["images"] = string(imagesJSON)
		c.Images = string(imagesJSON)
	}
	if severityRank(r.Severity) > severityRank(c.Severity) {
		updates["severity"] = r.Severity
		c.Severity = r.Severity
	}
	if c.CustomerCode == "" && r.CustomerCode != "" {
		updates["customer_code"] = r.CustomerCode
		c.CustomerCode = r.CustomerCode
	}

	action, kind := ActionFollowup, "followup"
	switch {
	case r.Source == SourceSummary:
		// The summarizer reads the session after the fact: it never counts
		// as the customer asking again and never reopens a case.
		action, kind = ActionLinked, "linked"
		if sessionAlreadyOnCase(ctx, c.ID, r.SessionID) {
			// The bot already recorded this session's report on the case.
			if len(updates) > 1 {
				err := db.DB.WithContext(ctx).Model(&models.CustomerCase{}).Where("id = ?", c.ID).Updates(updates).Error
				return c, action, err
			}
			return c, action, nil
		}
	case !IsOpen(c.Status):
		action, kind = ActionReopened, "reopened"
		updates["status"] = StatusOpen
		updates["resolved_at"] = nil
		updates["followup_count"] = gorm.Expr("followup_count + 1")
		updates["last_followup_at"] = now
		c.Status, c.ResolvedAt = StatusOpen, nil
		c.FollowupCount++
		c.LastFollowupAt = &now
	default:
		updates["followup_count"] = gorm.Expr("followup_count + 1")
		updates["last_followup_at"] = now
		c.FollowupCount++
		c.LastFollowupAt = &now
	}
	c.UpdatedAt = now
	err := db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.CustomerCase{}).Where("id = ?", c.ID).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Create(newEvent(c, kind, r.Source, r.ReporterName, r.SessionID, eventText(r), r.Images)).Error
	})
	return c, action, err
}

func sessionAlreadyOnCase(ctx context.Context, caseID, sessionID string) bool {
	if sessionID == "" {
		return false
	}
	var n int64
	db.DB.WithContext(ctx).Model(&models.CustomerCaseEvent{}).Where("case_id = ? AND session_id = ?", caseID, sessionID).Count(&n)
	return n > 0
}

func newEvent(c models.CustomerCase, kind, actor, actorName, sessionID, text string, images []models.CaseImage) *models.CustomerCaseEvent {
	imagesJSON := "[]"
	if len(images) > 0 {
		b, _ := json.Marshal(images)
		imagesJSON = string(b)
	}
	return &models.CustomerCaseEvent{
		ID: pkg.NewUUID(), TenantID: c.TenantID, CaseID: c.ID, Kind: kind, Actor: actor,
		ActorName: truncate(actorName, 250), SessionID: sessionID, Text: truncate(text, 6000),
		Images: imagesJSON, CreatedAt: time.Now(),
	}
}

// AddEventTx appends a history line written by staff, inside tx.
func AddEventTx(tx *gorm.DB, c models.CustomerCase, kind, actorName, text string) error {
	return tx.Create(newEvent(c, kind, SourceStaff, actorName, "", text, nil)).Error
}

func eventText(r Report) string {
	var parts []string
	if r.Title != "" {
		parts = append(parts, r.Title)
	}
	if r.Detail != "" && r.Detail != r.Title {
		parts = append(parts, r.Detail)
	}
	if r.Evidence != "" {
		parts = append(parts, fmt.Sprintf("Khách nói: \"%s\"", r.Evidence))
	}
	return strings.Join(parts, "\n")
}

// ParseImages reads a case's image list.
func ParseImages(raw string) []models.CaseImage { return parseImages(raw) }

func parseImages(raw string) []models.CaseImage {
	var out []models.CaseImage
	if strings.TrimSpace(raw) == "" || json.Unmarshal([]byte(raw), &out) != nil {
		return nil
	}
	return out
}

// mergeImages appends the images not already on the case, oldest kept
// first, up to maxImagesPerCase. It always returns a non-nil list.
func mergeImages(have, add []models.CaseImage) []models.CaseImage {
	out := append([]models.CaseImage{}, have...)
	seen := make(map[string]bool, len(have))
	for _, im := range have {
		seen[im.URL] = true
	}
	for _, im := range add {
		if im.URL == "" || seen[im.URL] || len(out) >= maxImagesPerCase {
			continue
		}
		seen[im.URL] = true
		out = append(out, im)
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
