package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// fakeZaloOA serves `total` recent chats and one message per chat. A user in
// broken answers its messages with Zalo's per-conversation error -1. After
// limitAfter message reads (0 = never) every read hits the OA call limit.
func fakeZaloOA(total int, broken map[string]bool, limitAfter int64, messageCalls *int64) graphRoundTrip {
	recent := time.Now().Add(-time.Minute).UnixMilli()
	respond := func(request *http.Request, body string) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}
	return func(request *http.Request) (*http.Response, error) {
		var params struct {
			Offset int    `json:"offset"`
			UserID string `json:"user_id"`
		}
		_ = json.Unmarshal([]byte(request.URL.Query().Get("data")), &params)
		switch {
		case strings.HasSuffix(request.URL.Path, "/listrecentchat"):
			var items []string
			for i := params.Offset; i < total && i < params.Offset+10; i++ {
				items = append(items, fmt.Sprintf(`{"src":1,"from_id":"u%d","from_display_name":"Khach %d","time":%d}`, i, i, recent))
			}
			return respond(request, `{"error":0,"message":"Success","data":[`+strings.Join(items, ",")+`]}`)
		case strings.HasSuffix(request.URL.Path, "/conversation"):
			calls := atomic.AddInt64(messageCalls, 1)
			if limitAfter > 0 && calls > limitAfter {
				return respond(request, `{"error":-32,"message":"Your OA reached limit call api"}`)
			}
			if broken[params.UserID] {
				return respond(request, `{"error":-1,"message":"Error fetching messages: -2"}`)
			}
			return respond(request, fmt.Sprintf(`{"error":0,"message":"Success","data":[{"message_id":"m_%s","message":"hi","src":1,"type":"text","time":%d}]}`, params.UserID, recent))
		default:
			return respond(request, `{"error":-201,"message":"not in test"}`)
		}
	}
}

func TestMySQLZaloSyncSkipsBrokenConversationAndStopsAtCallLimit(t *testing.T) {
	database := journeyEngineTestDatabase(t)
	if err := database.AutoMigrate(&models.Message{}, &models.ActivityLog{}); err != nil {
		t.Fatal(err)
	}
	previousDB := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previousDB; useTestGraph(nil) })

	key := "0123456789abcdef0123456789abcdef"
	creds, _ := json.Marshal(map[string]string{"app_id": "app", "access_token": "token", "refresh_token": "refresh", "oa_id": "oa"})
	encrypted, err := pkg.Encrypt(creds, key)
	if err != nil {
		t.Fatal(err)
	}
	tenant := models.Tenant{ID: pkg.NewUUID(), Name: "Test", Slug: pkg.NewUUID(), Settings: "{}"}
	lastSync := time.Now().Add(-30 * time.Minute).Truncate(time.Second)
	channel := models.Channel{ID: pkg.NewUUID(), TenantID: tenant.ID, ChannelType: "zalo_oa", ExternalID: "oa", Name: "OA", IsActive: true,
		CredentialsEncrypted: encrypted, Metadata: "{}", LastSyncAt: &lastSync, LastSyncStatus: "error"}
	for _, row := range []interface{}{&tenant, &channel} {
		if err := database.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	engine := NewSyncEngine(&config.Config{EncryptionKey: key})

	// The OA call limit stops the run at once and keeps the window.
	var limitedCalls int64
	useTestGraph(fakeZaloOA(30, nil, 5, &limitedCalls))
	if err := engine.SyncChannel(context.Background(), channel); err == nil {
		t.Fatal("hitting the OA call limit must report a sync error")
	}
	var stored models.Channel
	database.First(&stored, "id = ?", channel.ID)
	if limitedCalls != 6 {
		t.Fatalf("message reads = %d, want the sync to stop at the first call-limit answer (6)", limitedCalls)
	}
	if stored.LastSyncAt == nil || !stored.LastSyncAt.Equal(lastSync) || stored.LastSyncStatus != "error" {
		t.Fatalf("call-limited sync moved last_sync_at or hid the error: %+v %s", stored.LastSyncAt, stored.LastSyncStatus)
	}

	// One conversation Zalo always refuses no longer pins the window.
	var calls int64
	useTestGraph(fakeZaloOA(30, map[string]bool{"u7": true}, 0, &calls))
	if err := engine.SyncChannel(context.Background(), stored); err != nil {
		t.Fatalf("one broken conversation must not fail the sync: %v", err)
	}
	database.First(&stored, "id = ?", channel.ID)
	var messages int64
	database.Model(&models.Message{}).Where("tenant_id = ?", tenant.ID).Count(&messages)
	if stored.LastSyncStatus != "success" || stored.LastSyncAt == nil || !stored.LastSyncAt.After(lastSync) {
		t.Fatalf("sync with one broken conversation did not succeed: status=%s last_sync_at=%v", stored.LastSyncStatus, stored.LastSyncAt)
	}
	if messages != 29 {
		t.Fatalf("messages=%d, want the 29 readable conversations stored", messages)
	}
}
