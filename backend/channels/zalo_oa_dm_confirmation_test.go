package channels

import (
	"net/http"
	"testing"
)

func TestSendTextMessageRequiresZaloConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantError  bool
	}{
		{"confirmed", "{\"error\":0,\"data\":{\"message_id\":\"message\"}}", false},
		{"missing status", "{\"data\":{\"message_id\":\"message\"}}", true},
		{"rejected", "{\"error\":-213,\"message\":\"not allowed\"}", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := newTestZaloAdapter(ZaloOACredentials{}, func(_ *http.Request) (*http.Response, error) {
				return jsonResponse(tc.body), nil
			})
			err := adapter.SendTextMessage(t.Context(), "staff", "Câu hỏi cần quản lý kiểm tra")
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v, want error %t", err, tc.wantError)
			}
		})
	}
}
