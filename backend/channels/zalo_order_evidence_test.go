package channels

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestOrderEvidenceMessagesFindsExactPairAcrossPages(t *testing.T) {
	const (
		reply = `{"group_id":"g1","message_id":"reply","src":1,"time":1795597000000,"type":"text","from_id":"3028265287111933495","message":"  Đã   nhận\nđơn  "}`
		sent  = `{"group_id":"g1","message_id":"sent","src":0,"time":1795596999000,"type":"photo"}`
	)
	var offsets []string
	adapter := newTestZaloAdapter(ZaloOACredentials{AccessToken: "test-token"}, func(r *http.Request) (*http.Response, error) {
		query := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/v3.0/oa/group/conversation" ||
			query.Get("group_id") != "g1" || query.Get("count") != "20" ||
			strings.Join(r.Header["access_token"], "") != "test-token" {
			t.Fatalf("unexpected group conversation request: %s %s", r.Method, r.URL)
		}
		offset := query.Get("offset")
		offsets = append(offsets, offset)
		if offset == "0" {
			return jsonResponse(`{"error":0,"data":[` + reply + strings.Repeat(",null", 19) + "]}"), nil
		}
		return jsonResponse(`{"error":0,"data":[` + sent + "]}"), nil
	})
	got, err := adapter.OrderEvidenceMessages(t.Context(), "g1", "sent", "reply", "3028265287111933495", "Đã nhận đơn")
	want := GMFOrderEvidence{SentAtMillis: 1795596999000, ReplyAtMillis: 1795597000000, SentType: "photo"}
	if err != nil || got != want || !reflect.DeepEqual(offsets, []string{"0", "20"}) {
		t.Fatalf("got evidence %+v, offsets %v, err %v; want %+v and two consecutive pages", got, offsets, err, want)
	}
}

func TestOrderEvidenceMessagesRejectsUnverifiedItems(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		value any
		reply bool
	}{
		{"other group", "group_id", "g2", false},
		{"other message id", "message_id", "introduced", false},
		{"other sender", "from_id", "somebody-else", true},
		{"other reply text", "message", "Từ chối", true},
		{"blank sent text", "message", " \t", false},
		{"sent from member", "src", 1, false},
		{"reply from OA", "src", 0, true},
		{"string source", "src", "1", true},
		{"reply is image", "type", "photo", true},
		{"unsupported sent media", "type", "video", false},
		{"missing time", "time", 0, false},
		{"fractional time", "time", 1795597000000.5, true},
		{"out-of-range time", "time", 1 << 53, true},
		{"reply predates original", "time", 1795596998000, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sent := map[string]any{"group_id": "g1", "message_id": "sent", "src": 0, "type": "text", "message": "Phiếu đặt hàng", "time": 1795596999000}
			reply := map[string]any{"group_id": "g1", "message_id": "reply", "src": 1, "type": "text", "message": "Đã nhận đơn", "time": 1795597000000, "from_id": "customer"}
			if tc.reply {
				reply[tc.field] = tc.value
			} else {
				sent[tc.field] = tc.value
			}
			body, err := json.Marshal(map[string]any{"error": 0, "data": []any{reply, sent}})
			if err != nil {
				t.Fatal(err)
			}
			adapter := newTestZaloAdapter(ZaloOACredentials{}, func(_ *http.Request) (*http.Response, error) {
				return jsonResponse(string(body)), nil
			})
			if got, err := adapter.OrderEvidenceMessages(t.Context(), "g1", "sent", "reply", "customer", "Đã nhận đơn"); err == nil || got != (GMFOrderEvidence{}) {
				t.Fatalf("unexpected evidence %+v, err %v", got, err)
			}
		})
	}
}

func TestOrderEvidenceMessagesStopsAfterThreePages(t *testing.T) {
	var offsets []string
	adapter := newTestZaloAdapter(ZaloOACredentials{}, func(r *http.Request) (*http.Response, error) {
		offsets = append(offsets, r.URL.Query().Get("offset"))
		return jsonResponse(fmt.Sprintf(`{"error":0,"data":[null%s]}`, strings.Repeat(",null", 19))), nil
	})
	got, err := adapter.OrderEvidenceMessages(t.Context(), "g1", "sent", "reply", "customer", "Đã nhận đơn")
	if err == nil || got != (GMFOrderEvidence{}) || !reflect.DeepEqual(offsets, []string{"0", "20", "40"}) {
		t.Fatalf("got evidence %+v, offsets %v, err %v; want no evidence and three pages", got, offsets, err)
	}
}
