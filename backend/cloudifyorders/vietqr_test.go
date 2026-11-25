package cloudifyorders

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVietQRPaymentAccountAndMemo(t *testing.T) {
	valid := PaymentAccount{BankBIN: "970415", AccountNo: "00123456789", AccountName: "Công ty Điện Máy"}
	if valid.IsEmpty() || valid.Validate() != nil {
		t.Fatal("valid VietQR account was rejected")
	}
	if !(PaymentAccount{BankBIN: " "}).IsEmpty() {
		t.Fatal("whitespace-only account should be empty")
	}
	for _, account := range []PaymentAccount{
		{BankBIN: "ICB", AccountNo: valid.AccountNo, AccountName: valid.AccountName},
		{BankBIN: valid.BankBIN, AccountNo: "abc/123", AccountName: valid.AccountName},
		{BankBIN: valid.BankBIN, AccountNo: valid.AccountNo, AccountName: "A    B"},
	} {
		if account.Validate() == nil {
			t.Errorf("invalid account accepted: %#v", account)
		}
	}
	for _, code := range []string{"", "S-084", "S  084", strings.Repeat("x", 26)} {
		img, memo, err := VietQRImage(t.Context(), valid, 1000, code)
		if err == nil || img != nil || memo != "" {
			t.Errorf("expected VietQR to reject unusable customer code %q", code)
		}
	}
}

func TestVietQRImageWithOfflineSDK(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node runtime unavailable")
	}
	helper, err := vietQRHelperPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(helper), "node_modules", "vietqr", "package.json")); errors.Is(err, fs.ErrNotExist) {
		t.Skip("VietQR dependencies not installed; run npm ci in vietqr-node")
	} else if err != nil {
		t.Fatal(err)
	}
	account := PaymentAccount{BankBIN: "970415", AccountNo: "00123456789", AccountName: "Công ty Điện Máy"}
	for _, label := range []string{"S084", " S084 - Chợ Bảo Hộ "} {
		img, memo, err := VietQRImage(t.Context(), account, 2910000, label)
		if err != nil {
			t.Fatal(err)
		}
		if memo != "S084" || img.Bounds().Dx() < 450 {
			t.Fatalf("unexpected generated QR for %q: memo %q, bounds %v", label, memo, img.Bounds())
		}
	}
}
