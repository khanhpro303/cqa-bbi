package handlers

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"gorm.io/gorm"
)

// Campaign image upload limits (mirrored in the frontend MessageComposer).
const (
	maxCampaignImages   = 5
	maxCampaignImageMB  = 2
	campaignFilesBase   = "/var/lib/cqa/files"
	campaignImageSubdir = "campaigns"
)

// allowedCampaignImageExt maps accepted file extensions used for both naming and
// validation. jpg/jpeg/png/webp only.
var allowedCampaignImageExt = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".webp": true,
}

// ---- Response/request DTOs (camelCase to match frontend crm-campaigns/types.ts) ----

type campaignMessageDTO struct {
	Text         string   `json:"text"`
	Link         string   `json:"link,omitempty"`
	Images       []string `json:"images,omitempty"`
	ReminderText string   `json:"reminderText,omitempty"`
}

type campaignSegmentDTO struct {
	ID           string `json:"id"`
	GroupID      string `json:"groupId"`
	GroupName    string `json:"groupName"`
	ScheduleKind string `json:"scheduleKind"`
	Cron         string `json:"cron,omitempty"`
	RunAt        string `json:"runAt,omitempty"`
	NextRunAt    string `json:"nextRunAt,omitempty"`
}

type campaignDTO struct {
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	Description   string               `json:"description,omitempty"`
	ChannelID     string               `json:"channelId"`
	Status        string               `json:"status"`
	Message       campaignMessageDTO   `json:"message"`
	Segments      []campaignSegmentDTO `json:"segments"`
	SentThisMonth int                  `json:"sentThisMonth"`
	CreatedAt     string               `json:"createdAt"`
}

type campaignFormSegment struct {
	ID           string `json:"id"`
	GroupID      string `json:"groupId"`
	GroupName    string `json:"groupName"`
	ScheduleKind string `json:"scheduleKind"`
	Cron         string `json:"cron"`
	RunAt        string `json:"runAt"`
}

type campaignFormBody struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	ChannelID   string `json:"channelId" binding:"required"`
	Message     struct {
		Text         string   `json:"text"`
		Link         string   `json:"link"`
		Images       []string `json:"images"`
		ReminderText string   `json:"reminderText"`
	} `json:"message"`
	Segments []campaignFormSegment `json:"segments"`
}

// ---- Handlers ----

// ListCampaigns returns all campaigns for the tenant, shaped for the frontend.
func ListCampaigns(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	var campaigns []models.Campaign
	if err := db.DB.Preload("Segments").Where("tenant_id = ?", tenantID).
		Order("created_at DESC").Find(&campaigns).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_list_campaigns"})
		return
	}

	groupNames := loadGroupNames(tenantID)
	sentByCampaign := sentThisMonthByCampaign(tenantID)

	out := make([]campaignDTO, 0, len(campaigns))
	for i := range campaigns {
		out = append(out, toCampaignDTO(&campaigns[i], groupNames, sentByCampaign[campaigns[i].ID]))
	}
	c.JSON(http.StatusOK, out)
}

// CreateCampaign persists a new campaign (status draft) with its segments.
func CreateCampaign(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	var body campaignFormBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}
	images := sanitizeCampaignImages(body.Message.Images)
	if len(images) > maxCampaignImages {
		c.JSON(http.StatusBadRequest, gin.H{"error": "too_many_images"})
		return
	}

	now := time.Now()
	campaign := models.Campaign{
		ID:                  uuid.New().String(),
		TenantID:            tenantID,
		Name:                body.Name,
		Description:         body.Description,
		ChannelID:           body.ChannelID,
		Status:              "draft",
		MessageText:         body.Message.Text,
		MessageLink:         body.Message.Link,
		MessageImages:       images,
		MessageReminderText: body.Message.ReminderText,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	campaign.Segments = buildSegments(campaign.ID, body.Segments, tenantID)

	if err := db.DB.Create(&campaign).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_create_campaign"})
		return
	}
	reloadCampaignScheduler()

	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "campaign.create", "campaign", campaign.ID,
		fmt.Sprintf("Tạo chiến dịch '%s' (%d phân khúc)", campaign.Name, len(campaign.Segments)), "", c.ClientIP())

	groupNames := loadGroupNames(tenantID)
	c.JSON(http.StatusCreated, toCampaignDTO(&campaign, groupNames, 0))
}

