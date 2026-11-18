package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAstraDeleteOlderThanPages(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		raw, _ := json.Marshal(body["deleteMany"]["filter"])
		if !strings.Contains(string(raw), `"metadata.created_at":{"$lt":1700000000}`) {
			t.Errorf("filter = %s", raw)
		}
		if r.Header.Get("Token") != "tok" {
			t.Errorf("missing token")
		}
		if calls < 3 {
			w.Write([]byte(`{"status":{"deletedCount":20,"moreData":true}}`))
			return
		}
		w.Write([]byte(`{"status":{"deletedCount":7}}`))
	}))
	defer srv.Close()

	n, err := astraDeleteOlderThan(srv.URL, "tok", 1700000000)
	if err != nil || n != 47 || calls != 3 {
		t.Fatalf("deleted %d in %d calls, err %v", n, calls, err)
	}
}

func TestAstraDeleteOlderThanError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"errors":[{"message":"collection not found"}]}`))
	}))
	defer srv.Close()
	if _, err := astraDeleteOlderThan(srv.URL, "tok", 1); err == nil || !strings.Contains(err.Error(), "collection not found") {
		t.Fatalf("err = %v", err)
	}
}
