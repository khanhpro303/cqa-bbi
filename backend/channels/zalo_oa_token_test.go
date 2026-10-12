package channels

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func newTestZaloAdapter(creds ZaloOACredentials, rt roundTripFunc) *ZaloOAAdapter {
	adapter := NewZaloOAAdapter(creds)
	adapter.client = &http.Client{Transport: rt}
	return adapter
}

// fakeTokenStore records Rotate calls and answers with a fixed result.
type fakeTokenStore struct {
	staleSeen []string
	pair      ZaloTokenPair
	err       error
}

func (f *fakeTokenStore) Rotate(_ context.Context, stale string, _ ZaloRefreshExchange) (ZaloTokenPair, error) {
	f.staleSeen = append(f.staleSeen, stale)
	return f.pair, f.err
}

func TestExchangeRefreshTokenRejectedRequiresReauth(t *testing.T) {
	// Arrange: Zalo OAuth answers with the -14020 rejection seen in production.
	adapter := newTestZaloAdapter(ZaloOACredentials{AppID: "app", AppSecret: "secret"}, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(`{"error":-14020,"message":"Invalid refresh token"}`), nil
	})

	// Act
	_, err := adapter.exchangeRefreshToken(context.Background(), "dead-refresh")

	// Assert
	if !errors.Is(err, ErrZaloReauthRequired) {
		t.Fatalf("expected ErrZaloReauthRequired, got %v", err)
	}
	if !strings.Contains(err.Error(), "-14020") {
		t.Fatalf("expected Zalo error code in message, got %q", err.Error())
	}
}

func TestExchangeRefreshTokenEmptyTokenSkipsRequest(t *testing.T) {
	adapter := newTestZaloAdapter(ZaloOACredentials{AppID: "app"}, func(r *http.Request) (*http.Response, error) {
		t.Fatal("no request expected for an empty refresh token")
		return nil, nil
	})

	_, err := adapter.exchangeRefreshToken(context.Background(), "  ")

	if !errors.Is(err, ErrZaloReauthRequired) {
		t.Fatalf("expected ErrZaloReauthRequired, got %v", err)
	}
}

func TestExchangeRefreshTokenSendsGivenRefreshToken(t *testing.T) {
	var gotRefresh, gotSecret string
	adapter := newTestZaloAdapter(ZaloOACredentials{AppID: "app", AppSecret: "secret", RefreshToken: "in-memory"}, func(r *http.Request) (*http.Response, error) {
		gotRefresh = r.URL.Query().Get("refresh_token")
		gotSecret = r.Header.Get("secret_key")
		return jsonResponse(`{"access_token":"new-access","refresh_token":"new-refresh"}`), nil
	})

	pair, err := adapter.exchangeRefreshToken(context.Background(), "stored-refresh")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotRefresh != "stored-refresh" || gotSecret != "secret" {
		t.Fatalf("request used refresh=%q secret=%q", gotRefresh, gotSecret)
	}
	if pair != (ZaloTokenPair{AccessToken: "new-access", RefreshToken: "new-refresh"}) {
		t.Fatalf("unexpected pair %+v", pair)
	}
}

func TestExchangeRefreshTokenRejectsEmptyPair(t *testing.T) {
	adapter := newTestZaloAdapter(ZaloOACredentials{AppID: "app"}, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(`{"access_token":"","refresh_token":""}`), nil
	})

	if _, err := adapter.exchangeRefreshToken(context.Background(), "r"); err == nil {
		t.Fatal("expected error for empty token pair")
	}
}

func TestExpiredAccessTokenRotatesThroughStoreAndRetries(t *testing.T) {
	// Arrange: first API call says the token expired; the retry must carry the
	// token returned by the store, and the adapter must not call OAuth itself.
	store := &fakeTokenStore{pair: ZaloTokenPair{AccessToken: "fresh", RefreshToken: "fresh-r"}}
	var tokensUsed []string
	adapter := newTestZaloAdapter(ZaloOACredentials{AccessToken: "expired", RefreshToken: "old-r"}, func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Host, "oauth") {
			t.Fatal("adapter with a store must not call Zalo OAuth itself")
		}
		tokensUsed = append(tokensUsed, strings.Join(r.Header["access_token"], ","))
		if len(tokensUsed) == 1 {
			return jsonResponse(`{"error":-216,"message":"Access token is invalid"}`), nil
		}
		return jsonResponse(`{"error":0,"data":{"oa_id":"1"}}`), nil
	})
	adapter.SetTokenStore(store)

	// Act
	err := adapter.HealthCheck(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.staleSeen) != 1 || store.staleSeen[0] != "expired" {
		t.Fatalf("store should be asked once with the stale token, got %v", store.staleSeen)
	}
	if len(tokensUsed) != 2 || tokensUsed[1] != "fresh" {
		t.Fatalf("retry should use the rotated token, got %v", tokensUsed)
	}
}

func TestStoreRotationFailureSurfacesReauthError(t *testing.T) {
	store := &fakeTokenStore{err: fmt.Errorf("zalo token refresh error -14020: x: %w", ErrZaloReauthRequired)}
	adapter := newTestZaloAdapter(ZaloOACredentials{AccessToken: "expired"}, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(`{"error":-216,"message":"Access token is invalid"}`), nil
	})
	adapter.SetTokenStore(store)

	err := adapter.HealthCheck(context.Background())

	if !errors.Is(err, ErrZaloReauthRequired) {
		t.Fatalf("expected reauth error to propagate, got %v", err)
	}
}
