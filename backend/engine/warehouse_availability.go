package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/vietbui/chat-quality-agent/pkg"
	"golang.org/x/sync/errgroup"
)

const WarehouseAvailabilityTimeout = 30 * time.Second

var warehouseAvailabilitySlots = make(chan struct{}, 12)

// WarehouseAvailableStock looks up each distinct, already authorized SKU. A
// result may include individually verified quantities alongside an error. A
// caller reporting a total must use it only when every requested SKU succeeded.
func WarehouseAvailableStock(ctx context.Context, client *pkg.CloudifyClient, skus []string) (map[string]float64, error) {
	ctx, cancel := context.WithTimeout(ctx, WarehouseAvailabilityTimeout)
	defer cancel()
	quantities := make(map[string]float64, len(skus))
	seen := make(map[string]struct{}, len(skus))
	var mu sync.Mutex
	var group errgroup.Group
	group.SetLimit(4)
	for _, sku := range skus {
		sku = strings.TrimSpace(sku)
		if _, found := seen[sku]; sku == "" || found {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		seen[sku] = struct{}{}
		group.Go(func() error {
			select {
			case warehouseAvailabilitySlots <- struct{}{}:
				defer func() { <-warehouseAvailabilitySlots }()
			case <-ctx.Done():
				return ctx.Err()
			}
			quantity, err := client.MainWarehouseAvailableStock(ctx, sku)
			if err != nil {
				return fmt.Errorf("stock availability for %s: %w", sku, err)
			}
			mu.Lock()
			quantities[sku] = quantity
			mu.Unlock()
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return quantities, err
	}
	return quantities, ctx.Err()
}
