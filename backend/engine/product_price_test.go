package engine

import "testing"

func TestFormatVNDPrice(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want string
	}{
		{"thousands", 1290000, "1.290.000đ"},
		{"millions", 11900000, "11.900.000đ"},
		{"sub-thousand", 990, "990đ"},
		{"exact thousand", 1000, "1.000đ"},
		{"rounds half up", 1290000.6, "1.290.001đ"},
		{"zero", 0, "0đ"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatVNDPrice(tc.in); got != tc.want {
				t.Errorf("FormatVNDPrice(%v) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFormatPriceRange(t *testing.T) {
	cases := []struct {
		name             string
		minPrice, maxPrice float64
		want             string
	}{
		{"distinct bounds", 990000, 1450000, "990.000đ - 1.450.000đ"},
		{"equal bounds collapse", 1290000, 1290000, "1.290.000đ"},
		{"zero min is unavailable", 0, 1450000, "Liên hệ"},
		{"zero max is unavailable", 990000, 0, "Liên hệ"},
		{"both zero", 0, 0, "Liên hệ"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatPriceRange(tc.minPrice, tc.maxPrice); got != tc.want {
				t.Errorf("FormatPriceRange(%v,%v) = %q; want %q", tc.minPrice, tc.maxPrice, got, tc.want)
			}
		})
	}
}

func TestPriceRangeOfPrices(t *testing.T) {
	cases := []struct {
		name             string
		prices           []float64
		wantMin, wantMax float64
	}{
		{"mixed", []float64{1450000, 990000, 1200000}, 990000, 1450000},
		{"ignores non-positive", []float64{0, -5, 1290000}, 1290000, 1290000},
		{"all non-positive yields zero", []float64{0, -1}, 0, 0},
		{"empty yields zero", nil, 0, 0},
		{"single", []float64{1290000}, 1290000, 1290000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotMin, gotMax := PriceRangeOfPrices(tc.prices)
			if gotMin != tc.wantMin || gotMax != tc.wantMax {
				t.Errorf("PriceRangeOfPrices(%v) = (%v,%v); want (%v,%v)", tc.prices, gotMin, gotMax, tc.wantMin, tc.wantMax)
			}
		})
	}
}
