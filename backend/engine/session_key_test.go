package engine

import "testing"

// TestBuildSessionKeyDefaultIsLegacy is the non-regression guard: with the
// per-sender feature OFF (the default), BuildSessionKey must return byte-for-byte
// the same strings it always did, for both 1:1 and group chats. If this ever
// changes, every existing 1:1 and group flow (pending_options replay, debt/order
// state machines) breaks because the worker and ERP would read/write a different
// key than the data was stored under.
func TestBuildSessionKeyDefaultIsLegacy(t *testing.T) {
	prev := PerSenderGroupSessions()
	SetPerSenderGroupSessions(false)
	defer SetPerSenderGroupSessions(prev)

	cases := []struct {
		name                           string
		channelID, zaloUserID, groupID string
		want                           string
	}{
		{"1:1 dm", "chan1", "user1", "", "zalo_session:chan1:user1"},
		{"group", "chan1", "user1", "g1", "zalo_session:chan1:group:g1"},
		{"group ignores empty user", "chan1", "", "g1", "zalo_session:chan1:group:g1"},
	}
	for _, tc := range cases {
		if got := BuildSessionKey(tc.channelID, tc.zaloUserID, tc.groupID); got != tc.want {
			t.Errorf("%s: BuildSessionKey(%q,%q,%q) = %q; want %q (legacy default must not change)",
				tc.name, tc.channelID, tc.zaloUserID, tc.groupID, got, tc.want)
		}
	}
}

// TestBuildSessionKeyPerSender verifies that enabling the feature scopes ONLY
// group keys to the sender; 1:1 keys are never affected, and a group with no
// sender still collapses to the group-level key (defensive — never a key with a
// dangling ":user:").
func TestBuildSessionKeyPerSender(t *testing.T) {
	prev := PerSenderGroupSessions()
	SetPerSenderGroupSessions(true)
	defer SetPerSenderGroupSessions(prev)

	cases := []struct {
		name                           string
		channelID, zaloUserID, groupID string
		want                           string
	}{
		{"group scoped to sender", "chan1", "userA", "g1", "zalo_session:chan1:group:g1:user:userA"},
		{"different sender, different key", "chan1", "userB", "g1", "zalo_session:chan1:group:g1:user:userB"},
		{"1:1 dm unaffected", "chan1", "userA", "", "zalo_session:chan1:userA"},
		{"group without sender stays group-level", "chan1", "", "g1", "zalo_session:chan1:group:g1"},
	}
	for _, tc := range cases {
		if got := BuildSessionKey(tc.channelID, tc.zaloUserID, tc.groupID); got != tc.want {
			t.Errorf("%s: BuildSessionKey(%q,%q,%q) = %q; want %q",
				tc.name, tc.channelID, tc.zaloUserID, tc.groupID, got, tc.want)
		}
	}
}

// TestPerSenderGroupKeySweptByGroupClear guards the teardown contract: the
// per-sender group key and its sidecars must match the glob patterns
// ClearGroupSessionState scans, so ending a group session wipes every member's
// flow-state. A change to the key shape that escapes those globs would orphan
// per-user state and let it bleed into the next session.
func TestPerSenderGroupKeySweptByGroupClear(t *testing.T) {
	prev := PerSenderGroupSessions()
	SetPerSenderGroupSessions(true)
	defer SetPerSenderGroupSessions(prev)

	base := BuildSessionKey("chan1", "userA", "g1") // zalo_session:chan1:group:g1:user:userA
	sidecar := base + PendingOptionsSuffix
	// ClearGroupSessionState scans "zalo_session:*:group:g1" and
	// "zalo_session:*:group:g1:*"; the second must cover both the per-user base
	// and its sidecars.
	const wantPrefix = "zalo_session:chan1:group:g1:"
	if len(base) <= len(wantPrefix) || base[:len(wantPrefix)] != wantPrefix {
		t.Errorf("per-sender base key %q is not under group glob prefix %q*", base, wantPrefix)
	}
	if len(sidecar) <= len(wantPrefix) || sidecar[:len(wantPrefix)] != wantPrefix {
		t.Errorf("per-sender sidecar key %q is not under group glob prefix %q*", sidecar, wantPrefix)
	}
}
