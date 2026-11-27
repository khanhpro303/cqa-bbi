package handlers

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/customercases"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

// ListCustomerCases lists complaint cases for the Insights khách page.
// Query: status (active = open + in_progress, the default; open,
// in_progress, resolved, cancelled, all), type, severity, stream ("none" for
// cases without one), q (title, customer
// name or code), subject_type + subject_id (one chat), customer_code,
// channel_id (required with a stream filter). The response also counts cases
// per status for the tabs.
func ListCustomerCases(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	page, perPage := pageParams(c)
	status := c.DefaultQuery("status", "active")
	caseType, severity, stream := c.Query("type"), c.Query("severity"), c.Query("stream")
	q := strings.TrimSpace(c.Query("q"))
	subjectType, subjectID := c.Query("subject_type"), c.Query("subject_id")
	customerCode, channelID := strings.TrimSpace(c.Query("customer_code")), c.Query("channel_id")
	if stream != "" && channelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "stream_requires_channel"})
		return
	}
	ctx := c.Request.Context()
	conn := db.DB.WithContext(ctx)
	channels, err := listComplaintChannels(conn, tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_cases_failed"})
		return
	}

	base := func(tx *gorm.DB) *gorm.DB {
		tx = tx.Where("tenant_id = ?", tenantID)
		if subjectID != "" {
			if customerCode != "" {
				// A private (guest) case belongs to its own chat only.
				tx = tx.Where("((subject_type = ? AND subject_id = ?) OR (customer_code = ? AND private = ?))", subjectType, subjectID, customerCode, false)
			} else {
				tx = tx.Where("subject_type = ? AND subject_id = ?", subjectType, subjectID)
			}
		} else if customerCode != "" {
			tx = tx.Where("customer_code = ?", customerCode)
		}
		if channelID != "" {
			tx = tx.Where("channel_id = ?", channelID)
		}
		if caseType != "" {
			tx = tx.Where("type = ?", caseType)
		}
		if severity != "" {
			tx = tx.Where("severity = ?", severity)
		}
		switch stream {
		case "":
		case "none":
			tx = tx.Where("stream = ?", "")
		default:
			tx = tx.Where("stream = ?", stream)
		}
		if q != "" {
			like := "%" + q + "%"
			tx = tx.Where("title LIKE ? OR customer_name LIKE ? OR customer_code LIKE ? OR reporter_name LIKE ?", like, like, like, like)
		}
		return tx
	}
	withStatus := func(tx *gorm.DB) *gorm.DB {
		switch status {
		case "active":
			return tx.Where("status IN ?", []string{customercases.StatusOpen, customercases.StatusInProgress})
		case customercases.StatusOpen, customercases.StatusInProgress, customercases.StatusResolved, customercases.StatusCancelled:
			return tx.Where("status = ?", status)
		}
		return tx
	}

	var total int64
	if err := withStatus(base(conn.Model(&models.CustomerCase{}))).Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_cases_failed"})
		return
	}
	var rows []models.CustomerCase
	order := "updated_at DESC"
	if status == "active" {
		// Most urgent first: high severity, then the longest waiting.
		order = "FIELD(severity, 'high', 'medium', 'low'), created_at ASC"
	}
	if err := withStatus(base(conn.Model(&models.CustomerCase{}))).Order(order).
		Offset((page - 1) * perPage).Limit(perPage).Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_cases_failed"})
		return
	}

	var counts []struct {
		Status string
		N      int64
	}
	if err := base(conn.Model(&models.CustomerCase{})).Select("status, COUNT(*) AS n").Group("status").Scan(&counts).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_cases_failed"})
		return
	}
	byStatus := map[string]int64{}
	for _, row := range counts {
		byStatus[row.Status] = row.N
	}
	cases, err := buildCustomerCaseViews(conn, tenantID, rows, channels)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_cases_failed"})
		return
	}
	streams, err := listComplaintStreamFilters(conn, tenantID, channelID, channels)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_cases_failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": cases, "total": total, "page": page, "per_page": perPage, "counts": byStatus,
		"channels": channels, "streams": streams})
}

