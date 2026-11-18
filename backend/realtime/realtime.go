// Package realtime fans small "something changed" signals out to browser tabs
// over Server-Sent Events. Events carry no data beyond a type name; the UI
// re-fetches the affected list, so authorization stays on the normal REST
// endpoints and nothing sensitive goes over the stream.
//
// When Redis is configured, events go through Redis pub/sub so a worker
// running in a different process from the API still reaches the browser.
// Without Redis, an in-process broadcaster is used (worker and API share the
// process in the default deployment).
package realtime

import (
	"context"
	"log"
	"sync"

	"github.com/redis/go-redis/v9"
	"github.com/vietbui/chat-quality-agent/db"
)

// EventZaloWhitelistUpdated fires when a staff whitelist entry changes outside
// the admin UI, e.g. an employee pairs their Zalo account with "verify <code>".
const EventZaloWhitelistUpdated = "zalo_whitelist_updated"

func channelName(tenantID string) string {
	return "cqa:realtime:" + tenantID
}

var local = struct {
	sync.Mutex
	subs map[string]map[chan string]struct{}
}{subs: map[string]map[chan string]struct{}{}}

// Publish sends an event to every open stream of the tenant. It never blocks
// the caller on slow subscribers and only logs failures: a missed live update
// costs a manual reload, not data.
func Publish(ctx context.Context, tenantID, event string) {
	if tenantID == "" || event == "" {
		return
	}
	if client := db.RedisClient; client != nil {
		if err := client.Publish(ctx, channelName(tenantID), event).Err(); err != nil {
			log.Printf("[realtime] publish %s for tenant %s failed: %v", event, tenantID, err)
		}
		return
	}
	local.Lock()
	defer local.Unlock()
	for ch := range local.subs[tenantID] {
		select {
		case ch <- event:
		default: // subscriber is behind; it will catch up on its next refetch
		}
	}
}

// Subscribe returns a channel of event names for the tenant. The channel is
// closed after ctx is done.
func Subscribe(ctx context.Context, tenantID string) <-chan string {
	out := make(chan string, 8)
	if client := db.RedisClient; client != nil {
		go forwardRedis(ctx, client, tenantID, out)
		return out
	}

	local.Lock()
	if local.subs[tenantID] == nil {
		local.subs[tenantID] = map[chan string]struct{}{}
	}
	local.subs[tenantID][out] = struct{}{}
	local.Unlock()

	go func() {
		<-ctx.Done()
		local.Lock()
		delete(local.subs[tenantID], out)
		if len(local.subs[tenantID]) == 0 {
			delete(local.subs, tenantID)
		}
		local.Unlock()
		close(out)
	}()
	return out
}

func forwardRedis(ctx context.Context, client *redis.Client, tenantID string, out chan<- string) {
	defer close(out)
	pubsub := client.Subscribe(ctx, channelName(tenantID))
	defer pubsub.Close()
	msgs := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-msgs:
			if !ok {
				return
			}
			select {
			case out <- msg.Payload:
			default:
			}
		}
	}
}
