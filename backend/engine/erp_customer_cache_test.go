package engine

import (
	"testing"

	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestApplyRegions(t *testing.T) {
	tests := []struct {
		name         string
		customers    []models.CachedCustomer
		regionByCode map[string]string
		wantEnriched int
		wantRegions  map[string]string // MA -> expected REGION
	}{
		{
			name:         "empty region map leaves regions blank",
			customers:    []models.CachedCustomer{{MA: "KH001"}, {MA: "KH002"}},
			regionByCode: map[string]string{},
			wantEnriched: 0,
			wantRegions:  map[string]string{"KH001": "", "KH002": ""},
		},
		{
			name:         "exact code match sets region",
			customers:    []models.CachedCustomer{{MA: "KH001"}, {MA: "KH002"}},
			regionByCode: map[string]string{"KH001": "Miền Bắc", "KH002": "Miền Nam"},
			wantEnriched: 2,
			wantRegions:  map[string]string{"KH001": "Miền Bắc", "KH002": "Miền Nam"},
		},
		{
			name:         "missing code in map leaves that customer blank",
			customers:    []models.CachedCustomer{{MA: "KH001"}, {MA: "KH999"}},
			regionByCode: map[string]string{"KH001": "Miền Trung"},
			wantEnriched: 1,
			wantRegions:  map[string]string{"KH001": "Miền Trung", "KH999": ""},
		},
		{
			name:         "empty region value does not count as enriched",
			customers:    []models.CachedCustomer{{MA: "KH001"}},
			regionByCode: map[string]string{"KH001": ""},
			wantEnriched: 0,
			wantRegions:  map[string]string{"KH001": ""},
		},
		{
			name:         "MA with surrounding whitespace still matches",
			customers:    []models.CachedCustomer{{MA: " KH001 "}},
			regionByCode: map[string]string{"KH001": "Miền Bắc"},
			wantEnriched: 1,
			wantRegions:  map[string]string{" KH001 ": "Miền Bắc"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyRegions(tt.customers, tt.regionByCode)
			if got != tt.wantEnriched {
				t.Errorf("applyRegions enriched count = %d, want %d", got, tt.wantEnriched)
			}
			for _, c := range tt.customers {
				if want, ok := tt.wantRegions[c.MA]; ok && c.REGION != want {
					t.Errorf("customer %q REGION = %q, want %q", c.MA, c.REGION, want)
				}
			}
		})
	}
}
