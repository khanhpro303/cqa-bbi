package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckpointAtomicReplacementAndPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	first := checkpoint{Tenant: "tenant", Channel: "channel", Kind: "orders", After: "first"}
	if err := saveCheckpoint(path, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.After = "second"
	if err := saveCheckpoint(path, second); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var actual checkpoint
	if err = json.Unmarshal(body, &actual); err != nil {
		t.Fatal(err)
	}
	if actual != second {
		t.Fatalf("checkpoint=%+v", actual)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%v", info.Mode())
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatal("temporary checkpoint files leaked")
	}
}
func TestBackfillStableIDsSeparateSourceAndRevision(t *testing.T) {
	id := eventID("order", "result-1", "order-1")
	if id != eventID("order", "result-1", "order-1") {
		t.Fatal("retry changed ID")
	}
	if id == eventID("order", "result-2", "order-1") {
		t.Fatal("revision reused ID")
	}
	if id == eventID("referral", "result-1", "order-1") {
		t.Fatal("source kind reused ID")
	}
}
