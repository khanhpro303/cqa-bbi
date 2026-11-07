package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

type graphRoundTrip func(*http.Request) (*http.Response, error)

func (fn graphRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

// The sync starts a detached label capture that keeps using the default HTTP
// transport, so tests switch a delegate atomically instead of swapping it.
var (
	testGraph         atomic.Pointer[graphRoundTrip]
	installTestGraph  sync.Once
	originalTransport http.RoundTripper
)

func useTestGraph(fake graphRoundTrip) {
	installTestGraph.Do(func() {
		originalTransport = http.DefaultTransport
		http.DefaultTransport = graphRoundTrip(func(request *http.Request) (*http.Response, error) {
			if current := testGraph.Load(); current != nil {
				return (*current)(request)
			}
			return originalTransport.RoundTrip(request)
		})
	})
	if fake == nil {
		testGraph.Store(nil)
		return
	}
	testGraph.Store(&fake)
}

// fakeGraph serves `total` conversations updated inside the sync window, in
// pages of 100, and one message per conversation. A conversation listed in
// failing returns a Graph error for its messages.
func fakeGraph(t *testing.T, total int, failing map[string]bool) graphRoundTrip {
	updated := time.Now().UTC().Add(-time.Minute).Format("2006-01-02T15:04:05-0700")
	respond := func(request *http.Request, body string) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}
	return func(request *http.Request) (*http.Response, error) {
		path := request.URL.Path
		switch {
		case strings.HasSuffix(path, "/100/conversations"):
			offset := 0
			fmt.Sscanf(request.URL.Query().Get("offset"), "%d", &offset)
			var items []string
			for i := offset; i < total && i < offset+100; i++ {
				items = append(items, fmt.Sprintf(`{"id":"t_%d","updated_time":"%s","participants":{"data":[{"id":"100"},{"id":"%d"}]}}`, i, updated, 1000+i))
			}
			next := ""
			if offset+100 < total {
				next = fmt.Sprintf(`,"paging":{"next":"https://graph.facebook.com/v21.0/100/conversations?offset=%d"}`, offset+100)
			}
			return respond(request, `{"data":[`+strings.Join(items, ",")+`]`+next+`}`)
		case strings.HasSuffix(path, "/messages"):
			id := strings.TrimSuffix(strings.TrimPrefix(path[strings.LastIndex(path[:len(path)-len("/messages")], "/"):], "/"), "/messages")
			if failing[id] {
				return respond(request, `{"error":{"message":"temporary","code":2}}`)
			}
			return respond(request, fmt.Sprintf(`{"data":[{"id":"m_%s","message":"hi","from":{"id":"9"},"created_time":"%s"}]}`, id, updated))
		default:
			return respond(request, `{"error":{"message":"not in test","code":100}}`)
		}
	}
}

func TestMySQLFacebookSyncReadsWholeWindowAndRetriesFailures(t *testing.T) {
	database := journeyEngineTestDatabase(t)
	if err := database.AutoMigrate(&models.Message{}, &models.ActivityLog{}, &models.MessengerLabelState{}); err != nil {
		t.Fatal(err)
	}
	previousDB := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previousDB; useTestGraph(nil) })

	key := "0123456789abcdef0123456789abcdef"
	creds, _ := json.Marshal(map[string]string{"page_id": "100", "access_token": "token"})
	encrypted, err := pkg.Encrypt(creds, key)
	if err != nil {
		t.Fatal(err)
	}
	tenant := models.Tenant{ID: pkg.NewUUID(), Name: "Test", Slug: pkg.NewUUID(), Settings: "{}"}
	lastSync := time.Now().Add(-30 * time.Minute).Truncate(time.Second)
	channel := models.Channel{ID: pkg.NewUUID(), TenantID: tenant.ID, ChannelType: "facebook", ExternalID: "100", Name: "Page", IsActive: true,
		CredentialsEncrypted: encrypted, Metadata: "{}", LastSyncAt: &lastSync, LastSyncStatus: "success"}
	for _, row := range []interface{}{&tenant, &channel} {
		if err := database.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	engine := NewSyncEngine(&config.Config{EncryptionKey: key})

	// 150 conversations in the window, one message fetch fails.
	useTestGraph(fakeGraph(t, 150, map[string]bool{"t_7": true}))
	if err := engine.SyncChannel(context.Background(), channel); err == nil {
		t.Fatal("a failed conversation must report a sync error")
	}
	var stored models.Channel
	database.First(&stored, "id = ?", channel.ID)
	if stored.LastSyncAt == nil || !stored.LastSyncAt.Equal(lastSync) || stored.LastSyncStatus != "error" {
		t.Fatalf("failed sync moved last_sync_at or hid the error: %+v %s", stored.LastSyncAt, stored.LastSyncStatus)
	}
	var conversations, messages int64
	database.Model(&models.Conversation{}).Where("channel_id = ?", channel.ID).Count(&conversations)
	database.Model(&models.Message{}).Where("tenant_id = ?", tenant.ID).Count(&messages)
	if conversations != 150 || messages != 149 {
		t.Fatalf("conversations=%d messages=%d, want all 150 conversations and 149 messages", conversations, messages)
	}

	// The retry re-reads the same window and completes the failed conversation.
	useTestGraph(fakeGraph(t, 150, nil))
	if err := engine.SyncChannel(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	database.First(&stored, "id = ?", channel.ID)
	database.Model(&models.Message{}).Where("tenant_id = ?", tenant.ID).Count(&messages)
	if messages != 150 || stored.LastSyncStatus != "success" || !stored.LastSyncAt.After(lastSync) {
		t.Fatalf("retry did not complete: messages=%d status=%s", messages, stored.LastSyncStatus)
	}
	time.Sleep(200 * time.Millisecond) // let the detached label capture finish before cleanup
}