// UpdateCampaign replaces an existing campaign's fields and segments.
func UpdateCampaign(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	id := c.Param("id")

	var body campaignFormBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}
	images := sanitizeCampaignImages(body.Message.Images)
	if len(images) > maxCampaignImages {
		c.JSON(http.StatusBadRequest, gin.H{"error": "too_many_images"})
		return
	}

	var campaign models.Campaign
	if err := db.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&campaign).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "campaign_not_found"})
		return
	}

	campaign.Name = body.Name
	campaign.Description = body.Description
	campaign.ChannelID = body.ChannelID
	campaign.MessageText = body.Message.Text
	campaign.MessageLink = body.Message.Link
	campaign.MessageImages = images
	campaign.MessageReminderText = body.Message.ReminderText
	campaign.MessageImageName = "" // migrated to MessageImages
	campaign.UpdatedAt = time.Now()

	// If an active campaign is repointed at a missing/disabled channel, demote it to
	// paused — otherwise it would stay "active" while every scheduled send dies at
	// the channel guard (channel_inactive). Mirrors the activation block in
	// SetCampaignStatus so the two paths can never disagree.
	autoPaused := false
	if campaign.Status == "active" {
		if err := assertCampaignChannelActive(campaign.ChannelID, tenantID); err != nil &&
			(errors.Is(err, engine.ErrCampaignChannelInactive) || errors.Is(err, engine.ErrCampaignChannelNotFound)) {
			campaign.Status = "paused"
			autoPaused = true
		}
	}

	err := db.DB.Transaction(func(tx *gorm.DB) error {
		if e := tx.Save(&campaign).Error; e != nil {
			return e
		}
		if e := tx.Where("campaign_id = ?", campaign.ID).Delete(&models.CampaignSegment{}).Error; e != nil {
			return e
		}
		segs := buildSegments(campaign.ID, body.Segments, tenantID)
		if len(segs) > 0 {
			if e := tx.Create(&segs).Error; e != nil {
				return e
			}
		}
		campaign.Segments = segs
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_update_campaign"})
		return
	}
	reloadCampaignScheduler()

	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "campaign.update", "campaign", campaign.ID,
		fmt.Sprintf("Cập nhật chiến dịch '%s'", campaign.Name), "", c.ClientIP())
	if autoPaused {
		db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "campaign.auto_paused", "campaign", campaign.ID,
			fmt.Sprintf("Chiến dịch '%s' tự chuyển sang tạm dừng: kênh Zalo OA đã tắt", campaign.Name), "", c.ClientIP())
	}

	groupNames := loadGroupNames(tenantID)
	c.JSON(http.StatusOK, toCampaignDTO(&campaign, groupNames, sentThisMonthByCampaign(tenantID)[campaign.ID]))
}

