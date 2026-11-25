package pkg

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCloudifyFreshSearchDoesNotBorrowCachedSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/web/session/authenticate":
			var rpc struct {
				Params struct {
					DB string `json:"db"`
				} `json:"params"`
			}
			if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
				http.Error(w, "invalid auth payload", http.StatusBadRequest)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "session_id", Value: rpc.Params.DB})
			_, _ = fmt.Fprint(w, `{"result":{"uid":1}}`)
		case "/rest_api/private/saorders/search":
			session, err := r.Cookie("session_id")
			if err != nil {
				http.Error(w, "missing session", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{"session": session.Value}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	a := &CloudifyClient{BaseURL: srv.URL, DB: "database-a", Login: "same-login", Password: "password-a"}
	b := &CloudifyClient{BaseURL: srv.URL, DB: "database-b", Login: "same-login", Password: "password-b"}
	t.Cleanup(a.InvalidateSession)
	t.Cleanup(b.InvalidateSession)
	body := map[string]any{"SO_DON_HANG": "ĐH000016"}
	rows, err := a.SearchCustomEndpointWithBody("saorders/search", body)
	if err != nil || len(rows) != 1 || rows[0]["session"] != "database-a" {
		t.Fatalf("warm legacy cache: rows=%#v err=%v", rows, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err = b.SearchWithFreshCloudifySession(ctx, "saorders/search", body)
	if err != nil || len(rows) != 1 || rows[0]["session"] != "database-b" {
		t.Fatalf("fresh CRM query: rows=%#v err=%v", rows, err)
	}
	rows, err = a.SearchCustomEndpointWithBody("saorders/search", body)
	if err != nil || len(rows) != 1 || rows[0]["session"] != "database-a" {
		t.Fatalf("fresh query must preserve legacy cache: rows=%#v err=%v", rows, err)
	}
}
