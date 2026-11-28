package knowledge

import (
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeTags(t *testing.T) {
	// "Công nợ" typed with a decomposed (NFD) input method, odd spacing and
	// case all become one tag.
	got, err := NormalizeTags([]string{"Công  nợ", "công nợ", " bảo hành ", "", "Bảo Hành"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"bảo hành", "công nợ"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tags = %q, want %q", got, want)
	}
	_, err = NormalizeTags([]string{"#khuyến mãi"})
	var bad ErrInvalidTag
	if !errors.As(err, &bad) || bad.Tag != "#khuyến mãi" {
		t.Fatalf("err = %v", err)
	}
	if got, _ := NormalizeTags(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil input = %#v", got)
	}
}

func TestSplitTagInput(t *testing.T) {
	got := SplitTagInput([]string{"bảo hành, giao hàng", "công nợ"})
	if want := []string{"bảo hành", " giao hàng", "công nợ"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("split = %q", got)
	}
}

// An agent filter that is entirely invalid must not widen to every document.
func TestFilterTagsNeverWidens(t *testing.T) {
	if tags, none := filterTags(nil); tags != nil || none {
		t.Fatalf("no filter = %v %v", tags, none)
	}
	if tags, none := filterTags([]string{"#x", "@y"}); len(tags) != 0 || !none {
		t.Fatalf("invalid filter = %v %v", tags, none)
	}
	if tags, none := filterTags([]string{"Công nợ", "#x"}); !reflect.DeepEqual(tags, []string{"công nợ"}) || none {
		t.Fatalf("mixed filter = %v %v", tags, none)
	}
}
