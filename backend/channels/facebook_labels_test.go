package channels

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type labelRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn labelRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func newLabelTestAdapter(fn labelRoundTripFunc) *FacebookAdapter {
	adapter := NewFacebookAdapter(FacebookCredentials{PageID: "page-1", AccessToken: "secret-token"})
	adapter.client = &http.Client{Transport: fn}
	return adapter
}

func labelJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func requireLabelError(t *testing.T, err error, kind string, code, subcode int) *LabelAPIError {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var apiErr *LabelAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T; want *LabelAPIError", err)
	}
	if apiErr.Kind != kind || apiErr.Code != code || apiErr.Subcode != subcode {
		t.Fatalf("error = %#v; want kind=%q code=%d subcode=%d", apiErr, kind, code, subcode)
	}
	return apiErr
}

func TestFacebookAdapterFetchPageLabelsPaginatesWithCursorAndDeduplicates(t *testing.T) {
	requests := 0
	adapter := newLabelTestAdapter(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Method != http.MethodGet {
			t.Fatalf("method = %s; want GET", request.Method)
		}
		if request.URL.Scheme != "https" || request.URL.Host != "graph.facebook.com" {
			t.Fatalf("unexpected Graph origin: %s", request.URL.String())
		}
		if request.URL.Path != "/v21.0/page-1/custom_labels" {
			t.Fatalf("path = %s", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Fatalf("authorization = %q", got)
		}
		if request.URL.Query().Get("access_token") != "" {
			t.Fatal("access token must not be placed in URL")
		}
		if requests == 1 {
			if request.URL.Query().Get("after") != "" {
				t.Fatal("first request must not have an after cursor")
			}
			return labelJSONResponse(http.StatusOK, `{"data":[{"id":"1","page_label_name":"Potential"},{"id":"2","page_label_name":"Qualified"}],"paging":{"cursors":{"after":"cursor-2"},"next":"https://evil.example/steal?access_token=x"}}`), nil
		}
		if got := request.URL.Query().Get("after"); got != "cursor-2" {
			t.Fatalf("after = %q; want cursor-2", got)
		}
		return labelJSONResponse(http.StatusOK, `{"data":[{"id":"2","page_label_name":"Qualified"},{"id":"3","page_label_name":"Not qualified"}]}`), nil
	})

	labels, err := adapter.FetchPageLabels(context.Background())
	if err != nil {
		t.Fatalf("FetchPageLabels returned error: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d; want 2", requests)
	}
	if len(labels) != 3 || labels[0].ID != "1" || labels[1].ID != "2" || labels[2].ID != "3" {
		t.Fatalf("labels = %#v", labels)
	}
}

func TestFacebookAdapterFetchUserLabelsAllowsEmptyData(t *testing.T) {
	adapter := newLabelTestAdapter(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v21.0/psid-9/custom_labels" {
			t.Fatalf("path = %s", request.URL.Path)
		}
		return labelJSONResponse(http.StatusOK, `{"data":[]}`), nil
	})

	labels, err := adapter.FetchUserLabels(context.Background(), "psid-9")
	if err != nil {
		t.Fatalf("FetchUserLabels returned error: %v", err)
	}
	if labels == nil || len(labels) != 0 {
		t.Fatalf("labels = %#v; want non-nil empty slice", labels)
	}
}

func TestFacebookAdapterFetchLabelsAcceptsTerminalCursorWithoutNext(t *testing.T) {
	requests := 0
	adapter := newLabelTestAdapter(func(*http.Request) (*http.Response, error) {
		requests++
		return labelJSONResponse(http.StatusOK, `{"data":[{"id":"1","page_label_name":"Potential"}],"paging":{"cursors":{"after":"terminal-cursor"}}}`), nil
	})

	labels, err := adapter.FetchPageLabels(context.Background())
	if err != nil {
		t.Fatalf("FetchPageLabels returned error: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d; terminal cursor must not trigger another request", requests)
	}
	if len(labels) != 1 || labels[0].ID != "1" {
		t.Fatalf("labels = %#v", labels)
	}
}

func TestFacebookAdapterFetchLabelsRejectsMissingNullAndMalformedData(t *testing.T) {
	tests := []string{
		`{}`,
		`{"data":null}`,
		`{"data":{}}`,
		`{"data":[{"id":"","page_label_name":"Potential"}]}`,
		`{"data":[{"id":"1","page_label_name":""}]}`,
		`{"data":[{"id":"1","page_label_name":"A"},{"id":"1","page_label_name":"B"}]}`,
	}
	for _, body := range tests {
		t.Run(body, func(t *testing.T) {
			adapter := newLabelTestAdapter(func(*http.Request) (*http.Response, error) {
				return labelJSONResponse(http.StatusOK, body), nil
			})
			_, err := adapter.FetchPageLabels(context.Background())
			requireLabelError(t, err, "invalid_response", 0, 0)
		})
	}
}

func TestFacebookAdapterFetchLabelsClassifiesGraphErrors(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		kind    string
		code    int
		subcode int
	}{
		{"permission", `{"error":{"message":"sensitive","code":10,"error_subcode":2018065}}`, "permission", 10, 2018065},
		{"rate limit", `{"error":{"message":"slow down","code":613}}`, "rate_limit", 613, 0},
		{"auth", `{"error":{"message":"token secret-token invalid","code":190}}`, "auth", 190, 0},
		{"unsupported", `{"error":{"message":"field unsupported","code":100}}`, "unsupported", 100, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			adapter := newLabelTestAdapter(func(*http.Request) (*http.Response, error) {
				return labelJSONResponse(http.StatusBadRequest, tc.body), nil
			})
			_, err := adapter.FetchPageLabels(context.Background())
			apiErr := requireLabelError(t, err, tc.kind, tc.code, tc.subcode)
			if strings.Contains(apiErr.Error(), "secret-token") || strings.Contains(apiErr.Error(), "sensitive") {
				t.Fatalf("sanitized error leaked response data: %s", apiErr.Error())
			}
		})
	}
}

