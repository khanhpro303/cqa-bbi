package chatmemory

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type astraTestTransport func(*http.Request) (*http.Response, error)

func (f astraTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAstraUpsertRetriesSameWriteOnTimeout(t *testing.T) {
	previous := vectorHTTP
	t.Cleanup(func() { vectorHTTP = previous })
	var body string
	calls := 0
	vectorHTTP = &http.Client{Transport: astraTestTransport(func(r *http.Request) (*http.Response, error) {
		raw, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			return nil, err
		}
		calls++
		if calls == 1 {
			body = string(raw)
			return nil, context.DeadlineExceeded
		}
		if string(raw) != body {
			t.Errorf("retry changed the upsert body: %s", raw)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status":{}}`))}, nil
	})}

	index := &AstraIndex{Endpoint: "https://astra.example", Collection: "knowledge", ensured: true}
	err := index.Upsert(context.Background(), []VectorDoc{{ID: "chunk-1", Text: "Bảo hành", Vector: []float32{1}}})
	if err != nil || calls != 2 || !strings.Contains(body, `"_id":"chunk-1"`) {
		t.Fatalf("upsert calls = %d, body = %s, err = %v", calls, body, err)
	}
}

func TestAstraWriteRetryStopsForPermanentError(t *testing.T) {
	want := &astraStatusError{status: http.StatusUnauthorized}
	calls := 0
	err := retryAstraWrite(context.Background(), func() error {
		calls++
		return want
	})
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("calls = %d, err = %v", calls, err)
	}
}
