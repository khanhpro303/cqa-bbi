// Package orderactions holds chatbot order drafts (create, edit, cancel a
// Cloudify sales order) and commits them once the customer confirms.
//
// The AI agent never writes to Cloudify. Its MCP tools only store a pending
// draft whose body the backend built from trusted data (the signed permission
// token, the product cache, the live order). The worker commits the draft when
// the same Zalo user answers "1" (or "xác nhận"), before the intent
// classifier runs, and claims it with a conditional UPDATE so a redelivered
// confirmation can never write twice.
package orderactions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"
)

// Draft kinds.
const (
	KindCreate = "create"
	KindUpdate = "update"
	KindCancel = "cancel"
)

// Draft statuses.
const (
	StatusPending    = "pending"
	StatusExecuting  = "executing"
	StatusDone       = "done"
	StatusFailed     = "failed"
	StatusUnknown    = "unknown"
	StatusDeclined   = "declined"
	StatusSuperseded = "superseded"
)

// Cloudify TRANG_THAI codes (see cloudifyorders.StatusName).
const (
	OrderStatusCancelled  = "0"
	OrderStatusInProgress = "1"
	OrderStatusCompleted  = "2"
	OrderStatusDelivering = "3"
)

// DraftTTL is how long a draft waits for the customer's confirmation.
const DraftTTL = 10 * time.Minute

// Endpoints of the Cloudify sales-order API.
const (
	createEndpoint = "saorders/create"
	updateEndpoint = "saorders/%d/update"
)

// Reply is how the customer answered a pending draft.
type Reply int

const (
	ReplyNone Reply = iota
	ReplyConfirm
	ReplyDecline
)

var confirmWords = map[string]bool{
	"1": true, "xac nhan": true, "dong y": true, "ok": true, "oke": true, "okay": true,
	"co": true, "yes": true, "chot": true, "chot don": true, "dat": true, "dung": true, "xn": true,
}

var declineWords = map[string]bool{
	"2": true, "huy": true, "khong": true, "ko": true, "k": true, "thoi": true, "no": true,
	"huy don": true, "khong dat": true, "bo": true,
}

// ParseReply reads a whole message as a confirmation or a refusal. Only short,
// unambiguous answers count: anything longer is a new request and goes to the
// agent as usual, leaving the draft pending.
func ParseReply(text string) Reply {
	normalized := normalize(text)
	switch {
	case confirmWords[normalized]:
		return ReplyConfirm
	case declineWords[normalized]:
		return ReplyDecline
	}
	return ReplyNone
}

func normalize(text string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(text)) {
		switch {
		case r == 'đ':
			b.WriteRune('d')
		case unicode.Is(unicode.Mn, r):
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// Save stores a new pending draft and supersedes any older pending draft of
// the same chat, so a "1" always confirms the summary the customer saw last.
// It also clears the chat's numbered menu, whose "1"/"2" would otherwise
// compete with the confirmation.
func Save(ctx context.Context, action *models.OrderAction) error {
	if action.ID == "" {
		action.ID = uuid.NewString()
	}
	action.Status = StatusPending
	if action.ExpiresAt.IsZero() {
		action.ExpiresAt = time.Now().Add(DraftTTL)
	}
	err := db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.OrderAction{}).
			Where("tenant_id = ? AND work_key = ? AND status = ?", action.TenantID, action.WorkKey, StatusPending).
			Update("status", StatusSuperseded).Error; err != nil {
			return err
		}
		return tx.Create(action).Error
	})
	if err != nil {
		return fmt.Errorf("save order draft: %w", err)
	}
	if db.RedisClient != nil && action.WorkKey != "" {
		db.RedisClient.Del(ctx, action.WorkKey+engine.PendingOptionsSuffix)
	}
	return nil
}

// Pending returns the chat's live draft, if any.
func Pending(ctx context.Context, tenantID, workKey string) (*models.OrderAction, bool) {
	if workKey == "" || db.DB == nil {
		return nil, false
	}
	var action models.OrderAction
	err := db.DB.WithContext(ctx).
		Where("tenant_id = ? AND work_key = ? AND status = ? AND expires_at > ?", tenantID, workKey, StatusPending, time.Now()).
		Order("created_at DESC").First(&action).Error
	if err != nil {
		return nil, false
	}
	return &action, true
}

// Decline marks the draft as refused by its owner.
func Decline(ctx context.Context, id, zaloUserID string) bool {
	res := db.DB.WithContext(ctx).Model(&models.OrderAction{}).
		Where("id = ? AND zalo_user_id = ? AND status = ?", id, zaloUserID, StatusPending).
		Update("status", StatusDeclined)
	return res.Error == nil && res.RowsAffected == 1
}

// Writer sends the draft body to Cloudify. *pkg.CloudifyClient implements it.
type Writer interface {
	WriteJSON(ctx context.Context, method, endpoint string, body any) (json.RawMessage, error)
}

// Outcome is what happened when a confirmed draft was committed.
type Outcome struct {
	Status    string
	OrderCode string
	Message   string // Vietnamese text for the customer
}

// ErrNotClaimable means the draft expired, was already handled, or belongs to
// another user.
var ErrNotClaimable = errors.New("order draft is not pending")

