package models

import "time"

// MessengerJourneySequence serializes event allocation per tenant. The row is
// locked until commit, so a consumer cannot skip a later-committing event.
type MessengerJourneySequence struct {
	TenantID     string    `gorm:"type:char(36);primaryKey"`
	NextSequence uint64    `gorm:"not null"`
	UpdatedAt    time.Time `gorm:"not null"`
}

// MessengerJourneyReceipt makes every accepted producer event ID idempotent,
// including IDs aliased to an existing semantic event. Its fingerprint is
// one-way and remains safe after subject erasure.
type MessengerJourneyReceipt struct {
	ID                string    `gorm:"type:char(36);primaryKey"`
	TenantID          string    `gorm:"type:char(36);not null;uniqueIndex:idx_mjrec_tenant_event,priority:1"`
	EventID           string    `gorm:"type:varchar(191);not null;uniqueIndex:idx_mjrec_tenant_event,priority:2"`
	Fingerprint       string    `gorm:"type:char(64);not null"`
	CanonicalSequence uint64    `gorm:"not null;index"`
	CreatedAt         time.Time `gorm:"not null"`
}

// MessengerJourneyEvent is the append-only integration log. Payloads contain
// only the minimal structured facts needed by CQA and AdBazooka, never chat text.
type MessengerJourneyEvent struct {
	ID             string    `gorm:"type:char(36);primaryKey" json:"-"`
	TenantID       string    `gorm:"type:char(36);not null;uniqueIndex:idx_mje_tenant_event,priority:1;uniqueIndex:idx_mje_tenant_sequence,priority:1;uniqueIndex:idx_mje_tenant_natural,priority:1;index:idx_mje_scope,priority:1" json:"tenant_id"`
	Sequence       uint64    `gorm:"not null;uniqueIndex:idx_mje_tenant_sequence,priority:2" json:"sequence"`
	EventID        string    `gorm:"type:varchar(191);not null;uniqueIndex:idx_mje_tenant_event,priority:2" json:"event_id"`
	NaturalKey     *string   `gorm:"type:char(64);uniqueIndex:idx_mje_tenant_natural,priority:2" json:"-"`
	Kind           string    `gorm:"type:varchar(32);not null;index" json:"kind"`
	SubjectKey     string    `gorm:"column:subject_id;type:char(64);not null;index" json:"subject_id"`
	PageID         string    `gorm:"type:varchar(64);not null;index:idx_mje_scope,priority:2" json:"page_id"`
	PSID           string    `gorm:"column:psid;type:varchar(64)" json:"psid,omitempty"`
	ChannelID      string    `gorm:"type:char(36);index" json:"channel_id,omitempty"`
	ConversationID string    `gorm:"type:char(36);index" json:"conversation_id,omitempty"`
	EntityID       string    `gorm:"type:varchar(191);index" json:"entity_id,omitempty"`
	Source         string    `gorm:"type:varchar(32);not null" json:"source"`
	Version        string    `gorm:"type:varchar(64)" json:"version,omitempty"`
	PolicyVersion  string    `gorm:"type:varchar(64)" json:"policy_version,omitempty"`
	Payload        string    `gorm:"type:json;not null" json:"payload"`
	OccurredAt     time.Time `gorm:"type:datetime(3);not null;index" json:"occurred_at"`
	ObservedAt     time.Time `gorm:"type:datetime(3);not null" json:"observed_at"`
	CreatedAt      time.Time `gorm:"not null" json:"created_at"`
}

// MessengerJourneyCustomer is the fast current view for one Page-scoped user.
type MessengerJourneyCustomer struct {
	SubjectKey                  string     `gorm:"column:subject_id;type:char(64);primaryKey" json:"subject_id"`
	TenantID                    string     `gorm:"type:char(36);not null;uniqueIndex:idx_mjc_subject,priority:1;index:idx_mjc_scope,priority:1" json:"tenant_id"`
	PageID                      string     `gorm:"type:varchar(64);not null;uniqueIndex:idx_mjc_subject,priority:2;index:idx_mjc_scope,priority:2" json:"page_id"`
	PSID                        string     `gorm:"column:psid;type:varchar(64);not null;uniqueIndex:idx_mjc_subject,priority:3" json:"psid"`
	ChannelID                   string     `gorm:"type:char(36);index" json:"channel_id,omitempty"`
	ConversationID              string     `gorm:"type:char(36);index" json:"conversation_id,omitempty"`
	FirstReferral               *string    `gorm:"type:json" json:"first_referral,omitempty"`
	FirstReferralAt             *time.Time `gorm:"type:datetime(3)" json:"first_referral_at,omitempty"`
	LatestReferral              *string    `gorm:"type:json" json:"latest_referral,omitempty"`
	LatestReferralAt            *time.Time `gorm:"type:datetime(3)" json:"latest_referral_at,omitempty"`
	Labels                      *string    `gorm:"type:json" json:"labels,omitempty"`
	LabelsHash                  string     `gorm:"type:char(64)" json:"-"`
	LabelsSequence              uint64     `gorm:"not null" json:"-"`
	LabelsObservedAt            *time.Time `gorm:"type:datetime(3)" json:"labels_observed_at,omitempty"`
	Classification              string     `gorm:"type:varchar(32)" json:"classification,omitempty"`
	ClassificationPolicyVersion string     `gorm:"type:varchar(64)" json:"classification_policy_version,omitempty"`
	ClassificationSequence      uint64     `gorm:"not null" json:"-"`
	ClassificationObservedAt    *time.Time `gorm:"type:datetime(3)" json:"classification_observed_at,omitempty"`
	LastSequence                uint64     `gorm:"not null;index" json:"last_sequence"`
	CreatedAt                   time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt                   time.Time  `gorm:"not null" json:"updated_at"`
}

