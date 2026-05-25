package channels

import (
	"testing"
	"time"
)

func TestParseGMFQuotaAssetsFromZaloDocsShape(t *testing.T) {
	result := map[string]interface{}{
		"data": []interface{}{
			map[string]interface{}{
				"product_type":  "gmf10",
				"quota_type":    "sub_quota",
				"asset_id":      "asset-available",
				"valid_through": "10/10/2024",
				"auto_renew":    false,
				"status":        "available",
				"used_id":       "",
			},
			map[string]interface{}{
				"product_type":  "gmf50",
				"quota_type":    "purchase_quota",
				"asset_id":      "asset-used",
				"valid_through": "2024-11-12",
				"status":        "used",
				"used_id":       "group-id",
			},
		},
		"error":   float64(0),
		"message": "Success",
	}

	assets, err := parseGMFQuotaAssets(result)
	if err != nil {
		t.Fatalf("parseGMFQuotaAssets returned error: %v", err)
	}
	if len(assets) != 2 {
		t.Fatalf("expected 2 assets, got %d", len(assets))
	}

	first := assets[0]
	if first.AssetID != "asset-available" {
		t.Fatalf("expected first asset_id asset-available, got %q", first.AssetID)
	}
	if first.AssetType != "GMF10 - sub_quota" {
		t.Fatalf("expected first asset type GMF10 - sub_quota, got %q", first.AssetType)
	}
	if first.TotalGroup != 1 || first.UsedGroup != 0 {
		t.Fatalf("expected available asset quota 0/1, got %d/%d", first.UsedGroup, first.TotalGroup)
	}
	expectedValidThrough := time.Date(2024, 10, 10, 0, 0, 0, 0, time.UTC).UnixMilli()
	if first.ValidThrough != expectedValidThrough {
		t.Fatalf("expected valid_through %d, got %d", expectedValidThrough, first.ValidThrough)
	}

	second := assets[1]
	if second.TotalGroup != 1 || second.UsedGroup != 1 {
		t.Fatalf("expected used asset quota 1/1, got %d/%d", second.UsedGroup, second.TotalGroup)
	}
}

func TestParseGMFQuotaAssetsFromLegacyAssetsShape(t *testing.T) {
	result := map[string]interface{}{
		"data": map[string]interface{}{
			"assets": []interface{}{
				map[string]interface{}{
					"asset_id":    "legacy-asset",
					"asset_type":  "GMF-100",
					"total_group": float64(10),
					"used_group":  float64(3),
				},
			},
		},
	}

	assets, err := parseGMFQuotaAssets(result)
	if err != nil {
		t.Fatalf("parseGMFQuotaAssets returned error: %v", err)
	}
	if len(assets) != 1 {
		t.Fatalf("expected 1 asset, got %d", len(assets))
	}
	if assets[0].AssetType != "GMF-100" || assets[0].TotalGroup != 10 || assets[0].UsedGroup != 3 {
		t.Fatalf("unexpected legacy asset: %+v", assets[0])
	}
}

func TestParseGMFQuotaAssetsRejectsUnexpectedShape(t *testing.T) {
	_, err := parseGMFQuotaAssets(map[string]interface{}{
		"data": map[string]interface{}{"unexpected": []interface{}{}},
	})
	if err == nil {
		t.Fatal("expected error for unexpected quota response shape")
	}
}
