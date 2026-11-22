package engine

import "testing"

func TestIsDKCopy(t *testing.T) {
	cases := []struct {
		ma, maCha string
		want      bool
	}{
		{"SP000556_DK", "SP000557_DK", true},
		{"5039910980050_DK", "SP000557", true},
		{"SP001435", " sp000557_dk ", true},
		{"SP001435", "SP000557", false},
		{"DKSP1", "SP_DKX", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := IsDKCopy(c.ma, c.maCha); got != c.want {
			t.Errorf("IsDKCopy(%q, %q) = %v; want %v", c.ma, c.maCha, got, c.want)
		}
	}
}

func TestRealParentOf(t *testing.T) {
	for in, want := range map[string]string{"SP000557_DK": "SP000557", " SP000557_dk ": "SP000557", "SP000557": "SP000557", "": ""} {
		if got := RealParentOf(in); got != want {
			t.Errorf("RealParentOf(%q) = %q; want %q", in, got, want)
		}
	}
}
