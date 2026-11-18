package engine

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestResolveLangflowConfigPrecedence(t *testing.T) {
	cfg := &config.Config{LangflowAPIURL: "http://env/", LangflowAPIKey: "env-key", LangflowFlowID: "env-flow", LangflowPublicFlowID: "env-pub"}
	settings := map[string]models.AppSetting{
		"ai_engine_langflow_flow_id":        {ValuePlain: "tenant-flow"},
		"ai_engine_langflow_public_flow_id": {ValuePlain: "tenant-pub"},
		"ai_engine_langflow_token":          {ValuePlain: "tenant-key"},
	}

	got := ResolveLangflowConfig(LangflowOverrides{FlowID: "chan-flow"}, settings, cfg)
	if got.FlowID != "chan-flow" || got.FlowIDSource != LangflowSourceChannel {
		t.Errorf("flow = %q/%s; want channel override", got.FlowID, got.FlowIDSource)
	}
	if got.PublicFlowID != "tenant-pub" || got.PublicFlowIDSource != LangflowSourceTenant {
		t.Errorf("public flow = %q/%s; want tenant", got.PublicFlowID, got.PublicFlowIDSource)
	}
	if got.APIKey != "tenant-key" || got.APIKeySource != LangflowSourceTenant {
		t.Errorf("key = %q/%s; want tenant", got.APIKey, got.APIKeySource)
	}
	if got.APIURL != "http://env" || got.APIURLSource != LangflowSourceEnv {
		t.Errorf("url = %q/%s; want env without trailing slash", got.APIURL, got.APIURLSource)
	}
	if !got.UsesChannelOverride() {
		t.Error("UsesChannelOverride should be true")
	}

	tenant := ResolveTenantLangflowConfig(settings, cfg)
	if tenant.FlowID != "tenant-flow" || tenant.UsesChannelOverride() {
		t.Errorf("tenant config = %+v; want tenant-flow without override", tenant)
	}
	if got.Equivalent(tenant, "private") {
		t.Error("channel override and tenant config target different private flows")
	}
	if !got.Equivalent(tenant, "public") {
		t.Error("public flow is identical, configs should be equivalent")
	}
}

func TestFlowForPublicFallsBackToPrivate(t *testing.T) {
	c := LangflowConfig{FlowID: "priv", FlowIDSource: LangflowSourceTenant}
	if f, _ := c.FlowFor("public"); f != "priv" {
		t.Errorf("public flow = %q; want fallback to private flow", f)
	}
}

func TestIsLangflowConfigError(t *testing.T) {
	if !IsLangflowConfigError(errors.New(`langflow api error (status 403): {"detail":"Invalid or missing API key"}`)) {
		t.Error("403 should be a config error")
	}
	if IsLangflowConfigError(errors.New("langflow api error (status 502): bad gateway")) {
		t.Error("502 should be retryable")
	}
	if IsLangflowConfigError(nil) {
		t.Error("nil is not an error")
	}
}

func TestCheckLangflowFlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "good" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.URL.Path != "/api/v1/flows/f1" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"name":"BBI_RAG_Bot_Int","data":{"nodes":[]}}`))
	}))
	defer srv.Close()
	ctx := context.Background()

	if r := CheckLangflowFlow(ctx, srv.URL, "good", "f1"); !r.OK || r.Name != "BBI_RAG_Bot_Int" {
		t.Errorf("good config: %+v", r)
	}
	if r := CheckLangflowFlow(ctx, srv.URL, "bad", "f1"); r.Problem != "bad_key" {
		t.Errorf("bad key: %+v", r)
	}
	if r := CheckLangflowFlow(ctx, srv.URL, "good", "missing"); r.Problem != "flow_missing" {
		t.Errorf("missing flow: %+v", r)
	}
	if r := CheckLangflowFlow(ctx, "", "good", "f1"); r.Problem != "not_configured" {
		t.Errorf("not configured: %+v", r)
	}
}
