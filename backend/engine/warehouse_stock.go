package engine

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
)

const MainWarehouseCode = "1.KHO-TONG"

// WarehouseProductAllowed uses catalog group and brand fields only.
func WarehouseProductAllowed(group, brand string, allowedGroups, allowedBrands []string, allBrands bool) bool {
	if len(allowedGroups) > 0 {
		catalogGroups := strings.FieldsFunc(group, func(r rune) bool {
			return r == ',' || r == ';' || r == '|' || r == '\n'
		})
		matched := false
		for _, actual := range catalogGroups {
			for _, allowed := range allowedGroups {
				if strings.EqualFold(strings.TrimSpace(actual), strings.TrimSpace(allowed)) {
					matched = true
					break
				}
			}
		}
		if !matched {
			return false
		}
	}
	if allBrands || len(allowedBrands) == 0 {
		return true
	}
	for _, allowed := range allowedBrands {
		if strings.EqualFold(strings.TrimSpace(brand), strings.TrimSpace(allowed)) {
			return true
		}
	}
	return false
}

type warehouseStockKey struct{}

type warehouseStockLookup struct {
	tenantID string
	once     sync.Once
	totals   map[string]float64
	err      error
}

// WithWarehouseStock lets sequential lookups in one request share a single
// closing-stock report, including lookups across several product parents.
func WithWarehouseStock(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, warehouseStockKey{}, &warehouseStockLookup{tenantID: tenantID})
}

func MainWarehouseStock(ctx context.Context, tenantID string, client *pkg.CloudifyClient) (map[string]float64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if lookup, ok := ctx.Value(warehouseStockKey{}).(*warehouseStockLookup); ok && lookup.tenantID == tenantID {
		lookup.once.Do(func() {
			lookup.totals, lookup.err = loadMainWarehouseStock(ctx, tenantID, client)
		})
		return lookup.totals, lookup.err
	}
	return loadMainWarehouseStock(ctx, tenantID, client)
}

func loadMainWarehouseStock(ctx context.Context, tenantID string, client *pkg.CloudifyClient) (map[string]float64, error) {
	branch := "BBI NỘI BỘ"
	var setting models.AppSetting
	err := db.DB.WithContext(ctx).Where("tenant_id = ? AND setting_key = ?", tenantID, "erp_branch_name").First(&setting).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("load inventory branch: %w", err)
	}
	if name := strings.TrimSpace(setting.ValuePlain); name != "" {
		branch = name
	}
	date := time.Now().In(time.FixedZone("Asia/Ho_Chi_Minh", 7*60*60)).Format(time.DateOnly)
	reportCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	rows, err := client.SearchCustomEndpointContext(reportCtx, "tong_hop_ton_kho/search", map[string]string{
		"CHI_NHANH": branch, "DS_KHO": MainWarehouseCode, "TU_NGAY": date, "DEN_NGAY": date,
		"BAO_GOM_SO_LIEU_CHI_NHANH_PHU_THUOC": "true",
	})
	if err != nil {
		return nil, fmt.Errorf("load warehouse closing stock: %w", err)
	}
	if rows == nil {
		return nil, fmt.Errorf("warehouse report did not return stock rows")
	}
	totals := make(map[string]float64, len(rows))
	for _, row := range rows {
		sku, _ := row["MA_HANG"].(string)
		sku = strings.TrimSpace(sku)
		qty, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(row["SO_LUONG_CUOI_KY"])), 64)
		if sku == "" || err != nil || math.IsNaN(qty) || math.IsInf(qty, 0) {
			return nil, fmt.Errorf("invalid SKU or closing quantity in warehouse report")
		}
		totals[sku] += qty
	}
	return totals, nil
}
