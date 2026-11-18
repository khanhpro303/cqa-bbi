package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
	"github.com/vietbui/chat-quality-agent/servicequality"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func GetServiceQuality(c *gin.Context) {
	tid := middleware.GetTenantID(c)
	p, err := servicequality.LoadPolicy(db.DB.WithContext(c.Request.Context()), tid)
	if err != nil {
		serviceQualityError(c, err)
		return
	}
	now := time.Now()
	from, to, err := servicequality.DateWindow(c.Query("from"), c.Query("to"), now, p)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	r, err := servicequality.Build(c.Request.Context(), db.DB, tid, c.Query("channel_id"), now, from, to, p)
	if errors.Is(err, servicequality.ErrReportTooLarge) && r != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error(), "pages": r.Pages})
		return
	}
	if err != nil {
		serviceQualityError(c, err)
		return
	}
	job, err := findProductGroupingJob(c.Request.Context(), db.DB, tid)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		serviceQualityError(c, err)
		return
	}
	version := ""
	if err == nil {
		version = productGroupingInputHash(nil, productGroupingPrompt(job)+job.ID)
	}
	c.JSON(http.StatusOK, struct {
		*servicequality.Report
		ProductGroupingEnabled bool   `json:"product_grouping_enabled"`
		ProductGroupingVersion string `json:"product_grouping_version"`
	}{r, err == nil, version})
}

func SaveServiceQualityPolicy(c *gin.Context) {
	var p servicequality.Policy
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cấu hình không hợp lệ"})
		return
	}
	if err := p.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tid := middleware.GetTenantID(c)
	data, _ := json.Marshal(p)
	now := time.Now()
	s := models.AppSetting{ID: pkg.NewUUID(), TenantID: tid, SettingKey: servicequality.PolicyKey, ValuePlain: string(data), CreatedAt: now, UpdatedAt: now}
	err := db.DB.WithContext(c.Request.Context()).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "setting_key"}}, DoUpdates: clause.Assignments(map[string]interface{}{"value_plain": s.ValuePlain, "updated_at": now})}).Create(&s).Error
	if err != nil {
		serviceQualityError(c, err)
		return
	}
	db.LogActivity(tid, middleware.GetUserID(c), middleware.GetUserEmail(c), "service_quality.policy_updated", "settings", servicequality.PolicyKey, string(data), "", c.ClientIP())
	c.JSON(http.StatusOK, p)
}

func ResolveServiceConversation(c *gin.Context) {
	var req struct {
		ThroughMessageID string `json:"through_message_id"`
		Note             string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.ThroughMessageID == "" || strings.TrimSpace(req.Note) == "" || len([]rune(req.Note)) > 500 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cần tin nhắn đã xem và lý do xử lý (tối đa 500 ký tự)"})
		return
	}
	tid := middleware.GetTenantID(c)
	cid := c.Param("conversationId")
	conflict := errors.New("hội thoại đã có thay đổi; tải lại danh sách trước khi xử lý")
	err := db.DB.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var conv models.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", cid, tid).First(&conv).Error; err != nil {
			return err
		}
		var channel models.Channel
		if err := tx.Where("id = ? AND tenant_id = ? AND channel_type = ?", conv.ChannelID, tid, "facebook").First(&channel).Error; err != nil {
			return err
		}
		var messages []models.Message
		if err := tx.Where("conversation_id = ? AND tenant_id = ?", cid, tid).Order("sent_at ASC, id ASC").Find(&messages).Error; err != nil {
			return err
		}
		var resolutions []models.ServiceResolution
		if err := tx.Where("conversation_id = ? AND tenant_id = ?", cid, tid).Find(&resolutions).Error; err != nil {
			return err
		}
		p, err := servicequality.LoadPolicy(tx, tid)
		if err != nil {
			return err
		}
		now := time.Now()
		timeline := servicequality.Calculate(messages, resolutions, now, p)
		if timeline.Pending == nil || timeline.Pending.LastCustomerID != req.ThroughMessageID {
			return conflict
		}
		r := models.ServiceResolution{ID: pkg.NewUUID(), TenantID: tid, ConversationID: cid, ThroughMessageID: req.ThroughMessageID, ThroughAt: timeline.Pending.LastCustomerAt, ResolvedAt: now, ResolvedBy: middleware.GetUserID(c), Note: strings.TrimSpace(req.Note)}
		return tx.Create(&r).Error
	})
	if errors.Is(err, conflict) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		serviceQualityError(c, err)
		return
	}
	db.LogActivity(tid, middleware.GetUserID(c), middleware.GetUserEmail(c), "service_quality.resolved", "conversation", cid, req.Note, "", c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"status": "resolved"})
}

