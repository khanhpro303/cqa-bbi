package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	zaloAPIBaseV2 = "https://openapi.zalo.me/v2.0/oa"
	zaloAPIBaseV3 = "https://openapi.zalo.me/v3.0/oa"
	zaloOAuthURL  = "https://oauth.zaloapp.com/v4/oa/access_token"
)

// ZaloOACredentials holds the credentials needed for Zalo OA API.
type ZaloOACredentials struct {
	AppID        string `json:"app_id"`
	AppSecret    string `json:"app_secret"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	OAId         string `json:"oa_id"`
}

// OnTokenRefresh is called when tokens are refreshed — caller should persist new creds.
type OnTokenRefresh func(newAccessToken, newRefreshToken string)

type ZaloOAAdapter struct {
	creds          ZaloOACredentials
	client         *http.Client
	mu             sync.Mutex
	onTokenRefresh OnTokenRefresh
}

func NewZaloOAAdapter(creds ZaloOACredentials) *ZaloOAAdapter {
	return &ZaloOAAdapter{
		creds:  creds,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (z *ZaloOAAdapter) SetTokenRefreshCallback(cb OnTokenRefresh) {
	z.onTokenRefresh = cb
}

// refreshToken performs Zalo token refresh (single-use rotation).
func (z *ZaloOAAdapter) refreshToken(ctx context.Context) error {
	z.mu.Lock()
	defer z.mu.Unlock()

	form := url.Values{
		"refresh_token": {z.creds.RefreshToken},
		"app_id":        {z.creds.AppID},
		"grant_type":    {"refresh_token"},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", zaloOAuthURL, nil)
	if err != nil {
		return fmt.Errorf("create zalo token refresh request: %w", err)
	}
	req.URL.RawQuery = form.Encode()
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("secret_key", z.creds.AppSecret)

	resp, err := z.client.Do(req)
	if err != nil {
		return fmt.Errorf("zalo token refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Error        int    `json:"error"`
		Message      string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("zalo token refresh decode failed: %w", err)
	}
	if result.Error != 0 {
		return fmt.Errorf("zalo token refresh error %d: %s", result.Error, result.Message)
	}

	z.creds.AccessToken = result.AccessToken
	z.creds.RefreshToken = result.RefreshToken

	if z.onTokenRefresh != nil {
		z.onTokenRefresh(result.AccessToken, result.RefreshToken)
	}

	return nil
}

// doRequest makes an authenticated Zalo API request with auto-retry on token expiry.
func (z *ZaloOAAdapter) doRequest(ctx context.Context, method, apiURL string, params map[string]interface{}) (map[string]interface{}, error) {
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, apiURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create zalo api request: %w", err)
		}

		// Zalo API: params go as JSON-encoded `data` query param
		if params != nil {
			q := req.URL.Query()
			paramsJSON, _ := json.Marshal(params)
			q.Set("data", string(paramsJSON))
			req.URL.RawQuery = q.Encode()
		}

		z.mu.Lock()
		token := z.creds.AccessToken
		z.mu.Unlock()
		req.Header["access_token"] = []string{token}

		resp, err := z.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("zalo api request failed: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("zalo api read body failed: %w", err)
		}

		log.Printf("[zalo] API %s: status=%d len=%d", apiURL, resp.StatusCode, len(body))

		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("zalo api decode failed: %w", err)
		}

		// Check for token expired error (error=-216)
		if errCode, ok := result["error"].(float64); ok && errCode == -216 && attempt == 0 {
			z.mu.Lock()
			currentToken := z.creds.AccessToken
			z.mu.Unlock()

			if currentToken == token {
				if refreshErr := z.refreshToken(ctx); refreshErr != nil {
					return nil, fmt.Errorf("token refresh failed: %w", refreshErr)
				}
			}
			continue
		}

		if errCode, ok := result["error"].(float64); ok && errCode != 0 {
			msg, _ := result["message"].(string)
			return nil, fmt.Errorf("zalo api error %v: %s", errCode, msg)
		}

		return result, nil
	}
	return nil, fmt.Errorf("zalo api failed after retry")
}

func (z *ZaloOAAdapter) FetchRecentConversations(ctx context.Context, since time.Time, limit int) ([]SyncedConversation, error) {
	var conversations []SyncedConversation
	offset := 0
	pageSize := 10 // Zalo max is 10

	for {
		if limit > 0 && len(conversations) >= limit {
			break
		}

		result, err := z.doRequest(ctx, "GET", zaloAPIBaseV2+"/listrecentchat", map[string]interface{}{
			"offset": offset,
			"count":  pageSize,
		})
		if err != nil {
			return conversations, err
		}

		// Zalo response can be {"data": [...]} or {"data": {"data": [...]}}
		data := extractZaloDataArray(result)
		log.Printf("[zalo] extractZaloDataArray returned %d items, result keys: %v, data type: %T", len(data), mapKeys(result), result["data"])
		if len(data) == 0 {
			break
		}

		for _, item := range data {
			conv, ok := item.(map[string]interface{})
			if !ok {
				continue
			}

			// Zalo listrecentchat: src=0 means OA sent (from=OA, to=customer), src=1 means customer sent
			var userID, displayName string
			src, _ := conv["src"].(float64)
			if src == 0 {
				// OA sent last message → customer is "to"
				userID, _ = conv["to_id"].(string)
				displayName, _ = conv["to_display_name"].(string)
			} else {
				// Customer sent last message → customer is "from"
				userID, _ = conv["from_id"].(string)
				displayName, _ = conv["from_display_name"].(string)
			}

			// Parse timestamp (Zalo uses milliseconds)
			var lastMsgAt time.Time
			if ts, ok := conv["time"].(float64); ok {
				lastMsgAt = time.UnixMilli(int64(ts))
			}

			// Don't filter by since here — let DB dedup handle it.
			// Zalo listrecentchat is already sorted newest first and max 10 per page.

			conversations = append(conversations, SyncedConversation{
				ExternalID:     userID, // Zalo uses user_id as conversation key
				ExternalUserID: userID,
				CustomerName:   displayName,
				LastMessageAt:  lastMsgAt,
				Metadata:       conv,
			})
		}

		if len(data) < pageSize {
			break
		}
		offset += pageSize
	}

	return conversations, nil
}

func (z *ZaloOAAdapter) FetchMessages(ctx context.Context, conversationID string, since time.Time) ([]SyncedMessage, error) {
	var messages []SyncedMessage
	offset := 0
	pageSize := 10

	for {
		result, err := z.doRequest(ctx, "GET", zaloAPIBaseV2+"/conversation", map[string]interface{}{
			"user_id": conversationID,
			"offset":  offset,
			"count":   pageSize,
		})
		if err != nil {
			return messages, err
		}

		data := extractZaloDataArray(result)
		if len(data) == 0 {
			break
		}

		for _, item := range data {
			msg, ok := item.(map[string]interface{})
			if !ok {
				continue
			}

			var sentAt time.Time
			if ts, ok := msg["time"].(float64); ok {
				sentAt = time.UnixMilli(int64(ts))
			}

			// Don't filter by since — let DB dedup handle duplicates

			msgID := fmt.Sprintf("%v", msg["message_id"])
			content, _ := msg["message"].(string)
			senderType := "customer"
			senderName := ""

			if src, ok := msg["src"].(float64); ok && src == 0 {
				senderType = "agent"
				senderName = "OA"
			}
			if from, ok := msg["from_display_name"].(string); ok && senderType == "customer" {
				senderName = from
			}

			syncedMsg := SyncedMessage{
				ExternalID:  msgID,
				SenderType:  senderType,
				SenderName:  senderName,
				Content:     content,
				ContentType: "text",
				SentAt:      sentAt,
				RawData:     msg,
			}

			// Check for attachments (image, file, sticker, gif, etc.)
			if msgType, ok := msg["type"].(string); ok && msgType != "text" {
				syncedMsg.ContentType = msgType

				// Extract attachment URL from Zalo message
				aURL := ""
				aName := ""
				if u, ok := msg["url"].(string); ok && u != "" {
					aURL = u
				} else if u, ok := msg["thumb"].(string); ok && u != "" {
					aURL = u
				}
				// For file type: check links array
				if links, ok := msg["links"].([]interface{}); ok && len(links) > 0 {
					if link, ok := links[0].(map[string]interface{}); ok {
						if u, ok := link["url"].(string); ok {
							aURL = u
						}
						if n, ok := link["name"].(string); ok {
							aName = n
						}
					}
				}
				if aURL != "" {
					if aName == "" {
						aName = fmt.Sprintf("%s-%s", msgType, msgID)
					}
					syncedMsg.Attachments = append(syncedMsg.Attachments, Attachment{
						Type: msgType,
						URL:  aURL,
						Name: aName,
					})
				}
			}

			messages = append(messages, syncedMsg)
		}

		if len(data) < pageSize {
			break
		}
		offset += pageSize
	}

	return messages, nil
}

// extractZaloDataArray handles both {"data": [...]} and {"data": {"data": [...]}}
func extractZaloDataArray(result map[string]interface{}) []interface{} {
	if arr, ok := result["data"].([]interface{}); ok {
		return arr
	}
	if nested, ok := result["data"].(map[string]interface{}); ok {
		if arr, ok := nested["data"].([]interface{}); ok {
			return arr
		}
	}
	return nil
}

func mapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func (z *ZaloOAAdapter) HealthCheck(ctx context.Context) error {
	_, err := z.doRequest(ctx, "GET", zaloAPIBaseV2+"/getoa", nil)
	return err
}

func (z *ZaloOAAdapter) SendMessage(ctx context.Context, conversationID string, content string) error {
	payload := map[string]interface{}{
		"recipient": map[string]interface{}{
			"user_id": conversationID,
		},
		"message": map[string]interface{}{
			"text": content,
		},
	}

	result, err := z.doRequest(ctx, "POST", zaloAPIBaseV3+"/message/cs", payload)
	if err != nil {
		return fmt.Errorf("zalo send message failed: %w", err)
	}

	// Zalo API returns error code in "error" field
	if errCode, ok := result["error"].(float64); ok && errCode != 0 {
		msg, _ := result["message"].(string)
		return fmt.Errorf("zalo send message api error %v: %s", errCode, msg)
	}

	return nil
}

type ZaloUserProfile struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Avatar      string `json:"avatar"`
}

func (z *ZaloOAAdapter) FetchUserProfile(ctx context.Context, userID string) (*ZaloUserProfile, error) {
	result, err := z.doRequest(ctx, "GET", zaloAPIBaseV3+"/user/detail", map[string]interface{}{
		"user_id": userID,
	})
	if err != nil {
		return nil, err
	}

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid profile data in response")
	}

	profile := &ZaloUserProfile{
		UserID: userID,
	}
	if name, ok := data["display_name"].(string); ok {
		profile.DisplayName = name
	}
	if avatar, ok := data["avatar"].(string); ok {
		profile.Avatar = avatar
	}

	return profile, nil
}

// doRequestJSON makes an authenticated Zalo API request with a JSON request body.
func (z *ZaloOAAdapter) doRequestJSON(ctx context.Context, method, apiURL string, body interface{}) (map[string]interface{}, error) {
	for attempt := 0; attempt < 2; attempt++ {
		var bodyReader io.Reader
		if body != nil {
			bodyBytes, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("marshal json body: %w", err)
			}
			bodyReader = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequestWithContext(ctx, method, apiURL, bodyReader)
		if err != nil {
			return nil, fmt.Errorf("create zalo api json request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")

		z.mu.Lock()
		token := z.creds.AccessToken
		z.mu.Unlock()
		req.Header["access_token"] = []string{token}

		resp, err := z.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("zalo api json request failed: %w", err)
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("zalo api json read body failed: %w", err)
		}

		log.Printf("[zalo] JSON API %s: status=%d len=%d", apiURL, resp.StatusCode, len(respBody))

		var result map[string]interface{}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return nil, fmt.Errorf("zalo api json decode failed: %w", err)
		}

		// Check for token expired error (error=-216)
		if errCode, ok := result["error"].(float64); ok && errCode == -216 && attempt == 0 {
			z.mu.Lock()
			currentToken := z.creds.AccessToken
			z.mu.Unlock()

			if currentToken == token {
				if refreshErr := z.refreshToken(ctx); refreshErr != nil {
					return nil, fmt.Errorf("token refresh failed: %w", refreshErr)
				}
			}
			continue
		}

		if errCode, ok := result["error"].(float64); ok && errCode != 0 {
			msg, _ := result["message"].(string)
			return nil, fmt.Errorf("zalo api json error %v: %s", errCode, msg)
		}

		return result, nil
	}
	return nil, fmt.Errorf("zalo api json failed after retry")
}

type GMFAssetInfo struct {
	AssetID      string `json:"asset_id"`
	AssetType    string `json:"asset_type"` // e.g. GMF-10, GMF-50, GMF-100, GMF-1000
	TotalGroup   int    `json:"total_group"`
	UsedGroup    int    `json:"used_group"`
	ValidThrough int64  `json:"valid_through"` // milliseconds
}

const zaloGMFMaxMembersPerCreateRequest = 99

// GetGMFQuota retrieves the available GMF assets of the OA
func (z *ZaloOAAdapter) GetGMFQuota(ctx context.Context) ([]GMFAssetInfo, error) {
	productTypes := []string{"gmf10", "gmf50", "gmf100", "gmf1000"}
	quotaTypes := []string{"sub_quota", "purchase_quota", "reward_quota"}

	var mu sync.Mutex
	var assets []GMFAssetInfo
	var wg sync.WaitGroup

	var succeededCount int
	var lastErr error

	for _, pt := range productTypes {
		for _, qt := range quotaTypes {
			wg.Add(1)
			go func(prodType, quotaType string) {
				defer wg.Done()

				payload := map[string]interface{}{
					"quota_owner":  "OA",
					"product_type": prodType,
					"quota_type":   quotaType,
				}

				result, err := z.doRequestJSON(ctx, "POST", "https://openapi.zalo.me/v3.0/oa/quota/group", payload)
				if err != nil {
					mu.Lock()
					lastErr = err
					mu.Unlock()
					return
				}

				localAssets, parseErr := parseGMFQuotaAssets(result)
				if parseErr != nil {
					mu.Lock()
					lastErr = parseErr
					mu.Unlock()
					return
				}

				mu.Lock()
				succeededCount++
				assets = append(assets, localAssets...)
				mu.Unlock()
			}(pt, qt)
		}
	}

	wg.Wait()

	if succeededCount == 0 && lastErr != nil {
		return nil, fmt.Errorf("failed to fetch GMF quota from Zalo API: %w", lastErr)
	}

	// Deduplicate assets by asset_id
	seen := make(map[string]bool)
	var uniqueAssets []GMFAssetInfo
	for _, asset := range assets {
		if asset.AssetID != "" && !seen[asset.AssetID] {
			seen[asset.AssetID] = true
			uniqueAssets = append(uniqueAssets, asset)
		}
	}

	return uniqueAssets, nil
}

func parseGMFQuotaAssets(result map[string]interface{}) ([]GMFAssetInfo, error) {
	data, ok := result["data"]
	if !ok {
		return nil, fmt.Errorf("invalid GMF quota response: missing data")
	}

	assetsRaw, ok := data.([]interface{})
	if !ok {
		if dataMap, isMap := data.(map[string]interface{}); isMap {
			assetsRaw, ok = dataMap["assets"].([]interface{})
		}
	}
	if !ok {
		return nil, fmt.Errorf("invalid GMF quota response: data is %T", data)
	}

	assets := make([]GMFAssetInfo, 0, len(assetsRaw))
	for _, raw := range assetsRaw {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		asset, ok := parseGMFQuotaAsset(m)
		if ok {
			assets = append(assets, asset)
		}
	}
	return assets, nil
}

func parseGMFQuotaAsset(m map[string]interface{}) (GMFAssetInfo, bool) {
	assetID, _ := m["asset_id"].(string)
	if assetID == "" {
		return GMFAssetInfo{}, false
	}

	productType, _ := m["product_type"].(string)
	quotaType, _ := m["quota_type"].(string)
	assetType, _ := m["asset_type"].(string)
	if assetType == "" {
		assetType = strings.ToUpper(productType)
		if assetType != "" && quotaType != "" {
			assetType = fmt.Sprintf("%s - %s", assetType, quotaType)
		}
	}

	status, _ := m["status"].(string)
	usedID, _ := m["used_id"].(string)
	usedGroup := 0
	if strings.EqualFold(status, "used") || usedID != "" {
		usedGroup = 1
	}

	asset := GMFAssetInfo{
		AssetID:      assetID,
		AssetType:    assetType,
		TotalGroup:   1,
		UsedGroup:    usedGroup,
		ValidThrough: parseGMFValidThrough(m["valid_through"]),
	}
	if total, ok := parseGMFInt(m["total_group"]); ok {
		asset.TotalGroup = total
	}
	if used, ok := parseGMFInt(m["used_group"]); ok {
		asset.UsedGroup = used
	}
	return asset, true
}

func parseGMFInt(value interface{}) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case json.Number:
		n, err := v.Int64()
		return int(n), err == nil
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		return n, err == nil
	default:
		return 0, false
	}
}

func parseGMFValidThrough(value interface{}) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return n
		}
	case string:
		raw := strings.TrimSpace(v)
		if raw == "" {
			return 0
		}
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return n
		}
		for _, layout := range []string{"02/01/2006", "2006-01-02", time.RFC3339} {
			parsed, err := time.ParseInLocation(layout, raw, time.UTC)
			if err == nil {
				return parsed.UnixMilli()
			}
		}
	}
	return 0
}

// CreateGMFGroup calls the creategroupwithoa API
func (z *ZaloOAAdapter) CreateGMFGroup(ctx context.Context, name, description, assetID string, memberUserIDs []string) (string, string, error) {
	groupName := strings.TrimSpace(name)
	if groupName == "" {
		return "", "", fmt.Errorf("group_name is required")
	}

	assetID = strings.TrimSpace(assetID)
	if assetID == "" {
		return "", "", fmt.Errorf("asset_id is required")
	}

	members := normalizeGMFMemberUserIDs(memberUserIDs, zaloGMFMaxMembersPerCreateRequest)
	if len(members) == 0 {
		return "", "", fmt.Errorf("member_user_ids is required")
	}

	payload := map[string]interface{}{
		"group_name":      groupName,
		"asset_id":        assetID,
		"member_user_ids": members,
	}
	if groupDescription := strings.TrimSpace(description); groupDescription != "" {
		payload["group_description"] = groupDescription
	}

	result, err := z.doRequestJSON(ctx, "POST", "https://openapi.zalo.me/v3.0/oa/group/creategroupwithoa", payload)
	if err != nil {
		return "", "", err
	}

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return "", "", fmt.Errorf("invalid create group response structure")
	}

	groupID, _ := data["group_id"].(string)
	groupLink, _ := data["group_link"].(string)

	if groupID == "" {
		return "", "", fmt.Errorf("zalo API did not return group_id")
	}

	return groupID, groupLink, nil
}

func normalizeGMFMemberUserIDs(memberUserIDs []string, limit int) []string {
	if limit <= 0 {
		return []string{}
	}

	seen := make(map[string]bool, len(memberUserIDs))
	members := make([]string, 0, len(memberUserIDs))
	for _, rawID := range memberUserIDs {
		userID := strings.TrimSpace(rawID)
		if userID == "" || seen[userID] {
			continue
		}
		seen[userID] = true
		members = append(members, userID)
		if len(members) >= limit {
			break
		}
	}
	return members
}

// DeleteGMFGroup disbands a group chat on Zalo OA
func (z *ZaloOAAdapter) DeleteGMFGroup(ctx context.Context, groupID string) error {
	payload := map[string]interface{}{
		"group_id": groupID,
	}

	_, err := z.doRequestJSON(ctx, "POST", "https://openapi.zalo.me/v3.0/oa/group/delete", payload)
	return err
}

// InviteGMFGroupMembers invites users into a GMF group chat
func (z *ZaloOAAdapter) InviteGMFGroupMembers(ctx context.Context, groupID string, memberUserIDs []string) error {
	payload := map[string]interface{}{
		"group_id":        groupID,
		"member_user_ids": memberUserIDs,
	}

	_, err := z.doRequestJSON(ctx, "POST", "https://openapi.zalo.me/v3.0/oa/group/invite", payload)
	return err
}

// RemoveGMFGroupMembers removes users from a GMF group chat
func (z *ZaloOAAdapter) RemoveGMFGroupMembers(ctx context.Context, groupID string, memberUserIDs []string) error {
	payload := map[string]interface{}{
		"group_id":        groupID,
		"member_user_ids": memberUserIDs,
	}

	_, err := z.doRequestJSON(ctx, "POST", "https://openapi.zalo.me/v3.0/oa/group/removemembers", payload)
	return err
}

type GMFGroupMember struct {
	OAID   string `json:"oa_id,omitempty"`
	UserID string `json:"user_id,omitempty"`
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
}

type GMFGroupMembersData struct {
	Offset      int              `json:"offset"`
	Count       int              `json:"count"`
	Total       int              `json:"total"`
	MemberCount int              `json:"member_count"`
	Members     []GMFGroupMember `json:"members"`
}

// doRequestQueryParams makes an authenticated GET request with standard query parameters.
func (z *ZaloOAAdapter) doRequestQueryParams(ctx context.Context, method, apiURL string, params map[string]string) (map[string]interface{}, error) {
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, apiURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create zalo api request: %w", err)
		}

		if params != nil {
			q := req.URL.Query()
			for k, v := range params {
				q.Set(k, v)
			}
			req.URL.RawQuery = q.Encode()
		}

		z.mu.Lock()
		token := z.creds.AccessToken
		z.mu.Unlock()
		req.Header["access_token"] = []string{token}

		resp, err := z.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("zalo api request failed: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("zalo api read body failed: %w", err)
		}

		log.Printf("[zalo] GET API %s: status=%d len=%d", apiURL, resp.StatusCode, len(body))

		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("zalo api decode failed: %w", err)
		}

		// Check for token expired error (error=-216)
		if errCode, ok := result["error"].(float64); ok && errCode == -216 && attempt == 0 {
			z.mu.Lock()
			currentToken := z.creds.AccessToken
			z.mu.Unlock()

			if currentToken == token {
				if refreshErr := z.refreshToken(ctx); refreshErr != nil {
					return nil, fmt.Errorf("token refresh failed: %w", refreshErr)
				}
			}
			continue
		}

		if errCode, ok := result["error"].(float64); ok && errCode != 0 {
			msg, _ := result["message"].(string)
			return nil, fmt.Errorf("zalo api error %v: %s", errCode, msg)
		}

		return result, nil
	}
	return nil, fmt.Errorf("zalo api failed after retry")
}

// GetGMFGroupMembers fetches the list of members in a GMF group chat.
func (z *ZaloOAAdapter) GetGMFGroupMembers(ctx context.Context, groupID string, offset, count int) ([]GMFGroupMember, error) {
	params := map[string]string{
		"group_id": groupID,
		"offset":   strconv.Itoa(offset),
		"count":    strconv.Itoa(count),
	}

	result, err := z.doRequestQueryParams(ctx, "GET", "https://openapi.zalo.me/v3.0/oa/group/listmember", params)
	if err != nil {
		return nil, err
	}

	dataVal := result["data"]
	if dataVal == nil {
		return []GMFGroupMember{}, nil
	}

	dataBytes, err := json.Marshal(dataVal)
	if err != nil {
		return nil, fmt.Errorf("marshal data: %w", err)
	}

	var data GMFGroupMembersData
	if err := json.Unmarshal(dataBytes, &data); err == nil {
		return data.Members, nil
	}

	var membersArr []GMFGroupMember
	if err := json.Unmarshal(dataBytes, &membersArr); err == nil {
		return membersArr, nil
	}

	return []GMFGroupMember{}, nil
}

// GetGMFGroupPendingInvites fetches the list of pending invites in a GMF group chat.
func (z *ZaloOAAdapter) GetGMFGroupPendingInvites(ctx context.Context, groupID string, offset, count int) ([]GMFGroupMember, error) {
	params := map[string]string{
		"group_id": groupID,
		"offset":   strconv.Itoa(offset),
		"count":    strconv.Itoa(count),
	}

	result, err := z.doRequestQueryParams(ctx, "GET", "https://openapi.zalo.me/v3.0/oa/group/listpendinginvite", params)
	if err != nil {
		return nil, err
	}

	dataVal := result["data"]
	if dataVal == nil {
		return []GMFGroupMember{}, nil
	}

	dataBytes, err := json.Marshal(dataVal)
	if err != nil {
		return nil, fmt.Errorf("marshal data: %w", err)
	}

	var data GMFGroupMembersData
	if err := json.Unmarshal(dataBytes, &data); err == nil {
		return data.Members, nil
	}

	var membersArr []GMFGroupMember
	if err := json.Unmarshal(dataBytes, &membersArr); err == nil {
		return membersArr, nil
	}

	return []GMFGroupMember{}, nil
}

// AcceptGMFGroupPendingInvites accepts pending invites to a GMF group chat.
func (z *ZaloOAAdapter) AcceptGMFGroupPendingInvites(ctx context.Context, groupID string, memberUserIDs []string) error {
	payload := map[string]interface{}{
		"group_id":        groupID,
		"member_user_ids": memberUserIDs,
	}

	_, err := z.doRequestJSON(ctx, "POST", "https://openapi.zalo.me/v3.0/oa/group/acceptpendinginvite", payload)
	return err
}

// RejectGMFGroupPendingInvites rejects pending invites to a GMF group chat.
func (z *ZaloOAAdapter) RejectGMFGroupPendingInvites(ctx context.Context, groupID string, memberUserIDs []string) error {
	payload := map[string]interface{}{
		"group_id":        groupID,
		"member_user_ids": memberUserIDs,
	}

	_, err := z.doRequestJSON(ctx, "POST", "https://openapi.zalo.me/v3.0/oa/group/rejectpendinginvite", payload)
	return err
}

