package engine

import "testing"

func TestBuildProductEmbeddingLabel(t *testing.T) {
	cases := []struct {
		name     string
		ma       string
		web, ten string
		tt1, tt2 string
		want     string
	}{
		{"name + both attrs", "FF800", "FF800 Gloss", "FF800", "Gloss White", "L", "FF800 Gloss — FF800 — Gloss White — L"},
		{"web + ten distinct", "SP1", "LS2 FF818", "Mu fullface FF818", "", "", "LS2 FF818 — Mu fullface FF818"},
		{"only web", "SP2", "LS2 FF818", "", "", "", "LS2 FF818"},
		{"only ten", "SP3", "", "Mu fullface", "", "", "Mu fullface"},
		{"all empty → ma", "FF999", "", "", "", "", "FF999"},
		{"equalfold web/ten deduped", "SP5", "Foo", "foo", "", "", "Foo"},
		{"attr equalfold deduped", "SP7", "Storm", "", "storm", "L", "Storm — L"},
		{"whitespace trimmed", "SP6", "  LS2  ", " Mu  ", "  Đen  ", "", "LS2 — Mu — Đen"},
		{"only attrs", "SP8", "", "", "Trắng", "XL", "Trắng — XL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildProductEmbeddingLabel(tc.ma, tc.web, tc.ten, tc.tt1, tc.tt2)
			if got != tc.want {
				t.Errorf("buildProductEmbeddingLabel(%q,%q,%q,%q,%q) = %q, want %q",
					tc.ma, tc.web, tc.ten, tc.tt1, tc.tt2, got, tc.want)
			}
		})
	}
}

func TestIsSpecificSKUMatch(t *testing.T) {
	cases := []struct {
		name    string
		results []productEmbeddingMatch
		want    bool
	}{
		{"empty → false", nil, false},
		{
			"single result → specific",
			[]productEmbeddingMatch{{MA: "A1", MaCha: "P", Vector: 0.9}},
			true,
		},
		{
			"top dominates sibling → specific",
			[]productEmbeddingMatch{
				{MA: "A1", MaCha: "P", Vector: 0.90},
				{MA: "A2", MaCha: "P", Vector: 0.80},
			},
			true,
		},
		{
			"siblings clustered → family (not specific)",
			[]productEmbeddingMatch{
				{MA: "A1", MaCha: "P", Vector: 0.90},
				{MA: "A2", MaCha: "P", Vector: 0.89},
			},
			false,
		},
		{
			"next is different family → specific",
			[]productEmbeddingMatch{
				{MA: "A1", MaCha: "P", Vector: 0.90},
				{MA: "B1", MaCha: "Q", Vector: 0.899},
			},
			true,
		},
		{
			"different family then close sibling → family",
			[]productEmbeddingMatch{
				{MA: "A1", MaCha: "P", Vector: 0.90},
				{MA: "B1", MaCha: "Q", Vector: 0.88},
				{MA: "A2", MaCha: "P", Vector: 0.89},
			},
			false,
		},
		{
			"sibling gap exactly at threshold → family (strict >)",
			[]productEmbeddingMatch{
				{MA: "A1", MaCha: "P", Vector: 0.90},
				{MA: "A2", MaCha: "P", Vector: 0.85}, // gap 0.05, not > 0.05
			},
			false,
		},
		{
			"sibling gap just over threshold → specific",
			[]productEmbeddingMatch{
				{MA: "A1", MaCha: "P", Vector: 0.90},
				{MA: "A2", MaCha: "P", Vector: 0.849}, // gap 0.051 > 0.05
			},
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSpecificSKUMatch(tc.results); got != tc.want {
				t.Errorf("isSpecificSKUMatch(%+v) = %v, want %v", tc.results, got, tc.want)
			}
		})
	}
}

func TestPassesRelevanceFloor(t *testing.T) {
	cases := []struct {
		name string
		top  productEmbeddingMatch
		want bool
	}{
		{"strong positive rerank → accept", productEmbeddingMatch{Rerank: 9.1, Vector: 0.74}, true},
		{"small positive rerank → accept", productEmbeddingMatch{Rerank: 0.5, Vector: 0.40}, true},
		{"negative rerank with high cosine → fallback", productEmbeddingMatch{Rerank: -7.39, Vector: 0.62}, false},
		{"zero rerank → fallback (strict >)", productEmbeddingMatch{Rerank: 0.0, Vector: 0.80}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := passesRelevanceFloor(tc.top); got != tc.want {
				t.Errorf("passesRelevanceFloor(%+v) = %v, want %v", tc.top, got, tc.want)
			}
		})
	}
}

