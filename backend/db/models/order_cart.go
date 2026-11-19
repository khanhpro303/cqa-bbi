package models

import "time"

// OrderCart is the order a Zalo chat is putting together with the chatbot,
// one product at a time, before it becomes a confirmed order draft
// (OrderAction). It lives across turns, so the customer can correct it at any
// point ("lấy 2 cái thôi", "bỏ kính") and the bot always works from the same
// list. Lines and the questions still waiting for the customer's choice are
// JSON (see orderactions.CartState).
//
// A chat has at most one open cart: OpenKey holds the chat's work key while
// the cart is open and is NULL afterwards, and it is unique.
type OrderCart struct {
	ID       string  `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID string  `gorm:"type:char(36);not null;index:idx_ocart_tenant_status,priority:1" json:"tenant_id"`
	OpenKey  *string `gorm:"type:varchar(255);uniqueIndex" json:"-"`
	// Status: open | ordered | cancelled | expired.
	Status    string `gorm:"type:varchar(16);not null;index:idx_ocart_tenant_status,priority:2" json:"status"`
	AgentType string `gorm:"type:varchar(16);not null;default:''" json:"agent_type"`
	// WorkKey is the worker's per-chat flow-state key (engine.BuildSessionKey).
	WorkKey     string `gorm:"type:varchar(255);not null;default:'';index" json:"work_key"`
	ChannelID   string `gorm:"type:char(36);not null;default:''" json:"channel_id"`
	ZaloUserID  string `gorm:"type:varchar(64);not null;default:''" json:"zalo_user_id"`
	ZaloGroupID string `gorm:"type:varchar(64);not null;default:''" json:"zalo_group_id"`
	// CustomerCode is set by staff ordering for a customer. Customers order
	// for their own code, resolved from the permission token at checkout.
	CustomerCode string `gorm:"type:varchar(64);not null;default:''" json:"customer_code"`
	CustomerName string `gorm:"type:varchar(255);not null;default:''" json:"customer_name"`
	Note         string `gorm:"type:varchar(1000);not null;default:''" json:"note"`
	State        string `gorm:"type:mediumtext" json:"state"`
	// DraftID is the last confirmation draft sent for this cart; OrderCode is
	// the Cloudify order it became.
	DraftID   string    `gorm:"type:char(36);not null;default:''" json:"draft_id"`
	OrderCode string    `gorm:"type:varchar(64);not null;default:''" json:"order_code"`
	ExpiresAt time.Time `gorm:"not null" json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (OrderCart) TableName() string { return "order_carts" }
