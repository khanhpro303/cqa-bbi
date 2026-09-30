package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestGetBaseURLHonorsForwardedHTTPS(t *testing.T) {
	tests := []struct {
		name           string
		requestURL     string
		forwardedProto string
		want           string
	}{
		{name: "plain HTTP", requestURL: "http://internal/", want: "http://crm.example.com"},
		{name: "direct TLS", requestURL: "https://internal/", want: "https://crm.example.com"},
		{name: "HTTPS reverse proxy", requestURL: "http://internal/", forwardedProto: "https", want: "https://crm.example.com"},
		{name: "proxy chain uses original protocol", requestURL: "http://internal/", forwardedProto: " HTTPS, http ", want: "https://crm.example.com"},
		{name: "HTTP reverse proxy", requestURL: "http://internal/", forwardedProto: "http", want: "http://crm.example.com"},
	}

	gin.SetMode(gin.TestMode)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tc.requestURL, nil)
			request.Host = "crm.example.com"
			if tc.forwardedProto != "" {
				request.Header.Set("X-Forwarded-Proto", tc.forwardedProto)
			}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = request

			if got := getBaseURL(ctx); got != tc.want {
				t.Fatalf("getBaseURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

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
