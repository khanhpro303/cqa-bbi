package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
		personalOptIn        bool
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
			name:                 "legacy personal zalo remains off until explicitly enabled",
			channelType:          "personal_zalo_import",
			wantSupported:        true,
			wantAutoReplyEnabled: false,
		},
		{
			name:                 "personal zalo can opt in",
			channelType:          "personal_zalo_import",
			personalOptIn:        true,
			wantSupported:        true,
			wantAutoReplyEnabled: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			response := channelToResponse(models.Channel{
				ChannelType:                  tc.channelType,
				AutoReplyEnabled:             true,
				PersonalZaloAutoReplyEnabled: tc.personalOptIn,
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

func TestCanManageChannelCredentialsRequiresWritePermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name        string
		role        string
		permissions string
		want        bool
	}{
		{name: "owner", role: "owner", want: true},
		{name: "admin", role: "admin", want: true},
		{name: "channel writer", role: "member", permissions: `{"channels":"rw"}`, want: true},
		{name: "channel reader", role: "member", permissions: `{"channels":"r"}`},
		{name: "malformed permissions", role: "member", permissions: `{`},
		{name: "missing context"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			if tc.role != "" {
				ctx.Set("tenant_role", tc.role)
			}
			if tc.permissions != "" {
				ctx.Set("tenant_permissions", tc.permissions)
			}
			if got := canManageChannelCredentials(ctx); got != tc.want {
				t.Fatalf("canManageChannelCredentials() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEnrichPersonalZaloAvatarsPopulatesLatestMatch(t *testing.T) {
	setPersonalZaloGatewayAdminSecret(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.Header.Get("Authorization"); got != "Bearer "+testPersonalZaloGatewayAdminSecret {
			t.Errorf("authorization = %q", got)
			return
		}
		if r.URL.Path != "/api/v1/accounts" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(personalZaloGatewayListResponse{
			Accounts: []personalZaloGatewayAccountRecord{
				{
					ID:        "old",
					TenantID:  "tenant-1",
					ChannelID: "ch-personal-1",
					AvatarURL: "https://cdn.example.com/old.jpg",
					UpdatedAt: "2026-11-26T10:00:00Z",
				},
				{
					ID:        "latest",
					TenantID:  "tenant-1",
					ChannelID: "ch-personal-1",
					AvatarURL: " https://cdn.example.com/latest.jpg ",
					UpdatedAt: "2026-11-26T12:00:00Z",
				},
				{
					ID:        "other-tenant",
					TenantID:  "tenant-2",
					ChannelID: "ch-personal-2",
					AvatarURL: "https://cdn.example.com/other-tenant.jpg",
					UpdatedAt: "2026-11-26T12:00:00Z",
				},
			},
		})
	}))
	defer server.Close()

	channels := []ChannelResponse{
		{ID: "ch-oa", TenantID: "tenant-1", ChannelType: "zalo_oa"},
		{ID: "ch-personal-1", TenantID: "tenant-1", ChannelType: "personal_zalo_import"},
		{ID: "ch-personal-2", TenantID: "tenant-1", ChannelType: "personal_zalo_import"},
	}

	enrichPersonalZaloAvatars(context.Background(), "tenant-1", channels, server.URL)

	if calls != 1 {
		t.Fatalf("gateway call count = %d, want 1", calls)
	}
	if channels[0].AvatarURL != "" {
		t.Fatalf("expected non-personal channel avatar to stay empty, got %q", channels[0].AvatarURL)
	}
	if channels[1].AvatarURL != "https://cdn.example.com/latest.jpg" {
		t.Fatalf("channels[1].AvatarURL = %q, want latest trimmed url", channels[1].AvatarURL)
	}
	if channels[2].AvatarURL != "" {
		t.Fatalf("expected cross-tenant account not to populate channels[2], got %q", channels[2].AvatarURL)
	}
}

func TestEnrichPersonalZaloAvatarsSkipsWithoutPersonalZalo(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	channels := []ChannelResponse{
		{ID: "ch-oa", TenantID: "tenant-1", ChannelType: "zalo_oa"},
		{ID: "ch-fb", TenantID: "tenant-1", ChannelType: "facebook"},
	}

	enrichPersonalZaloAvatars(context.Background(), "tenant-1", channels, server.URL)

	if called {
		t.Fatal("expected gateway not to be called when no personal_zalo_import channels exist")
	}
}

func TestEnrichPersonalZaloAvatarsDegradesGracefullyOnFailure(t *testing.T) {
	setPersonalZaloGatewayAdminSecret(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	channels := []ChannelResponse{
		{ID: "ch-personal", TenantID: "tenant-1", ChannelType: "personal_zalo_import"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	enrichPersonalZaloAvatars(ctx, "tenant-1", channels, server.URL)

	if channels[0].AvatarURL != "" {
		t.Fatalf("expected empty AvatarURL on gateway error, got %q", channels[0].AvatarURL)
	}
}
