package cloudifyorders

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestZaloScreenshotCodeVerifiesIdentifiersAndScopesSession(t *testing.T) {
	profile := ZaloScreenshotProfile{
		TenantID: "tenant-a", ChannelID: "oa-a", ZaloOAID: "123456", ZaloGroupIDs: []string{"group-a"},
		StorageState: json.RawMessage(`{"cookies":[{"name":"example","value":"fixture","domain":".zalo.me"}],"origins":[]}`),
	}
	row := models.CloudifyOrderConfirmation{
		TenantID: profile.TenantID, ChannelID: profile.ChannelID, ZaloGroupID: "group-a",
		OutboundMessageID: "sent-a", MessageID: "reply-a", Text: "duyệt nhé",
	}
	evidence := channels.GMFOrderEvidence{SentAtMillis: 1795596006396, ReplyAtMillis: 1795596060000, SentText: "Phiếu đơn mẫu", SentType: "text"}
	code, err := zaloScreenshotCode(profile, row, evidence)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"group-a", "123456", "1795596006396", "1795596060000", "Phiếu đơn mẫu", "duyệt nhé", "page.screenshot", "https://oa.zalo.me/chat"} {
		if !strings.Contains(code, part) {
			t.Errorf("missing %q in capture script", part)
		}
	}
	old := row.TenantID
	row.TenantID = "another-tenant"
	if _, err := zaloScreenshotCode(profile, row, evidence); err == nil {
		t.Fatal("attempt to use a different tenant's browser session was accepted")
	}
	row.TenantID = old
	row.ZaloGroupID = "another-group"
	if _, err := zaloScreenshotCode(profile, row, evidence); err == nil {
		t.Fatal("a group outside the screenshot profile was accepted")
	}
	row.ZaloGroupID = "group-a"
	badEvidence := evidence
	badEvidence.ReplyAtMillis = badEvidence.SentAtMillis
	if _, err := zaloScreenshotCode(profile, row, badEvidence); err == nil {
		t.Fatal("ambiguous Zalo message timestamps were accepted")
	}
	profile.ZaloOAID = "unexpected"
	if _, err := zaloScreenshotCode(profile, row, evidence); err == nil {
		t.Fatal("invalid Zalo OA identifier was accepted")
	}
}

type screenshotRoundTripper func(*http.Request) (*http.Response, error)

func TestZaloScreenshotProfileCaptureScope(t *testing.T) {
	for _, test := range []struct {
		name, settings string
		allowed        bool
		wantError      bool
	}{
		{"pilot", `"zalo_group_ids":["pilot-id"]`, false, false},
		{"all linked", `"all_linked_groups":true`, true, false},
		{"ambiguous", `"all_linked_groups":true,"zalo_group_ids":["pilot-id"]`, false, true},
		{"empty", `"all_linked_groups":false`, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("ZALO_ORDER_SCREENSHOT_PROFILE", `{"tenant_id":"t","channel_id":"c","zalo_oa_id":"123",`+test.settings+`}`)
			profile, _, _, err := LoadZaloScreenshotProfile("t", "c")
			if (err != nil) != test.wantError {
				t.Fatalf("scope validation error = %v", err)
			}
			if profile.AllowsGroup("new-linked-group") != test.allowed || profile.AllowsGroup("bad.group") {
				t.Fatal("unexpected group scope")
			}
			other, _, _, err := LoadZaloScreenshotProfile("t", "other")
			if err != nil || other.AllowsGroup("new-linked-group") || other.AllowsGroup("pilot-id") {
				t.Fatal("scope escaped the configured channel")
			}
		})
	}
}

func (f screenshotRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBrowserRunPreservesPNGAndClosesSession(t *testing.T) {
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		status      int
		contentType string
		body        []byte
		wantError   bool
	}{
		{"original PNG", 200, "image/png", imageData.Bytes(), false},
		{"login failure", 422, "application/json", []byte(`{"success":false}`), true},
		{"mislabeled body", 200, "image/png", []byte("not a PNG"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var closed bool
			client := &http.Client{Transport: screenshotRoundTripper(func(req *http.Request) (*http.Response, error) {
				if req.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("missing token")
				}
				switch req.Method {
				case http.MethodPost:
					if req.URL.Path != "/browser-run/script" {
						t.Fatalf("unexpected script path %q", req.URL.Path)
					}
					return &http.Response{StatusCode: tc.status, Header: http.Header{
						"Content-Type": {tc.contentType}, "Cf-Browser-Session-Id": {"session-a"},
					}, Body: io.NopCloser(bytes.NewReader(tc.body))}, nil
				case http.MethodDelete:
					closed = req.URL.Path == "/browser-run/devtools/browser/session-a"
					return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
				}
				t.Fatalf("unexpected request %s", req.Method)
				return nil, nil
			})}
			got, err := runZaloScreenshot(context.Background(), client, "https://example.invalid/browser-run", "test-token", []byte("{}"))
			if (err != nil) != tc.wantError {
				t.Fatalf("capture error = %v, want error: %v", err, tc.wantError)
			}
			if !tc.wantError && !bytes.Equal(got, imageData.Bytes()) {
				t.Fatal("stored evidence does not match Browser Run image bytes")
			}
			if !closed {
				t.Fatal("browser session remained open")
			}
		})
	}
}
