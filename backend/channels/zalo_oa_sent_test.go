package channels

import "testing"

func TestSentMessageFromPayload(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]interface{}
		result  map[string]interface{}
		want    SentMessage
	}{
		{
			name: "text to user",
			payload: map[string]interface{}{
				"recipient": map[string]interface{}{"user_id": "u1"},
				"message":   map[string]interface{}{"text": "Chào anh"},
			},
			result: map[string]interface{}{"data": map[string]interface{}{"message_id": "m1"}},
			want:   SentMessage{UserID: "u1", MessageID: "m1", Text: "Chào anh", ContentType: "text"},
		},
		{
			name: "text to group, numeric id",
			payload: map[string]interface{}{
				"recipient": map[string]interface{}{"group_id": "g1"},
				"message":   map[string]interface{}{"text": "Đơn đã tạo"},
			},
			result: map[string]interface{}{"data": map[string]interface{}{"message_id": float64(12345)}},
			want:   SentMessage{GroupID: "g1", MessageID: "12345", Text: "Đơn đã tạo", ContentType: "text"},
		},
		{
			name: "template without id",
			payload: map[string]interface{}{
				"recipient": map[string]interface{}{"user_id": "u2"},
				"message": map[string]interface{}{"attachment": map[string]interface{}{
					"type": "template", "payload": map[string]interface{}{"template_type": "list"},
				}},
			},
			result: map[string]interface{}{},
			want:   SentMessage{UserID: "u2", ContentType: "template"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sentMessageFromPayload(tc.payload, tc.result)
			if got.UserID != tc.want.UserID || got.GroupID != tc.want.GroupID || got.MessageID != tc.want.MessageID || got.ContentType != tc.want.ContentType {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			if tc.want.Text != "" && got.Text != tc.want.Text {
				t.Fatalf("text = %q, want %q", got.Text, tc.want.Text)
			}
			if tc.want.ContentType == "template" && got.Text == "" {
				t.Fatalf("template text should hold the attachment JSON")
			}
			if got.SentAt.IsZero() {
				t.Fatalf("SentAt not set")
			}
		})
	}
}
