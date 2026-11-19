package engine

import (
	"context"
	"testing"

	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestSyncChannelSkipsExternallyManagedImportChannels(t *testing.T) {
	engine := NewSyncEngine(&config.Config{})

	err := engine.SyncChannel(context.Background(), models.Channel{
		ID:          "channel-1",
		Name:        "Personal Zalo Import",
		ChannelType: "personal_zalo_import",
	})
	if err != nil {
		t.Fatalf("SyncChannel should skip externally managed import channels without error: %v", err)
	}
}

func TestSkippedConversationsBlockSync(t *testing.T) {
	tests := []struct {
		name           string
		skipped, total int
		want           bool
	}{
		{"nothing skipped", 0, 100, false},
		{"one broken conversation of 100", 1, 100, false},
		{"ten of 100 is still tolerated", 10, 100, false},
		{"eleven of 100 looks like an outage", 11, 100, true},
		{"one broken of three", 1, 3, false},
		{"two of three", 2, 3, true},
		{"the only conversation in a quiet window", 1, 1, false},
		{"every conversation of two", 2, 2, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := skippedConversationsBlockSync(tt.skipped, tt.total); got != tt.want {
				t.Errorf("skippedConversationsBlockSync(%d, %d) = %v, want %v", tt.skipped, tt.total, got, tt.want)
			}
		})
	}
}
