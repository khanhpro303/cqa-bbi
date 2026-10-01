package handlers

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
)

func facebookLoginOAuthSession(id string, now time.Time) models.FacebookLoginOAuthSession {
	return models.FacebookLoginOAuthSession{
		ID: id, StateHash: hashFacebookOAuthSecret("state-" + id), BrowserHash: hashFacebookOAuthSecret("browser-" + id),
		RedirectURI: "https://crm.example.com/api/v1/channels/facebook/callback", Status: "pending",
		ExpiresAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now,
	}
}

func TestMySQLFacebookLoginOAuthClaimIsAtomicBrowserBoundAndExpiring(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(&models.FacebookLoginOAuthSession{}); err != nil {
		t.Fatal(err)
	}
	if !database.Migrator().HasTable(&models.FacebookLoginOAuthSession{}) {
		t.Fatal("facebook login OAuth session migration is incomplete")
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	store := &gormFacebookLoginOAuthStore{}

	mismatch := facebookLoginOAuthSession("browser-mismatch", now)
	if err := store.Create(context.Background(), &mismatch); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(context.Background(), mismatch.StateHash, hashFacebookOAuthSecret("wrong-browser"), now); !errors.Is(err, errFacebookLoginOAuthInvalid) {
		t.Fatalf("wrong browser error=%v", err)
	}

	expired := facebookLoginOAuthSession("expired", now)
	expired.ExpiresAt = now.Add(-time.Second)
	if err := store.Create(context.Background(), &expired); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(context.Background(), expired.StateHash, expired.BrowserHash, now); !errors.Is(err, errFacebookLoginOAuthExpired) {
		t.Fatalf("expired error=%v", err)
	}

	atomic := facebookLoginOAuthSession("atomic", now)
	if err := store.Create(context.Background(), &atomic); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errorsFound := make(chan error, 2)
	var wait sync.WaitGroup
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := store.Claim(context.Background(), atomic.StateHash, atomic.BrowserHash, now)
			errorsFound <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsFound)
	successes, replays := 0, 0
	for err := range errorsFound {
		if err == nil {
			successes++
		} else if errors.Is(err, errFacebookLoginOAuthReplay) {
			replays++
		} else {
			t.Fatalf("unexpected claim error=%v", err)
		}
	}
	if successes != 1 || replays != 1 {
		t.Fatalf("successes=%d replays=%d", successes, replays)
	}
}
