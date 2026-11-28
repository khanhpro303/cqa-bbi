package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/redis/go-redis/v9"
	"github.com/vietbui/chat-quality-agent/customercases"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
)

const (
	personalZaloComplaintConfirmTTL = 10 * time.Minute
	personalZaloComplaintNeedCode   = "Dạ, em chưa ghi nhận khiếu nại. Anh/chị cho em xin mã khách cần ghi nhận ạ."
	personalZaloComplaintCacheError = "Dạ, em chưa kiểm tra được mã khách. Anh/chị thử lại sau giúp em ạ."
	personalZaloComplaintCodeMarker = engine.ComplaintCodeMarker
)

var (
	commonComplaintCustomerCode = regexp.MustCompile(`^(?:S|KH)[0-9]+$`)
	labelledComplaintCode       = regexp.MustCompile(`(?i)\b(?:mã\s+(?:khách(?:\s+hàng)?|kh)|khách(?:\s+hàng)?)\s*[:#]?\s*([a-z][a-z0-9_-]{0,39})\b`)
	nonCustomerComplaintCode    = regexp.MustCompile(`(?i)\b(?:sản\s+phẩm|mã\s+hàng|sku|đơn(?:\s+hàng)?)\s*[:#]?\s*([a-z][a-z0-9_-]{0,39})\b`)
	complaintCodeFormat         = regexp.MustCompile(`^[A-Z][A-Z0-9_-]*[0-9][A-Z0-9_-]*$`)
	complaintConfirmation       = regexp.MustCompile(`(?i)^(?:xác\s+nhận\s+)?(?:(?:mã\s+)?(?:khách(?:\s+hàng)?|kh)\s*[:#]?\s*)?([a-z][a-z0-9_-]*[0-9][a-z0-9_-]*)(?:\s+ạ)?[.!]?$`)
	explicitComplaintProblem    = regexp.MustCompile(`\b(?:khieu nai|phan anh|ghi nhan|bao thay|bao loi|bao van de|bi)\b`)
)

func personalZaloComplaintCodes(text string) (explicit, possible []string) {
	var excluded []string
	for _, match := range nonCustomerComplaintCode.FindAllStringSubmatch(text, -1) {
		excluded = append(excluded, strings.ToUpper(match[1]))
	}
	add := func(codes []string, code string) []string {
		if len(code) <= 40 && complaintCodeFormat.MatchString(code) && !slices.Contains(codes, code) {
			return append(codes, code)
		}
		return codes
	}
	for _, word := range strings.FieldsFunc(strings.ToUpper(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) && r != '_' && r != '-'
	}) {
		if slices.Contains(excluded, word) {
			continue
		}
		possible = add(possible, word)
		if commonComplaintCustomerCode.MatchString(word) {
			explicit = add(explicit, word)
		}
	}
	for _, match := range labelledComplaintCode.FindAllStringSubmatch(text, -1) {
		code := strings.ToUpper(match[1])
		explicit = add(explicit, code)
		possible = add(possible, code)
	}
	return explicit, possible
}

type complaintCachedCustomer struct {
	id       int64
	name     string
	conflict bool
}

// The AI Search customer cache can contain either a bare code or a Cloudify
// label. Require an exact leading code and refuse conflicting cache records.
func lookupPersonalZaloComplaintCustomers(ctx context.Context, tenantID string, codes []string) (map[string]complaintCachedCustomer, error) {
	matches := make(map[string]complaintCachedCustomer)
	if len(codes) == 0 {
		return matches, nil
	}
	if db.DB == nil {
		return nil, errors.New("customer cache unavailable")
	}
	query := db.DB.Where("ma IN ?", codes)
	for _, code := range codes {
		query = query.Or("ma LIKE ? ESCAPE '!'", strings.ReplaceAll(code, "_", "!_")+" - %")
	}
	var rows []models.CachedCustomer
	err := db.DB.WithContext(ctx).Select("ma", "ho_va_ten", "erp_id").Where("tenant_id = ?", tenantID).Where(query).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		code := strings.ToUpper(leadingCustomerCode(row.MA))
		if !slices.Contains(codes, code) {
			continue
		}
		match := complaintCachedCustomer{id: row.ERP_ID, name: strings.TrimSpace(row.HO_VA_TEN)}
		if prior, ok := matches[code]; ok {
			match.conflict = prior.conflict || prior.id != match.id || prior.name != match.name
		}
		matches[code] = match
	}
	return matches, nil
}

