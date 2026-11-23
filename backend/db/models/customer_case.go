package models

import "time"

// CustomerCase is one customer problem staff must follow up until it is done
// (a complaint, a defective product, a wrong delivery, a payment mistake...).
// The chatbot opens it through the complaint_record MCP tool, the chat-memory
// summarizer opens it from a session issue the bot missed, and staff manage it
// on the Insights khách page. A new report about the same problem is added to
// the open case instead of opening another one (see customercases.Report).
//
// The customer never sees an id or number for it: the bot only says the
// problem was recorded and passed on.
type CustomerCase struct {
	ID        string `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID  string `gorm:"type:char(36);not null;index:idx_ccase_tenant_status,priority:1;index:idx_ccase_tenant_subject,priority:1;index:idx_ccase_tenant_code,priority:1" json:"tenant_id"`
	ChannelID string `gorm:"type:char(36);not null;default:''" json:"channel_id"`
	// SubjectType/SubjectID are the chat the case came from, as in
	// chat_sessions: "user" + Zalo user id, or "group" + Zalo group id.
	SubjectType  string `gorm:"type:varchar(10);not null;default:'';index:idx_ccase_tenant_subject,priority:2" json:"subject_type"`
	SubjectID    string `gorm:"type:varchar(255);not null;default:'';index:idx_ccase_tenant_subject,priority:3" json:"subject_id"`
	ZaloUserID   string `gorm:"type:varchar(64);not null;default:''" json:"zalo_user_id"`
	ZaloGroupID  string `gorm:"type:varchar(64);not null;default:''" json:"zalo_group_id"`
	ReporterName string `gorm:"type:varchar(255);not null;default:''" json:"reporter_name"`
	CustomerName string `gorm:"type:varchar(255);not null;default:''" json:"customer_name"`
	CustomerCode string `gorm:"type:varchar(64);not null;default:'';index:idx_ccase_tenant_code,priority:2" json:"customer_code"`
	// Private: reported by someone in a group they are not a CRM member of.
	// Only that person's own chat sees it; the customer code does not link it
	// to other chats.
	Private bool `gorm:"not null;default:false" json:"private"`

	// Type uses the chat-memory issue types: complaint, delivery,
	// product_defect, price, payment, staff_request, other.
	Type     string `gorm:"type:varchar(30);not null;default:'other'" json:"type"`
	Severity string `gorm:"type:varchar(10);not null;default:'medium'" json:"severity"`
	Title    string `gorm:"type:varchar(255);not null;default:''" json:"title"`
	Detail   string `gorm:"type:text" json:"detail"`
	Evidence string `gorm:"type:text" json:"evidence"`
	// Images is a JSON list of CaseImage: pictures the customer sent about
	// the problem, with what the AI read in them.
	Images string `gorm:"type:json" json:"images"`

	// Status: open | in_progress | resolved | cancelled.
	Status string `gorm:"type:varchar(20);not null;default:'open';index:idx_ccase_tenant_status,priority:2" json:"status"`
	// Source: bot | summary | staff (who opened it).
	Source          string `gorm:"type:varchar(20);not null;default:'bot'" json:"source"`
	OpenedSessionID string `gorm:"type:varchar(100);not null;default:''" json:"opened_session_id"`
	AssigneeID      string `gorm:"type:char(36);not null;default:''" json:"assignee_id"`
	AssigneeName    string `gorm:"type:varchar(255);not null;default:''" json:"assignee_name"`
	// CustomerUpdate is what staff allow the bot to tell the customer about
	// progress. Internal notes go to case events instead.
	CustomerUpdate string `gorm:"type:text" json:"customer_update"`
	Resolution     string `gorm:"type:text" json:"resolution"`

	FollowupCount  int        `gorm:"not null;default:0" json:"followup_count"`
	LastFollowupAt *time.Time `json:"last_followup_at"`
	LastAlertAt    *time.Time `json:"last_alert_at"`
	ResolvedAt     *time.Time `json:"resolved_at"`
	CreatedAt      time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt      time.Time  `gorm:"not null;index:idx_ccase_tenant_status,priority:3" json:"updated_at"`
}

func (CustomerCase) TableName() string { return "customer_cases" }

// CaseImage is one picture attached to a case.
type CaseImage struct {
	URL         string    `json:"url"`
	Description string    `json:"description,omitempty"`
	At          time.Time `json:"at"`
}

// CustomerCaseEvent is one line of a case's history: opened, the customer
// asking again, a status change, a staff note.
type CustomerCaseEvent struct {
	ID       string `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID string `gorm:"type:char(36);not null" json:"tenant_id"`
	CaseID   string `gorm:"type:char(36);not null;index:idx_ccevent_case_time,priority:1" json:"case_id"`
	// Kind: opened | followup | reopened | linked | status | update | note |
	// assign | edit.
	Kind string `gorm:"type:varchar(20);not null" json:"kind"`
	// Actor: bot | summary | staff.
	Actor     string `gorm:"type:varchar(20);not null" json:"actor"`
	ActorName string `gorm:"type:varchar(255);not null;default:''" json:"actor_name"`
	SessionID string `gorm:"type:varchar(100);not null;default:''" json:"session_id"`
	// ZaloUserID: who reported it (bot and summary events).
	ZaloUserID string `gorm:"type:varchar(100);not null;default:''" json:"zalo_user_id"`
	// TurnID: the chat turn that made this report (bot events).
	TurnID string `gorm:"type:varchar(36);not null;default:'';index" json:"-"`
	// StaffNotified: staff were alerted about this report, or about the case
	// recently enough that no new alert was needed.
	StaffNotified bool      `gorm:"not null;default:false" json:"staff_notified"`
	Text          string    `gorm:"type:text" json:"text"`
	Images        string    `gorm:"type:json" json:"images"`
	CreatedAt     time.Time `gorm:"not null;index:idx_ccevent_case_time,priority:2" json:"created_at"`
}

func (CustomerCaseEvent) TableName() string { return "customer_case_events" }
