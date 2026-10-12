package zalotoken

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/vietbui/chat-quality-agent/channels"
)

const storedCreds = `{"app_id":"app","app_secret":"secret","access_token":"A1","refresh_token":"R1","oa_id":"oa","oa_name":"BBI"}`

func TestRotateCredentialsExchangesStoredRefreshToken(t *testing.T) {
	// Arrange
	var exchangedWith string
	exchange := func(_ context.Context, refresh string) (channels.ZaloTokenPair, error) {
		exchangedWith = refresh
		return channels.ZaloTokenPair{AccessToken: "A2", RefreshToken: "R2"}, nil
	}

	// Act
	updated, pair, changed, err := rotateCredentials(context.Background(), []byte(storedCreds), "A1", exchange)

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed || exchangedWith != "R1" {
		t.Fatalf("expected exchange with stored refresh R1, changed=%v exchangedWith=%q", changed, exchangedWith)
	}
	if pair.AccessToken != "A2" || pair.RefreshToken != "R2" {
		t.Fatalf("unexpected pair %+v", pair)
	}
	var got map[string]string
	if err := json.Unmarshal(updated, &got); err != nil {
		t.Fatalf("updated creds not JSON: %v", err)
	}
	if got["access_token"] != "A2" || got["refresh_token"] != "R2" {
		t.Fatalf("tokens not persisted: %v", got)
	}
	if got["oa_name"] != "BBI" || got["app_secret"] != "secret" {
		t.Fatalf("unrelated fields must be preserved: %v", got)
	}
}

func TestRotateCredentialsReusesTokensRotatedByAnotherWorker(t *testing.T) {
	// Arrange: this adapter still holds A0, but another worker already rotated
	// the row to A1/R1. Exchanging again would spend a consumed refresh token.
	exchange := func(context.Context, string) (channels.ZaloTokenPair, error) {
		t.Fatal("exchange must not run when tokens were already rotated")
		return channels.ZaloTokenPair{}, nil
	}

	// Act
	_, pair, changed, err := rotateCredentials(context.Background(), []byte(storedCreds), "A0", exchange)

	// Assert
	if err != nil || changed {
		t.Fatalf("expected reuse without change, changed=%v err=%v", changed, err)
	}
	if pair.AccessToken != "A1" || pair.RefreshToken != "R1" {
		t.Fatalf("expected persisted pair, got %+v", pair)
	}
}

func TestRotateCredentialsPropagatesExchangeError(t *testing.T) {
	exchange := func(context.Context, string) (channels.ZaloTokenPair, error) {
		return channels.ZaloTokenPair{}, channels.ErrZaloReauthRequired
	}

	_, _, changed, err := rotateCredentials(context.Background(), []byte(storedCreds), "A1", exchange)

	if !errors.Is(err, channels.ErrZaloReauthRequired) || changed {
		t.Fatalf("expected reauth error and no change, changed=%v err=%v", changed, err)
	}
}

func TestRotateCredentialsRejectsInvalidJSON(t *testing.T) {
	exchange := func(context.Context, string) (channels.ZaloTokenPair, error) {
		t.Fatal("exchange must not run on unreadable credentials")
		return channels.ZaloTokenPair{}, nil
	}

	if _, _, _, err := rotateCredentials(context.Background(), []byte("{"), "A1", exchange); err == nil {
		t.Fatal("expected parse error")
	}
}
