// Package messengerjourney owns CQA's canonical, append-only Messenger journey.
package messengerjourney

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
)

const (
	KindReferral       = "referral"
	KindLabels         = "labels"
	KindClassification = "classification"
	KindOrder          = "order"
	KindCoverage       = "coverage"
	KindDeletion       = "deletion"

	maxPayloadBytes = 64 << 10
	maxPageSize     = 500
)

var (
	ErrInvalidInput  = errors.New("invalid Messenger journey input")
	ErrInvalidCursor = errors.New("invalid Messenger journey cursor")
	ErrSubjectErased = errors.New("Messenger journey subject was erased")
	ErrEventConflict = errors.New("Messenger journey event ID conflicts with an accepted event")
)

type BaseInput struct {
	EventID, TenantID, PageID, PSID, ChannelID, ConversationID string
	Source, Version                                            string
	OccurredAt, ObservedAt                                     time.Time
}

type RecordInput struct {
	BaseInput
	Kind, EntityID, PolicyVersion string
	Payload                       json.RawMessage
	// SubjectKey is reserved for non-PII deletion tombstones.
	SubjectKey string
}

type ReferralInput struct {
	BaseInput
	Payload json.RawMessage
}
type LabelsInput struct {
	BaseInput
	Labels json.RawMessage
}
type ClassificationInput struct {
	BaseInput
	Classification, PolicyVersion string
	Payload                       json.RawMessage
}
type OrderInput struct {
	BaseInput
	OrderID string
	Payload json.RawMessage
}
type CoverageInput struct {
	BaseInput
	Status, ThroughMessageID string
	Payload                  json.RawMessage
}
type EraseInput struct {
	EventID, TenantID, PageID, PSID, Source string
	ObservedAt                              time.Time
}

type RecordResult struct {
	Event    models.MessengerJourneyEvent
	Inserted bool
}

type EventQuery struct {
	TenantID      string
	PageIDs       []string
	AfterSequence uint64
	Limit         int
}
type EventPage struct {
	Events       []models.MessengerJourneyEvent
	NextSequence uint64
	HasMore      bool
}

type JourneyStatus struct {
	HighWatermark   uint64 `json:"high_watermark"`
	Events          uint64 `json:"events"`
	Customers       uint64 `json:"customers"`
	Orders          uint64 `json:"orders"`
	CoveragePending uint64 `json:"coverage_pending"`
}