func complaintCustomerFromCache(matches map[string]complaintCachedCustomer, code string) (name, reply string) {
	match, ok := matches[code]
	if !ok {
		return "", "Dạ, em không tìm thấy mã khách hàng " + code + ". Anh/chị kiểm tra và nhắn lại một mã giúp em. Em chưa ghi nhận khiếu nại ạ."
	}
	if match.conflict {
		return "", "Dạ, mã khách hàng " + code + " khớp nhiều hồ sơ. Anh/chị kiểm tra lại trong AI Search giúp em. Em chưa ghi nhận khiếu nại ạ."
	}
	return match.name, ""
}

func personalZaloComplaintCustomer(ctx context.Context, tenantID, code string) (name, reply string) {
	matches, err := lookupPersonalZaloComplaintCustomers(ctx, tenantID, []string{code})
	if err != nil {
		return "", personalZaloComplaintCacheError
	}
	return complaintCustomerFromCache(matches, code)
}

type pendingPersonalZaloComplaint struct {
	Text    string   `json:"text"`
	TurnID  string   `json:"turn_id"`
	Choices []string `json:"choices,omitempty"`
}

type personalZaloComplaintTarget struct {
	code       string
	reply      string
	key        string
	raw        string
	receiptKey string
	recorded   bool
	pending    pendingPersonalZaloComplaint
}

func resolvePersonalZaloComplaintTarget(ctx context.Context, req personalZaloChatbotRequest, identityID, scope string) personalZaloComplaintTarget {
	target := personalZaloComplaintTarget{key: personalZaloSessionKey(req) + ":complaint:" + identityID + ":" + scope}
	if match := complaintConfirmation.FindStringSubmatch(req.Text); match != nil && len(match[1]) <= 40 {
		target.reply = "Dạ, anh/chị gửi mã khách cùng nội dung khiếu nại giúp em ạ."
		if db.RedisClient == nil {
			return target
		}
		target.receiptKey = target.key + ":recorded:" + personalZaloComplaintTurnID(req)
		state, err := db.RedisClient.MGet(ctx, target.key, target.receiptKey).Result()
		if err != nil {
			target.reply = personalZaloComplaintCacheError
			return target
		}
		code := strings.ToUpper(match[1])
		if state[1] == code {
			target.recorded = true
			return target
		}
		raw, _ := state[0].(string)
		var pending pendingPersonalZaloComplaint
		if raw != "" && json.Unmarshal([]byte(raw), &pending) == nil && pending.TurnID != "" && pending.Text != "" {
			target.raw = raw
			target.pending = pending
			if len(target.pending.Choices) > 0 && !slices.Contains(target.pending.Choices, code) {
				target.reply = personalZaloComplaintChooseCode(target.pending.Choices)
			} else if _, target.reply = personalZaloComplaintCustomer(ctx, req.TenantID, code); target.reply == "" {
				target.code = code
			}
		}
		return target
	}
	// The gateway processes a chat in order. A new message with its own
	// content supersedes the old question before another bare code can arrive.
	if db.RedisClient != nil {
		_ = db.RedisClient.Del(ctx, target.key).Err()
	}
	codes, possible := personalZaloComplaintCodes(req.Text)
	target.pending = pendingPersonalZaloComplaint{Text: req.Text, TurnID: personalZaloComplaintTurnID(req)}
	if len(possible) > 32 {
		target.reply = "Dạ, tin nhắn có nhiều mã. Anh/chị nhắn riêng một mã khách cần ghi nhận giúp em ạ."
		return target
	}
	matches, err := lookupPersonalZaloComplaintCustomers(ctx, req.TenantID, possible)
	if err != nil {
		target.reply = personalZaloComplaintCacheError
		return target
	}
	for _, code := range possible {
		if _, ok := matches[code]; ok && !slices.Contains(codes, code) {
			codes = append(codes, code)
		}
	}
	switch len(codes) {
	case 0:
		target.reply = personalZaloComplaintNeedCode
	case 1:
		if _, target.reply = complaintCustomerFromCache(matches, codes[0]); target.reply == "" {
			target.code = codes[0]
		}
	default:
		target.pending.Choices = codes
		target.reply = personalZaloComplaintChooseCode(codes)
	}
	return target
}

