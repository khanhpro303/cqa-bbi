package workers

import (
	"context"
	"fmt"
	"time"

	"github.com/vietbui/chat-quality-agent/db"
)

// Pairing codes are 6 characters, so a sender who could try codes without
// limit would eventually hit someone else's. Each Zalo account gets
// verifyMaxFailures wrong codes per verifyFailureWindow.
const (
	verifyMaxFailures   = 5
	verifyFailureWindow = time.Hour

	verifyLockedMessage        = "Bạn đã nhập sai mã xác thực quá nhiều lần. Vui lòng thử lại sau 1 giờ hoặc liên hệ nhân viên để được hỗ trợ."
	verifyAlreadyLinkedMessage = "Mã xác thực này đã được dùng cho một tài khoản Zalo khác. Vui lòng liên hệ nhân viên để được hỗ trợ."
)

func verifyFailureKey(tenantID, zaloUserID string) string {
	return fmt.Sprintf("verify_fail:%s:%s", tenantID, zaloUserID)
}

// verifyAttemptsExceeded reports whether this sender has used up their wrong
// codes for the current window. Without Redis there is no counter, and the
// check passes.
func verifyAttemptsExceeded(ctx context.Context, tenantID, zaloUserID string) bool {
	if db.RedisClient == nil || zaloUserID == "" {
		return false
	}
	n, err := db.RedisClient.Get(ctx, verifyFailureKey(tenantID, zaloUserID)).Int()
	return err == nil && n >= verifyMaxFailures
}

// recordVerifyFailure counts one wrong or refused code for the sender.
func recordVerifyFailure(ctx context.Context, tenantID, zaloUserID string) {
	if db.RedisClient == nil || zaloUserID == "" {
		return
	}
	key := verifyFailureKey(tenantID, zaloUserID)
	if n, err := db.RedisClient.Incr(ctx, key).Result(); err == nil && n == 1 {
		db.RedisClient.Expire(ctx, key, verifyFailureWindow)
	}
}
