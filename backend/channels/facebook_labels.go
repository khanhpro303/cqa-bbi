package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	facebookLabelPageLimit  = 100
	facebookLabelCountLimit = 10000
	facebookLabelBodyLimit  = 1 << 20
)

// FacebookLabel is a custom label configured in Meta Business Suite Inbox.
type FacebookLabel struct {
	ID   string `json:"id"`
	Name string `json:"page_label_name"`
}

// LabelAPIError is a sanitized Meta Graph API error. It deliberately omits
// request identifiers, access tokens, PSIDs, response bodies and Meta's error
// message because those values may contain credentials or customer data.
type LabelAPIError struct {
	Kind    string
	Code    int
	Subcode int
}

func (e *LabelAPIError) Error() string {
	if e == nil {
		return "facebook label api error"
	}
	if e.Code != 0 && e.Subcode != 0 {
		return fmt.Sprintf("facebook label api error: %s (code %d, subcode %d)", e.Kind, e.Code, e.Subcode)
	}
	if e.Code != 0 {
		return fmt.Sprintf("facebook label api error: %s (code %d)", e.Kind, e.Code)
	}
	return fmt.Sprintf("facebook label api error: %s", e.Kind)
}

type facebookGraphError struct {
	Code         int `json:"code"`
	ErrorSubcode int `json:"error_subcode"`
}

type facebookPaging struct {
	Next    string `json:"next"`
	Cursors struct {
		After string `json:"after"`
	} `json:"cursors"`
}

type facebookLabelsResponse struct {
	Data   json.RawMessage     `json:"data"`
	Error  *facebookGraphError `json:"error"`
	Paging *facebookPaging     `json:"paging"`
}

type facebookParticipantsResponse struct {
	Participants json.RawMessage     `json:"participants"`
	Error        *facebookGraphError `json:"error"`
}

type facebookParticipants struct {
	Data   json.RawMessage `json:"data"`
	Paging *facebookPaging `json:"paging"`
}

type facebookParticipant struct {
	ID string `json:"id"`
}

// FetchPageLabels returns every custom label configured for the adapter's Page.
func (f *FacebookAdapter) FetchPageLabels(ctx context.Context) ([]FacebookLabel, error) {
	return f.fetchLabels(ctx, f.creds.PageID)
}

// FetchUserLabels returns the custom labels currently attached to a Page-scoped
// user ID (PSID).
func (f *FacebookAdapter) FetchUserLabels(ctx context.Context, psid string) ([]FacebookLabel, error) {
	return f.fetchLabels(ctx, psid)
}

// ResolveConversationPSID resolves a Graph conversation ID to its sole
// non-Page participant. Group or incompletely paginated participant lists are
// rejected because they cannot be mapped to one customer safely.
func (f *FacebookAdapter) ResolveConversationPSID(ctx context.Context, conversationID string) (string, error) {
	if strings.TrimSpace(conversationID) == "" || strings.TrimSpace(f.creds.PageID) == "" {
		return "", labelError("invalid_response", 0, 0)
	}

	endpoint := graphObjectURL(conversationID, "")
	query := endpoint.Query()
	query.Set("fields", "participants")
	endpoint.RawQuery = query.Encode()

	body, status, err := f.getLabelAPI(ctx, endpoint)
	if err != nil {
		return "", err
	}

	var response facebookParticipantsResponse
	if err := decodeLabelJSON(body, &response); err != nil {
		return "", classifyLabelResponseError(status, nil)
	}
	if response.Error != nil {
		return "", classifyLabelResponseError(status, response.Error)
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return "", classifyLabelResponseError(status, nil)
	}
	if rawJSONMissing(response.Participants) {
		return "", labelError("invalid_response", 0, 0)
	}

	var participants facebookParticipants
	if err := decodeLabelJSON(response.Participants, &participants); err != nil || rawJSONMissing(participants.Data) {
		return "", labelError("invalid_response", 0, 0)
	}
	if participants.Paging != nil && strings.TrimSpace(participants.Paging.Next) != "" {
		return "", labelError("invalid_response", 0, 0)
	}

	var entries []facebookParticipant
	if err := decodeLabelJSON(participants.Data, &entries); err != nil {
		return "", labelError("invalid_response", 0, 0)
	}

	nonPage := make(map[string]struct{})
	for _, participant := range entries {
		id := strings.TrimSpace(participant.ID)
		if id == "" {
			return "", labelError("invalid_response", 0, 0)
		}
		if id != f.creds.PageID {
			nonPage[id] = struct{}{}
		}
	}
	if len(nonPage) != 1 {
		return "", labelError("invalid_response", 0, 0)
	}
	for id := range nonPage {
		return id, nil
	}
	return "", labelError("invalid_response", 0, 0)
}

