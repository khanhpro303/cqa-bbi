package engine

import (
	"reflect"
	"testing"
)

func TestRankProductWebGroupsDedupsByWebName(t *testing.T) {
	products := []map[string]interface{}{
		{"MA": "SP458484-RED", "MA_CHA": "SP458484", "TEN_DONG_BO_WEB": "FF901"},
		{"MA": "SP458484-BLUE", "MA_CHA": "SP458484", "TEN_DONG_BO_WEB": "FF901"},
		{"MA": "SP458516-BLK", "MA_CHA": "SP458516", "TEN_DONG_BO_WEB": "FF901 Carbon"},
		{"MA": "SP001187-S", "MA_CHA": "SP001187", "TEN_DONG_BO_WEB": "FF901"},
	}

	groups := RankProductWebGroups(products)

	if len(groups) != 2 {
		t.Fatalf("expected 2 dedup groups, got %d: %#v", len(groups), groups)
	}

	if groups[0].WebName != "FF901" {
		t.Fatalf("top group webname = %q; want FF901", groups[0].WebName)
	}
	if groups[0].Count != 3 {
		t.Fatalf("FF901 count = %d; want 3", groups[0].Count)
	}
	wantParents := []string{"SP001187", "SP458484"}
	if !reflect.DeepEqual(groups[0].ParentCodes, wantParents) {
		t.Fatalf("FF901 parent codes = %#v; want %#v (sorted)", groups[0].ParentCodes, wantParents)
	}

	if groups[1].WebName != "FF901 Carbon" {
		t.Fatalf("group[1] webname = %q; want FF901 Carbon", groups[1].WebName)
	}
	if groups[1].Count != 1 || !reflect.DeepEqual(groups[1].ParentCodes, []string{"SP458516"}) {
		t.Fatalf("FF901 Carbon group = %#v; want count=1 parents=[SP458516]", groups[1])
	}

	for _, g := range groups {
		if g.IsFallback {
			t.Fatalf("did not expect fallback group when web names are present: %#v", g)
		}
	}
}

func TestRankProductWebGroupsFallsBackWhenWebNameMissing(t *testing.T) {
	products := []map[string]interface{}{
		{"MA": "SKU-A1", "MA_CHA": "SP999", "TEN_DONG_BO_WEB": ""},
		{"MA": "SKU-A2", "MA_CHA": "SP999", "TEN_DONG_BO_WEB": "   "},
	}

	groups := RankProductWebGroups(products)

	if len(groups) != 1 {
		t.Fatalf("expected single fallback group, got %d: %#v", len(groups), groups)
	}
	if !groups[0].IsFallback {
		t.Fatalf("expected IsFallback=true, got %#v", groups[0])
	}
	if groups[0].WebName != "SP999" {
		t.Fatalf("fallback webname = %q; want SP999", groups[0].WebName)
	}
	if groups[0].Count != 2 {
		t.Fatalf("fallback count = %d; want 2", groups[0].Count)
	}
}

func TestRankProductWebGroupsTieBreaksAlphabeticallyByWebName(t *testing.T) {
	products := []map[string]interface{}{
		{"MA": "SKU1", "MA_CHA": "SP1", "TEN_DONG_BO_WEB": "Zeta"},
		{"MA": "SKU2", "MA_CHA": "SP2", "TEN_DONG_BO_WEB": "Alpha"},
		{"MA": "SKU3", "MA_CHA": "SP3", "TEN_DONG_BO_WEB": "Mu"},
	}

	groups := RankProductWebGroups(products)

	if len(groups) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(groups))
	}
	wantOrder := []string{"Alpha", "Mu", "Zeta"}
	for i, want := range wantOrder {
		if groups[i].WebName != want {
			t.Fatalf("groups[%d].WebName = %q; want %q (alpha order on tie)", i, groups[i].WebName, want)
		}
	}
}

func TestRankProductWebGroupsSkipsRecordsWithNoIdentifiers(t *testing.T) {
	products := []map[string]interface{}{
		{"MA": "", "MA_CHA": "", "TEN_DONG_BO_WEB": ""},
		{"MA": "SKU1", "MA_CHA": "SP1", "TEN_DONG_BO_WEB": "FF901"},
	}

	groups := RankProductWebGroups(products)

	if len(groups) != 1 {
		t.Fatalf("expected only valid product to produce a group, got %d: %#v", len(groups), groups)
	}
	if groups[0].WebName != "FF901" {
		t.Fatalf("unexpected group: %#v", groups[0])
	}
}

func TestRankProductWebGroupsAcceptsLowerCaseKeysAndMaFallback(t *testing.T) {
	products := []map[string]interface{}{
		{"ma": "SKU1", "ma_cha": "", "ten_dong_bo_web": "FF901"},
	}

	groups := RankProductWebGroups(products)

	if len(groups) != 1 || groups[0].WebName != "FF901" {
		t.Fatalf("expected single FF901 group, got %#v", groups)
	}
	if len(groups[0].ParentCodes) != 1 || groups[0].ParentCodes[0] != "SKU1" {
		t.Fatalf("expected MA fallback to populate ParentCodes, got %#v", groups[0])
	}
}

