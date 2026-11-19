package models

import "time"

// OrderAction is one order change the chatbot proposed on behalf of a Zalo
// user: create a sales order, edit one, or cancel one. The AI agent only ever
// writes a pending row (a draft). The change reaches Cloudify only after the
// same user replies to confirm, and the worker flips the row
// pending → executing with a conditional UPDATE, so a draft is written at
// most once even when the confirmation is redelivered.
type OrderAction struct {
	ID        string `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID  string `gorm:"type:char(36);not null;index:idx_oa_tenant_status,priority:1" json:"tenant_id"`
	Kind      string `gorm:"type:varchar(16);not null" json:"kind"` // create | update | cancel
	Status    string `gorm:"type:varchar(16);not null;index:idx_oa_tenant_status,priority:2" json:"status"`
	AgentType string `gorm:"type:varchar(16);not null;default:''" json:"agent_type"`
	// WorkKey is the worker's per-chat flow-state key (engine.BuildSessionKey).
	WorkKey     string `gorm:"type:varchar(255);not null;default:'';index" json:"work_key"`
	ChannelID   string `gorm:"type:char(36);not null;default:''" json:"channel_id"`
	ZaloUserID  string `gorm:"type:varchar(64);not null;default:''" json:"zalo_user_id"`
	ZaloGroupID string `gorm:"type:varchar(64);not null;default:''" json:"zalo_group_id"`
	// CustomerCode / CustomerName are resolved by the backend from the
	// permission token (customers) or the cache (staff), never from the model.
	CustomerCode string `gorm:"type:varchar(64);not null;default:''" json:"customer_code"`
	CustomerName string `gorm:"type:varchar(255);not null;default:''" json:"customer_name"`
	// OrderID / OrderCode identify the Cloudify order. For create they are
	// filled after the write succeeds.
	OrderID int64 `gorm:"not null;default:0" json:"order_id"`
	// CartID links a create draft to the chat cart it was built from, so the
	// confirmation closes the cart.
	CartID    string `gorm:"type:char(36);not null;default:'';index" json:"cart_id"`
	OrderCode string `gorm:"type:varchar(64);not null;default:''" json:"order_code"`
	// Payload is the exact JSON body sent to Cloudify on confirmation.
	Payload string `gorm:"type:mediumtext" json:"payload"`
	Summary string `gorm:"type:text" json:"summary"`
	Result  string `gorm:"type:text" json:"result"`
	Error   string `gorm:"type:varchar(1000);not null;default:''" json:"error"`

	ExpiresAt   time.Time  `gorm:"not null" json:"expires_at"`
	ConfirmedAt *time.Time `json:"confirmed_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func (OrderAction) TableName() string { return "order_actions" }
