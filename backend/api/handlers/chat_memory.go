package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/chatmemory"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
)

func pageParams(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "20"))
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 20
	}
	if perPage > 100 {
		perPage = 100
	}
	return page, perPage
}

// ListChatProfiles lists customer profiles built from chatbot sessions.
// Query: q (name, customer code or Zalo id), channel_id, subject_type.
func ListChatProfiles(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	page, perPage := pageParams(c)
	q := strings.TrimSpace(c.Query("q"))
	channelID := c.Query("channel_id")
	subjectType := c.Query("subject_type")
	scope := func(tx *gorm.DB) *gorm.DB {
		tx = tx.Where("tenant_id = ?", tenantID)
		if channelID != "" {
			tx = tx.Where("channel_id = ?", channelID)
		}
		if subjectType == "user" || subjectType == "group" {
			tx = tx.Where("subject_type = ?", subjectType)
		}
		if q != "" {
			like := "%" + q + "%"
			tx = tx.Where("customer_name LIKE ? OR customer_code LIKE ? OR subject_id = ?", like, like, q)
		}
		return tx
	}
	var total int64
	scope(db.DB.Model(&models.ChatCustomerProfile{})).Count(&total)
	var rows []models.ChatCustomerProfile
	scope(db.DB.Model(&models.ChatCustomerProfile{})).Order("last_session_at DESC").
		Offset((page - 1) * perPage).Limit(perPage).Find(&rows)
	c.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "per_page": perPage})
}

// DeleteChatProfile removes a customer profile (e.g. on the customer's
// request). Session summaries stay; the next session starts a new profile.
func DeleteChatProfile(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	id := c.Param("profileId")
	var p models.ChatCustomerProfile
	if err := db.DB.Where("tenant_id = ? AND id = ?", tenantID, id).First(&p).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	if err := db.DB.Delete(&p).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "delete_failed"})
		return
	}
	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "chat_memory.profile_deleted", "chat_customer_profile", p.ID,
		fmt.Sprintf("%s %s", p.SubjectType, p.SubjectID), "", c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ListChatSessions lists chatbot sessions. Query: subject_type + subject_id
// (one customer or group), channel_id, status, issues ("any" or "high"),
// alerted ("true"), from / to (YYYY-MM-DD, last activity).
func ListChatSessions(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	page, perPage := pageParams(c)
	subjectType, subjectID := c.Query("subject_type"), c.Query("subject_id")
	channelID, status, issues := c.Query("channel_id"), c.Query("status"), c.Query("issues")
	alerted := c.Query("alerted") == "true"
	from, to := c.Query("from"), c.Query("to")
	scope := func(tx *gorm.DB) *gorm.DB {
		tx = tx.Where("tenant_id = ?", tenantID)
		if subjectID != "" {
			tx = tx.Where("subject_type = ? AND subject_id = ?", subjectType, subjectID)
		}
		if channelID != "" {
			tx = tx.Where("channel_id = ?", channelID)
		}
		switch status {
		case "open", "summarizing", "summarized", "skipped", "failed":
			tx = tx.Where("status = ?", status)
		}
		switch issues {
		case "any":
			tx = tx.Where("JSON_LENGTH(issues) > 0")
		case "high":
			tx = tx.Where("JSON_SEARCH(issues, 'one', 'high', NULL, '$[*].severity') IS NOT NULL")
		}
		if alerted {
			tx = tx.Where("alerted_at IS NOT NULL")
		}
		if t, err := time.Parse("2006-01-02", from); err == nil {
			tx = tx.Where("last_activity_at >= ?", t)
		}
		if t, err := time.Parse("2006-01-02", to); err == nil {
			tx = tx.Where("last_activity_at < ?", t.AddDate(0, 0, 1))
		}
		return tx
	}
	var total int64
	scope(db.DB.Model(&models.ChatSession{})).Count(&total)
	var rows []models.ChatSession
	scope(db.DB.Model(&models.ChatSession{})).Order("last_activity_at DESC").
		Offset((page - 1) * perPage).Limit(perPage).Find(&rows)
	c.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "per_page": perPage})
}