// MessengerJourneyOrder is independent of ephemeral AI job results. Revisions
// remain in MessengerJourneyEvent; this row is only the latest accepted version.
type MessengerJourneyOrder struct {
	ID             string    `gorm:"type:char(64);primaryKey" json:"id"`
	TenantID       string    `gorm:"type:char(36);not null;uniqueIndex:idx_mjo_tenant_order,priority:1;index:idx_mjo_scope,priority:1" json:"tenant_id"`
	OrderID        string    `gorm:"type:varchar(191);not null;uniqueIndex:idx_mjo_tenant_order,priority:2" json:"order_id"`
	SubjectKey     string    `gorm:"column:subject_id;type:char(64);not null;index" json:"subject_id"`
	PageID         string    `gorm:"type:varchar(64);not null;index:idx_mjo_scope,priority:2" json:"page_id"`
	PSID           string    `gorm:"column:psid;type:varchar(64);not null" json:"psid"`
	ChannelID      string    `gorm:"type:char(36);index" json:"channel_id,omitempty"`
	ConversationID string    `gorm:"type:char(36);index" json:"conversation_id,omitempty"`
	Version        string    `gorm:"type:varchar(64)" json:"version,omitempty"`
	Payload        string    `gorm:"type:json;not null" json:"payload"`
	StateHash      string    `gorm:"type:char(64);not null" json:"-"`
	OccurredAt     time.Time `gorm:"type:datetime(3);not null" json:"occurred_at"`
	ObservedAt     time.Time `gorm:"type:datetime(3);not null" json:"observed_at"`
	LastSequence   uint64    `gorm:"not null;index" json:"last_sequence"`
	CreatedAt      time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt      time.Time `gorm:"not null" json:"updated_at"`
}

// MessengerJourneyCoverage records how far CQA has observed/analyzed a chat.
type MessengerJourneyCoverage struct {
	SubjectKey       string    `gorm:"column:subject_id;type:char(64);primaryKey" json:"subject_id"`
	TenantID         string    `gorm:"type:char(36);not null;index:idx_mjcov_scope,priority:1" json:"tenant_id"`
	PageID           string    `gorm:"type:varchar(64);not null;index:idx_mjcov_scope,priority:2" json:"page_id"`
	PSID             string    `gorm:"column:psid;type:varchar(64);not null" json:"psid"`
	ConversationID   string    `gorm:"type:char(36);index" json:"conversation_id,omitempty"`
	Status           string    `gorm:"type:varchar(32);not null" json:"status"`
	ThroughMessageID string    `gorm:"type:varchar(255)" json:"through_message_id,omitempty"`
	Payload          string    `gorm:"type:json;not null" json:"payload"`
	ObservedAt       time.Time `gorm:"type:datetime(3);not null" json:"observed_at"`
	LastSequence     uint64    `gorm:"not null;index" json:"last_sequence"`
	CreatedAt        time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt        time.Time `gorm:"not null" json:"updated_at"`
}

// MessengerJourneyErasure is a non-PII deny marker. Once present, delayed
// deliveries for the erased subject cannot recreate its current projection.
type MessengerJourneyErasure struct {
	SubjectKey string    `gorm:"column:subject_id;type:char(64);primaryKey" json:"subject_id"`
	TenantID   string    `gorm:"type:char(36);not null;index:idx_mjer_scope,priority:1" json:"tenant_id"`
	PageID     string    `gorm:"type:varchar(64);not null;index:idx_mjer_scope,priority:2" json:"page_id"`
	EventID    string    `gorm:"type:varchar(191);not null" json:"event_id"`
	ErasedAt   time.Time `gorm:"type:datetime(3);not null" json:"erased_at"`
	CreatedAt  time.Time `gorm:"not null" json:"created_at"`
}
