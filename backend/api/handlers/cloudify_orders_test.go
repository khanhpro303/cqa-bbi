package handlers

import "testing"

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
