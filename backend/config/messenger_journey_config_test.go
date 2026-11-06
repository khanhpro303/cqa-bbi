package config

import "testing"

func setRequiredJourneyTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("JWT_SECRET", "test-jwt-secret-at-least-32-chars-long")
	t.Setenv("ENCRYPTION_KEY", "12345678901234567890123456789012")
	t.Setenv("DB_PASSWORD", "testpassword")
}

func TestMessengerJourneyDefaultsDisabled(t *testing.T) {
	setRequiredJourneyTestEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.MessengerJourneyEnabled {
		t.Fatal("messenger journey must be opt-in")
	}
}

func TestMessengerJourneyRequiresSeparateSecretsAndTenant(t *testing.T) {
	setRequiredJourneyTestEnv(t)
	t.Setenv("CQA_MESSENGER_JOURNEY_ENABLED", "true")
	if _, err := Load(); err == nil {
		t.Fatal("enabled journey accepted missing integration configuration")
	}

	t.Setenv("CQA_ABZ_EXPORT_TENANT_ID", "tenant-a")
	t.Setenv("CQA_ABZ_EXPORT_SECRET", "export-secret")
	t.Setenv("CQA_MESSENGER_RELAY_SECRET", "export-secret")
	if _, err := Load(); err == nil {
		t.Fatal("enabled journey accepted a shared read/write secret")
	}

	t.Setenv("CQA_MESSENGER_RELAY_SECRET", "relay-secret")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("enabled journey rejected valid configuration: %v", err)
	}
	if !cfg.MessengerJourneyEnabled || cfg.MessengerRelaySecret != "relay-secret" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}
