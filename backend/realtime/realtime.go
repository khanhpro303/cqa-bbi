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
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/vietbui/chat-quality-agent/db"
)

// EventZaloWhitelistUpdated fires when a staff whitelist entry changes outside
// the admin UI, e.g. an employee pairs their Zalo account with "verify <code>".
const EventZaloWhitelistUpdated = "zalo_whitelist_updated"

const (
	EventCloudifyOrdersUpdated  = "cloudify_orders_updated"
	EventCRMGroupMembersUpdated = "crm_group_members_updated"
)

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

// Subscribe returns a ready subscription. The channel closes when ctx ends
// or Redis resubscribes, so clients can refresh after an interruption.
func Subscribe(ctx context.Context, tenantID string) (<-chan string, error) {
	out := make(chan string, 8)
	if client := db.RedisClient; client != nil {
		const subscribeTimeout = 3 * time.Second
		readyCtx, cancel := context.WithTimeout(ctx, subscribeTimeout)
		defer cancel()
		name := channelName(tenantID)
		pubsub := client.Subscribe(readyCtx, name)
		ack, err := pubsub.ReceiveTimeout(readyCtx, subscribeTimeout)
		if err != nil {
			pubsub.Close()
			return nil, err
		}
		if sub, ok := ack.(*redis.Subscription); !ok || sub.Kind != "subscribe" || sub.Channel != name {
			pubsub.Close()
			return nil, fmt.Errorf("unexpected Redis subscription response: %T", ack)
		}
		go forwardRedis(ctx, pubsub, out)
		return out, nil
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
	return out, nil
}

func forwardRedis(ctx context.Context, pubsub *redis.PubSub, out chan<- string) {
	defer close(out)
	defer pubsub.Close()
	msgs := pubsub.ChannelWithSubscriptions()
	for {
		select {
		case <-ctx.Done():
			return
		case update, ok := <-msgs:
			if !ok {
				return
			}
			switch msg := update.(type) {
			case *redis.Subscription:
				// The initial acknowledgement was consumed by Subscribe.
				// End the stream so the browser reloads a snapshot on reconnect.
				return
			case *redis.Message:
				select {
				case out <- msg.Payload:
				default:
				}
			}
		}
	}
}
