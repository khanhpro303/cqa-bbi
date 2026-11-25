package engine

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/vietbui/chat-quality-agent/db/models"
)

type unansweredHandoverTestSender struct {
	groupID      string
	groupReceipt string
	groupErr     error
	dms          []string
	dmErr        map[string]error
}

func (s *unansweredHandoverTestSender) SendTrackedGroupMessage(_ context.Context, groupID, _ string) (string, error) {
	s.groupID = groupID
	return s.groupReceipt, s.groupErr
}

func (s *unansweredHandoverTestSender) SendTextMessage(_ context.Context, userID, _ string) error {
	s.dms = append(s.dms, userID)
	return s.dmErr[userID]
}

func TestUnansweredHandoverRequiresGroupReceipt(t *testing.T) {
	for _, tc := range []struct {
		name    string
		receipt string
		err     error
		wantOK  bool
	}{
		{name: "confirmed", receipt: "zalo-receipt", wantOK: true},
		{name: "missing receipt"},
		{name: "whitespace receipt", receipt: " "},
		{name: "provider rejected", err: errors.New("provider rejected")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sender := &unansweredHandoverTestSender{groupReceipt: tc.receipt, groupErr: tc.err}
			err := sendUnansweredHandover(t.Context(), sender, unansweredHandoverDestination{id: "manager", isGroup: true}, "customer question")
			if (err == nil) != tc.wantOK || sender.groupID != "manager" || len(sender.dms) != 0 {
				t.Fatalf("group=%q, dms=%v, err=%v; want confirmed=%t only for manager", sender.groupID, sender.dms, err, tc.wantOK)
			}
		})
	}
}

func unansweredHandoverTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(t.Context()).Err(); err != nil {
		t.Fatalf("connect to test Redis: %v", err)
	}
	return client
}

func TestUnansweredHandoverAttemptsEveryRecipient(t *testing.T) {
	client := unansweredHandoverTestRedis(t)
	channel := models.Channel{ID: "channel", TenantID: uuid.NewString()}
	destinations := []unansweredHandoverDestination{{id: "fails"}, {id: "confirms"}, {id: "also-confirms"}}
	t.Cleanup(func() {
		var keys []string
		for _, turn := range []string{"turn", "all-fail"} {
			for _, dest := range destinations {
				keys = append(keys, "unanswered_handover:"+channel.TenantID+":channel:"+turn+":dm:"+dest.id)
			}
		}
		_ = client.Del(context.Background(), keys...).Err()
	})
	wantDMs := []string{"fails", "confirms", "also-confirms"}
	rejection := errors.New("provider rejected")
	sender := &unansweredHandoverTestSender{dmErr: map[string]error{"fails": rejection}}
	if !notifyUnansweredHandoverRecipients(t.Context(), client, sender, channel, "turn", "customer question", destinations) {
		t.Fatal("at least one employee confirmed receipt")
	}
	if !reflect.DeepEqual(sender.dms, wantDMs) || sender.groupID != "" {
		t.Fatalf("notified %v, group %q, want all employees %v", sender.dms, sender.groupID, wantDMs)
	}
	if !notifyUnansweredHandoverRecipients(t.Context(), client, sender, channel, "turn", "customer question", destinations) {
		t.Fatal("confirmed receipt was lost during retry")
	}
	if !reflect.DeepEqual(sender.dms, wantDMs) {
		t.Fatalf("retry repeated one or more DMs: %v", sender.dms)
	}

	sender.dms = nil
	sender.dmErr = map[string]error{"fails": rejection, "confirms": rejection, "also-confirms": rejection}
	if notifyUnansweredHandoverRecipients(t.Context(), client, sender, channel, "all-fail", "customer question", destinations) {
		t.Fatal("all DMs failed, but delivery was confirmed")
	}
	if !reflect.DeepEqual(sender.dms, wantDMs) {
		t.Fatalf("notified %v, want all employees %v", sender.dms, wantDMs)
	}
}

func TestUnansweredHandoverPendingIsNotConfirmation(t *testing.T) {
	client := unansweredHandoverTestRedis(t)
	key := "unanswered_handover:test:" + uuid.NewString()
	t.Cleanup(func() { _ = client.Del(context.Background(), key).Err() })
	if err := client.Set(t.Context(), key, "pending:another-owner", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	var sendCalls int
	ok := deliverUnansweredHandover(t.Context(), client, key, func() error {
		sendCalls++
		return nil
	})
	if ok || sendCalls != 0 {
		t.Fatalf("in-flight send was treated as delivered=%t or re-sent=%d", ok, sendCalls)
	}
}
