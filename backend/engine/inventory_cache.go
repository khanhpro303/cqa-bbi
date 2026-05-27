package engine

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/vietbui/chat-quality-agent/db"
)

// InventoryStockCache caches per-SKU live ERP stock values with a short TTL.
// Two backends are supported transparently:
//   - Redis (when db.RedisClient is configured) — shared across instances
//   - in-memory map — local fallback for dev/test or when Redis is missing
//
// The cache exists to avoid N×ERP calls when summing stock for a parent code
// with many child SKUs. TTL is intentionally short (default 120s) so stock
// numbers stay close to live.
type InventoryStockCache struct {
	ttl    time.Duration
	memory sync.Map // map[string]memoryEntry
}

type memoryEntry struct {
	value     float64
	expiresAt time.Time
}

// NewInventoryStockCache returns a cache with the given TTL. A TTL of zero or
// negative disables the cache (Get always misses, Set is a no-op).
func NewInventoryStockCache(ttl time.Duration) *InventoryStockCache {
	return &InventoryStockCache{ttl: ttl}
}

// inventoryStockKey returns the storage key for a tenant + SKU pair.
func inventoryStockKey(tenantID, sku string) string {
	return fmt.Sprintf("ton_kho:%s:%s", tenantID, sku)
}

// Get returns the cached stock for (tenantID, sku) and true on hit. On miss,
// expired entry, parse error, or disabled cache it returns 0 and false.
func (c *InventoryStockCache) Get(ctx context.Context, tenantID, sku string) (float64, bool) {
	if c == nil || c.ttl <= 0 || tenantID == "" || sku == "" {
		return 0, false
	}
	key := inventoryStockKey(tenantID, sku)

	if db.RedisClient != nil {
		val, err := db.RedisClient.Get(ctx, key).Result()
		if err == nil {
			f, parseErr := strconv.ParseFloat(val, 64)
			if parseErr == nil {
				return f, true
			}
		}
		// Redis miss or parse error falls through to in-memory.
	}

	raw, ok := c.memory.Load(key)
	if !ok {
		return 0, false
	}
	entry, ok := raw.(memoryEntry)
	if !ok || time.Now().After(entry.expiresAt) {
		c.memory.Delete(key)
		return 0, false
	}
	return entry.value, true
}

// Set stores the stock value with the cache's TTL. Writes go to both Redis
// (best-effort) and the in-memory map so a Redis outage still benefits the
// current process.
func (c *InventoryStockCache) Set(ctx context.Context, tenantID, sku string, value float64) {
	if c == nil || c.ttl <= 0 || tenantID == "" || sku == "" {
		return
	}
	key := inventoryStockKey(tenantID, sku)

	if db.RedisClient != nil {
		_ = db.RedisClient.Set(ctx, key, strconv.FormatFloat(value, 'f', -1, 64), c.ttl).Err()
	}
	c.memory.Store(key, memoryEntry{value: value, expiresAt: time.Now().Add(c.ttl)})
}

// PurgeExpired removes expired entries from the in-memory map. Optional —
// the cache lazily evicts on Get; this is useful for long-lived processes that
// rarely read entries that they wrote.
func (c *InventoryStockCache) PurgeExpired() {
	if c == nil {
		return
	}
	now := time.Now()
	c.memory.Range(func(key, value any) bool {
		entry, ok := value.(memoryEntry)
		if !ok || now.After(entry.expiresAt) {
			c.memory.Delete(key)
		}
		return true
	})
}

// DefaultInventoryStockCacheTTL is the TTL used by the package-level
// singleton. Short enough that stock readouts stay close to live, long enough
// to absorb burst traffic from a single chat turn that resolves a parent code
// to many child SKUs.
const DefaultInventoryStockCacheTTL = 120 * time.Second

var (
	defaultStockCacheOnce sync.Once
	defaultStockCache     *InventoryStockCache
)

// DefaultInventoryStockCache returns the process-wide cache used by ERP
// handlers. Lazy-initialised so tests can construct their own caches without
// touching globals.
func DefaultInventoryStockCache() *InventoryStockCache {
	defaultStockCacheOnce.Do(func() {
		defaultStockCache = NewInventoryStockCache(DefaultInventoryStockCacheTTL)
	})
	return defaultStockCache
}
