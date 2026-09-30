package handlers

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// facebookOAuthTestDatabase is opt-in and only creates/drops a uniquely named
// schema. It never migrates or truncates the database named in the supplied DSN.
func facebookOAuthTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("MESSENGER_LABELS_TEST_DSN")
	if dsn == "" {
		t.Skip("MySQL integration requires MESSENGER_LABELS_TEST_DSN")
	}
	cfg, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	cfg.DBName = ""
	cfg.ParseTime = true
	cfg.Timeout = 5 * time.Second
	admin, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("cannot connect to test MySQL")
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminSQL.Close() })

	schema := "cqa_facebook_oauth_test_" + strings.ReplaceAll(pkg.NewUUID(), "-", "")
	if err := admin.Exec("CREATE DATABASE `" + schema + "` CHARACTER SET utf8mb4").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP DATABASE `" + schema + "`").Error; err != nil {
			t.Error(err)
		}
	})

	cfg.DBName = schema
	database, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("cannot open temporary test schema")
	}
	pool, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = pool.Close() })
	if err := database.AutoMigrate(&models.Tenant{}, &models.Channel{}, &models.FacebookOAuthSession{}); err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("ALTER TABLE channels ADD UNIQUE INDEX uq_channel_tenant_type_ext (tenant_id, channel_type, external_id)").Error; err != nil {
		t.Fatal(err)
	}

	previous := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previous })
	return database
}

func createFacebookOAuthTenant(t *testing.T, database *gorm.DB, id string) {
	t.Helper()
	tenant := models.Tenant{ID: id, Name: id, Slug: id + "-" + pkg.NewUUID(), Settings: "{}"}
	if err := database.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
}

func facebookOAuthSession(id, tenantID, userID, status string, now time.Time) models.FacebookOAuthSession {
	return models.FacebookOAuthSession{
		ID: id, TenantID: tenantID, UserID: userID,
		StateHash: hashFacebookOAuthSecret("state-" + id), BrowserHash: hashFacebookOAuthSecret("browser-" + id),
		Status: status, PagesEncrypted: []byte("encrypted-page-token"), ExpiresAt: now.Add(time.Minute),
		CreatedAt: now, UpdatedAt: now,
	}
}

func TestMySQLFacebookOAuthMigrationClaimIsolationReplayAndExpiry(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if !database.Migrator().HasTable(&models.FacebookOAuthSession{}) ||
		!database.Migrator().HasColumn(&models.FacebookOAuthSession{}, "pages_encrypted") ||
		!database.Migrator().HasColumn(&models.FacebookOAuthSession{}, "selected_page_id") {
		t.Fatal("facebook OAuth session migration is incomplete")
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	store := &gormFacebookPageOAuthStore{}
	pending := facebookOAuthSession("pending", "tenant-a", "user-a", "pending", now)
	if err := store.Create(context.Background(), &pending); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(context.Background(), pending.StateHash, hashFacebookOAuthSecret("wrong-browser"), now); !errors.Is(err, errFacebookPageOAuthInvalid) {
		t.Fatalf("wrong browser claim error=%v", err)
	}
	claimed, err := store.Claim(context.Background(), pending.StateHash, pending.BrowserHash, now)
	if err != nil || claimed.ID != pending.ID {
		t.Fatalf("valid claim session=%+v error=%v", claimed, err)
	}
	if _, err := store.Claim(context.Background(), pending.StateHash, pending.BrowserHash, now); !errors.Is(err, errFacebookPageOAuthReplay) {
		t.Fatalf("replayed claim error=%v", err)
	}

	authorized := facebookOAuthSession("authorized", "tenant-a", "user-a", "authorized", now)
	if err := store.Create(context.Background(), &authorized); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(context.Background(), authorized.ID, "tenant-b", authorized.UserID, now); !errors.Is(err, errFacebookPageOAuthInvalid) {
		t.Fatalf("cross-tenant load error=%v", err)
	}
	if _, err := store.Load(context.Background(), authorized.ID, authorized.TenantID, "user-b", now); !errors.Is(err, errFacebookPageOAuthInvalid) {
		t.Fatalf("cross-user load error=%v", err)
	}

	expired := facebookOAuthSession("expired", "tenant-a", "user-a", "pending", now)
	expired.ExpiresAt = now.Add(-time.Second)
	if err := store.Create(context.Background(), &expired); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(context.Background(), expired.StateHash, expired.BrowserHash, now); !errors.Is(err, errFacebookPageOAuthExpired) {
		t.Fatalf("expired claim error=%v", err)
	}
}

func TestMySQLFacebookOAuthReserveIsIdempotentForSamePageAndRejectsConcurrentDifferentPage(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	session := facebookOAuthSession("reserve", "tenant-a", "user-a", "authorized", now)
	if err := database.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	store := &gormFacebookPageOAuthStore{}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, pageID := range []string{"page-a", "page-b"} {
		pageID := pageID
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.Reserve(context.Background(), session.ID, session.TenantID, session.UserID, pageID, now)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	successes, replays := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, errFacebookPageOAuthReplay):
			replays++
		default:
			t.Fatalf("unexpected reserve error: %v", err)
		}
	}
	if successes != 1 || replays != 1 {
		t.Fatalf("different-page reservation successes=%d replays=%d", successes, replays)
	}

	var stored models.FacebookOAuthSession
	if err := database.First(&stored, "id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.Reserve(context.Background(), session.ID, session.TenantID, session.UserID, stored.SelectedPageID, now); err != nil {
		t.Fatalf("same-page retry must be idempotent: %v", err)
	}
}

