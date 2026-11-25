package cloudifyorders

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	vietQRMaxAmount = 9_999_999_999_999
	vietQRMaxMemo   = 25
)

var (
	vietQRBankBIN       = regexp.MustCompile(`^[0-9]{6}$`)
	vietQRAccountNo     = regexp.MustCompile(`^[A-Za-z0-9]{3,25}$`)
	vietQRNameChars     = regexp.MustCompile(`[^A-Za-z ]`)
	vietQRTransferChars = regexp.MustCompile(`[^A-Za-z0-9 ]`)
	vietQRMemoFormat    = regexp.MustCompile(`^[A-Za-z0-9]+( [A-Za-z0-9]+)*$`)
	vietQRVietnameseD   = strings.NewReplacer("Đ", "D", "đ", "d")
)

// PaymentAccount identifies the tenant's bank account for order confirmations.
type PaymentAccount struct {
	BankBIN     string `json:"bankBin"`
	AccountNo   string `json:"accountNo"`
	AccountName string `json:"accountName"`
}

func (a PaymentAccount) IsEmpty() bool {
	return strings.TrimSpace(a.BankBIN) == "" && strings.TrimSpace(a.AccountNo) == "" && strings.TrimSpace(a.AccountName) == ""
}

// Validate checks the input against the offline VietQR SDK's account format.
func (a PaymentAccount) Validate() error {
	if !vietQRBankBIN.MatchString(strings.TrimSpace(a.BankBIN)) {
		return errors.New("VietQR bank BIN must contain 6 digits")
	}
	if !vietQRAccountNo.MatchString(strings.TrimSpace(a.AccountNo)) {
		return errors.New("VietQR account number must contain 3 to 25 letters or digits")
	}
	name := strings.TrimSpace(a.AccountName)
	// The SDK strips accents, symbols and digits before validating the owner name.
	lettersAndDigits := vietQRTransferChars.ReplaceAllString(vietQRVietnameseD.Replace(norm.NFD.String(name)), "")
	spaced := strings.Join(strings.Fields(lettersAndDigits), " ")
	normalized := strings.TrimSpace(vietQRNameChars.ReplaceAllString(spaced, ""))
	if utf8.RuneCountInString(name) > 50 || len(normalized) < 5 || len(normalized) > 50 {
		return errors.New("VietQR account holder name must contain 5 to 50 letters and spaces")
	}
	return nil
}

// VietQRImage returns a PNG decoded as an image and the exact transfer memo it encodes.
// The official VietQR Node SDK runs locally and receives payment details over stdin.
func VietQRImage(ctx context.Context, account PaymentAccount, amount int64, customerLabel string) (image.Image, string, error) {
	if err := account.Validate(); err != nil {
		return nil, "", err
	}
	if amount <= 0 || amount > vietQRMaxAmount {
		return nil, "", errors.New("VietQR amount must be a positive whole amount of at most 13 digits")
	}
	memo := customerCode(customerLabel)
	// Reject codes the SDK would alter, so the QR always contains the exact customer code.
	if len(memo) > vietQRMaxMemo || !vietQRMemoFormat.MatchString(memo) {
		return nil, "", errors.New("customer code is missing or cannot be encoded exactly as a VietQR transfer memo")
	}
	input, err := json.Marshal(struct {
		PaymentAccount
		Amount int64  `json:"amount"`
		Memo   string `json:"memo"`
	}{
		PaymentAccount: PaymentAccount{
			BankBIN:     strings.TrimSpace(account.BankBIN),
			AccountNo:   strings.TrimSpace(account.AccountNo),
			AccountName: strings.TrimSpace(account.AccountName),
		},
		Amount: amount,
		Memo:   memo,
	})
	if err != nil {
		return nil, "", fmt.Errorf("encode VietQR request: %w", err)
	}
	helper, err := vietQRHelperPath()
	if err != nil {
		return nil, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", helper)
	command.Env = []string{}
	command.Stdin = bytes.NewReader(input)
	data, err := command.Output()
	if err != nil {
		return nil, "", fmt.Errorf("VietQR image generation failed: %w", err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Width != config.Height || config.Width > 1024 {
		return nil, "", errors.New("VietQR helper returned an invalid PNG")
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("decode VietQR image: %w", err)
	}
	return img, memo, nil
}

func vietQRHelperPath() (string, error) {
	// The container ships the helper beside cqa-server. Local builds use the
	// source tree, from either the root, backend, or package directory.
	var directories []string
	if executable, err := os.Executable(); err == nil {
		directories = append(directories, filepath.Dir(executable))
	}
	directories = append(directories, ".", "cloudifyorders", "backend/cloudifyorders")
	for _, dir := range directories {
		path, err := filepath.Abs(filepath.Join(dir, "vietqr-node", "generate.mjs"))
		if err != nil {
			continue
		}
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	return "", errors.New("VietQR helper is unavailable")
}