// DeleteCampaign removes a campaign together with its segments and run logs.
func DeleteCampaign(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	id := c.Param("id")

	var campaign models.Campaign
	if err := db.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&campaign).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "campaign_not_found"})
		return
	}

	if err := db.DB.Transaction(func(tx *gorm.DB) error {
		if e := tx.Where("campaign_id = ?", id).Delete(&models.CampaignSegment{}).Error; e != nil {
			return e
		}
		if e := tx.Where("campaign_id = ? AND tenant_id = ?", id, tenantID).Delete(&models.CampaignRun{}).Error; e != nil {
			return e
		}
		return tx.Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&models.Campaign{}).Error
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_delete_campaign"})
		return
	}
	reloadCampaignScheduler()

	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "campaign.delete", "campaign", id,
		fmt.Sprintf("Xóa chiến dịch '%s'", campaign.Name), "", c.ClientIP())

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// SetCampaignStatus toggles draft/active/paused/done.
func SetCampaignStatus(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	id := c.Param("id")

	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	switch req.Status {
	case "draft", "active", "paused", "done":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_status"})
		return
	}

	var campaign models.Campaign
	if err := db.DB.Preload("Segments").Where("id = ? AND tenant_id = ?", id, tenantID).First(&campaign).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "campaign_not_found"})
		return
	}

	// Refuse to activate a campaign whose Zalo OA channel is missing or disabled.
	// Without this guard the scheduler still registers the segment jobs and they
	// fire on schedule, but every send dies at the channel guard in
	// engine.prepareCampaignBroadcast (channel_inactive) — the campaign looks
	// "active" while silently sending nothing. Block it at the source instead.
	if req.Status == "active" {
		if err := assertCampaignChannelActive(campaign.ChannelID, tenantID); err != nil {
			switch {
			case errors.Is(err, engine.ErrCampaignChannelNotFound):
				c.JSON(http.StatusConflict, gin.H{"error": "channel_not_found"})
			case errors.Is(err, engine.ErrCampaignChannelInactive):
				c.JSON(http.StatusConflict, gin.H{"error": "channel_inactive"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_check_channel"})
			}
			return
		}
	}

	oldStatus := campaign.Status
	campaign.Status = req.Status
	campaign.UpdatedAt = time.Now()
	if err := db.DB.Model(&campaign).Updates(map[string]interface{}{"status": req.Status, "updated_at": campaign.UpdatedAt}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_update_status"})
		return
	}
	reloadCampaignScheduler()

	if req.Status != oldStatus {
		db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "campaign.status_changed", "campaign", id,
			fmt.Sprintf("Chiến dịch '%s': trạng thái %s→%s", campaign.Name, oldStatus, req.Status), "", c.ClientIP())
	}

	groupNames := loadGroupNames(tenantID)
	c.JSON(http.StatusOK, toCampaignDTO(&campaign, groupNames, sentThisMonthByCampaign(tenantID)[campaign.ID]))
}

// SendNow broadcasts the campaign message to each segment's GMF group immediately.
// It refuses to send if the campaign's Zalo OA channel is inactive.
func SendNow(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	id := c.Param("id")

	sent, fail, err := engine.SendCampaignNow(c.Request.Context(), id, tenantID)
	if err != nil {
		switch {
		case errors.Is(err, engine.ErrCampaignNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "campaign_not_found"})
		case errors.Is(err, engine.ErrCampaignChannelNotFound):
			c.JSON(http.StatusConflict, gin.H{"error": "channel_not_found"})
		case errors.Is(err, engine.ErrCampaignChannelInactive):
			c.JSON(http.StatusConflict, gin.H{"error": "channel_inactive"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "campaign.send_now", "campaign", id,
		fmt.Sprintf("Gửi chiến dịch ngay: %d thành công, %d lỗi", sent, fail), "", c.ClientIP())

	c.JSON(http.StatusOK, gin.H{"sent": sent, "fail": fail})
}

// UploadCampaignImages accepts up to maxCampaignImages image files (multipart
// field "images"), validates and stores each under
// /var/lib/cqa/files/<tenant>/campaigns/<uuid>.<ext>, and returns the relative
// paths so the frontend can attach them to the campaign payload and render them
// via the existing GET /api/v1/files/<path> route.
func UploadCampaignImages(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_multipart_form"})
		return
	}
	files := form.File["images"]
	if len(files) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no_images"})
		return
	}
	if len(files) > maxCampaignImages {
		c.JSON(http.StatusBadRequest, gin.H{"error": "too_many_images"})
		return
	}

	destDir := filepath.Join(campaignFilesBase, tenantID, campaignImageSubdir)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_prepare_storage"})
		return
	}

	saved := make([]string, 0, len(files))
	for _, fh := range files {
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		if !allowedCampaignImageExt[ext] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported_image_type", "filename": fh.Filename})
			return
		}
		if fh.Size > maxCampaignImageMB*1024*1024 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "image_too_large", "filename": fh.Filename})
			return
		}
		relPath, err := saveCampaignImage(fh, destDir, tenantID, ext)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_save_image", "filename": fh.Filename})
			return
		}
		saved = append(saved, relPath)
	}

	c.JSON(http.StatusOK, gin.H{"images": saved})
}

