package models

import "time"

// CloudifyOrderConfirmation records one new-order notification sent to a CRM
// group and the first verified customer reply confirming that notification.
type CloudifyOrderConfirmation struct {
	ID                string     `gorm:"type:char(36);primaryKey"`
	TenantID          string     `gorm:"type:char(36);not null;index:idx_coc_tenant_delivery,priority:1;index:idx_coc_group_delivery,priority:1"`
	EventID           string     `gorm:"type:char(36);not null;uniqueIndex:uq_coc_recipient,priority:1"`
	GroupID           string     `gorm:"type:char(36);not null;uniqueIndex:uq_coc_recipient,priority:2;index:idx_coc_group_delivery,priority:2"`
	ChannelID         string     `gorm:"type:char(36);not null"`
	ZaloGroupID       string     `gorm:"type:varchar(255);not null"`
	GroupName         string     `gorm:"type:varchar(255);not null"`
	OrderID           int64      `gorm:"not null"`
	OrderCode         string     `gorm:"type:varchar(64);not null"`
	CustomerCode      string     `gorm:"type:varchar(64);not null"`
	CustomerName      string     `gorm:"type:varchar(255);not null"`
	OutboundMessageID string     `gorm:"type:varchar(255);not null"`
	DeliveredAt       *time.Time `gorm:"index:idx_coc_tenant_delivery,priority:2;index:idx_coc_group_delivery,priority:3"`

	ReceiptDelivery *CloudifyReceiptDelivery `gorm:"type:json;serializer:json" json:"-"`

	ConfirmedAt         *time.Time
	SenderID            string  `gorm:"type:varchar(255);not null;default:''"`
	SenderName          string  `gorm:"type:varchar(255);not null;default:''"`
	MessageID           string  `gorm:"type:varchar(255);not null;default:''"`
	QuoteMessageID      string  `gorm:"type:varchar(255);not null;default:''"`
	ReplyKey            *string `gorm:"type:char(64);uniqueIndex"`
	Text                string  `gorm:"type:text"`
	EvidenceImage       []byte  `gorm:"type:mediumblob" json:"-"`
	EvidenceContentType string  `gorm:"type:varchar(32);not null;default:''"`
	EvidenceUploadedAt  *time.Time
	EvidenceUploadedBy  string `gorm:"type:char(36);not null;default:''"`
}

func (CloudifyOrderConfirmation) TableName() string { return "cloudify_order_confirmations" }

// CloudifyReceiptDelivery checkpoints a receipt's layout and successful sends.
type CloudifyReceiptDelivery struct {
	LayoutKey  string   `json:"layout_key"`
	MessageIDs []string `json:"message_ids"` // In page order, including missing IDs.
}
