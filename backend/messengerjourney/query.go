package messengerjourney

import (
	"context"
	"strings"

	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

func pageLimit(value int) int {
	if value <= 0 {
		return 100
	}
	if value > maxPageSize {
		return maxPageSize
	}
	return value
}
func scoped(query *gorm.DB, pageIDs []string) *gorm.DB {
	if len(pageIDs) > 0 {
		return query.Where("page_id IN ?", pageIDs)
	}
	return query
}

func HighWatermark(ctx context.Context, db *gorm.DB, tenantID string) (uint64, error) {
	if strings.TrimSpace(tenantID) == "" {
		return 0, ErrInvalidInput
	}
	var sequence models.MessengerJourneySequence
	err := db.WithContext(ctx).First(&sequence, "tenant_id = ?", tenantID).Error
	if err == gorm.ErrRecordNotFound {
		return 0, nil
	}
	return sequence.NextSequence, err
}

func ListEvents(ctx context.Context, db *gorm.DB, q EventQuery) (EventPage, error) {
	if strings.TrimSpace(q.TenantID) == "" {
		return EventPage{}, ErrInvalidInput
	}
	limit := pageLimit(q.Limit)
	var rows []models.MessengerJourneyEvent
	query := db.WithContext(ctx).Where("tenant_id = ? AND sequence > ?", q.TenantID, q.AfterSequence)
	query = scoped(query, q.PageIDs)
	if err := query.Order("sequence ASC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return EventPage{}, err
	}
	page := EventPage{Events: rows}
	if len(rows) > limit {
		page.HasMore = true
		page.Events = rows[:limit]
	}
	if len(page.Events) > 0 {
		page.NextSequence = page.Events[len(page.Events)-1].Sequence
	} else {
		page.NextSequence = q.AfterSequence
	}
	return page, nil
}

func ListConversation(ctx context.Context, db *gorm.DB, tenantID, conversationID string, afterSequence uint64, limit int) (EventPage, error) {
	if tenantID == "" || conversationID == "" {
		return EventPage{}, ErrInvalidInput
	}
	var rows []models.MessengerJourneyEvent
	limit = pageLimit(limit)
	err := db.WithContext(ctx).Where("tenant_id = ? AND conversation_id = ? AND sequence > ?", tenantID, conversationID, afterSequence).Order("sequence ASC").Limit(limit + 1).Find(&rows).Error
	if err != nil {
		return EventPage{}, err
	}
	page := EventPage{Events: rows}
	if len(rows) > limit {
		page.HasMore = true
		page.Events = rows[:limit]
	}
	if len(page.Events) > 0 {
		page.NextSequence = page.Events[len(page.Events)-1].Sequence
	} else {
		page.NextSequence = afterSequence
	}
	return page, nil
}

func Status(ctx context.Context, db *gorm.DB, tenantID string, pageIDs []string) (JourneyStatus, error) {
	watermark, err := HighWatermark(ctx, db, tenantID)
	if err != nil {
		return JourneyStatus{}, err
	}
	result := JourneyStatus{HighWatermark: watermark}
	counts := []struct {
		model  any
		target *uint64
		extra  string
	}{
		{&models.MessengerJourneyEvent{}, &result.Events, ""},
		{&models.MessengerJourneyCustomer{}, &result.Customers, ""},
		{&models.MessengerJourneyOrder{}, &result.Orders, ""},
		{&models.MessengerJourneyCoverage{}, &result.CoveragePending, "status <> 'complete'"},
	}
	for _, item := range counts {
		var count int64
		query := db.WithContext(ctx).Model(item.model).Where("tenant_id = ?", tenantID)
		query = scoped(query, pageIDs)
		if item.extra != "" {
			query = query.Where(item.extra)
		}
		if err := query.Count(&count).Error; err != nil {
			return JourneyStatus{}, err
		}
		*item.target = uint64(count)
	}
	return result, nil
}
