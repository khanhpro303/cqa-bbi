package handlers

import (
	"fmt"
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
// in_progress, resolved, cancelled, all), type, severity, q (title, customer
// name or code), subject_type + subject_id (one chat), customer_code,
// channel_id. The response also counts cases per status for the tabs.
func ListCustomerCases(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	page, perPage := pageParams(c)
	status := c.DefaultQuery("status", "active")
	caseType, severity := c.Query("type"), c.Query("severity")
	q := strings.TrimSpace(c.Query("q"))
	subjectType, subjectID := c.Query("subject_type"), c.Query("subject_id")
	customerCode, channelID := strings.TrimSpace(c.Query("customer_code")), c.Query("channel_id")

	base := func(tx *gorm.DB) *gorm.DB {
		tx = tx.Where("tenant_id = ?", tenantID)
		if subjectID != "" {
			if customerCode != "" {
				tx = tx.Where("((subject_type = ? AND subject_id = ?) OR customer_code = ?)", subjectType, subjectID, customerCode)
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
	withStatus(base(db.DB.Model(&models.CustomerCase{}))).Count(&total)
	var rows []models.CustomerCase
	order := "updated_at DESC"
	if status == "active" {
		// Most urgent first: high severity, then the longest waiting.
		order = "FIELD(severity, 'high', 'medium', 'low'), created_at ASC"
	}
	withStatus(base(db.DB.Model(&models.CustomerCase{}))).Order(order).
		Offset((page - 1) * perPage).Limit(perPage).Find(&rows)

	var counts []struct {
		Status string
		N      int64
	}
	base(db.DB.Model(&models.CustomerCase{})).Select("status, COUNT(*) AS n").Group("status").Scan(&counts)
	byStatus := map[string]int64{}
	for _, row := range counts {
		byStatus[row.Status] = row.N
	}
	c.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "per_page": perPage, "counts": byStatus})
}

// GetCustomerCase returns a case with its history, newest last.
func GetCustomerCase(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var cc models.CustomerCase
	if err := db.DB.Where("tenant_id = ? AND id = ?", tenantID, c.Param("caseId")).First(&cc).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	var events []models.CustomerCaseEvent
	db.DB.Where("tenant_id = ? AND case_id = ?", tenantID, cc.ID).Order("created_at ASC").Limit(500).Find(&events)
	c.JSON(http.StatusOK, gin.H{"case": cc, "events": events})
}

type caseUpdateRequest struct {
	Status         *string `json:"status"`
	Severity       *string `json:"severity"`
	Type           *string `json:"type"`
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
	if req.Title != nil && strings.TrimSpace(*req.Title) != "" && strings.TrimSpace(*req.Title) != cc.Title {
		t := truncateRunes(strings.TrimSpace(*req.Title), 200)
		updates["title"] = t
		event("edit", "Tiêu đề: "+t)
	}
	if req.AssigneeName != nil && strings.TrimSpace(*req.AssigneeName) != cc.AssigneeName {
		name := truncateRunes(strings.TrimSpace(*req.AssigneeName), 250)
		id := ""
		if req.AssigneeID != nil {
			id = strings.TrimSpace(*req.AssigneeID)
		}
		if id != "" && !tenantHasUser(tenantID, id) {
			id = ""
		}
		updates["assignee_name"], updates["assignee_id"] = name, id
		if name == "" {
			event("assign", "Bỏ người phụ trách")
		} else {
			event("assign", "Người phụ trách: "+name)
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
