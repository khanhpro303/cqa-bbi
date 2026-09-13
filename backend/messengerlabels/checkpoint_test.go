package messengerlabels

import (
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestOnlyCompletedSyncPublishesClassification(t *testing.T) {
	for _, status := range []string{"never", "syncing", "error", "", "unexpected"} {
		if completedSync(status) {
			t.Fatalf("%s must not publish classification", status)
		}
	}
	if !completedSync("success") || !completedSync("partial") {
		t.Fatal("completed reads must be available")
	}
}

func TestLeaseOwnershipRejectsStaleWorkers(t *testing.T) {
	now := time.Now()
	future, past := now.Add(time.Minute), now.Add(-time.Minute)
	valid := models.MessengerLabelState{Enabled: true, SyncToken: "current", SyncStatus: "syncing", LeaseUntil: &future}
	if !ownsLease(valid, "current", now) {
		t.Fatal("valid owner rejected")
	}
	for _, change := range []func(*models.MessengerLabelState){
		func(s *models.MessengerLabelState) { s.SyncToken = "new-owner" },
		func(s *models.MessengerLabelState) { s.LeaseUntil = nil },
		func(s *models.MessengerLabelState) { s.LeaseUntil = &past },
		func(s *models.MessengerLabelState) { s.LeaseUntil = &now },
		func(s *models.MessengerLabelState) { s.Enabled = false },
		func(s *models.MessengerLabelState) { s.SyncStatus = "success" },
	} {
		state := valid
		change(&state)
		if ownsLease(state, "current", now) {
			t.Fatalf("stale ownership accepted: %+v", state)
		}
	}
	if ownsLease(valid, "", now) {
		t.Fatal("empty token accepted")
	}
}
