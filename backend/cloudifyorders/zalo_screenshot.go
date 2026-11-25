package cloudifyorders

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
)

//go:embed zalo_screenshot.js
var zaloScreenshotScript string

const maxZaloScreenshotBytes = 5 << 20

var screenshotID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var cloudflareAccountID = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)

// ZaloScreenshotProfile is installation-specific. Its selectors must reference
// actual Zalo OA Manager DOM attributes and match a unique live element.
type ZaloScreenshotProfile struct {
	TenantID                   string          `json:"tenant_id"`
	ChannelID                  string          `json:"channel_id"`
	ZaloGroupIDs               []string        `json:"zalo_group_ids"`
	OpenGroupSelector          string          `json:"open_group_selector"`
	ActiveConversationSelector string          `json:"active_conversation_selector"`
	MessageSelector            string          `json:"message_selector"`
	StorageState               json.RawMessage `json:"storage_state"`
}

// AllowsGroup keeps Browser Run restricted to groups explicitly enabled for capture.
func (p ZaloScreenshotProfile) AllowsGroup(id string) bool {
	for _, groupID := range p.ZaloGroupIDs {
		if id == groupID && screenshotID.MatchString(id) {
			return true
		}
	}
	return false
}

func LoadZaloScreenshotProfile(tenantID, channelID string) (ZaloScreenshotProfile, string, string, error) {
	var profile ZaloScreenshotProfile
	raw := os.Getenv("ZALO_ORDER_SCREENSHOT_PROFILE")
	if raw == "" {
		return profile, "", "", nil
	}
	if err := json.Unmarshal([]byte(raw), &profile); err != nil {
		return profile, "", "", errors.New("invalid Zalo screenshot profile JSON")
	}
	if profile.TenantID != tenantID || profile.ChannelID != channelID {
		return profile, "", "", nil
	}
	if len(profile.ZaloGroupIDs) == 0 {
		return profile, "", "", errors.New("Zalo screenshot profile must specify at least one group")
	}
	accountID := strings.TrimSpace(os.Getenv("ZALO_ORDER_SCREENSHOT_CF_ACCOUNT_ID"))
	token := strings.TrimSpace(os.Getenv("ZALO_ORDER_SCREENSHOT_CF_API_TOKEN"))
	return profile, accountID, token, nil
}

func CaptureZaloOrderScreenshot(ctx context.Context, profile ZaloScreenshotProfile, accountID, token string, row models.CloudifyOrderConfirmation) ([]byte, error) {
	if !cloudflareAccountID.MatchString(accountID) || token == "" || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("invalid Cloudflare Browser Run credentials")
	}
	script, err := zaloScreenshotCode(profile, row)
	if err != nil {
		return nil, err
	}
	request, err := json.Marshal(map[string]any{"code": script, "library": "playwright", "timeout": 35000})
	if err != nil {
		return nil, err
	}
	return runZaloScreenshot(ctx, &http.Client{
		Timeout:       42 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, "https://api.cloudflare.com/client/v4/accounts/"+accountID+"/browser-run", token, request)
}

