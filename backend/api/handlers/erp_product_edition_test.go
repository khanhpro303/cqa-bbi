package handlers

import (
	"slices"
	"testing"

	"github.com/vietbui/chat-quality-agent/engine"
)

func TestRomanProductEditionKeepsExactModel(t *testing.T) {
	for _, tc := range []struct {
		search string
		want   []string
	}{
		{"Storm II", []string{"LS2 FF800"}},
		{"Storm III", []string{"LS2 FF818"}},
		{"Storm I", nil},
		{"Storm", []string{"LS2 FF800", "LS2 FF818", "Unrelated model"}},
	} {
		t.Run(tc.search, func(t *testing.T) {
			products := []map[string]interface{}{
				{"MA": "800", "TEN_DONG_BO_WEB": "LS2 FF800", "TEN": "Nón LS2 STORM II - White L"},
				{"MA": "818", "TEN_DONG_BO_WEB": "LS2 FF818", "TEN": "Nón LS2 Storm III"},
				{"MA": "unrelated", "TEN_DONG_BO_WEB": "Unrelated model", "TEN": "Hyperstorm II"},
			}
			var got []string
			for _, group := range engine.RankProductWebGroups(matchRomanProductEdition(products, tc.search)) {
				got = append(got, group.WebName)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("groups = %v, want %v", got, tc.want)
			}
		})
	}
}
