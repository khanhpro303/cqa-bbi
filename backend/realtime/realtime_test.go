package realtime

import (
	"context"
	"testing"
	"time"
)

func TestLocalPublishReachesOnlySameTenant(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := Subscribe(ctx, "tenant-a")
	b := Subscribe(ctx, "tenant-b")

	Publish(ctx, "tenant-a", EventZaloWhitelistUpdated)

	select {
	case got := <-a:
		if got != EventZaloWhitelistUpdated {
			t.Fatalf("got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("tenant-a subscriber did not receive the event")
	}
	select {
	case got := <-b:
		t.Fatalf("tenant-b received another tenant's event %q", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSubscribeClosesAfterContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := Subscribe(ctx, "tenant-c")
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel")
		}
	case <-time.After(time.Second):
		t.Fatal("channel not closed after cancel")
	}
	Publish(context.Background(), "tenant-c", EventZaloWhitelistUpdated) // must not panic
}
