package engine

import "testing"

func TestBuildProductEmbeddingLabel(t *testing.T) {
	cases := []struct {
		name     string
		maCha    string
		web, ten string
		want     string
	}{
		{"both distinct → joined", "SP1", "LS2 FF818", "Mu fullface FF818", "LS2 FF818 — Mu fullface FF818"},
		{"only web → web", "SP2", "LS2 FF818", "", "LS2 FF818"},
		{"only ten → ten", "SP3", "", "Mu fullface", "Mu fullface"},
		{"both empty → ma_cha", "SP4", "", "", "SP4"},
		{"equalfold → no merge", "SP5", "Foo", "foo", "Foo"},
		{"whitespace trimmed", "SP6", "  LS2  ", " Mu  ", "LS2 — Mu"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildProductEmbeddingLabel(tc.maCha, tc.web, tc.ten)
			if got != tc.want {
				t.Errorf("buildProductEmbeddingLabel(%q,%q,%q) = %q, want %q",
					tc.maCha, tc.web, tc.ten, got, tc.want)
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
