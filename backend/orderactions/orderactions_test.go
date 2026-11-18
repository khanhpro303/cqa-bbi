package orderactions

import "testing"

func TestParseReply(t *testing.T) {
	cases := map[string]Reply{
		"1":                  ReplyConfirm,
		" Xác nhận ":         ReplyConfirm,
		"OK!":                ReplyConfirm,
		"đồng ý":             ReplyConfirm,
		"chốt đơn":           ReplyConfirm,
		"2":                  ReplyDecline,
		"Huỷ":                ReplyDecline,
		"hủy":                ReplyDecline,
		"không":              ReplyDecline,
		"ko":                 ReplyDecline,
		"1 cái FF818 size L": ReplyNone,
		"huỷ đơn ĐH000016":   ReplyNone,
		"còn hàng không":     ReplyNone,
		"":                   ReplyNone,
		"12":                 ReplyNone,
	}
	for text, want := range cases {
		if got := ParseReply(text); got != want {
			t.Errorf("ParseReply(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestFormatVND(t *testing.T) {
	cases := map[float64]string{0: "0đ", 950: "950đ", 1250000: "1.250.000đ", 4490000.4: "4.490.000đ", -12000: "-12.000đ"}
	for amount, want := range cases {
		if got := FormatVND(amount); got != want {
			t.Errorf("FormatVND(%v) = %q, want %q", amount, got, want)
		}
	}
}

func TestCreatedOrderCode(t *testing.T) {
	cases := map[string]string{
		`{"code":200,"message":"ok","data":{"id":12,"SO_DON_HANG":"ĐH000017"}}`: "ĐH000017",
		`{"data":[{"id":12,"MA_HOA_DON":"ĐH000018"}]}`:                          "ĐH000018",
		`{"result":{"record":{"SO_DON_HANG":"ĐH000019"}}}`:                      "ĐH000019",
		`[{"SO_DON_HANG":"ĐH000020"}]`:                                          "ĐH000020",
	}
	for raw, want := range cases {
		if got, ok := CreatedOrderCode([]byte(raw)); !ok || got != want {
			t.Errorf("CreatedOrderCode(%s) = %q %v, want %q", raw, got, ok, want)
		}
	}
	if _, ok := CreatedOrderCode([]byte(`{"status":"ok"}`)); ok {
		t.Error("expected no code")
	}
}