// GetCustomerCase returns a case with its history, newest last.
func GetCustomerCase(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	ctx := c.Request.Context()
	conn := db.DB.WithContext(ctx)
	var cc models.CustomerCase
	if err := conn.Where("tenant_id = ? AND id = ?", tenantID, c.Param("caseId")).First(&cc).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_cases_failed"})
		return
	}
	var channel complaintChannel
	if cc.ChannelID != "" {
		err := conn.Model(&models.Channel{}).Select("id, name, channel_type, metadata").Where("tenant_id = ? AND id = ?", tenantID, cc.ChannelID).First(&channel).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "load_cases_failed"})
			return
		}
	}
	cases, err := buildCustomerCaseViews(conn, tenantID, []models.CustomerCase{cc}, []complaintChannel{channel})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_cases_failed"})
		return
	}
	// The newest 500 events, shown oldest first.
	var events []models.CustomerCaseEvent
	if err := conn.Where("tenant_id = ? AND case_id = ?", tenantID, cc.ID).Order("created_at DESC, id DESC").Limit(500).Find(&events).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_cases_failed"})
		return
	}
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	response := gin.H{"case": cases[0], "events": events, "streams": complaintStreamOptions(channel)}
	if alerts, err := customercases.CaseAlerts(ctx, tenantID, cc.ID); err == nil {
		response["alerts"] = alerts
	} else {
		log.Printf("[cases] alert history for case %s: %v", cc.ID, err)
		response["alerts_unavailable"] = true
	}
	c.JSON(http.StatusOK, response)
}

type caseUpdateRequest struct {
	Status         *string `json:"status"`
	Severity       *string `json:"severity"`
	Type           *string `json:"type"`
	Stream         *string `json:"stream"`
	Title          *string `json:"title"`
	AssigneeID     *string `json:"assignee_id"`
	AssigneeName   *string `json:"assignee_name"`
	CustomerUpdate *string `json:"customer_update"`
	Resolution     *string `json:"resolution"`
	Note           string  `json:"note"`
}