// saveCampaignImage writes one uploaded file to destDir under a uuid name and
// returns its tenant-relative path (e.g. "<tenant>/campaigns/<uuid>.jpg").
func saveCampaignImage(fh *multipart.FileHeader, destDir, tenantID, ext string) (string, error) {
	src, err := fh.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()

	name := uuid.New().String() + ext
	fullPath := filepath.Join(destDir, name)
	dst, err := os.Create(fullPath)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/%s/%s", tenantID, campaignImageSubdir, name), nil
}

// sanitizeCampaignImages trims, drops blanks, and de-duplicates the image path
// list coming from the form body.
func sanitizeCampaignImages(in []string) models.JSONStringSlice {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make(models.JSONStringSlice, 0, len(in))
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

const maxChartDays = 31

// GetCampaignStats aggregates CampaignRun rows for the dashboard.
func GetCampaignStats(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	from := c.Query("from")
	to := c.Query("to")
	campaignID := c.Query("campaignId")

	loc := tenantLocation(tenantID)
	start, end := parseDateRange(from, to, loc)

	runQuery := db.DB.Model(&models.CampaignRun{}).
		Where("tenant_id = ? AND started_at >= ? AND started_at <= ?", tenantID, start, end)
	if campaignID != "" {
		runQuery = runQuery.Where("campaign_id = ?", campaignID)
	}
	var runs []models.CampaignRun
	runQuery.Order("started_at DESC").Find(&runs)

	sentTotal, failTotal := 0, 0
	perDay := map[string]int{}
	for _, r := range runs {
		sentTotal += r.SentCount
		failTotal += r.FailCount
		day := r.StartedAt.In(loc).Format("2006-01-02")
		perDay[day] += r.SentCount
	}

	byDay := buildByDay(start, end, loc, perDay)

	successRate := 100.0
	if sentTotal+failTotal > 0 {
		successRate = float64(sentTotal) / float64(sentTotal+failTotal) * 100
	}

	stats := gin.H{
		"messagesSentThisMonth": sentTotal,
		"successRate":           successRate,
		"byDay":                 byDay,
	}

	if campaignID != "" {
		var campaign models.Campaign
		if err := db.DB.Preload("Segments").Where("id = ? AND tenant_id = ?", campaignID, tenantID).First(&campaign).Error; err != nil {
			c.JSON(http.StatusOK, emptyStats())
			return
		}
		stats["campaignsThisMonth"] = len(campaign.Segments)
		stats["upcomingRuns"] = countUpcomingRuns([]models.Campaign{campaign})
		lastRun := latestRunAt(runs)
		stats["recent"] = []gin.H{{
			"id":        campaign.ID,
			"name":      campaign.Name,
			"status":    campaign.Status,
			"sent":      sentTotal,
			"fail":      failTotal,
			"lastRunAt": lastRun,
		}}
		c.JSON(http.StatusOK, stats)
		return
	}

	var campaigns []models.Campaign
	db.DB.Preload("Segments").Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&campaigns)
	stats["campaignsThisMonth"] = len(campaigns)
	stats["upcomingRuns"] = countUpcomingRuns(campaigns)
	stats["recent"] = recentRows(campaigns, runs)

	c.JSON(http.StatusOK, stats)
}

// ---- helpers ----

