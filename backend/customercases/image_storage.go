package customercases

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const (
	TemporaryImageRetention = 24 * time.Hour
	closedImageRetention    = 90 * 24 * time.Hour
	imagePurgeInterval      = 15 * time.Minute
)

// StageImage keeps the downloaded bytes available until a case claims them.
// Retries of the same attachment reuse the identifier and the original expiry.
func StageImage(ctx context.Context, tenantID, channelID, senderID, turnID, sourceURL, mediaType string, data []byte) (string, error) {
	if db.DB == nil || tenantID == "" || channelID == "" || senderID == "" || turnID == "" || !AllowedImageURL(sourceURL) ||
		len(data) == 0 || len(data) > 8<<20 || http.DetectContentType(data) != mediaType {
		return "", errors.New("invalid inbound image")
	}
	switch mediaType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		return "", errors.New("unsupported inbound image type")
	}
	key := strings.Join([]string{tenantID, channelID, senderID, turnID, sourceURL}, "\x00")
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String()
	image := models.CustomerImageFile{
		ID: id, TenantID: tenantID, ChannelID: channelID, SenderID: senderID, MediaType: mediaType, Data: data,
	}
	// SQL logging can interpolate the binary parameters on slow queries.
	if err := db.DB.WithContext(ctx).Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).
		Clauses(clause.OnConflict{DoNothing: true}).Create(&image).Error; err != nil {
		// Preserve the same candidate ID in the message if a later delivery
		// attempt succeeds. A case only claims files that actually exist.
		return id, err
	}
	return id, nil
}

func linkCaseImagesTx(tx *gorm.DB, caseID string, r Report) ([]models.CaseImage, error) {
	var ids []string
	for _, image := range r.Images {
		if image.ImageID != "" {
			ids = append(ids, image.ImageID)
		}
	}
	if len(ids) == 0 {
		return r.Images, nil
	}
	// Take the same row locks as the expiration DELETE so it cannot remove
	// the bytes between checking the staged file and inserting the link.
	var files []models.CustomerImageFile
	if err := tx.Select("id").Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id IN ? AND tenant_id = ? AND channel_id = ? AND sender_id = ?", ids, r.TenantID, r.ChannelID, r.ZaloUserID).
		Where(`(created_at > ? OR EXISTS (
			SELECT 1 FROM customer_case_image_links link WHERE link.case_id = ? AND link.image_id = customer_image_files.id
		))`, time.Now().Add(-TemporaryImageRetention), caseID).
		Order("id").Find(&files).Error; err != nil {
		return nil, err
	}
	links := make([]models.CustomerCaseImageLink, 0, len(files))
	linked := make(map[string]bool, len(files))
	for _, file := range files {
		links = append(links, models.CustomerCaseImageLink{CaseID: caseID, ImageID: file.ID})
		linked[file.ID] = true
	}
	if len(links) > 0 {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&links).Error; err != nil {
			return nil, err
		}
	}
	images := make([]models.CaseImage, 0, len(r.Images))
	for _, image := range r.Images {
		if linked[image.ImageID] {
			image.URL = ""
		} else {
			image.ImageID = ""
		}
		if image.ImageID != "" || image.URL != "" {
			images = append(images, image)
		}
	}
	return images, nil
}

// ReadCaseImage verifies both the case and its link before loading image bytes.
func ReadCaseImage(ctx context.Context, tenantID, caseID, imageID string, now time.Time) (models.CustomerImageFile, error) {
	var image models.CustomerImageFile
	err := db.DB.WithContext(ctx).Model(&image).Select("customer_image_files.media_type, customer_image_files.data").
		Joins("JOIN customer_case_image_links link ON link.image_id = customer_image_files.id").
		Joins("JOIN customer_cases cc ON cc.id = link.case_id AND cc.tenant_id = customer_image_files.tenant_id").
		Where("customer_image_files.id = ? AND cc.id = ? AND cc.tenant_id = ?", imageID, caseID, tenantID).
		Where(caseImageRetentionPredicate, StatusCancelled, now.Add(-closedImageRetention)).
		Take(&image).Error
	return image, err
}

const caseImageRetentionPredicate = "(cc.status <> ? OR cc.resolved_at IS NULL OR cc.resolved_at > ?)"

// purgeExpiredImages removes bounded batches. The foreign keys remove links
// with a deleted file, and any still-retained case keeps a shared file alive.
func purgeExpiredImages(ctx context.Context, now time.Time) error {
	for i := 0; i < 10; i++ {
		result := db.DB.WithContext(ctx).Exec(`
DELETE FROM customer_image_files
WHERE created_at <= ?
  AND NOT EXISTS (
    SELECT 1
    FROM customer_case_image_links link
    JOIN customer_cases cc ON cc.id = link.case_id
    WHERE link.image_id = customer_image_files.id AND cc.tenant_id = customer_image_files.tenant_id
      AND `+caseImageRetentionPredicate+`
  )
ORDER BY created_at, id
LIMIT 100`, now.Add(-TemporaryImageRetention), StatusCancelled, now.Add(-closedImageRetention))
		if result.Error != nil || result.RowsAffected < 100 {
			return result.Error
		}
	}
	return nil
}

func RunImageRetention(ctx context.Context) {
	ticker := time.NewTicker(imagePurgeInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		runCtx, cancel := context.WithTimeout(ctx, time.Minute)
		err := purgeExpiredImages(runCtx, time.Now())
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Printf("[case-images] image retention: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
