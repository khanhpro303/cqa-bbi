package engine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeSetting struct {
	plain     string
	encrypted []byte
}

func lookupFrom(m map[string]fakeSetting) SettingLookup {
	return func(key string) (string, []byte, bool) {
		s, ok := m[key]
		return s.plain, s.encrypted, ok
	}
}

func TestResolveLangflowConfig_TenantSettingsWinOverStaleChannelOverride(t *testing.T) {
	lookup := lookupFrom(map[string]fakeSetting{
		SettingLangflowURL:          {plain: "https://lf.example"},
		SettingLangflowToken:        {plain: "sk-tenant-key-0001"},
		SettingLangflowFlowID:       {plain: "flow-private-good"},
		SettingLangflowPublicFlowID: {plain: "flow-public-good"},
	})
	channel := LangflowValues{APIKey: "sk-stale-channel-key-9999", FlowID: "flow-deleted"}
	env := LangflowValues{APIURL: "https://env", APIKey: "sk-env", FlowID: "flow-env"}

	cfg := ResolveLangflowConfig(lookup, nil, channel, env)

	if cfg.APIKey != "sk-tenant-key-0001" || cfg.PrivateFlowID != "flow-private-good" {
		t.Fatalf("tenant settings must win, got key=%q flow=%q", cfg.APIKey, cfg.PrivateFlowID)
	}
	if cfg.Sources["api_key"] != LangflowSourceTenant || cfg.Sources["flow_id"] != LangflowSourceTenant {
		t.Fatalf("unexpected sources: %v", cfg.Sources)
	}
	if len(cfg.Warnings) != 2 {
		t.Fatalf("expected 2 stale-override warnings, got %v", cfg.Warnings)
	}
	joined := strings.Join(cfg.Warnings, "\n")
	if strings.Contains(joined, "sk-stale-channel-key-9999") {
		t.Fatalf("warning leaked the full channel key: %s", joined)
	}
	if !strings.Contains(joined, "flow-deleted") {
		t.Fatalf("warning should name the ignored flow id: %s", joined)
	}
}

func TestResolveLangflowConfig_ChannelUsedWhenTenantEmpty(t *testing.T) {
	cfg := ResolveLangflowConfig(lookupFrom(nil), nil,
		LangflowValues{APIKey: "sk-channel", FlowID: "flow-channel"},
		LangflowValues{APIURL: "https://env", APIKey: "sk-env", FlowID: "flow-env"})

	if cfg.APIKey != "sk-channel" || cfg.PrivateFlowID != "flow-channel" || cfg.APIURL != "https://env" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.Sources["api_key"] != LangflowSourceChannel || cfg.Sources["url"] != LangflowSourceEnv {
		t.Fatalf("unexpected sources: %v", cfg.Sources)
	}
	if cfg.Sources["public_flow_id"] != LangflowSourceNone {
		t.Fatalf("public flow should be unset, got %v", cfg.Sources)
	}
	if len(cfg.Warnings) != 0 {
		t.Fatalf("no warnings expected, got %v", cfg.Warnings)
	}
}

func TestResolveLangflowConfig_DecryptsTenantToken(t *testing.T) {
	lookup := lookupFrom(map[string]fakeSetting{SettingLangflowToken: {encrypted: []byte("cipher")}})
	decrypt := func(b []byte) ([]byte, error) { return []byte("sk-decrypted"), nil }

	cfg := ResolveLangflowConfig(lookup, decrypt, LangflowValues{}, LangflowValues{APIKey: "sk-env"})

	if cfg.APIKey != "sk-decrypted" || cfg.Sources["api_key"] != LangflowSourceTenant {
		t.Fatalf("expected decrypted tenant token, got %q (%s)", cfg.APIKey, cfg.Sources["api_key"])
	}
}

func TestResolveLangflowConfig_DecryptFailureFallsBackAndWarns(t *testing.T) {
	lookup := lookupFrom(map[string]fakeSetting{SettingLangflowToken: {encrypted: []byte("cipher")}})
	decrypt := func(b []byte) ([]byte, error) { return nil, errors.New("bad key") }

	cfg := ResolveLangflowConfig(lookup, decrypt, LangflowValues{}, LangflowValues{APIKey: "sk-env"})

	if cfg.APIKey != "sk-env" || cfg.Sources["api_key"] != LangflowSourceEnv {
		t.Fatalf("expected env fallback after decrypt failure, got %q (%s)", cfg.APIKey, cfg.Sources["api_key"])
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "could not be decrypted") {
		t.Fatalf("expected decrypt warning, got %v", cfg.Warnings)
	}
}

func TestLangflowConfig_FlowFor(t *testing.T) {
	cfg := LangflowConfig{PrivateFlowID: "priv", PublicFlowID: "pub"}
	if cfg.FlowFor(true) != "priv" || cfg.FlowFor(false) != "pub" {
		t.Fatalf("unexpected flow routing")
	}
	noPublic := LangflowConfig{PrivateFlowID: "priv"}
	if noPublic.FlowFor(false) != "priv" {
		t.Fatalf("public must fall back to private flow when unset")
	}
}

func TestMaskLangflowSecret(t *testing.T) {
	cases := map[string]string{
		"":                        "<empty>",
		"short":                   "***(5)",
		"sk-abcdefghijklmnop1234": "sk-…1234(23)",
	}
	for in, want := range cases {
		if got := MaskLangflowSecret(in); got != want {
			t.Errorf("MaskLangflowSecret(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsPermanentLangflowError(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 422} {
		err := fmt.Errorf("langflow error: %w", newRunAPIError(status, []byte("x")))
		if !IsPermanentLangflowError(err) {
			t.Errorf("status %d should be permanent", status)
		}
	}
	for _, status := range []int{429, 500, 502, 503} {
		if IsPermanentLangflowError(newRunAPIError(status, []byte("x"))) {
			t.Errorf("status %d should be retryable", status)
		}
	}
	if IsPermanentLangflowError(errors.New("langflow request failed: timeout")) {
		t.Error("network errors should be retryable")
	}
}

func TestLangflowAPIError_KeepsLegacyMessage(t *testing.T) {
	err := newRunAPIError(403, []byte(`{"detail":"Invalid API key"}`))
	want := `langflow api error (status 403): {"detail":"Invalid API key"}`
	if err.Error() != want {
		t.Fatalf("got %q, want %q", err.Error(), want)
	}
}

func TestCheckFlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "sk-good" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"detail":"Invalid API key"}`))
			return
		}
		if r.URL.Path != "/api/v1/flows/flow-1" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"nodes":[]}}`))
	}))
	defer srv.Close()

	client := &LangflowClient{client: &http.Client{Timeout: 5 * time.Second}}
	ctx := context.Background()

	if err := client.CheckFlow(ctx, srv.URL, "sk-bad", "flow-1"); !IsPermanentLangflowError(err) {
		t.Fatalf("bad key should be a permanent error, got %v", err)
	}
	if err := client.CheckFlow(ctx, srv.URL, "sk-good", "flow-missing"); !IsPermanentLangflowError(err) {
		t.Fatalf("missing flow should be a permanent error, got %v", err)
	}
	if err := client.CheckFlow(ctx, srv.URL, "sk-good", "flow-1"); err != nil {
		t.Fatalf("valid key + flow should pass, got %v", err)
	}
	if err := client.CheckFlow(ctx, srv.URL, "", ""); err == nil {
		t.Fatal("empty config must error")
	}
}
