package handlers

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/engine"
)

// TestSelectUniqueVariantMatch covers the cross-line PRICE pivot decision that
// fixes the disambiguation loop: when a variant keyword ("FF901") LIKE-collides
// into several lines ("LS2 FF901" vs "LS2 FF901 Carbon"), answer directly only
// when exactly one line carries the requested color/size — otherwise fall back
// to the disambiguation list rather than drop the attributes into a family-price
// loop.
func TestSelectUniqueVariantMatch(t *testing.T) {
	groups := func(parents ...[]string) []engine.WebGroupMatch {
		out := make([]engine.WebGroupMatch, 0, len(parents))
		for i, pcs := range parents {
			out = append(out, engine.WebGroupMatch{
				WebName:     "line" + string(rune('A'+i)),
				ParentCodes: pcs,
			})
		}
		return out
	}

	// resolverFor reports a match for exactly the parent codes in matchSet.
	resolverFor := func(matchSet map[string]bool, calls *[]string) func(string) (gin.H, bool) {
		return func(pc string) (gin.H, bool) {
			if calls != nil {
				*calls = append(*calls, pc)
			}
			if matchSet[pc] {
				return gin.H{"count": 1, "parent_code": pc}, true
			}
			return gin.H{"count": 0}, false
		}
	}

	t.Run("exactly one line carries the variant → pivot", func(t *testing.T) {
		g := groups([]string{"SP_FF901"}, []string{"SP_FF901C"})
		resp, ok := selectUniqueVariantMatch(g, resolverFor(map[string]bool{"SP_FF901": true}, nil))
		if !ok {
			t.Fatalf("expected unique match, got ok=false")
		}
		if resp["parent_code"] != "SP_FF901" {
			t.Errorf("matched wrong parent: %v", resp["parent_code"])
		}
		if resp["pivoted_from"] != "products" {
			t.Errorf("expected pivoted_from=products, got %v", resp["pivoted_from"])
		}
	})

	t.Run("two lines carry the variant → ambiguous, no pivot", func(t *testing.T) {
		g := groups([]string{"SP_FF901"}, []string{"SP_FF901C"})
		resp, ok := selectUniqueVariantMatch(g, resolverFor(map[string]bool{"SP_FF901": true, "SP_FF901C": true}, nil))
		if ok || resp != nil {
			t.Fatalf("expected fall-through on ambiguity, got ok=%v resp=%v", ok, resp)
		}
	})

	t.Run("no line carries the variant → no pivot", func(t *testing.T) {
		g := groups([]string{"SP_FF901"}, []string{"SP_FF901C"})
		resp, ok := selectUniqueVariantMatch(g, resolverFor(map[string]bool{}, nil))
		if ok || resp != nil {
			t.Fatalf("expected fall-through on zero matches, got ok=%v resp=%v", ok, resp)
		}
	})

	t.Run("blank parent codes are skipped", func(t *testing.T) {
		g := groups([]string{"", "SP_FF901"})
		var calls []string
		resp, ok := selectUniqueVariantMatch(g, resolverFor(map[string]bool{"SP_FF901": true}, &calls))
		if !ok || resp["parent_code"] != "SP_FF901" {
			t.Fatalf("expected match on non-blank parent, got ok=%v resp=%v", ok, resp)
		}
		for _, c := range calls {
			if c == "" {
				t.Errorf("resolver was called with a blank parent code")
			}
		}
	})

	t.Run("scan caps at variantPivotMaxCandidates", func(t *testing.T) {
		parents := make([][]string, 0, variantPivotMaxCandidates+2)
		for i := 0; i < variantPivotMaxCandidates+2; i++ {
			parents = append(parents, []string{"P" + string(rune('0'+i))})
		}
		g := groups(parents...)
		var calls []string
		// The unique match sits PAST the cap, so it must never be reached → no pivot.
		_, ok := selectUniqueVariantMatch(g, resolverFor(map[string]bool{"P" + string(rune('0'+variantPivotMaxCandidates+1)): true}, &calls))
		if ok {
			t.Fatalf("expected no pivot when match is beyond the candidate cap")
		}
		if len(calls) > variantPivotMaxCandidates {
			t.Errorf("scanned %d candidates, expected at most %d", len(calls), variantPivotMaxCandidates)
		}
	})
}
