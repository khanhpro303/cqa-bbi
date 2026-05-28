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
			[]productEmbeddingMatch{{MA: "A1", MaCha: "P", Similarity: 0.9}},
			true,
		},
		{
			"top dominates sibling → specific",
			[]productEmbeddingMatch{
				{MA: "A1", MaCha: "P", Similarity: 0.90},
				{MA: "A2", MaCha: "P", Similarity: 0.80},
			},
			true,
		},
		{
			"siblings clustered → family (not specific)",
			[]productEmbeddingMatch{
				{MA: "A1", MaCha: "P", Similarity: 0.90},
				{MA: "A2", MaCha: "P", Similarity: 0.89},
			},
			false,
		},
		{
			"next is different family → specific",
			[]productEmbeddingMatch{
				{MA: "A1", MaCha: "P", Similarity: 0.90},
				{MA: "B1", MaCha: "Q", Similarity: 0.899},
			},
			true,
		},
		{
			"different family then close sibling → family",
			[]productEmbeddingMatch{
				{MA: "A1", MaCha: "P", Similarity: 0.90},
				{MA: "B1", MaCha: "Q", Similarity: 0.88},
				{MA: "A2", MaCha: "P", Similarity: 0.89},
			},
			false,
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
