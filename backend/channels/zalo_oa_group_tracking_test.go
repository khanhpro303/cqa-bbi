package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestSendTrackedGroupMessageKeepsChunkingAndObserver(t *testing.T) {
	requests := 0
	adapter := newTestZaloAdapter(ZaloOACredentials{}, func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" || r.URL.String() != zaloAPIBaseV3+"/group/message" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL)
		}
		var payload struct {
			Recipient struct {
				GroupID string `json:"group_id"`
			} `json:"recipient"`
			Message struct {
				Text string `json:"text"`
			} `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Recipient.GroupID != "g1" || payload.Message.Text == "" {
			t.Fatalf("unexpected payload: %+v", payload)
		}
		requests++
		return jsonResponse(fmt.Sprintf(`{"error":0,"data":{"message_id":"m%d","group_id":"g1"}}`, requests)), nil
	})
	var observed []string
	adapter.SetSentObserver(func(_ context.Context, sent SentMessage) {
		observed = append(observed, sent.MessageID)
	})

	id, err := adapter.SendTrackedGroupMessage(t.Context(), "g1", strings.Repeat("a", zaloMaxTextRunes+1))
	if err != nil || id != "m2" || strings.Join(observed, ",") != "m1,m2" {
		t.Fatalf("id=%q, observer=%v, err=%v", id, observed, err)
	}
}

func TestSendTrackedGroupImageValidatesResponseID(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response string
		wantID   string
		wantErr  bool
	}{
		{"matching group", `{"error":0,"data":{"message_id":"m1","group_id":"g1"}}`, "m1", false},
		{"wrong group", `{"error":0,"data":{"message_id":"m1","group_id":"g2"}}`, "", false},
		{"missing group", `{"error":0,"data":{"message_id":"m1"}}`, "", false},
		{"numeric id", `{"error":0,"data":{"message_id":9007199254740993,"group_id":"g1"}}`, "", false},
		{"API error", `{"error":-213,"data":{"message_id":"m1","group_id":"g1"}}`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := newTestZaloAdapter(ZaloOACredentials{}, func(r *http.Request) (*http.Response, error) {
				return jsonResponse(tc.response), nil
			})
			id, err := adapter.SendTrackedGroupImage(t.Context(), "g1", "attachment")
			if id != tc.wantID || (err != nil) != tc.wantErr {
				t.Fatalf("got id=%q, err=%v, want id=%q, error=%t", id, err, tc.wantID, tc.wantErr)
			}
		})
	}
}

func TestGroupMessageFollowsChecksScopeOrderAndSource(t *testing.T) {
	const (
		reply     = `{"message_id":"reply","src":1,"group_id":"g1","time":120}`
		notice    = `{"message_id":"notice","src":0,"group_id":"g1","time":110}`
		unrelated = `{"message_id":"other","src":1,"group_id":"g1","time":115}`
	)
	for _, tc := range []struct {
		name     string
		messages string
		want     bool
	}{
		{"adjacent", reply + "," + notice, true},
		{"intervening chat", reply + "," + unrelated + "," + notice, false},
		{"reply alone", reply, false},
		{"wrong sort order", notice + "," + reply, false},
		{"wrong group", strings.Replace(reply, "g1", "g2", 1) + "," + notice, false},
		{"wrong source", reply + "," + strings.Replace(notice, `"src":0`, `"src":1`, 1), false},
		{"time reversed", reply + "," + strings.Replace(notice, "110", "130", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := newTestZaloAdapter(ZaloOACredentials{AccessToken: "bot-token"}, func(r *http.Request) (*http.Response, error) {
				query := r.URL.Query()
				if r.Method != "GET" || r.URL.Path != "/v3.0/oa/group/conversation" ||
					query.Get("group_id") != "g1" || query.Get("count") != "20" || query.Get("offset") != "0" ||
					strings.Join(r.Header["access_token"], "") != "bot-token" {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL)
				}
				return jsonResponse(`{"error":0,"data":[` + tc.messages + `]}`), nil
			})
			got, err := adapter.GroupMessageFollows(t.Context(), "g1", "reply", "notice")
			if err != nil || got != tc.want {
				t.Fatalf("got %t, %v; want %t", got, err, tc.want)
			}
		})
	}
}