func TestFacebookAdapterFetchLabelsClassifiesHTTPAndTransportErrors(t *testing.T) {
	adapter := newLabelTestAdapter(func(*http.Request) (*http.Response, error) {
		return labelJSONResponse(http.StatusTooManyRequests, `not-json`), nil
	})
	_, err := adapter.FetchPageLabels(context.Background())
	requireLabelError(t, err, "rate_limit", http.StatusTooManyRequests, 0)

	adapter = newLabelTestAdapter(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed for psid-should-not-leak")
	})
	_, err = adapter.FetchUserLabels(context.Background(), "psid-should-not-leak")
	apiErr := requireLabelError(t, err, "transport", 0, 0)
	if strings.Contains(apiErr.Error(), "psid-should-not-leak") {
		t.Fatalf("transport error leaked PSID: %s", apiErr.Error())
	}
}

func TestFacebookAdapterFetchLabelsRejectsIncompleteAndRepeatedPagination(t *testing.T) {
	t.Run("next without cursor", func(t *testing.T) {
		adapter := newLabelTestAdapter(func(*http.Request) (*http.Response, error) {
			return labelJSONResponse(http.StatusOK, `{"data":[],"paging":{"next":"https://evil.example/"}}`), nil
		})
		_, err := adapter.FetchPageLabels(context.Background())
		requireLabelError(t, err, "invalid_response", 0, 0)
	})

	t.Run("repeated cursor", func(t *testing.T) {
		requests := 0
		adapter := newLabelTestAdapter(func(*http.Request) (*http.Response, error) {
			requests++
			return labelJSONResponse(http.StatusOK, `{"data":[],"paging":{"cursors":{"after":"same"},"next":"https://graph.facebook.com/ignored"}}`), nil
		})
		_, err := adapter.FetchPageLabels(context.Background())
		requireLabelError(t, err, "invalid_response", 0, 0)
		if requests != 2 {
			t.Fatalf("requests = %d; want 2", requests)
		}
	})
}

func TestFacebookAdapterFetchLabelsDoesNotReturnPartialDataOnLaterFailure(t *testing.T) {
	requests := 0
	adapter := newLabelTestAdapter(func(*http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return labelJSONResponse(http.StatusOK, `{"data":[{"id":"1","page_label_name":"Potential"}],"paging":{"cursors":{"after":"more"},"next":"https://graph.facebook.com/ignored"}}`), nil
		}
		return labelJSONResponse(http.StatusForbidden, `{"error":{"code":10}}`), nil
	})

	labels, err := adapter.FetchPageLabels(context.Background())
	requireLabelError(t, err, "permission", 10, 0)
	if labels != nil {
		t.Fatalf("partial labels returned as complete: %#v", labels)
	}
}

func TestFacebookAdapterResolveConversationPSID(t *testing.T) {
	adapter := newLabelTestAdapter(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v21.0/conversation-1" || request.URL.Query().Get("fields") != "participants" {
			t.Fatalf("unexpected URL: %s", request.URL.String())
		}
		return labelJSONResponse(http.StatusOK, `{"participants":{"data":[{"id":"page-1","name":"Page"},{"id":"psid-7","name":"Customer"}],"paging":{"cursors":{"after":"terminal-cursor"}}}}`), nil
	})

	psid, err := adapter.ResolveConversationPSID(context.Background(), "conversation-1")
	if err != nil {
		t.Fatalf("ResolveConversationPSID returned error: %v", err)
	}
	if psid != "psid-7" {
		t.Fatalf("PSID = %q; want psid-7", psid)
	}
}

func TestFacebookAdapterResolveConversationPSIDRejectsAmbiguousMissingAndPaginated(t *testing.T) {
	tests := []string{
		`{"participants":{"data":[{"id":"page-1"}]}}`,
		`{"participants":{"data":[{"id":"page-1"},{"id":"psid-1"},{"id":"psid-2"}]}}`,
		`{"participants":{"data":[{"id":"page-1"},{"id":"psid-1"}],"paging":{"cursors":{"after":"more"},"next":"https://evil.example/"}}}`,
		`{"participants":{"data":[{"id":""}]}}`,
		`{"participants":null}`,
	}
	for _, body := range tests {
		t.Run(body, func(t *testing.T) {
			adapter := newLabelTestAdapter(func(*http.Request) (*http.Response, error) {
				return labelJSONResponse(http.StatusOK, body), nil
			})
			_, err := adapter.ResolveConversationPSID(context.Background(), "conversation-1")
			requireLabelError(t, err, "invalid_response", 0, 0)
		})
	}
}

func TestFacebookAdapterLabelReadsDoNotFollowRedirects(t *testing.T) {
	requests := 0
	adapter := newLabelTestAdapter(func(*http.Request) (*http.Response, error) {
		requests++
		response := labelJSONResponse(http.StatusFound, `{}`)
		response.Header.Set("Location", "https://evil.example/steal")
		return response, nil
	})

	_, err := adapter.FetchPageLabels(context.Background())
	requireLabelError(t, err, "invalid_response", http.StatusFound, 0)
	if requests != 1 {
		t.Fatalf("requests = %d; redirect was followed", requests)
	}
}
