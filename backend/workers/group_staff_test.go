package workers

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/engine"
)

func TestZaloSessionStartForStaffCall(t *testing.T) {
	on, off := true, false
	const question = "Bull ơi, Giày LS2 Zoe Man - Black H-V Yellow - 44 còn tồn khả dụng không?"
	tests := []struct {
		name, text, role, groupID, customerCode string
		crm, assistant                          bool
		switchValue                             *bool
		want                                    zaloSessionStart
	}{
		{"staff question", question, groupRoleStaff, "s084", "S084", true, false, &on, zaloSessionStaffCall},
		{"default on", question, groupRoleStaff, "s084", "S084", true, false, nil, zaloSessionStaffCall},
		{"manual keyword with name", "Chào Bull", groupRoleStaff, "s084", "S084", true, false, &on, zaloSessionManual},
		{"second manual keyword", "hi bull", groupRoleStaff, "s084", "S084", true, true, &on, zaloSessionManual},
		{"closing keyword with name", "Tạm biệt Bull", groupRoleStaff, "s084", "S084", true, false, &on, zaloSessionStayClosed},
		{"staff talking to customer", "anh ơi, mai em giao nhé", groupRoleStaff, "s084", "S084", true, false, &on, zaloSessionStayClosed},
		{"switch off", question, groupRoleStaff, "s084", "S084", true, false, &off, zaloSessionStayClosed},
		{"customer calling", question, groupRoleCustomer, "s084", "S084", true, false, &on, zaloSessionStayClosed},
		{"unverified sender", question, groupRoleUnverified, "s084", "S084", true, false, &on, zaloSessionStayClosed},
		{"staff direct message", question, groupRoleStaff, "", "", false, false, &on, zaloSessionStayClosed},
		{"staff internal group", question, groupRoleStaff, "internal", "", true, false, &on, zaloSessionStayClosed},
		{"unlinked group", question, groupRoleStaff, "unlinked", "S084", false, false, &on, zaloSessionStayClosed},
		{"assistant mode", "anh ơi, mai em giao nhé", groupRoleStaff, "s084", "S084", true, true, &on, zaloSessionAssistant},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := ChannelMetadata{
				GroupBotNames:      "Bull;Bell;bot",
				GroupStaffCallOnly: tt.switchValue,
				SessionKeyword:     "Chào Bull; Hi Bull",
				SessionEndKeyword:  "Tạm biệt Bull",
			}
			staffGated := staffCallOnlyOn(meta, tt.groupID, tt.crm, tt.customerCode, tt.role)
			if got := zaloSessionStartFor(meta, tt.text, staffGated, tt.assistant); got != tt.want {
				t.Fatalf("session start = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOpenZaloSessionSharesGroupActivationAcrossSenders(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	previousRedis, previousPerSender := db.RedisClient, engine.PerSenderGroupSessions()
	db.RedisClient = client
	engine.SetPerSenderGroupSessions(true)
	t.Cleanup(func() {
		db.RedisClient = previousRedis
		engine.SetPerSenderGroupSessions(previousPerSender)
		_ = client.Close()
	})

	ctx := context.Background()
	groupID := "g-open-" + time.Now().Format("150405.000000")
	sessionKey := engine.BuildSessionKey("test", "", groupID)
	senders := []string{"staff1", "staff2"}
	for _, sender := range senders {
		workKey := engine.BuildSessionKey("test", sender, groupID)
		if err := client.Set(ctx, workKey+engine.PendingOptionsSuffix, "stale", time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
		token, ok := engine.AcquireSessionLock(ctx, workKey+":lock")
		if !ok {
			t.Fatalf("could not acquire %s's processing lock", sender)
		}
		t.Cleanup(func() { engine.ReleaseSessionLock(ctx, workKey+":lock", token) })
	}
	t.Cleanup(func() { _, _ = engine.ClearGroupSessionState(ctx, groupID) })

	type result struct {
		id     string
		opened bool
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, len(senders))
	for _, sender := range senders {
		go func() {
			<-start
			workKey := engine.BuildSessionKey("test", sender, groupID)
			id, opened, err := openZaloSession(ctx, sessionKey, workKey, groupID, time.Minute)
			results <- result{id, opened, err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.id == "" || first.id != second.id || first.opened == second.opened {
		t.Fatalf("expected exactly one opening and a shared session ID, got %+v and %+v", first, second)
	}
	if id, err := client.Get(ctx, sessionKey).Result(); err != nil || id != first.id {
		t.Fatalf("stored session = %q, %v; want %q", id, err, first.id)
	}
	for _, sender := range senders {
		workKey := engine.BuildSessionKey("test", sender, groupID)
		if n, err := client.Exists(ctx, workKey+":lock").Result(); n != 1 || err != nil {
			t.Fatalf("%s's processing lock was lost: %d, %v", sender, n, err)
		}
		if n, err := client.Exists(ctx, workKey+engine.PendingOptionsSuffix).Result(); n != 0 || err != nil {
			t.Fatalf("%s's stale options remain: %d, %v", sender, n, err)
		}
	}
}

func TestStaffCallOnlyOn(t *testing.T) {
	off := false
	on := true
	tests := []struct {
		name  string
		meta  ChannelMetadata
		group string
		crm   bool
		code  string
		role  string
		want  bool
	}{
		{"staff in customer group, switch unset", ChannelMetadata{}, "g1", true, "KH01", groupRoleStaff, true},
		{"staff in customer group, switch on", ChannelMetadata{GroupStaffCallOnly: &on}, "g1", true, "KH01", groupRoleStaff, true},
		{"switch off", ChannelMetadata{GroupStaffCallOnly: &off}, "g1", true, "KH01", groupRoleStaff, false},
		{"customer", ChannelMetadata{}, "g1", true, "KH01", groupRoleCustomer, false},
		{"unverified member", ChannelMetadata{}, "g1", true, "KH01", groupRoleUnverified, false},
		{"staff 1:1", ChannelMetadata{}, "", false, "", groupRoleStaff, false},
		{"staff group without customer code", ChannelMetadata{}, "g1", true, "", groupRoleStaff, false},
		{"group not in CRM", ChannelMetadata{}, "g1", false, "", groupRoleStaff, false},
	}
	for _, tt := range tests {
		if got := staffCallOnlyOn(tt.meta, tt.group, tt.crm, tt.code, tt.role); got != tt.want {
			t.Errorf("%s: staffCallOnlyOn = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestStaffCallOnlyMetadataDefault(t *testing.T) {
	var meta ChannelMetadata
	if err := json.Unmarshal([]byte(`{"session_keyword":"Chào Bull"}`), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.GroupStaffCallOnly != nil {
		t.Fatalf("missing switch should stay unset (on), got %v", *meta.GroupStaffCallOnly)
	}
	if err := json.Unmarshal([]byte(`{"group_staff_call_only":false}`), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.GroupStaffCallOnly == nil || *meta.GroupStaffCallOnly {
		t.Fatal("explicit false should turn the switch off")
	}
}

func TestGroupBotNames(t *testing.T) {
	if got := groupBotNames(ChannelMetadata{}); len(got) != 1 || got[0] != "bot" {
		t.Errorf("default names = %v", got)
	}
	got := groupBotNames(ChannelMetadata{GroupBotNames: " Bull ; ;trợ lý "})
	if len(got) != 2 || got[0] != "Bull" || got[1] != "trợ lý" {
		t.Errorf("names = %v", got)
	}
}

func TestCallsGroupBot(t *testing.T) {
	names := []string{"Bull", "bot", "trợ lý"}
	keywords := "Chào Bull;Hi Bull"
	tests := []struct {
		text string
		want bool
	}{
		{"Bull ơi FF399 còn không", true},
		{"bull ơi", true},
		{"@Bull tra giúp đơn hôm qua", true},
		{"nhờ bot tra công nợ khách này", true},
		{"Trợ lý ơi giá FF800?", true},
		{"chào bull", true},
		{"hi bull", true},
		{"anh ơi FF399 chiều em giao nhé", false},
		{"robot hút bụi", false},
		{"bulletin", false},
		{"bottle", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := callsGroupBot(tt.text, names, keywords); got != tt.want {
			t.Errorf("callsGroupBot(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestContainsWordVietnamese(t *testing.T) {
	// "lý" next to another letter is part of a longer word.
	if containsWord("trợ lýx", "trợ lý") {
		t.Error("matched inside a longer word")
	}
	if !containsWord("ê trợ lý, check giúp", "trợ lý") {
		t.Error("missed a phrase followed by punctuation")
	}
	if containsWord("ábot", "bot") {
		t.Error("matched after an accented letter")
	}
}

// TestClassifyStaffTurn runs against a real Redis when TEST_REDIS_ADDR is set.
func TestClassifyStaffTurn(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	previous := db.RedisClient
	db.RedisClient = client
	t.Cleanup(func() {
		db.RedisClient = previous
		_ = client.Close()
	})
	ctx := context.Background()
	sessionKey := "zalo_session:test:group:g-staff-" + time.Now().Format("150405.000000")
	workKey := sessionKey + ":user:staff1"
	t.Cleanup(func() { client.Del(ctx, staffEngagedKey(sessionKey, "staff1")) })
	meta := ChannelMetadata{GroupBotNames: "Bull", SessionKeyword: "Chào Bull"}

	if got := classifyStaffTurn(ctx, meta, "t", sessionKey, workKey, "staff1", "anh ơi FF399 chiều giao nhé"); got != staffTurnNotForBot {
		t.Fatalf("talking to the customer = %v, want not for bot", got)
	}
	if got := classifyStaffTurn(ctx, meta, "t", sessionKey, workKey, "staff1", "Bull ơi FF399 còn bao nhiêu"); got != staffTurnCalled {
		t.Fatalf("calling the bot = %v, want called", got)
	}
	markStaffEngaged(ctx, sessionKey, "staff1")
	if got := classifyStaffTurn(ctx, meta, "t", sessionKey, workKey, "staff1", "kho HCM"); got != staffTurnEngaged {
		t.Fatalf("reply while talking with the bot = %v, want engaged", got)
	}
	if got := classifyStaffTurn(ctx, meta, "t", sessionKey, workKey, "staff2", "kho HCM"); got != staffTurnNotForBot {
		t.Fatalf("another staff member = %v, want not for bot", got)
	}
	if ttl := client.TTL(ctx, staffEngagedKey(sessionKey, "staff1")).Val(); ttl <= 0 || ttl > staffEngagedWindow {
		t.Fatalf("engaged ttl = %v", ttl)
	}
	clearStaffEngaged(ctx, sessionKey, "staff1")
	if got := classifyStaffTurn(ctx, meta, "t", sessionKey, workKey, "staff1", "kho HCM"); got != staffTurnNotForBot {
		t.Fatalf("after the session ended = %v, want not for bot", got)
	}
}