func (f *FacebookAdapter) fetchLabels(ctx context.Context, objectID string) ([]FacebookLabel, error) {
	if strings.TrimSpace(objectID) == "" {
		return nil, labelError("invalid_response", 0, 0)
	}

	labels := make([]FacebookLabel, 0)
	seenLabels := make(map[string]string)
	seenCursors := make(map[string]struct{})
	after := ""

	for page := 0; page < facebookLabelPageLimit; page++ {
		endpoint := graphObjectURL(objectID, "custom_labels")
		query := endpoint.Query()
		query.Set("fields", "page_label_name")
		query.Set("limit", "100")
		if after != "" {
			query.Set("after", after)
		}
		endpoint.RawQuery = query.Encode()

		body, status, err := f.getLabelAPI(ctx, endpoint)
		if err != nil {
			return nil, err
		}

		var response facebookLabelsResponse
		if err := decodeLabelJSON(body, &response); err != nil {
			return nil, classifyLabelResponseError(status, nil)
		}
		if response.Error != nil {
			return nil, classifyLabelResponseError(status, response.Error)
		}
		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			return nil, classifyLabelResponseError(status, nil)
		}
		if rawJSONMissing(response.Data) {
			return nil, labelError("invalid_response", 0, 0)
		}

		var pageLabels []FacebookLabel
		if err := decodeLabelJSON(response.Data, &pageLabels); err != nil {
			return nil, labelError("invalid_response", 0, 0)
		}
		for _, item := range pageLabels {
			item.ID = strings.TrimSpace(item.ID)
			item.Name = strings.TrimSpace(item.Name)
			if item.ID == "" || item.Name == "" {
				return nil, labelError("invalid_response", 0, 0)
			}
			if existingName, exists := seenLabels[item.ID]; exists {
				if existingName != item.Name {
					return nil, labelError("invalid_response", 0, 0)
				}
				continue
			}
			if len(labels) >= facebookLabelCountLimit {
				return nil, labelError("invalid_response", 0, 0)
			}
			seenLabels[item.ID] = item.Name
			labels = append(labels, item)
		}

		nextAfter := ""
		if response.Paging != nil {
			hasNext := strings.TrimSpace(response.Paging.Next) != ""
			if !hasNext {
				return labels, nil
			}
			nextAfter = strings.TrimSpace(response.Paging.Cursors.After)
			if nextAfter == "" {
				return nil, labelError("invalid_response", 0, 0)
			}
		}
		if nextAfter == "" {
			return labels, nil
		}
		if _, exists := seenCursors[nextAfter]; exists {
			return nil, labelError("invalid_response", 0, 0)
		}
		seenCursors[nextAfter] = struct{}{}
		after = nextAfter
	}

	return nil, labelError("invalid_response", 0, 0)
}

func (f *FacebookAdapter) getLabelAPI(ctx context.Context, endpoint *url.URL) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, 0, labelError("invalid_response", 0, 0)
	}
	req.Header.Set("Authorization", "Bearer "+f.creds.AccessToken)

	client := f.client
	if client == nil {
		client = http.DefaultClient
	}
	safeClient := *client
	safeClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	response, err := safeClient.Do(req)
	if err != nil {
		return nil, 0, labelError("transport", 0, 0)
	}
	defer response.Body.Close()

	limited := io.LimitReader(response.Body, facebookLabelBodyLimit+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, response.StatusCode, labelError("transport", 0, 0)
	}
	if len(body) > facebookLabelBodyLimit {
		return nil, response.StatusCode, labelError("invalid_response", 0, 0)
	}
	return body, response.StatusCode, nil
}

func graphObjectURL(objectID, edge string) *url.URL {
	base, _ := url.Parse(fbGraphBase)
	base.Path = strings.TrimSuffix(base.Path, "/") + "/" + url.PathEscape(objectID)
	if edge != "" {
		base.Path += "/" + url.PathEscape(edge)
	}
	return base
}

func decodeLabelJSON(body []byte, target interface{}) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}

func rawJSONMissing(value json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(value))
	return trimmed == "" || trimmed == "null"
}

func classifyLabelResponseError(status int, graphErr *facebookGraphError) error {
	if graphErr != nil {
		kind := labelErrorKindForCode(graphErr.Code)
		return labelError(kind, graphErr.Code, graphErr.ErrorSubcode)
	}
	switch status {
	case http.StatusUnauthorized:
		return labelError("auth", status, 0)
	case http.StatusForbidden:
		return labelError("permission", status, 0)
	case http.StatusTooManyRequests:
		return labelError("rate_limit", status, 0)
	case http.StatusNotFound:
		return labelError("unsupported", status, 0)
	default:
		return labelError("invalid_response", status, 0)
	}
}

func labelErrorKindForCode(code int) string {
	switch code {
	case 190:
		return "auth"
	case 4, 17, 32, 613:
		return "rate_limit"
	case 10, 200:
		return "permission"
	case 100:
		return "unsupported"
	default:
		return "invalid_response"
	}
}

func labelError(kind string, code, subcode int) *LabelAPIError {
	return &LabelAPIError{Kind: kind, Code: code, Subcode: subcode}
}