func buildSegments(campaignID string, in []campaignFormSegment, tenantID string) []models.CampaignSegment {
	loc := tenantLocation(tenantID)
	out := make([]models.CampaignSegment, 0, len(in))
	for _, s := range in {
		seg := models.CampaignSegment{
			ID:           uuid.New().String(),
			CampaignID:   campaignID,
			GroupID:      s.GroupID,
			ScheduleKind: s.ScheduleKind,
			Cron:         s.Cron,
		}
		if s.ScheduleKind == "once" {
			if t := parseISO(s.RunAt); t != nil {
				seg.RunAt = t
				seg.NextRunAt = t
			}
		} else if next := nextCronRun(s.Cron, loc); next != nil {
			seg.NextRunAt = next
		}
		out = append(out, seg)
	}
	return out
}

// assertCampaignChannelActive checks that the campaign's Zalo OA channel exists
// and is active, mirroring the guard in engine.prepareCampaignBroadcast so the
// activation path and the send path agree. Returns engine.ErrCampaignChannelNotFound
// or engine.ErrCampaignChannelInactive so callers can map to the same responses.
func assertCampaignChannelActive(channelID, tenantID string) error {
	var channel models.Channel
	if err := db.DB.Where("id = ? AND tenant_id = ? AND channel_type = ?", channelID, tenantID, "zalo_oa").
		First(&channel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return engine.ErrCampaignChannelNotFound
		}
		return fmt.Errorf("check campaign channel: %w", err)
	}
	if !channel.IsActive {
		return engine.ErrCampaignChannelInactive
	}
	return nil
}

// reloadCampaignScheduler re-registers campaign segment jobs after a mutation.
func reloadCampaignScheduler() {
	if sched := engine.GetDefaultScheduler(); sched != nil {
		sched.ReloadCampaignJobs()
	}
}

func toCampaignDTO(c *models.Campaign, groupNames map[string]string, sentThisMonth int) campaignDTO {
	segs := make([]campaignSegmentDTO, 0, len(c.Segments))
	for _, s := range c.Segments {
		segs = append(segs, campaignSegmentDTO{
			ID:           s.ID,
			GroupID:      s.GroupID,
			GroupName:    groupNames[s.GroupID],
			ScheduleKind: s.ScheduleKind,
			Cron:         s.Cron,
			RunAt:        isoOrEmpty(s.RunAt),
			NextRunAt:    isoOrEmpty(s.NextRunAt),
		})
	}
	return campaignDTO{
		ID:          c.ID,
		Name:        c.Name,
		Description: c.Description,
		ChannelID:   c.ChannelID,
		Status:      c.Status,
		Message: campaignMessageDTO{
			Text:         c.MessageText,
			Link:         c.MessageLink,
			Images:       c.Images(),
			ReminderText: c.MessageReminderText,
		},
		Segments:      segs,
		SentThisMonth: sentThisMonth,
		CreatedAt:     c.CreatedAt.Format(time.RFC3339),
	}
}

func loadGroupNames(tenantID string) map[string]string {
	var groups []models.CRMGroup
	db.DB.Select("id", "name").Where("tenant_id = ?", tenantID).Find(&groups)
	m := make(map[string]string, len(groups))
	for _, g := range groups {
		m[g.ID] = g.Name
	}
	return m
}

// sentThisMonthByCampaign sums successful sends in the current calendar month.
func sentThisMonthByCampaign(tenantID string) map[string]int {
	loc := tenantLocation(tenantID)
	now := time.Now().In(loc)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)

	type row struct {
		CampaignID string
		Total      int
	}
	var rows []row
	db.DB.Model(&models.CampaignRun{}).
		Select("campaign_id, SUM(sent_count) as total").
		Where("tenant_id = ? AND started_at >= ?", tenantID, monthStart).
		Group("campaign_id").Scan(&rows)

	m := make(map[string]int, len(rows))
	for _, r := range rows {
		m[r.CampaignID] = r.Total
	}
	return m
}

