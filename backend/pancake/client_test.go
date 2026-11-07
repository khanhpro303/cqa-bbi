package pancake

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const secret = "page-token-must-not-leak"

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c := NewClient("100", secret)
	c.BaseURL = server.URL
	c.MinInterval = 0
	return c
}

func TestFetchTagsAndConversations(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page_access_token") != secret {
			t.Errorf("token missing")
		}
		switch r.URL.Path {
		case "/v1/pages/100/tags":
			w.Write([]byte(`{"success":true,"tags":[{"id":1,"text":" Câu hỏi ","color":"#822ba1"},{"id":1,"text":"dup"},{"id":"x","text":"bad"},{"id":4,"text":"Chốt"}]}`))
		case "/v2/pages/100/conversations":
			if r.URL.Query().Get("type") != "INBOX" || r.URL.Query().Get("order_by") != "updated_at" {
				t.Errorf("unexpected query %s", r.URL.RawQuery)
			}
			if r.URL.Query().Get("last_conversation_id") == "" {
				w.Write([]byte(`{"success":true,"conversations":[{"id":"100_300","type":"INBOX","page_id":"100","updated_at":"2026-11-07T06:09:25.000000","tags":[{"id":4,"text":"Chốt"}]},{"id":"100_301","type":"INBOX","page_id":"100","updated_at":"2026-11-06T01:00:00","tags":null}]}`))
				return
			}
			w.Write([]byte(`{"success":true,"conversations":[]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	tags, err := c.FetchTags(context.Background())
	if err != nil || len(tags) != 2 || tags[0].Text != "Câu hỏi" || tags[1].ID != "4" {
		t.Fatalf("tags %+v %v", tags, err)
	}
	page, err := c.ListInboxConversations(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if page.NextCursor != "100_301" || page.Oldest.Format("2006-01-02") != "2026-11-06" {
		t.Fatalf("page %+v", page)
	}
	// "tags": null is treated as an empty set from a full conversation object.
	if len(page.Updates) != 2 || page.Updates[0].Tags[0].Text != "Chốt" || len(page.Updates[1].Tags) != 0 {
		t.Fatalf("updates %+v", page.Updates)
	}
}

func TestErrorsAreSanitized(t *testing.T) {
	for status, kind := range map[int]string{429: "rate_limit", 401: "auth", 403: "permission", 404: "not_found", 500: "transport"} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			w.Write([]byte(`{"message":"` + secret + `"}`))
		})
		_, err := c.FetchTags(context.Background())
		if ErrorKind(err) != kind || strings.Contains(err.Error(), secret) {
			t.Fatalf("status %d: %v", status, err)
		}
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"success":false,"message":"invalid token"}`)) })
	if _, err := c.FetchTags(context.Background()); ErrorKind(err) != "auth" {
		t.Fatalf("success:false must be auth, got %v", err)
	}
	empty := NewClient("100", "")
	if _, err := empty.FetchTags(context.Background()); ErrorKind(err) != "auth" {
		t.Fatalf("missing token must be auth, got %v", err)
	}
}
