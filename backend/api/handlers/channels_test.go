package handlers

import (
	"testing"

	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestChannelToResponseExposesAutoReplyCapability(t *testing.T) {
	tests := []struct {
		name                 string
		channelType          string
		wantSupported        bool
		wantAutoReplyEnabled bool
	}{
		{
			name:                 "zalo oa supports auto reply",
			channelType:          "zalo_oa",
			wantSupported:        true,
			wantAutoReplyEnabled: true,
		},
		{
			name:                 "facebook does not support auto reply",
			channelType:          "facebook",
			wantSupported:        false,
			wantAutoReplyEnabled: false,
		},
		{
			name:                 "personal zalo import does not support auto reply",
			channelType:          "personal_zalo_import",
			wantSupported:        false,
			wantAutoReplyEnabled: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			response := channelToResponse(models.Channel{
				ChannelType:      tc.channelType,
				AutoReplyEnabled: true,
			})

			if response.SupportsAutoReply != tc.wantSupported {
				t.Fatalf("SupportsAutoReply = %v, want %v", response.SupportsAutoReply, tc.wantSupported)
			}
			if response.AutoReplyEnabled != tc.wantAutoReplyEnabled {
				t.Fatalf("AutoReplyEnabled = %v, want %v", response.AutoReplyEnabled, tc.wantAutoReplyEnabled)
			}
		})
	}
}
