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
	if stored.LastSyncAt == nil || !stored.LastSyncAt.After(lastSync) || stored.LastSyncStatus != "error" {
		t.Fatalf("failed conversation must not hold the window back or hide the error: %+v %s", stored.LastSyncAt, stored.LastSyncStatus)
	}
	if !strings.Contains(stored.LastSyncError, "temporary") || !strings.Contains(stored.LastSyncError, "t_7") {
		t.Fatalf("sync error should name the failing conversation and its error: %q", stored.LastSyncError)
	}
	if retries := parseSyncRetryState(stored.SyncRetryState); len(retries) != 1 || retries["t_7"].Attempts != 1 {
		t.Fatalf("t_7 should wait for a retry: %+v", retries)
	}
	var conversations, messages int64
	database.Model(&models.Conversation{}).Where("channel_id = ?", channel.ID).Count(&conversations)
	database.Model(&models.Message{}).Where("tenant_id = ?", tenant.ID).Count(&messages)
	if conversations != 150 || messages != 149 {
		t.Fatalf("conversations=%d messages=%d, want all 150 conversations and 149 messages", conversations, messages)
	}

	// The next run retries the failed conversation and completes it.
	firstWindow := *stored.LastSyncAt
	useTestGraph(fakeGraph(t, 150, nil))
	if err := engine.SyncChannel(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	database.First(&stored, "id = ?", channel.ID)
	database.Model(&models.Message{}).Where("tenant_id = ?", tenant.ID).Count(&messages)
	if messages != 150 || stored.LastSyncStatus != "success" || !stored.LastSyncAt.After(firstWindow) || stored.SyncRetryState != nil {
		t.Fatalf("retry did not complete: messages=%d status=%s retry=%v", messages, stored.LastSyncStatus, stored.SyncRetryState)
	}
	time.Sleep(200 * time.Millisecond) // let the detached label capture finish before cleanup
}

// fakeZalo serves the Zalo OA listrecentchat and conversation APIs. Each
// conversation's last message time comes from times; broken conversations
// return Zalo's -1 "Error fetching messages: -2"; once quotaLeft reaches zero
// every conversation call returns -32 "reached limit call api".
type fakeZalo struct {
	mu        sync.Mutex
	order     []string
	times     map[string]time.Time
	broken    map[string]bool
	quotaLeft int // < 0 means unlimited
	calls     []string
}

func (f *fakeZalo) roundTrip(request *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	respond := func(body string) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}
	var params struct {
		UserID string `json:"user_id"`
		Offset int    `json:"offset"`
		Count  int    `json:"count"`
	}
	_ = json.Unmarshal([]byte(request.URL.Query().Get("data")), &params)
	switch {
	case strings.HasSuffix(request.URL.Path, "/listrecentchat"):
		var items []string
		for i := params.Offset; i < len(f.order) && i < params.Offset+params.Count; i++ {
			id := f.order[i]
			items = append(items, fmt.Sprintf(`{"src":1,"time":%d,"from_id":"%s","from_display_name":"Khach %s","to_id":"oa","message_id":"last_%s"}`, f.times[id].UnixMilli(), id, id, id))
		}
		return respond(`{"error":0,"message":"Success","data":[` + strings.Join(items, ",") + `]}`)
	case strings.HasSuffix(request.URL.Path, "/conversation"):
		f.calls = append(f.calls, params.UserID)
		if f.quotaLeft == 0 {
			return respond(`{"error":-32,"message":"Your OA reached limit call api"}`)
		}
		if f.quotaLeft > 0 {
			f.quotaLeft--
		}
		if f.broken[params.UserID] {
			return respond(`{"error":-1,"message":"Error fetching messages: -2"}`)
		}
		return respond(fmt.Sprintf(`{"error":0,"message":"Success","data":[{"src":1,"time":%d,"type":"text","message":"hi","message_id":"m_%s_%d","from_id":"%s"}]}`,
			f.times[params.UserID].UnixMilli(), params.UserID, f.times[params.UserID].Unix(), params.UserID))
	default:
		return respond(`{"error":-201,"message":"not in test"}`)
	}
}

func (f *fakeZalo) takeCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := f.calls
	f.calls = nil
	return calls
}