func TestBuildProductLexicalText(t *testing.T) {
	cases := []struct {
		name             string
		ma, maCha, label string
		want             string
	}{
		{"code + parent + label", "SP459780", "SPPARENT", "LS2 FF800 — White — L", "SP459780 SPPARENT LS2 FF800 — White — L"},
		{"ma == ma_cha deduped", "SP1", "sp1", "Mu fullface", "SP1 Mu fullface"},
		{"empty label", "SP1", "SPPARENT", "", "SP1 SPPARENT"},
		{"empty ma_cha", "SP1", "", "Mu fullface", "SP1 Mu fullface"},
		{"whitespace trimmed", "  SP1 ", " SPPARENT ", "  Label ", "SP1 SPPARENT Label"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildProductLexicalText(tc.ma, tc.maCha, tc.label); got != tc.want {
				t.Errorf("buildProductLexicalText(%q,%q,%q) = %q, want %q", tc.ma, tc.maCha, tc.label, got, tc.want)
			}
		})
	}
}

// TestEmbeddingSchemaVersionForcesRehash proves that folding the schema version
// and lexical text into the hash basis changes the hash versus the old
// label-only basis — so existing rows re-push once after the bump — while
// staying stable across repeated computation (idempotent on later syncs).
func TestEmbeddingSchemaVersionForcesRehash(t *testing.T) {
	label := "LS2 FF800 — White — L"
	lexical := "SP459780 SPPARENT " + label

	oldBasis := shortHash(label)
	newBasis := shortHash(embeddingSchemaVersion + "\x00" + label + "\x00" + lexical)
	if oldBasis == newBasis {
		t.Errorf("new hash basis must differ from the old label-only basis to force a re-push")
	}

	again := shortHash(embeddingSchemaVersion + "\x00" + label + "\x00" + lexical)
	if newBasis != again {
		t.Errorf("hash basis must be stable across calls, got %q then %q", newBasis, again)
	}
}

func TestShortHashStableAndDistinct(t *testing.T) {
	a := shortHash("LS2 FF818 — Storm III")
	b := shortHash("LS2 FF818 — Storm III")
	c := shortHash("LS2 FF818 — Storm IV")
	if a != b {
		t.Errorf("same input produced different hashes: %q vs %q", a, b)
	}
	if a == c {
		t.Errorf("different inputs produced same hash: %q", a)
	}
	if len(a) != 16 {
		t.Errorf("expected 16 hex chars, got %d", len(a))
	}
}

func TestDocID(t *testing.T) {
	got := docID("tenant-abc", "SP462693")
	want := "tenant-abc_SP462693"
	if got != want {
		t.Errorf("docID = %q, want %q", got, want)
	}
}

func TestProductEmbeddingConfigKeyspaceFallback(t *testing.T) {
	if cfg := (ProductEmbeddingConfig{}); cfg.keyspace() != "default_keyspace" {
		t.Errorf("empty keyspace must fall back to default_keyspace, got %q", cfg.keyspace())
	}
	if cfg := (ProductEmbeddingConfig{AstraKeyspace: "custom_ks"}); cfg.keyspace() != "custom_ks" {
		t.Errorf("explicit keyspace must be returned verbatim, got %q", cfg.keyspace())
	}
}

func TestProductEmbeddingConfigConfigured(t *testing.T) {
	cases := []struct {
		name string
		cfg  ProductEmbeddingConfig
		want bool
	}{
		{"empty", ProductEmbeddingConfig{}, false},
		{"only endpoint", ProductEmbeddingConfig{AstraEndpoint: "x"}, false},
		{"only token", ProductEmbeddingConfig{AstraToken: "y"}, false},
		{"both set", ProductEmbeddingConfig{AstraEndpoint: "x", AstraToken: "y"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.configured(); got != tc.want {
				t.Errorf("configured() = %v, want %v", got, tc.want)
			}
		})
	}
}
