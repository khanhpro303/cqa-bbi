package models

import "time"

// ChatSession is one chatbot session on a Zalo OA channel, recorded live by
// the worker. Its transcript lives in `messages` (conversation_id, between
// StartedAt and LastActivityAt); the summary columns are filled by the chat
// memory summarizer once the session has been idle long enough.
type ChatSession struct {
	// ID is the worker's session id (the Langflow session_id of the session).
	ID             string `gorm:"type:varchar(100);primaryKey" json:"id"`
	TenantID       string `gorm:"type:char(36);not null;index:idx_chatsess_tenant_subject,priority:1;index:idx_chatsess_status_activity,priority:1;index:idx_chatsess_tenant_code,priority:1" json:"tenant_id"`
	ChannelID      string `gorm:"type:char(36);not null" json:"channel_id"`
	ConversationID string `gorm:"type:char(36);index" json:"conversation_id"`
	// SubjectType is "user" for a 1:1 chat and "group" for a GMF group chat;
	// SubjectID is the Zalo user id or group id. Profiles are kept per subject.
	SubjectType  string `gorm:"type:varchar(10);not null;index:idx_chatsess_tenant_subject,priority:2" json:"subject_type"`
	SubjectID    string `gorm:"type:varchar(255);not null;index:idx_chatsess_tenant_subject,priority:3" json:"subject_id"`
	ZaloUserID   string `gorm:"type:varchar(255)" json:"zalo_user_id"`
	ZaloGroupID  string `gorm:"type:varchar(255)" json:"zalo_group_id"`
	CustomerName string `gorm:"type:varchar(255)" json:"customer_name"`
	CustomerCode string `gorm:"type:varchar(50);index:idx_chatsess_tenant_code,priority:2" json:"customer_code"`
	// AgentType is "public" (customer bot) or "private" (staff bot).
	AgentType      string    `gorm:"type:varchar(20)" json:"agent_type"`
	StartedAt      time.Time `gorm:"not null" json:"started_at"`
	LastActivityAt time.Time `gorm:"not null;index:idx_chatsess_status_activity,priority:3" json:"last_activity_at"`
	InboundCount   int       `gorm:"default:0" json:"inbound_count"`
	OutboundCount  int       `gorm:"default:0" json:"outbound_count"`

	// Status: open | summarized | skipped | failed.
	Status       string     `gorm:"type:varchar(20);not null;default:'open';index:idx_chatsess_status_activity,priority:2" json:"status"`
	Attempts     int        `gorm:"default:0" json:"attempts"`
	SummarizedAt *time.Time `json:"summarized_at"`
	Summary      string     `gorm:"type:text" json:"summary"`
	Outcome      string     `gorm:"type:varchar(30)" json:"outcome"`
	Sentiment    string     `gorm:"type:varchar(20)" json:"sentiment"`
	Intents      string     `gorm:"type:json" json:"intents"`
	Products     string     `gorm:"type:json" json:"products"`
	Issues       string     `gorm:"type:json" json:"issues"`
	SummaryModel string     `gorm:"type:varchar(100)" json:"summary_model"`
	SummaryError string     `gorm:"type:text" json:"summary_error,omitempty"`

	// Vector index bookkeeping (one vector per summary).
	VectorID        string     `gorm:"type:varchar(100)" json:"vector_id"`
	IndexedAt       *time.Time `gorm:"index" json:"indexed_at"`
	VectorDeletedAt *time.Time `json:"vector_deleted_at"`

	AlertedAt *time.Time `json:"alerted_at"`
	CreatedAt time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt time.Time  `gorm:"not null" json:"updated_at"`
}

func (ChatSession) TableName() string { return "chat_sessions" }

// ChatCustomerProfile is the running profile of one chat subject (a Zalo user
// or a GMF group), updated after each summarized session.
type ChatCustomerProfile struct {
	ID           string `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID     string `gorm:"type:char(36);not null;uniqueIndex:uq_chatprofile_subject,priority:1;index:idx_chatprofile_tenant_code,priority:1" json:"tenant_id"`
	ChannelID    string `gorm:"type:char(36);not null;uniqueIndex:uq_chatprofile_subject,priority:2" json:"channel_id"`
	SubjectType  string `gorm:"type:varchar(10);not null;uniqueIndex:uq_chatprofile_subject,priority:3" json:"subject_type"`
	SubjectID    string `gorm:"type:varchar(255);not null;uniqueIndex:uq_chatprofile_subject,priority:4" json:"subject_id"`
	CustomerName string `gorm:"type:varchar(255)" json:"customer_name"`
	CustomerCode string `gorm:"type:varchar(50);index:idx_chatprofile_tenant_code,priority:2" json:"customer_code"`
	// Profile is the structured JSON profile (traits with evidence).
	Profile string `gorm:"type:json" json:"profile"`
	// Card is the short text (<= 500 chars) that may be given to the chatbot.
	Card          string     `gorm:"type:text" json:"card"`
	SessionsCount int        `gorm:"default:0" json:"sessions_count"`
	LastSessionID string     `gorm:"type:varchar(100)" json:"last_session_id"`
	LastSessionAt *time.Time `json:"last_session_at"`
	Model         string     `gorm:"type:varchar(100)" json:"model"`
	CreatedAt     time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt     time.Time  `gorm:"not null" json:"updated_at"`
}

func (ChatCustomerProfile) TableName() string { return "chat_customer_profiles" }
