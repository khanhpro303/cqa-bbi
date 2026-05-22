package channels

import (
	"context"
	"time"
)

// SyncedConversation represents a conversation fetched from an external channel.
type SyncedConversation struct {
	ExternalID    string
	ExternalUserID string
	CustomerName  string
	LastMessageAt time.Time
	Metadata      map[string]interface{}
}

// SyncedMessage represents a message fetched from an external channel.
type SyncedMessage struct {
	ExternalID  string
	SenderType  string // "customer" | "agent" | "system"
	SenderName  string
	Content     string
	ContentType string // "text" | "image" | "file" | "sticker" | "gif"
	Attachments []Attachment
	SentAt      time.Time
	RawData     map[string]interface{}
}

// Attachment represents a media attachment in a message.
type Attachment struct {
	Type      string `json:"type"` // image, file, video, sticker
	URL       string `json:"url"`
	Name      string `json:"name,omitempty"`
	LocalPath string `json:"local_path,omitempty"` // relative path on server
}

// ChannelAdapter defines the interface for fetching data from external chat channels.
type ChannelAdapter interface {
	// FetchRecentConversations returns conversations updated since `since`, up to `limit`.
	FetchRecentConversations(ctx context.Context, since time.Time, limit int) ([]SyncedConversation, error)

	// FetchMessages returns messages for a conversation since `since`.
	FetchMessages(ctx context.Context, conversationID string, since time.Time) ([]SyncedMessage, error)

	// SendMessage sends a message to the external channel.
	SendMessage(ctx context.Context, conversationID string, content string) error

	// HealthCheck verifies the channel connection is working.
	HealthCheck(ctx context.Context) error
}
