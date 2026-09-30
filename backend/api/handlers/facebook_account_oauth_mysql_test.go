package handlers

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

func facebookAccountOAuthSession(id, userID, passwordHash string, now time.Time) models.FacebookAccountOAuthSession {
	return models.FacebookAccountOAuthSession{
		ID: id, UserID: userID, CredentialHash: hashFacebookOAuthSecret(passwordHash),
		StateHash: hashFacebookOAuthSecret("state-" + id), BrowserHash: hashFacebookOAuthSecret("browser-" + id),
		RedirectURI: "https://crm.example.com/api/v1/channels/facebook/callback", ReturnPath: "/",
		Status: "pending", ExpiresAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now,
	}
}

func TestMySQLFacebookAccountOAuthClaimSecurityAndRevocation(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if !database.Migrator().HasTable(&models.FacebookAccountOAuthSession{}) ||
		!database.Migrator().HasColumn(&models.FacebookAccountOAuthSession{}, "credential_hash") {
		t.Fatal("facebook account OAuth session migration is incomplete")
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	user := models.User{ID: pkg.NewUUID(), Email: "oauth-" + pkg.NewUUID() + "@example.com", PasswordHash: "hash-original", TokenVersion: 3, CreatedAt: now, UpdatedAt: now}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	store := &gormFacebookAccountOAuthStore{}

	mismatch := facebookAccountOAuthSession("browser-mismatch", user.ID, user.PasswordHash, now)
	if err := store.Create(context.Background(), &mismatch); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(context.Background(), mismatch.StateHash, hashFacebookOAuthSecret("wrong-browser"), now); !errors.Is(err, errFacebookAccountOAuthInvalid) {
		t.Fatalf("wrong browser claim error=%v", err)
	}

	expired := facebookAccountOAuthSession("expired", user.ID, user.PasswordHash, now)
	expired.ExpiresAt = now.Add(-time.Second)
	if err := store.Create(context.Background(), &expired); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(context.Background(), expired.StateHash, expired.BrowserHash, now); !errors.Is(err, errFacebookAccountOAuthExpired) {
		t.Fatalf("expired claim error=%v", err)
	}

	refreshed := facebookAccountOAuthSession("refreshed", user.ID, user.PasswordHash, now)
	if err := store.Create(context.Background(), &refreshed); err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&models.User{}).Where("id = ?", user.ID).Update("token_version", user.TokenVersion+1).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(context.Background(), refreshed.StateHash, refreshed.BrowserHash, now); err != nil {
		t.Fatalf("normal refresh must not revoke pending link: %v", err)
	}

	passwordChanged := facebookAccountOAuthSession("password-changed", user.ID, user.PasswordHash, now)
	if err := store.Create(context.Background(), &passwordChanged); err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&models.User{}).Where("id = ?", user.ID).Update("password_hash", "hash-new").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(context.Background(), passwordChanged.StateHash, passwordChanged.BrowserHash, now); !errors.Is(err, errFacebookAccountOAuthInvalid) {
		t.Fatalf("password change claim error=%v", err)
	}

	deletedUser := models.User{ID: pkg.NewUUID(), Email: "deleted-" + pkg.NewUUID() + "@example.com", PasswordHash: "hash-deleted", CreatedAt: now, UpdatedAt: now}
	if err := database.Create(&deletedUser).Error; err != nil {
		t.Fatal(err)
	}
	deleted := facebookAccountOAuthSession("deleted-user", deletedUser.ID, deletedUser.PasswordHash, now)
	if err := store.Create(context.Background(), &deleted); err != nil {
		t.Fatal(err)
	}
	if err := database.Delete(&deletedUser).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(context.Background(), deleted.StateHash, deleted.BrowserHash, now); !errors.Is(err, errFacebookAccountOAuthInvalid) {
		t.Fatalf("deleted user claim error=%v", err)
	}
}

func TestMySQLFacebookAccountOAuthClaimIsAtomic(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	user := models.User{ID: pkg.NewUUID(), Email: "atomic-" + pkg.NewUUID() + "@example.com", PasswordHash: "hash", TokenVersion: 4, CreatedAt: now, UpdatedAt: now}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	session := facebookAccountOAuthSession("atomic", user.ID, user.PasswordHash, now)
	store := &gormFacebookAccountOAuthStore{}
	if err := store.Create(context.Background(), &session); err != nil {
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
			_, err := store.Claim(context.Background(), session.StateHash, session.BrowserHash, now)
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
		} else if errors.Is(err, errFacebookAccountOAuthReplay) {
			replays++
		} else {
			t.Fatalf("unexpected concurrent claim error=%v", err)
		}
	}
	if successes != 1 || replays != 1 {
		t.Fatalf("successes=%d replays=%d", successes, replays)
	}
}
