package pkg

import (
	"context"
	"testing"
	"time"
)

func TestCloudifyAppointmentSessionDoesNotCrossDatabases(t *testing.T) {
	first := &CloudifyClient{BaseURL: "https://cloudify-fixture.test", DB: "first", Login: "shared"}
	second := &CloudifyClient{BaseURL: first.BaseURL, DB: "second", Login: first.Login}
	if first.cacheKey() == second.cacheKey() {
		t.Fatal("different Cloudify databases must use separate session keys")
	}

	cloudifySessionCacheMu.Lock()
	cloudifySessionCache[first.cacheKey()] = &cloudifySessionEntry{Cookie: "session_id=first", ExpiresAt: time.Now().Add(time.Minute)}
	cloudifySessionCacheMu.Unlock()
	t.Cleanup(first.InvalidateSession)

	if _, err := second.SearchOrderAppointments(context.Background(), []string{"DH123"}, []string{"S001"}); err == nil {
		t.Fatal("appointment lookup should skip when the correct database has no cached session")
	}
}
