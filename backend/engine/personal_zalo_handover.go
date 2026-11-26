package engine

import (
	"context"
	"time"

	"github.com/vietbui/chat-quality-agent/db"
)

// NotifyPersonalZaloHandover delivers one alert to a recipient already
// validated by the channel handler. The turn reservation and provider receipt
// checks are shared with the Zalo OA management handover.
func NotifyPersonalZaloHandover(ctx context.Context, sender unansweredHandoverSender, alert HandoverAlert, recipient string, isGroup bool, turnID string) bool {
	location, err := time.LoadLocation(tenantTimezone(alert.TenantID))
	if err != nil {
		location = time.Local
	}
	body := BuildHandoverAlertMessage(alert, time.Now().In(location))
	return notifyUnansweredHandoverRecipients(ctx, db.RedisClient, sender, alert.Channel, turnID, body,
		[]unansweredHandoverDestination{{id: recipient, isGroup: isGroup}})
}
