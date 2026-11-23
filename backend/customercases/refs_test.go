package customercases

import "testing"

func TestStripRefs(t *testing.T) {
	cases := map[string]string{
		"Dạ em đã ghi nhận. Mã ticket: #66656.":              "Dạ em đã ghi nhận.",
		"Dạ em đã ghi nhận (ticket #123) và chuyển bộ phận.": "Dạ em đã ghi nhận và chuyển bộ phận.",
		"Case số 4567 đang được xử lý":                       "đang được xử lý",
		"Số phiếu: 889 ạ":                                    "ạ",
		"Bên em sẽ đổi hàng, mã hồ sơ #A-12":                 "Bên em sẽ đổi hàng,",
		"Cảm ơn anh.\nTicket #66656\nEm sẽ báo lại.":         "Cảm ơn anh.\n\nEm sẽ báo lại.",
		"Dạ, bên em đang xử lý #66656 ạ":                     "Dạ, bên em đang xử lý ạ",
	}
	for in, want := range cases {
		if got := StripRefs(in); got != want {
			t.Errorf("StripRefs(%q) = %q, want %q", in, got, want)
		}
	}
	keep := []string{
		"Đơn DH0565913-3 giao ngày 22/11, tổng 1.250.000đ",
		"Kính LS2 FF800 size XL còn 12 cái",
		"Phiếu xuất kho XK0565913-3.1 đã có",
		"Mã KH KH0123",
		"Case FF800 bị nứt",
	}
	for _, s := range keep {
		if got := StripRefs(s); got != s {
			t.Errorf("StripRefs(%q) = %q, want unchanged", s, got)
		}
	}
}
