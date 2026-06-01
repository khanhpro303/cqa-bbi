package engine

import "testing"

func TestResolveNumericSelection(t *testing.T) {
	opts := []string{
		"#show_macha_options_by_web:Áo gió",
		"#show_macha_options_by_web:Áo khoác",
		"#show_macha_options:FF901",
	}
	cases := []struct {
		name        string
		in          string
		options     []string
		wantPayload string
		wantMatched bool
		wantInRange bool
	}{
		{"first option", "1", opts, opts[0], true, true},
		{"last option", "3", opts, opts[2], true, true},
		{"with surrounding spaces", " 2 ", opts, opts[1], true, true},
		{"tab and newline", "\t2\n", opts, opts[1], true, true},
		{"out of range high", "5", opts, "", true, false},
		{"zero out of range", "0", opts, "", true, false},
		{"not a number", "áo", opts, "", false, false},
		{"mixed text", "1 cái", opts, "", false, false},
		{"leading zero non-canonical", "01", opts, "", false, false},
		{"empty text", "", opts, "", false, false},
		{"no pending options", "1", nil, "", false, false},
		{"empty options slice", "1", []string{}, "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, matched, inRange := ResolveNumericSelection(tc.in, tc.options)
			if payload != tc.wantPayload {
				t.Errorf("payload = %q; want %q", payload, tc.wantPayload)
			}
			if matched != tc.wantMatched {
				t.Errorf("matched = %v; want %v", matched, tc.wantMatched)
			}
			if inRange != tc.wantInRange {
				t.Errorf("inRange = %v; want %v", inRange, tc.wantInRange)
			}
		})
	}
}

func TestBuildSessionKey(t *testing.T) {
	cases := []struct {
		name        string
		channelID   string
		zaloUserID  string
		zaloGroupID string
		want        string
	}{
		{"individual", "ch1", "u123", "", "zalo_session:ch1:u123"},
		{"group overrides user", "ch1", "u123", "g999", "zalo_session:ch1:group:g999"},
		{"empty user, no group", "ch1", "", "", "zalo_session:ch1:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildSessionKey(tc.channelID, tc.zaloUserID, tc.zaloGroupID)
			if got != tc.want {
				t.Errorf("BuildSessionKey(%q,%q,%q) = %q; want %q",
					tc.channelID, tc.zaloUserID, tc.zaloGroupID, got, tc.want)
			}
		})
	}
}
