package handlers

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/cloudifyorders"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

const crmOrderTrackingPageSize = 20
const crmOrderEvidenceMaxBytes = 5 << 20
const crmOrderEvidenceMaxPixels = 16_000_000

type crmOrderConfirmation struct {
	SenderName     string    `json:"sender_name"`
	SenderID       string    `json:"sender_id"`
	Text           string    `json:"text"`
	MessageID      string    `json:"message_id"`
	QuoteMessageID string    `json:"quote_message_id"`
	ConfirmedAt    time.Time `json:"confirmed_at"`
	HasEvidence    bool      `json:"has_evidence"`
}

type crmTrackedOrder struct {
	ID           string                `json:"id"`
	OrderCode    string                `json:"order_code"`
	CustomerCode string                `json:"customer_code"`
	CustomerName string                `json:"customer_name"`
	GroupName    string                `json:"group_name"`
	OrderDate    string                `json:"order_date"`
	Status       string                `json:"status"`
	StatusName   string                `json:"status_name" gorm:"-"`
	Total        float64               `json:"total"`
	DeliveredAt  time.Time             `json:"delivered_at"`
	Confirmation *crmOrderConfirmation `json:"confirmation,omitempty" gorm:"-"`

	ConfirmedAt    *time.Time `json:"-"`
	SenderName     string     `json:"-"`
	SenderID       string     `json:"-"`
	Text           string     `json:"-"`
	MessageID      string     `json:"-"`
	QuoteMessageID string     `json:"-"`
	HasEvidence    bool       `json:"-"`
}

func ListCRMOrderTracking(c *gin.Context) {
	page, err := strconv.ParseInt(c.DefaultQuery("page", "1"), 10, 32)
	search := strings.TrimSpace(c.Query("q"))
	confirmation := c.DefaultQuery("confirmation", "all")
	if err != nil || page < 1 || len(search) > 256 ||
		(confirmation != "all" && confirmation != "pending" && confirmation != "confirmed") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_order_tracking_filter"})
		return
	}

	query := db.DB.WithContext(c.Request.Context()).Table("cloudify_order_confirmations AS tracking").
		Where("tracking.tenant_id = ? AND tracking.delivered_at IS NOT NULL", middleware.GetTenantID(c))
	switch confirmation {
	case "pending":
		query = query.Where("tracking.confirmed_at IS NULL")
	case "confirmed":
		query = query.Where("tracking.confirmed_at IS NOT NULL")
	}
	if search != "" {
		literal := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(search) + "%"
		query = query.Where(`(tracking.order_code LIKE ? ESCAPE '!' OR tracking.customer_code LIKE ? ESCAPE '!'
			OR tracking.customer_name LIKE ? ESCAPE '!' OR tracking.group_name LIKE ? ESCAPE '!')`, literal, literal, literal, literal)
	}

	var count int64
	if err := query.Count(&count).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "order_tracking_unavailable"})
		return
	}
	rows := make([]crmTrackedOrder, 0)
	offset := (page - 1) * crmOrderTrackingPageSize
	if offset < count {
		err := query.Select(`tracking.id, tracking.order_code, tracking.customer_code, tracking.customer_name, tracking.group_name,
			tracking.delivered_at, tracking.confirmed_at, tracking.sender_name, tracking.sender_id, tracking.text,
			tracking.message_id, tracking.quote_message_id, COALESCE(tracking.evidence_content_type, '') <> '' AS has_evidence,
			COALESCE(snapshot.order_date, outbox.order_date, '') AS order_date,
			COALESCE(snapshot.status, outbox.status, '') AS status, COALESCE(snapshot.total, outbox.total, 0) AS total`).
			Joins("LEFT JOIN cloudify_order_events AS outbox ON outbox.id = tracking.event_id AND outbox.tenant_id = tracking.tenant_id").
			Joins(`LEFT JOIN cloudify_order_snapshots AS snapshot ON snapshot.tenant_id = tracking.tenant_id
				AND snapshot.order_id = tracking.order_id AND snapshot.order_code = tracking.order_code
				AND snapshot.customer_code = tracking.customer_code`).
			Order("tracking.delivered_at DESC, tracking.id DESC").
			Offset(int(offset)).Limit(crmOrderTrackingPageSize).Scan(&rows).Error
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "order_tracking_unavailable"})
			return
		}
	}
	for i := range rows {
		row := &rows[i]
		row.Status = cloudifyorders.NormalizeStatus(row.Status)
		row.StatusName = cloudifyorders.StatusName(row.Status)
		if row.ConfirmedAt != nil {
			row.Confirmation = &crmOrderConfirmation{
				SenderName: row.SenderName, SenderID: row.SenderID, Text: row.Text, MessageID: row.MessageID,
				QuoteMessageID: row.QuoteMessageID, ConfirmedAt: *row.ConfirmedAt, HasEvidence: row.HasEvidence,
			}
		}
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, gin.H{"data": rows, "total": count, "page": page, "page_size": crmOrderTrackingPageSize})
}