func personalZaloComplaintChooseCode(codes []string) string {
	shown := strings.Join(codes[:min(len(codes), 6)], ", ")
	if len(codes) > 6 {
		shown += ", …"
	}
	return "Dạ, em thấy nhiều mã khách (" + shown + "). Anh/chị xác nhận một mã cần ghi nhận bằng cách nhắn riêng mã đó giúp em. Em chưa ghi nhận khiếu nại ạ."
}

func personalZaloComplaintRefusalKey(turnID string) string {
	return "personal_zalo_complaint_refusal:" + turnID
}

func (target personalZaloComplaintTarget) ask(ctx context.Context) string {
	if target.raw != "" || target.pending.TurnID == "" {
		return target.reply
	}
	data, err := json.Marshal(target.pending)
	if err == nil && db.RedisClient != nil && db.RedisClient.Set(ctx, target.key, data, personalZaloComplaintConfirmTTL).Err() == nil {
		return target.reply
	}
	return target.reply + " Anh/chị gửi lại nội dung khiếu nại cùng một mã khách giúp em ạ."
}

// Keep a short receipt for gateway retries of the same confirmation turn.
var completePersonalZaloComplaintConfirmation = redis.NewScript(`
redis.call('SET', KEYS[2], ARGV[2], 'PX', ARGV[3])
if redis.call('GET', KEYS[1]) == ARGV[1] then
	return redis.call('DEL', KEYS[1])
end
return 0
`)

func applyPersonalZaloDirectComplaintSource(ctx context.Context, ch models.Channel, p *engine.GroupPermissionContext, r *customercases.Report) string {
	code := p.ComplaintCustomerCode
	if !complaintCodeFormat.MatchString(code) || len(code) > 40 {
		return personalZaloComplaintNeedCode
	}
	identity, err := resolvePersonalZaloERPIdentity(ctx, ch, personalZaloChatbotRequest{
		TenantID: ch.TenantID, ChannelID: ch.ID, AccountExternalID: ch.ExternalID,
		ThreadType: personalZaloThreadUser, ThreadID: p.ZaloUserID, SenderID: p.ZaloUserID,
	})
	const unidentified = "Dạ, em chưa xác định được tên nhân viên trên kênh này. Em chưa ghi nhận khiếu nại ạ."
	if err != nil || identity.AgentType != "private" || identity.ID != p.ComplaintStaffID {
		return unidentified
	}
	var reporter models.ZaloWhitelist
	if err := personalZaloStaffOptions(db.DB.WithContext(ctx), ch).Select("zalo_whitelist.name").
		Where("zalo_whitelist.id = ?", identity.ID).Take(&reporter).Error; err != nil || strings.TrimSpace(reporter.Name) == "" {
		return unidentified
	}
	name, reply := personalZaloComplaintCustomer(ctx, ch.TenantID, code)
	if reply != "" {
		return reply
	}
	r.CustomerCode, r.CustomerName, r.ReporterName = code, name, strings.TrimSpace(reporter.Name)
	r.MatchCustomerOnly = true
	return ""
}