// UpdateCustomerCase changes a case's status, assignee, what the bot may
// tell the customer, the resolution, and adds a note. Each change is written
// to the case history.
func UpdateCustomerCase(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var req caseUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	var cc models.CustomerCase
	if err := db.DB.Where("tenant_id = ? AND id = ?", tenantID, c.Param("caseId")).First(&cc).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	actor := staffName(c)
	now := time.Now()
	updates := map[string]interface{}{}
	var events []models.CustomerCaseEvent
	event := func(kind, text string) {
		events = append(events, models.CustomerCaseEvent{Kind: kind, Text: text})
	}

	if req.Status != nil && *req.Status != cc.Status {
		s := *req.Status
		if !validCaseStatus(s) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_status"})
			return
		}
		updates["status"] = s
		if s == customercases.StatusResolved || s == customercases.StatusCancelled {
			updates["resolved_at"] = now
		} else {
			updates["resolved_at"] = nil
		}
		event("status", fmt.Sprintf("%s → %s", customercases.StatusLabel(cc.Status), customercases.StatusLabel(s)))
	}
	if req.Severity != nil && *req.Severity != cc.Severity {
		s := customercases.ValidSeverity(*req.Severity)
		updates["severity"] = s
		event("edit", "Mức độ: "+s)
	}
	if req.Type != nil && *req.Type != cc.Type {
		t := customercases.ValidType(*req.Type)
		updates["type"] = t
		event("edit", "Loại: "+customercases.TypeLabel(t))
	}
	streamMoved := false
	if req.Stream != nil && strings.TrimSpace(*req.Stream) != cc.Stream {
		s := strings.TrimSpace(*req.Stream)
		var settings customercases.Settings
		if cc.ChannelID != "" {
			var ch models.Channel
			if err := db.DB.Select("metadata").Where("id = ? AND tenant_id = ?", cc.ChannelID, tenantID).First(&ch).Error; err == nil {
				settings = customercases.ParseSettings(ch.Metadata)
			}
		}
		if _, ok := settings.Stream(s); s != "" && !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_stream"})
			return
		}
		updates["stream"] = s
		streamMoved = s != ""
		if s == "" {
			event("edit", "Bỏ luồng xử lý")
		} else {
			event("edit", "Luồng: "+customercases.StreamLabel(settings, s))
		}
	}
	if req.Title != nil && strings.TrimSpace(*req.Title) != "" && strings.TrimSpace(*req.Title) != cc.Title {
		t := truncateRunes(strings.TrimSpace(*req.Title), 200)
		updates["title"] = t
		event("edit", "Tiêu đề: "+t)
	}
	if req.AssigneeName != nil {
		name := truncateRunes(strings.TrimSpace(*req.AssigneeName), 250)
		id := cc.AssigneeID
		if req.AssigneeID != nil {
			id = strings.TrimSpace(*req.AssigneeID)
			if id != "" && !tenantHasUser(tenantID, id) {
				id = ""
			}
		}
		if name == "" {
			id = ""
		}
		// Two staff can share a display name: a different user id is a
		// different assignee.
		if name != cc.AssigneeName || id != cc.AssigneeID {
			updates["assignee_name"], updates["assignee_id"] = name, id
			if name == "" {
				event("assign", "Bỏ người phụ trách")
			} else {
				event("assign", "Người phụ trách: "+name)
			}
		}
	}
	if req.CustomerUpdate != nil && strings.TrimSpace(*req.CustomerUpdate) != strings.TrimSpace(cc.CustomerUpdate) {
		u := truncateRunes(strings.TrimSpace(*req.CustomerUpdate), 2000)
		updates["customer_update"] = u
		if u == "" {
			event("update", "Xoá cập nhật cho khách")
		} else {
			event("update", "Cập nhật cho khách (bot được báo): "+u)
		}
	}
	if req.Resolution != nil && strings.TrimSpace(*req.Resolution) != strings.TrimSpace(cc.Resolution) {
		r := truncateRunes(strings.TrimSpace(*req.Resolution), 4000)
		updates["resolution"] = r
		event("edit", "Cách xử lý: "+r)
	}
	if note := strings.TrimSpace(req.Note); note != "" {
		event("note", truncateRunes(note, 4000))
	}
	if len(updates) == 0 && len(events) == 0 {
		c.JSON(http.StatusOK, gin.H{"case": cc})
		return
	}
	updates["updated_at"] = now
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.CustomerCase{}).Where("id = ?", cc.ID).Updates(updates).Error; err != nil {
			return err
		}
		for _, e := range events {
			if err := customercases.AddEventTx(tx, cc, e.Kind, actor, e.Text); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update_failed"})
		return
	}
	db.DB.Where("id = ?", cc.ID).First(&cc)
	if streamMoved && customercases.IsOpen(cc.Status) {
		// The new stream's group hears about the case it now owns.
		customercases.AlertStreamMoved(c.Request.Context(), cc, actor)
	}
	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "customer_case.updated", "customer_case", cc.ID, cc.Title, "", c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"case": cc})
}

func validCaseStatus(s string) bool {
	for _, known := range customercases.Statuses {
		if s == known {
			return true
		}
	}
	return false
}

func tenantHasUser(tenantID, userID string) bool {
	var n int64
	db.DB.Model(&models.UserTenant{}).Where("tenant_id = ? AND user_id = ?", tenantID, userID).Count(&n)
	return n > 0
}

// staffName is the signed-in user's display name for case history.
func staffName(c *gin.Context) string {
	var u models.User
	if err := db.DB.Select("name, email").Where("id = ?", middleware.GetUserID(c)).First(&u).Error; err == nil {
		if strings.TrimSpace(u.Name) != "" {
			return u.Name
		}
		return u.Email
	}
	return middleware.GetUserEmail(c)
}