// TestMySQLZaloSyncDoesNotFreezeOnBrokenConversation reproduces the BBI OA
// incident: one conversation always failed with -1/-2, the channel window
// never moved, every run re-read 100 conversations and used up the OA quota
// (-32). Now the window moves on, only the broken conversation is retried,
// a quota error stops the run without spending attempts, and the broken
// conversation is dropped after maxConversationSyncAttempts runs.
func TestMySQLZaloSyncDoesNotFreezeOnBrokenConversation(t *testing.T) {
	database := journeyEngineTestDatabase(t)
	if err := database.AutoMigrate(&models.Message{}, &models.ActivityLog{}, &models.MessengerLabelState{}); err != nil {
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
	channel := models.Channel{ID: pkg.NewUUID(), TenantID: tenant.ID, ChannelType: "zalo_oa", ExternalID: "oa", Name: "BBI", IsActive: true,
		CredentialsEncrypted: encrypted, Metadata: "{}", LastSyncAt: &lastSync, LastSyncStatus: "success"}
	for _, row := range []interface{}{&tenant, &channel} {
		if err := database.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	engine := NewSyncEngine(&config.Config{EncryptionKey: key})

	fake := &fakeZalo{times: map[string]time.Time{}, broken: map[string]bool{"u_5": true}, quotaLeft: -1}
	recent := time.Now().Add(-time.Minute)
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("u_%d", i)
		fake.order = append(fake.order, id)
		fake.times[id] = recent
	}
	useTestGraph(fake.roundTrip)
	reload := func() models.Channel {
		var stored models.Channel
		database.First(&stored, "id = ?", channel.ID)
		return stored
	}

	// Run 1: u_5 fails, everything else is stored and the window moves on.
	if err := engine.SyncChannel(context.Background(), reload()); err == nil {
		t.Fatal("a failed conversation must report a sync error")
	}
	stored := reload()
	if len(fake.takeCalls()) != 30 || !stored.LastSyncAt.After(lastSync) || stored.LastSyncStatus != "error" {
		t.Fatalf("run 1: last_sync_at=%v status=%s", stored.LastSyncAt, stored.LastSyncStatus)
	}
	if !strings.Contains(stored.LastSyncError, "zalo api error -1: Error fetching messages: -2") || !strings.Contains(stored.LastSyncError, "u_5") {
		t.Fatalf("run 1: error should show the Zalo error and conversation: %q", stored.LastSyncError)
	}

	// Run 2: only u_0 and u_1 have new messages. The run reads those two plus
	// the retry of u_5, not the whole old window again.
	older := time.Now().Add(-3 * time.Hour)
	for id := range fake.times {
		fake.times[id] = older
	}
	fake.times["u_0"], fake.times["u_1"] = time.Now(), time.Now()
	fake.order = append([]string{"u_0", "u_1"}, fake.order[2:]...)
	_ = engine.SyncChannel(context.Background(), reload())
	if calls := fake.takeCalls(); strings.Join(calls, ",") != "u_0,u_1,u_5" {
		t.Fatalf("run 2 read %v, want only u_0,u_1 and the u_5 retry", calls)
	}
	if retries := parseSyncRetryState(reload().SyncRetryState); retries["u_5"].Attempts != 2 || len(retries) != 1 {
		t.Fatalf("run 2 retries: %+v", retries)
	}

	// Run 3: quota is used up. The run stops at the first -32 and spends no
	// attempt on u_5.
	fake.quotaLeft = 0
	_ = engine.SyncChannel(context.Background(), reload())
	stored = reload()
	if calls := fake.takeCalls(); len(calls) != 1 {
		t.Fatalf("run 3 kept calling Zalo after the quota error: %v", calls)
	}
	if !strings.Contains(stored.LastSyncError, "quota") || parseSyncRetryState(stored.SyncRetryState)["u_5"].Attempts != 2 {
		t.Fatalf("run 3: error=%q retries=%v", stored.LastSyncError, stored.SyncRetryState)
	}

	// Runs 4-5: quota is back, u_5 keeps failing and is dropped after its
	// 4th failed run. The channel is healthy again.
	fake.quotaLeft = -1
	_ = engine.SyncChannel(context.Background(), reload())
	if err := engine.SyncChannel(context.Background(), reload()); err != nil {
		t.Fatalf("run 5 should succeed after giving up on u_5: %v", err)
	}
	stored = reload()
	if stored.LastSyncStatus != "success" || stored.SyncRetryState != nil {
		t.Fatalf("run 5: status=%s retries=%v", stored.LastSyncStatus, stored.SyncRetryState)
	}
	var skipped int64
	database.Model(&models.ActivityLog{}).Where("resource_id = ? AND action = ? AND error_message LIKE ?", channel.ID, "sync.error", "%Skipped 1 conversation%").Count(&skipped)
	if skipped != 1 {
		t.Fatalf("giving up on u_5 should be logged once, got %d", skipped)
	}
	var messages int64
	database.Model(&models.Message{}).Where("tenant_id = ?", tenant.ID).Count(&messages)
	if messages != 31 { // 29 conversations in run 1, then new messages for u_0 and u_1
		t.Fatalf("messages=%d, want 31", messages)
	}
}