// GetChatSessionMessages returns a session's transcript.
func GetChatSessionMessages(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var sess models.ChatSession
	if err := db.DB.Where("tenant_id = ? AND id = ?", tenantID, c.Param("sessionId")).First(&sess).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	msgs, err := chatmemory.SessionMessages(c.Request.Context(), sess)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"session": sess, "messages": []interface{}{}})
		return
	}
	type line struct {
		ID          string    `json:"id"`
		Speaker     string    `json:"speaker"` // customer | bot | staff
		SenderName  string    `json:"sender_name"`
		Content     string    `json:"content"`
		ContentType string    `json:"content_type"`
		SentAt      time.Time `json:"sent_at"`
	}
	out := make([]line, 0, len(msgs))
	for _, m := range msgs {
		speaker := "staff"
		if m.SenderType == "customer" {
			speaker = "customer"
		} else if chatmemory.IsBotMessage(m) {
			speaker = "bot"
		}
		out = append(out, line{ID: m.ID, Speaker: speaker, SenderName: m.SenderName, Content: m.Content, ContentType: m.ContentType, SentAt: m.SentAt})
	}
	c.JSON(http.StatusOK, gin.H{"session": sess, "messages": out})
}

// ResummarizeChatSession queues a session to be summarized again (after a
// prompt change, or a failed attempt). The profile counts it again.
func ResummarizeChatSession(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	res := db.DB.Model(&models.ChatSession{}).
		Where("tenant_id = ? AND id = ? AND status IN ?", tenantID, c.Param("sessionId"), []string{"summarized", "skipped", "failed"}).
		Updates(map[string]interface{}{"status": "open", "attempts": 0, "summary_error": "", "indexed_at": nil, "updated_at": time.Now()})
	if res.Error != nil || res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found_or_busy"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------------------------------------------------------------- prompts --

type taskPromptView struct {
	engine.TaskPrompt
	Value      string `json:"value"`
	Customized bool   `json:"customized"`
}

// ListTaskPrompts lists the editable AI task prompts with the tenant's text.
func ListTaskPrompts(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	prompts := engine.TaskPrompts()
	keys := make([]string, 0, len(prompts))
	for _, p := range prompts {
		keys = append(keys, p.SettingKey())
	}
	var settings []models.AppSetting
	db.DB.Where("tenant_id = ? AND setting_key IN ?", tenantID, keys).Find(&settings)
	overrides := map[string]string{}
	for _, s := range settings {
		overrides[s.SettingKey] = s.ValuePlain
	}
	out := make([]taskPromptView, 0, len(prompts))
	for _, p := range prompts {
		v := taskPromptView{TaskPrompt: p, Value: p.Instructions}
		if o := strings.TrimSpace(overrides[p.SettingKey()]); o != "" {
			v.Value, v.Customized = o, true
		}
		out = append(out, v)
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// SaveTaskPrompt stores the tenant's instructions for one task prompt. An
// empty value, or the default text, removes the override.
func SaveTaskPrompt(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	p, ok := engine.LookupTaskPrompt(c.Param("key"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown_prompt"})
		return
	}
	var req struct {
		Value string `json:"value"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	value := strings.TrimSpace(req.Value)
	if len(value) > 20000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "prompt_too_long"})
		return
	}
	old := getPlainSetting(tenantID, p.SettingKey())
	var err error
	if value == "" || value == strings.TrimSpace(p.Instructions) {
		value = ""
		err = db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, p.SettingKey()).Delete(&models.AppSetting{}).Error
	} else {
		now := time.Now()
		err = db.DB.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "setting_key"}},
			DoUpdates: clause.Assignments(map[string]interface{}{"value_plain": value, "value_encrypted": nil, "updated_at": now}),
		}).Create(&models.AppSetting{ID: pkg.NewUUID(), TenantID: tenantID, SettingKey: p.SettingKey(), ValuePlain: value, CreatedAt: now, UpdatedAt: now}).Error
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save_failed"})
		return
	}
	if value != old {
		db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "ai.task_prompt_updated", "settings", p.SettingKey(),
			fmt.Sprintf("Prompt %s: hash %s→%s", p.Key, shortHash(old), shortHash(value)), "", c.ClientIP())
	}
	resp := taskPromptView{TaskPrompt: p, Value: p.Instructions}
	if value != "" {
		resp.Value, resp.Customized = value, true
	}
	c.JSON(http.StatusOK, resp)
}