func recentRows(campaigns []models.Campaign, runs []models.CampaignRun) []gin.H {
	sentByCampaign := map[string]int{}
	failByCampaign := map[string]int{}
	lastByCampaign := map[string]time.Time{}
	for _, r := range runs {
		sentByCampaign[r.CampaignID] += r.SentCount
		failByCampaign[r.CampaignID] += r.FailCount
		if r.StartedAt.After(lastByCampaign[r.CampaignID]) {
			lastByCampaign[r.CampaignID] = r.StartedAt
		}
	}

	limit := len(campaigns)
	if limit > 5 {
		limit = 5
	}
	out := make([]gin.H, 0, limit)
	for i := 0; i < limit; i++ {
		c := campaigns[i]
		var lastRunAt interface{}
		if t, ok := lastByCampaign[c.ID]; ok {
			lastRunAt = t.Format(time.RFC3339)
		}
		out = append(out, gin.H{
			"id":        c.ID,
			"name":      c.Name,
			"status":    c.Status,
			"sent":      sentByCampaign[c.ID],
			"fail":      failByCampaign[c.ID],
			"lastRunAt": lastRunAt,
		})
	}
	return out
}

func countUpcomingRuns(campaigns []models.Campaign) int {
	now := time.Now()
	count := 0
	for _, c := range campaigns {
		if c.Status != "active" {
			continue
		}
		for _, s := range c.Segments {
			if s.NextRunAt != nil && s.NextRunAt.After(now) {
				count++
			}
		}
	}
	return count
}

func buildByDay(start, end time.Time, loc *time.Location, perDay map[string]int) []gin.H {
	days := []time.Time{}
	cur := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)
	last := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, loc)
	for !cur.After(last) && len(days) < maxChartDays {
		days = append(days, cur)
		cur = cur.AddDate(0, 0, 1)
	}
	out := make([]gin.H, 0, len(days))
	for _, d := range days {
		key := d.Format("2006-01-02")
		out = append(out, gin.H{"date": key, "sent": perDay[key]})
	}
	return out
}

func latestRunAt(runs []models.CampaignRun) interface{} {
	if len(runs) == 0 {
		return nil
	}
	latest := runs[0].StartedAt
	for _, r := range runs[1:] {
		if r.StartedAt.After(latest) {
			latest = r.StartedAt
		}
	}
	return latest.Format(time.RFC3339)
}

func emptyStats() gin.H {
	return gin.H{
		"campaignsThisMonth":    0,
		"messagesSentThisMonth": 0,
		"successRate":           0,
		"upcomingRuns":          0,
		"byDay":                 []gin.H{},
		"recent":                []gin.H{},
	}
}

// nextCronRun computes the next fire time for a 5-field cron expression in loc.
func nextCronRun(expr string, loc *time.Location) *time.Time {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(expr)
	if err != nil {
		return nil
	}
	next := sched.Next(time.Now().In(loc))
	if next.IsZero() {
		return nil
	}
	return &next
}

func parseISO(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t
	}
	return nil
}

func isoOrEmpty(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

// parseDateRange turns YYYY-MM-DD bounds into [start 00:00, end 23:59:59] in loc.
// Falls back to the trailing 14 days when inputs are missing/invalid.
func parseDateRange(from, to string, loc *time.Location) (time.Time, time.Time) {
	now := time.Now().In(loc)
	end := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, loc)
	start := end.AddDate(0, 0, -13)

	if t, err := time.ParseInLocation("2006-01-02", to, loc); err == nil {
		end = time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, loc)
	}
	if t, err := time.ParseInLocation("2006-01-02", from, loc); err == nil {
		start = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	}
	if start.After(end) {
		start = end.AddDate(0, 0, -13)
	}
	return start, end
}

// tenantLocation resolves the tenant timezone from app settings (default ICT).
func tenantLocation(tenantID string) *time.Location {
	var setting models.AppSetting
	tz := "Asia/Ho_Chi_Minh"
	if err := db.DB.Where("tenant_id = ? AND setting_key = 'timezone'", tenantID).First(&setting).Error; err == nil && setting.ValuePlain != "" {
		tz = setting.ValuePlain
	}
	if loc, err := time.LoadLocation(tz); err == nil {
		return loc
	}
	return time.UTC
}
