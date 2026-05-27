package engine

import "testing"

func TestSessionKey(t *testing.T) {
	tests := []struct {
		name        string
		channelID   string
		zaloUserID  string
		zaloGroupID string
		want        string
	}{
		{
			name:       "individual chat keyed by sender",
			channelID:  "ch-1",
			zaloUserID: "user-7",
			want:       "zalo_session:ch-1:user-7",
		},
		{
			name:        "group chat keyed by group, sender ignored",
			channelID:   "ch-1",
			zaloUserID:  "user-7",
			zaloGroupID: "grp-99",
			want:        "zalo_session:ch-1:group:grp-99",
		},
		{
			name:       "empty group falls back to user key",
			channelID:  "ch-2",
			zaloUserID: "user-1",
			want:       "zalo_session:ch-2:user-1",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := SessionKey(tc.channelID, tc.zaloUserID, tc.zaloGroupID)
			if got != tc.want {
				t.Errorf("SessionKey = %q, want %q", got, tc.want)
			}
		})
	}
}