func GetCRMTrackedOrder(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var row models.CloudifyOrderConfirmation
		query := trackedOrderQuery(c).Select("tenant_id", "order_id", "order_code", "customer_code")
		if !readCRMTrackedOrder(c, query, &row) {
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()
		details, err := cloudifyorders.LookupTrackedOrder(ctx, db.DB, row, cfg.EncryptionKey)
		switch {
		case errors.Is(err, cloudifyorders.ErrTrackedOrderNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "cloudify_order_not_found"})
		case errors.Is(err, cloudifyorders.ErrNotConfigured):
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "cloudify_not_configured"})
		case err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)):
			c.JSON(http.StatusGatewayTimeout, gin.H{"error": "cloudify_order_timeout"})
		case err != nil:
			c.JSON(http.StatusBadGateway, gin.H{"error": "cloudify_order_unavailable"})
		default:
			c.Header("Cache-Control", "private, no-store")
			c.JSON(http.StatusOK, gin.H{"data": details})
		}
	}
}

func GetCRMOrderConfirmationEvidence(c *gin.Context) {
	var row models.CloudifyOrderConfirmation
	query := trackedOrderQuery(c).Where("confirmed_at IS NOT NULL").Select("evidence_image", "evidence_content_type")
	if !readCRMTrackedOrder(c, query, &row) {
		return
	}
	extension := ""
	switch row.EvidenceContentType {
	case "image/png":
		extension = "png"
	case "image/jpeg":
		extension = "jpg"
	}
	if len(row.EvidenceImage) == 0 || extension == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "order_confirmation_evidence_not_found"})
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", `inline; filename="xac-nhan-zalo.`+extension+`"`)
	c.Data(http.StatusOK, row.EvidenceContentType, row.EvidenceImage)
}

func UploadCRMOrderConfirmationEvidence(c *gin.Context) {
	query := trackedOrderQuery(c).Where("confirmed_at IS NOT NULL")
	var row models.CloudifyOrderConfirmation
	if !readCRMTrackedOrder(c, query.Session(&gorm.Session{}).Select("id", "evidence_content_type"), &row) {
		return
	}
	if row.EvidenceContentType != "" {
		c.JSON(http.StatusConflict, gin.H{"error": "order_confirmation_evidence_exists"})
		return
	}

	data, contentType, status := readOrderConfirmationScreenshot(c)
	if status != http.StatusOK {
		code := "invalid_order_confirmation_screenshot"
		if status == http.StatusRequestEntityTooLarge {
			code = "order_confirmation_screenshot_too_large"
		}
		c.JSON(status, gin.H{"error": code})
		return
	}
	result := query.Model(&models.CloudifyOrderConfirmation{}).
		Where("COALESCE(evidence_content_type, '') = ''").
		Updates(map[string]any{
			"evidence_image": data, "evidence_content_type": contentType,
			"evidence_uploaded_by": middleware.GetUserID(c), "evidence_uploaded_at": time.Now().UTC(),
		})
	switch {
	case result.Error != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "order_confirmation_evidence_unavailable"})
	case result.RowsAffected == 0:
		c.JSON(http.StatusConflict, gin.H{"error": "order_confirmation_evidence_exists"})
	default:
		c.Status(http.StatusNoContent)
	}
}

func readOrderConfirmationScreenshot(c *gin.Context) ([]byte, string, int) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, crmOrderEvidenceMaxBytes+(64<<10))
	reader, err := c.Request.MultipartReader()
	if err != nil {
		return nil, "", http.StatusBadRequest
	}
	part, err := reader.NextRawPart()
	var limitError *http.MaxBytesError
	if errors.Is(err, io.EOF) {
		return nil, "", http.StatusBadRequest
	}
	if errors.As(err, &limitError) {
		return nil, "", http.StatusRequestEntityTooLarge
	}
	if err != nil || part.FormName() != "image" || part.FileName() == "" {
		return nil, "", http.StatusBadRequest
	}
	data, err := io.ReadAll(io.LimitReader(part, crmOrderEvidenceMaxBytes+1))
	if len(data) > crmOrderEvidenceMaxBytes || errors.As(err, &limitError) {
		return nil, "", http.StatusRequestEntityTooLarge
	}
	if err != nil {
		return nil, "", http.StatusBadRequest
	}
	metadata, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") {
		return nil, "", http.StatusBadRequest
	}
	if metadata.Width <= 0 || metadata.Height <= 0 || metadata.Width > crmOrderEvidenceMaxPixels/metadata.Height {
		return nil, "", http.StatusRequestEntityTooLarge
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return nil, "", http.StatusBadRequest
	}
	return data, "image/" + format, http.StatusOK
}

func trackedOrderQuery(c *gin.Context) *gorm.DB {
	return db.DB.WithContext(c.Request.Context()).
		Where("tenant_id = ? AND id = ? AND delivered_at IS NOT NULL", middleware.GetTenantID(c), c.Param("id"))
}

func readCRMTrackedOrder(c *gin.Context, query *gorm.DB, row *models.CloudifyOrderConfirmation) bool {
	err := query.First(row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "tracked_order_not_found"})
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "order_tracking_unavailable"})
	}
	return err == nil
}
