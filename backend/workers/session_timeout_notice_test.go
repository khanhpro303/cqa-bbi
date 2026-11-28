package workers

import (
	"encoding/json"
	"testing"
)

func TestOATimeoutNoticeDefaultsOn(t *testing.T) {
	for raw, want := range map[string]bool{
		`{}`:                               true,
		`{"session_timeout_notice":true}`:  true,
		`{"session_timeout_notice":false}`: false,
	} {
		var meta ChannelMetadata
		if err := json.Unmarshal([]byte(raw), &meta); err != nil {
			t.Fatal(err)
		}
		if got := oaTimeoutNoticeOn(meta); got != want {
			t.Errorf("%s: notice=%t, want %t", raw, got, want)
		}
	}
}

func TestPersonalZaloSessionTimeoutTaskRunsOnce(t *testing.T) {
	task, err := NewPersonalZaloSessionTimeoutTask(PersonalZaloSessionTimeoutPayload{SessionKey: "k", SessionID: "s"})
	if err != nil || task.Type() != TypePersonalZaloSessionTimeout {
		t.Fatalf("task=%v err=%v", task, err)
	}
	var payload PersonalZaloSessionTimeoutPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil || payload.SessionKey != "k" || payload.SessionID != "s" {
		t.Fatalf("payload=%+v err=%v", payload, err)
	}
}
