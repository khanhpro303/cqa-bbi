package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
)

func TestDeletePersonalZaloGatewayAccountsRemovesOnlyTheChannelsAccounts(t *testing.T) {
	setPersonalZaloGatewayAdminSecret(t)
	var mu sync.Mutex
	var deleted []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+testPersonalZaloGatewayAdminSecret {
			t.Errorf("authorization = %q", got)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts":
			_ = json.NewEncoder(w).Encode(personalZaloGatewayListResponse{Accounts: []personalZaloGatewayAccountRecord{
				{ID: "a1", TenantID: "t1", ChannelID: "gone"},
				{ID: "a2", TenantID: "t1", ChannelID: "gone"},
				{ID: "keep-other-channel", TenantID: "t1", ChannelID: "live"},
				{ID: "keep-other-tenant", TenantID: "t2", ChannelID: "gone"},
			}})
		case r.Method == http.MethodDelete:
			mu.Lock()
			deleted = append(deleted, r.URL.Path)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"status":"deleted"}`))
		default:
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	n, err := deletePersonalZaloGatewayAccounts(server.URL, "t1", "gone")
	if err != nil || n != 2 {
		t.Fatalf("deleted %d, err %v", n, err)
	}
	sort.Strings(deleted)
	if len(deleted) != 2 || deleted[0] != "/api/v1/accounts/a1" || deleted[1] != "/api/v1/accounts/a2" {
		t.Fatalf("deleted paths %v", deleted)
	}
}