func ReanalyseServiceConversation(c *gin.Context) {
	tid := middleware.GetTenantID(c)
	cid := c.Param("conversationId")

	var req struct {
		JobID string `json:"job_id"`
	}
	_ = c.ShouldBindJSON(&req)

	var conv models.Conversation
	if err := db.DB.WithContext(c.Request.Context()).Where("id = ? AND tenant_id = ?", cid, tid).First(&conv).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "hội thoại không tồn tại"})
			return
		}
		serviceQualityError(c, err)
		return
	}

	var job models.Job
	targetJobID := strings.TrimSpace(req.JobID)
	if targetJobID != "" {
		if err := db.DB.WithContext(c.Request.Context()).Where("id = ? AND tenant_id = ?", targetJobID, tid).First(&job).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "không tìm thấy tác vụ đã chỉ định"})
			return
		}
	} else {
		type prevJobRecord struct {
			JobID string `gorm:"column:job_id"`
		}
		var prev prevJobRecord
		_ = db.DB.WithContext(c.Request.Context()).Table("job_results AS j").
			Select("jr.job_id").
			Joins("JOIN job_runs AS jr ON jr.id = j.job_run_id").
			Where("j.tenant_id = ? AND j.conversation_id = ? AND j.result_type = ?", tid, cid, "conversation_insight").
			Order("j.created_at DESC, j.id DESC").
			Limit(1).Scan(&prev)

		if prev.JobID != "" {
			_ = db.DB.WithContext(c.Request.Context()).Where("id = ? AND tenant_id = ?", prev.JobID, tid).First(&job).Error
		}
	}

	if job.ID == "" || job.JobType != "classification" || !ai.ParseClassificationConfig(job.RulesConfig).MessengerInsightsEnabled() {
		var candidates []models.Job
		if err := db.DB.WithContext(c.Request.Context()).Where("tenant_id = ? AND job_type = ? AND is_active = ?", tid, "classification", true).Find(&candidates).Error; err == nil {
			for _, candidate := range candidates {
				if !ai.ParseClassificationConfig(candidate.RulesConfig).MessengerInsightsEnabled() {
					continue
				}
				var channels []string
				_ = json.Unmarshal([]byte(candidate.InputChannelIDs), &channels)
				hasChannel := false
				for _, ch := range channels {
					if ch == conv.ChannelID || ch == "global" {
						hasChannel = true
						break
					}
				}
				if hasChannel {
					job = candidate
					break
				}
				if job.ID == "" {
					job = candidate
				}
			}
		}
	}

	if job.ID == "" || job.JobType != "classification" || !ai.ParseClassificationConfig(job.RulesConfig).MessengerInsightsEnabled() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "không tìm thấy tác vụ phân tích Messenger Insights cho hội thoại này"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg, _ := config.Load()
	analyzer := engine.NewAnalyzer(cfg)
	run, err := analyzer.RunJobSingleConversation(ctx, job, cid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("phân tích thất bại: %v", err)})
		return
	}
	if run != nil && run.Status == "error" {
		msg := "phân tích thất bại: lỗi xử lý tác vụ"
		if run.ErrorMessage != "" {
			msg = fmt.Sprintf("phân tích thất bại: %s", run.ErrorMessage)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": msg})
		return
	}

	type insightRow struct {
		Detail    string    `gorm:"column:detail"`
		CreatedAt time.Time `gorm:"column:created_at"`
	}
	var latest insightRow
	if err := db.DB.WithContext(c.Request.Context()).Table("job_results").
		Select("detail, created_at").
		Where("tenant_id = ? AND conversation_id = ? AND result_type = ?", tid, cid, "conversation_insight").
		Order("created_at DESC, id DESC").
		First(&latest).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "hội thoại chưa có tin nhắn để phân tích"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "không thể tải kết quả phân tích mới"})
		return
	}

	isStale := servicequality.IsInsightStale(latest.Detail, conv.LastMessageAt)
	db.LogActivity(tid, middleware.GetUserID(c), middleware.GetUserEmail(c), "service_quality.reanalyse", "conversation", cid,
		fmt.Sprintf("Reanalysed conversation %s with job %s", cid, job.ID), "", c.ClientIP())

	c.JSON(http.StatusOK, gin.H{
		"conversation_id": cid,
		"insight":         json.RawMessage(latest.Detail),
		"insight_job_id":  job.ID,
		"insight_at":      latest.CreatedAt,
		"insight_stale":   isStale,
	})
}

func serviceQualityError(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "không tìm thấy Fanpage hoặc hội thoại"})
		return
	}
	if errors.Is(err, servicequality.ErrReportTooLarge) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": "không tải được dữ liệu CSKH; vui lòng thử lại"})
}
