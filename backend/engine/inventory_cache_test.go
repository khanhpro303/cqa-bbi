package engine

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestInventoryStockCache_GetMissThenSetThenHit(t *testing.T) {
	t.Parallel()

	cache := NewInventoryStockCache(200 * time.Millisecond)
	ctx := context.Background()

	if _, ok := cache.Get(ctx, "tenant-1", "SKU001"); ok {
		t.Fatal("expected miss on fresh cache")
	}

	cache.Set(ctx, "tenant-1", "SKU001", 42.5)

	got, ok := cache.Get(ctx, "tenant-1", "SKU001")
	if !ok {
		t.Fatal("expected hit after Set")
	}
	if got != 42.5 {
		t.Fatalf("expected 42.5, got %v", got)
	}
}

func TestInventoryStockCache_TTLExpiry(t *testing.T) {
	t.Parallel()

	cache := NewInventoryStockCache(50 * time.Millisecond)
	ctx := context.Background()

	cache.Set(ctx, "tenant-1", "SKU002", 7)
	time.Sleep(100 * time.Millisecond)

	if _, ok := cache.Get(ctx, "tenant-1", "SKU002"); ok {
		t.Fatal("expected miss after TTL expiry")
	}
}

func TestInventoryStockCache_DisabledWhenTTLZero(t *testing.T) {
	t.Parallel()

	cache := NewInventoryStockCache(0)
	ctx := context.Background()

	cache.Set(ctx, "tenant-1", "SKU003", 99)
	if _, ok := cache.Get(ctx, "tenant-1", "SKU003"); ok {
		t.Fatal("expected miss when TTL is zero (cache disabled)")
	}
}

func TestInventoryStockCache_IgnoresEmptyKeys(t *testing.T) {
	t.Parallel()

	cache := NewInventoryStockCache(time.Minute)
	ctx := context.Background()

	cache.Set(ctx, "", "SKU004", 1)
	if _, ok := cache.Get(ctx, "", "SKU004"); ok {
		t.Fatal("expected miss for empty tenant")
	}
	cache.Set(ctx, "tenant-1", "", 1)
	if _, ok := cache.Get(ctx, "tenant-1", ""); ok {
		t.Fatal("expected miss for empty SKU")
	}
}

func TestInventoryStockCache_TenantIsolation(t *testing.T) {
	t.Parallel()

	cache := NewInventoryStockCache(time.Minute)
	ctx := context.Background()

	cache.Set(ctx, "tenant-1", "SKU005", 10)
	cache.Set(ctx, "tenant-2", "SKU005", 20)

	v1, ok1 := cache.Get(ctx, "tenant-1", "SKU005")
	v2, ok2 := cache.Get(ctx, "tenant-2", "SKU005")
	if !ok1 || !ok2 {
		t.Fatal("expected hits for both tenants")
	}
	if v1 != 10 || v2 != 20 {
		t.Fatalf("expected (10, 20), got (%v, %v)", v1, v2)
	}
}

func TestInventoryStockCache_PurgeExpired(t *testing.T) {
	t.Parallel()

	cache := NewInventoryStockCache(30 * time.Millisecond)
	ctx := context.Background()

	cache.Set(ctx, "tenant-1", "SKU006", 5)
	cache.Set(ctx, "tenant-1", "SKU007", 6)
	time.Sleep(60 * time.Millisecond)

	cache.PurgeExpired()

	// Inspect underlying map after purge — both should be gone.
	count := 0
	cache.memory.Range(func(_, _ any) bool {
		count++
		return true
	})
	if count != 0 {
		t.Fatalf("expected 0 entries after PurgeExpired, got %d", count)
	}
}

func TestInventoryStockCache_ConcurrentSetGet(t *testing.T) {
	t.Parallel()

	cache := NewInventoryStockCache(time.Minute)
	ctx := context.Background()

	const workers = 8
	const writesPerWorker = 200

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < writesPerWorker; i++ {
				sku := "SKU-" + itoa(workerID) + "-" + itoa(i)
				cache.Set(ctx, "tenant-1", sku, float64(workerID*1000+i))
				if _, ok := cache.Get(ctx, "tenant-1", sku); !ok {
					t.Errorf("expected hit for %s", sku)
					return
				}
			}
		}(w)
	}
	wg.Wait()
}

// itoa avoids importing strconv just for the concurrent test.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	negative := false
	if i < 0 {
		negative = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if negative {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

func TestDefaultInventoryStockCache_Singleton(t *testing.T) {
	c1 := DefaultInventoryStockCache()
	c2 := DefaultInventoryStockCache()
	if c1 != c2 {
		t.Fatal("DefaultInventoryStockCache should return the same instance")
	}
	if c1.ttl != DefaultInventoryStockCacheTTL {
		t.Fatalf("expected TTL %v, got %v", DefaultInventoryStockCacheTTL, c1.ttl)
	}
}
