package pancake

import (
	"encoding/json"
	"testing"
)

// Frames copied from a pancake.vn browser capture (names and ids replaced).
const tagFrame = `["7",null,"pages:100","pages:tag_conversations",{"timestamp":1794039045360519,"unread_first":true,"conversations":{"100_300":[1,4]},"conversations_updated":[{"id":"100_300","type":"INBOX","tags":[1,4],"success":true,"seen":true,"from":{"id":"300","name":"Khách"},"customers":[{"fb_id":"300","id":"c1","name":"Khách"}],"page_id":"100","message_count":2}]}]`

const commentFrame = `["7",null,"pages:100","pages:tag_conversations",{"timestamp":1794039051821706,"conversations":{"555_777":[]},"conversations_updated":[{"id":"555_777","type":"COMMENT","tags":[],"from":{"id":"888"},"customers":[{"fb_id":"888"}],"page_id":"100"}]}]`

func TestDecodeFrameAndExtractInboxTags(t *testing.T) {
	frame, err := DecodeFrame([]byte(tagFrame))
	if err != nil {
		t.Fatal(err)
	}
	if frame.Event != "pages:tag_conversations" || PageIDFromTopic(frame.Topic) != "100" || frame.Ref != nil || frame.JoinRef == nil || *frame.JoinRef != "7" {
		t.Fatalf("unexpected frame %+v", frame)
	}
	updates := ExtractUpdates("100", frame.Payload)
	if len(updates) != 1 {
		t.Fatalf("expected one update, got %+v", updates)
	}
	u := updates[0]
	if u.PSID != "300" || u.PancakeConversationID != "100_300" || u.Timestamp != 1794039045360519 || len(u.Tags) != 2 || u.Tags[0].ID != "1" || u.Tags[1].ID != "4" {
		t.Fatalf("unexpected update %+v", u)
	}
}

func TestExtractSkipsCommentsAndKeepsEmptyTagSet(t *testing.T) {
	frame, _ := DecodeFrame([]byte(commentFrame))
	if updates := ExtractUpdates("100", frame.Payload); len(updates) != 0 {
		t.Fatalf("comments must be skipped, got %+v", updates)
	}
	removed := `{"timestamp":5,"conversations":{"100_300":[]},"conversations_updated":[{"id":"100_300","type":"INBOX","tags":[],"page_id":"100"}]}`
	updates := ExtractUpdates("100", json.RawMessage(removed))
	if len(updates) != 1 || len(updates[0].Tags) != 0 || updates[0].Tags == nil {
		t.Fatalf("removing every tag is a valid empty set, got %+v", updates)
	}
}

func TestExtractMapOnlyAndConversationObject(t *testing.T) {
	mapOnly := `{"timestamp":9,"conversations":{"100_301":[2],"999_1":[3]}}`
	updates := ExtractUpdates("100", json.RawMessage(mapOnly))
	if len(updates) != 1 || updates[0].PSID != "301" || updates[0].Tags[0].ID != "2" {
		t.Fatalf("only inbox ids with the Page prefix are safe, got %+v", updates)
	}
	withText := `{"conversation":{"id":"100_302","type":"INBOX","page_id":"100","tags":[{"id":3,"text":"Chốt đơn","color":"#fff"}]}}`
	updates = ExtractUpdates("100", json.RawMessage(withText))
	if len(updates) != 1 || updates[0].Tags[0].Text != "Chốt đơn" || updates[0].Timestamp != 0 {
		t.Fatalf("unexpected %+v", updates)
	}
	noTags := `{"conversation":{"id":"100_303","type":"INBOX","page_id":"100","snippet":"hi"}}`
	if updates := ExtractUpdates("100", json.RawMessage(noTags)); len(updates) != 0 {
		t.Fatalf("payload without tags must not clear tags, got %+v", updates)
	}
	otherPage := `{"conversation":{"id":"200_300","type":"INBOX","page_id":"200","tags":[1]}}`
	if updates := ExtractUpdates("100", json.RawMessage(otherPage)); len(updates) != 0 {
		t.Fatalf("other Page must be ignored, got %+v", updates)
	}
}

func TestEncodeFrameMatchesPhoenixShape(t *testing.T) {
	ref := "9"
	data, err := EncodeFrame(nil, &ref, "phoenix", "heartbeat", nil)
	if err != nil || string(data) != `[null,"9","phoenix","heartbeat",{}]` {
		t.Fatalf("got %s %v", data, err)
	}
	if _, err := DecodeFrame([]byte(`{"x":1}`)); err == nil {
		t.Fatal("expected error")
	}
}

func TestInboxPSIDFallbacks(t *testing.T) {
	cases := map[string]string{
		`{"id":"300","type":"INBOX","tags":[]}`:                                                   "300",
		`{"id":"x","type":"INBOX","tags":[],"customers":[{"fb_id":"301"}]}`:                       "301",
		`{"id":"x","type":"INBOX","tags":[],"customers":[{"fb_id":"301"},{"fb_id":"302"}]}`:       "",
		`{"id":"x","type":"INBOX","tags":[],"from":{"id":"303"}}`:                                 "303",
		`{"id":"x","type":"INBOX","tags":[],"page_customer":{"psid":"304"},"from":{"id":"100"}}`: "304",
		`{"id":"x","type":"INBOX","tags":[],"from":{"id":"100"}}`:                                 "",
	}
	for raw, want := range cases {
		var conv rawConversation
		if err := json.Unmarshal([]byte(raw), &conv); err != nil {
			t.Fatal(err)
		}
		update, ok := conv.update("100", 0)
		if want == "" {
			if ok {
				t.Errorf("%s: expected skip, got %+v", raw, update)
			}
			continue
		}
		if !ok || update.PSID != want {
			t.Errorf("%s: want %s got %+v", raw, want, update)
		}
	}
}