// Commit claims the draft (pending → executing, exactly once) and writes it to
// Cloudify. A write Cloudify refused is "failed"; a write whose outcome is
// unknown (timeout, dropped connection) is "unknown" and is never retried
// automatically, because the order may already exist.
func Commit(ctx context.Context, id, zaloUserID string, writer Writer) (Outcome, error) {
	now := time.Now()
	claim := db.DB.WithContext(ctx).Model(&models.OrderAction{}).
		Where("id = ? AND zalo_user_id = ? AND status = ? AND expires_at > ?", id, zaloUserID, StatusPending, now).
		Updates(map[string]any{"status": StatusExecuting, "confirmed_at": now})
	if claim.Error != nil {
		return Outcome{}, fmt.Errorf("claim order draft: %w", claim.Error)
	}
	if claim.RowsAffected != 1 {
		return Outcome{Status: "", Message: "Đơn nháp này đã hết hạn hoặc đã được xử lý. Bạn gửi lại yêu cầu giúp mình nhé."}, ErrNotClaimable
	}
	var action models.OrderAction
	if err := db.DB.WithContext(ctx).First(&action, "id = ?", id).Error; err != nil {
		return Outcome{}, fmt.Errorf("load order draft: %w", err)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(action.Payload), &body); err != nil {
		finish(ctx, action.ID, StatusFailed, "", "", "invalid stored payload: "+err.Error())
		return Outcome{Status: StatusFailed, Message: failedText(action)}, nil
	}

	endpoint := createEndpoint
	if action.Kind != KindCreate {
		endpoint = fmt.Sprintf(updateEndpoint, action.OrderID)
	}
	// Detach from the turn's context: once the request is sent, the result
	// must be recorded even if the worker's own deadline fires.
	writeCtx := context.WithoutCancel(ctx)
	raw, err := writer.WriteJSON(writeCtx, "POST", endpoint, body)
	if err != nil {
		if errors.Is(err, pkg.ErrCloudifyRejected) {
			finish(writeCtx, action.ID, StatusFailed, "", "", err.Error())
			return Outcome{Status: StatusFailed, Message: failedText(action)}, nil
		}
		finish(writeCtx, action.ID, StatusUnknown, "", "", err.Error())
		return Outcome{Status: StatusUnknown, Message: unknownText(action)}, nil
	}

	code := action.OrderCode
	if action.Kind == KindCreate {
		if created, ok := CreatedOrderCode(raw); ok {
			code = created
		}
	}
	finish(writeCtx, action.ID, StatusDone, code, string(raw), "")
	return Outcome{Status: StatusDone, OrderCode: code, Message: doneText(action, code)}, nil
}

func finish(ctx context.Context, id, status, code, result, errText string) {
	updates := map[string]any{"status": status, "result": truncate(result, 60000), "error": truncate(errText, 1000)}
	if code != "" {
		updates["order_code"] = code
	}
	db.DB.WithContext(ctx).Model(&models.OrderAction{}).Where("id = ?", id).Updates(updates)
}

// CreatedOrderCode finds the order code Cloudify assigned in a create
// response. The response shape is not documented, so look in the common
// places: the top level, "data"/"result"/"record", or the first list item.
func CreatedOrderCode(raw []byte) (string, bool) {
	var decoded any
	if json.Unmarshal(raw, &decoded) != nil {
		return "", false
	}
	return findCode(decoded, 0)
}

func findCode(v any, depth int) (string, bool) {
	if depth > 3 {
		return "", false
	}
	switch t := v.(type) {
	case map[string]any:
		for _, key := range []string{"SO_DON_HANG", "MA_HOA_DON", "so_don_hang", "name", "code"} {
			if s, ok := t[key].(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s), true
			}
		}
		for _, key := range []string{"data", "result", "record", "records"} {
			if inner, ok := t[key]; ok {
				if code, ok := findCode(inner, depth+1); ok {
					return code, true
				}
			}
		}
	case []any:
		if len(t) > 0 {
			return findCode(t[0], depth+1)
		}
	}
	return "", false
}

func kindLabel(kind string) string {
	switch kind {
	case KindCreate:
		return "tạo đơn"
	case KindUpdate:
		return "sửa đơn"
	case KindCancel:
		return "huỷ đơn"
	}
	return kind
}

func doneText(action models.OrderAction, code string) string {
	switch action.Kind {
	case KindCreate:
		if code != "" {
			return fmt.Sprintf("✅ Đã tạo đơn %s cho %s.", code, customerLabel(action))
		}
		return fmt.Sprintf("✅ Đã tạo đơn cho %s.", customerLabel(action))
	case KindCancel:
		return fmt.Sprintf("✅ Đã huỷ đơn %s.", code)
	}
	return fmt.Sprintf("✅ Đã cập nhật đơn %s.", code)
}

func failedText(action models.OrderAction) string {
	return fmt.Sprintf("❌ Chưa %s được: hệ thống ERP từ chối yêu cầu. Nhân viên sẽ kiểm tra lại giúp bạn.", kindLabel(action.Kind))
}

func unknownText(action models.OrderAction) string {
	return fmt.Sprintf("⚠️ Hệ thống ERP phản hồi chậm nên chưa chắc đã %s xong. Bạn đừng gửi lại nhé, nhân viên sẽ kiểm tra và báo lại.", kindLabel(action.Kind))
}

func customerLabel(action models.OrderAction) string {
	if action.CustomerName != "" {
		return action.CustomerCode + " - " + action.CustomerName
	}
	return action.CustomerCode
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// FormatVND renders 1250000 as "1.250.000đ".
func FormatVND(amount float64) string {
	n := int64(amount + 0.5)
	if amount < 0 {
		n = int64(amount - 0.5)
	}
	negative := n < 0
	if negative {
		n = -n
	}
	digits := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	if negative {
		return "-" + b.String() + "đ"
	}
	return b.String() + "đ"
}
