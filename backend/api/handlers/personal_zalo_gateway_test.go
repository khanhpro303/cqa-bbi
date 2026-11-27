package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const testPersonalZaloGatewayAdminSecret = "gateway-admin-test-secret-at-least-32-bytes"

func setPersonalZaloGatewayAdminSecret(t *testing.T) {
	t.Helper()
	t.Setenv("PERSONAL_ZALO_GATEWAY_ADMIN_SECRET", testPersonalZaloGatewayAdminSecret)
}

func TestDeterminePersonalZaloGatewayNextAction(t *testing.T) {
	tests := []struct {
		name    string
		account *personalZaloGatewayAccountRecord
		want    string
	}{
		{name: "missing account", account: nil, want: "create_account"},
		{name: "connected", account: &personalZaloGatewayAccountRecord{Status: "connected"}, want: "sync_now"},
		{name: "connecting", account: &personalZaloGatewayAccountRecord{Status: "connecting"}, want: "wait_for_connection"},
		{name: "qr pending", account: &personalZaloGatewayAccountRecord{Status: "qr_pending"}, want: "scan_qr"},
		{
			name: "disconnected with reusable session",
			account: &personalZaloGatewayAccountRecord{
				Status:      "disconnected",
				SessionData: map[string]interface{}{"imei": "abc"},
			},
			want: "reconnect",
		},
		{
			name: "disconnected forcing qr",
			account: &personalZaloGatewayAccountRecord{
				Status:    "disconnected",
				LastError: "session_unstable_qr_required",
			},
			want: "scan_qr",
		},
		{
			name: "disconnected with redacted reusable session",
			account: &personalZaloGatewayAccountRecord{
				Status:     "disconnected",
				HasSession: true,
			},
			want: "reconnect",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := determinePersonalZaloGatewayNextAction(tt.account); got != tt.want {
				t.Fatalf("determinePersonalZaloGatewayNextAction() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFindPersonalZaloGatewayAccountChoosesLatestMatch(t *testing.T) {
	accounts := []personalZaloGatewayAccountRecord{
		{ID: "older", TenantID: "tenant-1", ChannelID: "channel-1", UpdatedAt: "2026-04-08T09:00:00Z"},
		{ID: "other-channel", TenantID: "tenant-1", ChannelID: "channel-2", UpdatedAt: "2026-04-08T12:00:00Z"},
		{ID: "latest", TenantID: "tenant-1", ChannelID: "channel-1", UpdatedAt: "2026-04-08T11:00:00Z"},
	}

	account := findPersonalZaloGatewayAccount(accounts, "tenant-1", "channel-1")
	if account == nil {
		t.Fatal("expected account match")
	}
	if account.ID != "latest" {
		t.Fatalf("expected latest matching account, got %s", account.ID)
	}
}

func TestPersonalZaloGatewayClientRejectsRedirects(t *testing.T) {
	setPersonalZaloGatewayAdminSecret(t)
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected = true
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	if _, err := listPersonalZaloGatewayAccounts(server.URL); err == nil {
		t.Fatal("expected redirect response to be rejected")
	}
	if redirected {
		t.Fatal("gateway client forwarded credentials to a redirect target")
	}
}

func TestPersonalZaloGatewayClientFailsClosedWithoutAdminSecret(t *testing.T) {
	t.Setenv("PERSONAL_ZALO_GATEWAY_ADMIN_SECRET", "")
	if _, err := listPersonalZaloGatewayAccounts("http://127.0.0.1:1"); err == nil {
		t.Fatal("expected missing gateway admin secret to reject the request")
	}
}
