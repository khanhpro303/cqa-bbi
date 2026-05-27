package engine

import "testing"

// These tests cover the deterministic, DB-free branches of
// BuildTenantEmbedder. The encrypted/plain lookup branches require a live
// gorm.DB and are exercised end-to-end by manual smoke tests (see plan
// verification section) — the project has no sqlite/mock test harness today.

func TestBuildTenantEmbedder_NoFallback_NoTenant(t *testing.T) {
	got := BuildTenantEmbedder("", EmbeddingConfig{})
	if got != nil {
		t.Errorf("expected nil when no tenant and no fallback, got %v", got)
	}
}

func TestBuildTenantEmbedder_UsesFallbackWhenTenantLookupSkipped(t *testing.T) {
	// Empty tenant short-circuits the DB lookup, falling through to the
	// env-derived FallbackAPIKey.
	got := BuildTenantEmbedder("", EmbeddingConfig{
		FallbackAPIKey: "sk-env-fallback",
		Model:          "text-embedding-ada-002",
	})
	if got == nil {
		t.Fatal("expected embedder built from fallback key")
	}
	if got.Model() != "text-embedding-ada-002" {
		t.Errorf("model = %q", got.Model())
	}
}