func TestMySQLFacebookOAuthSelectCreatesReconnectsAndClearsTokens(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	createFacebookOAuthTenant(t, database, "tenant-a")
	createFacebookOAuthTenant(t, database, "tenant-b")
	now := time.Now().UTC().Truncate(time.Millisecond)
	store := &gormFacebookPageOAuthStore{}
	page := facebookOAuthPage{ID: "page-shared", Name: "Meta Page", AccessToken: "page-token"}
	request := facebookPageSelectRequest{PageID: page.ID, SyncInterval: 15, SyncFiles: true}

	selectPage := func(id, tenantID, userID string, credentials []byte) (models.Channel, bool) {
		t.Helper()
		session := facebookOAuthSession(id, tenantID, userID, "authorized", now)
		if err := database.Create(&session).Error; err != nil {
			t.Fatal(err)
		}
		reserved, err := store.Reserve(context.Background(), id, tenantID, userID, page.ID, now)
		if err != nil {
			t.Fatal(err)
		}
		channel, created, err := store.Select(context.Background(), reserved, page, request, credentials, now)
		if err != nil {
			t.Fatal(err)
		}
		var completed models.FacebookOAuthSession
		if err := database.First(&completed, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		if completed.Status != "completed" || completed.SelectedPageID != page.ID || len(completed.PagesEncrypted) != 0 || completed.CompletedAt == nil {
			t.Fatalf("session tokens were not cleared on completion: %+v", completed)
		}
		return channel, created
	}

	first, created := selectPage("select-a", "tenant-a", "user-a", []byte("encrypted-credentials-a"))
	if !created || first.Name != page.Name {
		t.Fatalf("first selection channel=%+v created=%v", first, created)
	}
	if err := database.Model(&models.Channel{}).Where("id = ?", first.ID).Updates(map[string]any{
		"name": "Custom Page Name", "metadata": `{"custom":"keep","sync_interval":15,"sync_files":true}`,
	}).Error; err != nil {
		t.Fatal(err)
	}
	request.SyncInterval, request.SyncFiles = 30, false
	reconnected, created := selectPage("select-b", "tenant-a", "user-a", []byte("encrypted-credentials-b"))
	if created || reconnected.ID != first.ID || reconnected.Name != "Custom Page Name" || !strings.Contains(reconnected.Metadata, `"custom":"keep"`) || !strings.Contains(reconnected.Metadata, `"sync_interval":30`) {
		t.Fatalf("reconnect did not preserve identity/custom fields: %+v created=%v", reconnected, created)
	}
	var tenantACount int64
	if err := database.Model(&models.Channel{}).Where("tenant_id = ? AND channel_type = ? AND external_id = ?", "tenant-a", "facebook", page.ID).Count(&tenantACount).Error; err != nil || tenantACount != 1 {
		t.Fatalf("tenant-a channel count=%d error=%v", tenantACount, err)
	}

	other, created := selectPage("select-c", "tenant-b", "user-b", []byte("encrypted-credentials-c"))
	if !created || other.TenantID != "tenant-b" || other.ID == first.ID {
		t.Fatalf("same Page must be connectable by a different tenant: %+v created=%v", other, created)
	}
}
