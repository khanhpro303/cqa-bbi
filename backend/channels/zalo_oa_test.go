package channels

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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

func TestNormalizeGMFMemberUserIDsTrimsDeduplicatesAndLimits(t *testing.T) {
	members := normalizeGMFMemberUserIDs([]string{"  user-1 ", "", "user-2", "user-1", "user-3"}, 2)

	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
	if members[0] != "user-1" || members[1] != "user-2" {
		t.Fatalf("unexpected members: %#v", members)
	}
}

func TestCreateGMFGroupValidatesRequiredFieldsBeforeRequest(t *testing.T) {
	adapter := NewZaloOAAdapter(ZaloOACredentials{})

	if _, _, err := adapter.CreateGMFGroup(t.Context(), "", "desc", "asset-1", []string{"user-1"}); err == nil {
		t.Fatal("expected missing group_name to fail")
	}
	if _, _, err := adapter.CreateGMFGroup(t.Context(), "Group", "desc", "", []string{"user-1"}); err == nil {
		t.Fatal("expected missing asset_id to fail")
	}
	if _, _, err := adapter.CreateGMFGroup(t.Context(), "Group", "desc", "asset-1", []string{"  "}); err == nil {
		t.Fatal("expected missing member_user_ids to fail")
	}
}

func TestChunkMessageTextShortStaysSingle(t *testing.T) {
	got := chunkMessageText("Tổng tồn kho: 208.0", 1800)
	if len(got) != 1 {
		t.Fatalf("expected 1 chunk for short text, got %d", len(got))
	}
	if got[0] != "Tổng tồn kho: 208.0" {
		t.Fatalf("content altered: %q", got[0])
	}
}

func TestChunkMessageTextSplitsLongBreakdownUnderCap(t *testing.T) {
	// Reproduces the real bug: a 32-line stock breakdown rejected by Zalo.
	var b strings.Builder
	b.WriteString("Tổng tồn kho của dòng LS2 FF901: 208.0\n\nChi tiết:\n")
	for i := 0; i < 200; i++ {
		b.WriteString(fmt.Sprintf("- LS2 FF901 Nón bảo hiểm Gloss Black Size XL biến thể %d: 12.0\n", i))
	}
	text := b.String()
	cap := 1800

	chunks := chunkMessageText(text, cap)
	if len(chunks) < 2 {
		t.Fatalf("expected long text to split, got %d chunk(s)", len(chunks))
	}
	for i, c := range chunks {
		if n := utf8.RuneCountInString(c); n > cap {
			t.Fatalf("chunk %d exceeds cap: %d > %d", i, n, cap)
		}
		if !utf8.ValidString(c) {
			t.Fatalf("chunk %d split a multibyte rune (invalid UTF-8)", i)
		}
	}
	// No content is lost: rejoining on '\n' reproduces the original lines.
	if rejoined := strings.Join(chunks, "\n"); rejoined != text {
		t.Fatalf("rejoined chunks differ from original\n got: %q", rejoined)
	}
}

func TestChunkMessageTextHardSplitsOverlongLine(t *testing.T) {
	line := strings.Repeat("á", 5000) // single line, no newline, multibyte
	chunks := chunkMessageText(line, 1800)
	if len(chunks) < 3 {
		t.Fatalf("expected an overlong single line to hard-split, got %d", len(chunks))
	}
	for i, c := range chunks {
		if n := utf8.RuneCountInString(c); n > 1800 {
			t.Fatalf("chunk %d exceeds cap: %d", i, n)
		}
		if !utf8.ValidString(c) {
			t.Fatalf("chunk %d is invalid UTF-8 (rune cut)", i)
		}
	}
	if strings.Join(chunks, "") != line {
		t.Fatalf("hard-split lost content")
	}
}

func TestPageReachedSince(t *testing.T) {
	since := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return since.Add(time.Duration(m) * time.Minute) }

	tests := []struct {
		name  string
		times []time.Time
		since time.Time
		want  bool
	}{
		{"zero since never stops", []time.Time{at(-60), at(-90)}, time.Time{}, false},
		{"empty page", nil, since, false},
		{"newest first, all new", []time.Time{at(30), at(10)}, since, false},
		{"newest first, crosses since", []time.Time{at(30), at(-10)}, since, true},
		{"newest first, all old", []time.Time{at(-5), at(-50)}, since, true},
		{"oldest first is not trusted", []time.Time{at(-50), at(30)}, since, false},
		{"missing timestamp keeps paging", []time.Time{at(30), {}}, since, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pageReachedSince(tt.times, tt.since); got != tt.want {
				t.Errorf("pageReachedSince() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsZaloQuotaError(t *testing.T) {
	if !IsZaloQuotaError(fmt.Errorf("fetch messages: %w", &ZaloAPIError{Code: -32, Message: "Your OA reached limit call api"})) {
		t.Fatal("-32 is the quota error")
	}
	if IsZaloQuotaError(&ZaloAPIError{Code: -1, Message: "Error fetching messages: -2"}) || IsZaloQuotaError(fmt.Errorf("network down")) {
		t.Fatal("other errors are not quota errors")
	}
	if got := (&ZaloAPIError{Code: -1, Message: "Error fetching messages: -2"}).Error(); got != "zalo api error -1: Error fetching messages: -2" {
		t.Fatalf("error text changed: %q", got)
	}
}
