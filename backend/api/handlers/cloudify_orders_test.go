package handlers

import (
	"testing"

	"github.com/vietbui/chat-quality-agent/cloudifyorders"
)

func TestApplyCloudifyPaymentSettings(t *testing.T) {
	bin, number, name := "970415", " 0012345678 ", " Công ty mẫu "
	blank := ""
	tests := []struct {
		name string
		body cloudifyOrderSettingsBody
		want map[string]string
		fail bool
	}{
		{name: "omitted"},
		{name: "partial", body: cloudifyOrderSettingsBody{PaymentAccountNo: &number}, fail: true},
		{
			name: "configured",
			body: cloudifyOrderSettingsBody{PaymentBankBIN: &bin, PaymentAccountNo: &number, PaymentAccountName: &name},
			want: map[string]string{
				cloudifyorders.KeyPaymentBankBIN: bin, cloudifyorders.KeyPaymentAccountNo: "0012345678",
				cloudifyorders.KeyPaymentAccountName: "Công ty mẫu",
			},
		},
		{
			name: "clear all",
			body: cloudifyOrderSettingsBody{PaymentBankBIN: &blank, PaymentAccountNo: &blank, PaymentAccountName: &blank},
			want: map[string]string{
				cloudifyorders.KeyPaymentBankBIN: "", cloudifyorders.KeyPaymentAccountNo: "", cloudifyorders.KeyPaymentAccountName: "",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := map[string]string{}
			err := applyCloudifyPaymentSettings(tt.body, got)
			if (err != nil) != tt.fail || len(got) != len(tt.want) {
				t.Fatalf("settings = %v, error = %v", got, err)
			}
			for key, want := range tt.want {
				if value, ok := got[key]; !ok || value != want {
					t.Errorf("%s = %q, want %q", key, value, want)
				}
			}
		})
	}
}

func TestCloudifyOrderCodeFromBody(t *testing.T) {
	cases := map[string]string{
		``:                                    "",
		`not json`:                            "",
		`{"_id": 16, "_model": "sale.order"}`: "",
		`{"_id": 16, "name": "ĐH000016"}`:     "ĐH000016",
		`{"MA_HOA_DON": " ĐH000017 "}`:        "ĐH000017",
		`{"record": {"SO_DON_HANG": "ĐH9"}}`:  "ĐH9",
		`{"data": {"order_code": "ĐH10"}}`:    "ĐH10",
	}
	for body, want := range cases {
		if got := cloudifyOrderCodeFromBody([]byte(body)); got != want {
			t.Errorf("cloudifyOrderCodeFromBody(%q) = %q, want %q", body, got, want)
		}
	}
}

func TestHashWebhookSecretIsStable(t *testing.T) {
	if hashWebhookSecret("abc") != hashWebhookSecret("abc") || hashWebhookSecret("abc") == hashWebhookSecret("abd") {
		t.Fatal("hash must be deterministic and distinct")
	}
	if len(hashWebhookSecret("abc")) != 64 {
		t.Fatal("expected hex sha256")
	}
}