func zaloScreenshotCode(profile ZaloScreenshotProfile, row models.CloudifyOrderConfirmation) (string, error) {
	if row.TenantID == "" || row.ChannelID == "" || row.TenantID != profile.TenantID || row.ChannelID != profile.ChannelID {
		return "", errors.New("Zalo screenshot profile does not match this tenant and channel")
	}
	if !profile.AllowsGroup(row.ZaloGroupID) {
		return "", errors.New("Zalo group is outside the browser screenshot profile")
	}
	if !screenshotID.MatchString(row.OutboundMessageID) ||
		!screenshotID.MatchString(row.MessageID) || row.OutboundMessageID == row.MessageID ||
		strings.TrimSpace(row.Text) == "" {
		return "", errors.New("Zalo screenshot is missing the exact group and message identifiers")
	}
	if err := validateZaloStorageState(profile.StorageState); err != nil {
		return "", err
	}
	for _, template := range []struct{ text, placeholder string }{
		{profile.OpenGroupSelector, "{group_id}"},
		{profile.ActiveConversationSelector, "{group_id}"},
		{profile.MessageSelector, "{message_id}"},
	} {
		if len(template.text) > 1024 || strings.Count(template.text, template.placeholder) != 1 {
			return "", errors.New("Zalo screenshot selector must contain its exact identifier placeholder")
		}
	}
	params, err := json.Marshal(map[string]any{
		"storageState": profile.StorageState,
		"openGroup":    strings.Replace(profile.OpenGroupSelector, "{group_id}", row.ZaloGroupID, 1),
		"conversation": strings.Replace(profile.ActiveConversationSelector, "{group_id}", row.ZaloGroupID, 1),
		"original":     strings.Replace(profile.MessageSelector, "{message_id}", row.OutboundMessageID, 1),
		"reply":        strings.Replace(profile.MessageSelector, "{message_id}", row.MessageID, 1),
		"replyText":    strings.TrimSpace(row.Text),
	})
	if err != nil {
		return "", err
	}
	return strings.Replace(zaloScreenshotScript, "__CQA_SCREENSHOT_INPUT__", string(params), 1), nil
}

func validateZaloStorageState(raw []byte) error {
	var state struct {
		Cookies []struct{ Domain string } `json:"cookies"`
		Origins []struct{ Origin string } `json:"origins"`
	}
	if len(raw) > 1<<20 || json.Unmarshal(raw, &state) != nil || len(state.Cookies)+len(state.Origins) == 0 {
		return errors.New("missing or invalid Zalo browser storage state")
	}
	for _, cookie := range state.Cookies {
		if !zaloBrowserDomain(strings.TrimPrefix(cookie.Domain, ".")) {
			return errors.New("Zalo screenshot profile contains cookies outside Zalo domains")
		}
	}
	for _, value := range state.Origins {
		origin, err := url.Parse(value.Origin)
		if err != nil || origin.Scheme != "https" || !zaloBrowserDomain(origin.Hostname()) {
			return errors.New("Zalo screenshot profile contains storage outside Zalo domains")
		}
	}
	return nil
}

func zaloBrowserDomain(domain string) bool {
	for _, base := range []string{"zalo.me", "zaloapp.com", "zdn.vn"} {
		if domain == base || strings.HasSuffix(domain, "."+base) {
			return true
		}
	}
	return false
}

func runZaloScreenshot(ctx context.Context, client *http.Client, endpoint, token string, body []byte) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/script?keep_alive=10000", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("Cloudflare Browser Run request failed")
	}
	defer response.Body.Close()
	if sessionID := response.Header.Get("CF-Browser-Session-ID"); screenshotID.MatchString(sessionID) {
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(closeCtx, http.MethodDelete, endpoint+"/devtools/browser/"+sessionID, nil)
			if err == nil {
				req.Header.Set("Authorization", "Bearer "+token)
				if done, err := client.Do(req); err == nil {
					done.Body.Close()
				}
			}
		}()
	}
	if response.StatusCode != http.StatusOK || !strings.EqualFold(response.Header.Get("Content-Type"), "image/png") {
		return nil, fmt.Errorf("Cloudflare Browser Run did not return a screenshot (HTTP %d)", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxZaloScreenshotBytes+1))
	if err != nil || len(data) > maxZaloScreenshotBytes {
		return nil, errors.New("cannot read Cloudflare Browser Run screenshot within the size limit")
	}
	dimensions, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || dimensions.Width <= 0 || dimensions.Height <= 0 || dimensions.Width > 16_000_000/dimensions.Height {
		return nil, errors.New("invalid Cloudflare Browser Run screenshot")
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return nil, errors.New("invalid Cloudflare Browser Run screenshot data")
	}
	return data, nil
}
