package engine

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
)

const (
	GuestKnowledgeVerificationReply = "Dạ, để tra giá, tồn kho, đơn hàng hoặc công nợ, anh/chị cần xác thực tài khoản Zalo. Anh/chị vui lòng liên hệ nhân viên để được cấp mã xác thực ạ."
	GuestKnowledgeUnavailableReply  = "Dạ, hiện em chưa tra được tài liệu tư vấn. Anh/chị vui lòng thử lại sau hoặc liên hệ nhân viên hỗ trợ ạ."
)

var ErrGuestKnowledgeNotConfigured = errors.New("public knowledge flow is not configured")

// Match common direct ERP questions before Langflow runs. Indirect questions and
// follow-ups are handled by the guest prompt, with the same knowledge-only grant.
var (
	guestERPLookup = regexp.MustCompile(`\b(?:` +
		`(?:tra|xem|hoi|xin) gia|gia bao nhieu|bao nhieu tien|gia$` +
		`|ton bao nhieu|so luong ton|con (?:hang|size|mau|san hang)` +
		`|(?:tra|xem|kiem tra) don|don (?:hang )?(?:cua|hom nay|den dau|toi dau|sap giao)|don nao sap giao` +
		`|(?:tra|xem|kiem tra) cong no|cong no cua|no bao nhieu)\b`)
	guestERPTopic = regexp.MustCompile(`\b(?:bao gia|don gia|gia (?:ban|si|le)|ton kho|het hang|trang thai don|cong no)\b`)
	guestPolicy   = regexp.MustCompile(`\b(?:chinh sach|quy trinh|quy dinh|nghia la gi)\b`)
)

func guestKnowledgeRequiresVerification(text string) bool {
	normalized := strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		if r == 'đ' {
			return 'd'
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, norm.NFD.String(strings.ToLower(text)))
	text = strings.Join(strings.Fields(normalized), " ")
	return guestERPLookup.MatchString(text) || (guestERPTopic.MatchString(text) && !guestPolicy.MatchString(text))
}

// RunGuestKnowledge calls the tenant's public flow with an MCP credential that
// exposes only public documents. It supplies no ERP grant or customer identity.
func RunGuestKnowledge(ctx context.Context, client *LangflowClient, flow LangflowConfig, key, tenantID, scope, text string) (LangflowResult, error) {
	return runGuestKnowledge(ctx, client, flow, key, tenantID, scope, text, "", "", PromptKnowledgeImages, "")
}

// RunPersonalZaloGuestKnowledge uses the public knowledge grant and the
// Personal Zalo attachment policy. The optional complaint permission is
// scoped to the current turn.
func RunPersonalZaloGuestKnowledge(ctx context.Context, client *LangflowClient, flow LangflowConfig, key, tenantID, scope, text, permission, guide string) (LangflowResult, error) {
	return runGuestKnowledge(ctx, client, flow, key, tenantID, scope, text, permission, guide, PromptPZKnowledgeImages, "")
}

// RunPersonalZaloGuestDraft uses the guest knowledge grant with the tenant's
// public writing instructions. It cannot enable tools that change records.
func RunPersonalZaloGuestDraft(ctx context.Context, client *LangflowClient, flow LangflowConfig, key, tenantID, scope, text, publicPrompt string) (LangflowResult, error) {
	return runGuestKnowledge(ctx, client, flow, key, tenantID, scope, text, "", "", PromptPZKnowledgeImages, publicPrompt)
}

// guestKnowledgePrompt is the unverified-guest system prompt: the tenant's
// guest rules followed by the product advice rules (both editable).
func guestKnowledgePrompt(tenantID string) string {
	return TaskPromptText(tenantID, PromptGuestKnowledge) + "\n\n" + TaskPromptText(tenantID, PromptProductAdvice)
}

func runGuestKnowledge(ctx context.Context, client *LangflowClient, flow LangflowConfig, key, tenantID, scope, text, permission, guide, imagePromptKey, publicPrompt string) (LangflowResult, error) {
	if permission == "" && guestKnowledgeRequiresVerification(text) {
		return LangflowResult{Text: GuestKnowledgeVerificationReply}, nil
	}
	if flow.APIURL == "" || flow.APIKey == "" || flow.PublicFlowID == "" || sameGuestFlowID(flow.FlowID, flow.PublicFlowID) ||
		flow.PublicFlowIDSource != LangflowSourceTenant || flow.APIKeySource != LangflowSourceTenant || key == "" || tenantID == "" || scope == "" {
		return LangflowResult{}, ErrGuestKnowledgeNotConfigured
	}
	token, err := SignKnowledgePreviewToken(key, tenantID, "public")
	if err != nil {
		return LangflowResult{}, err
	}
	session := fmt.Sprintf("zalo-guest-%x", sha256.Sum256([]byte(tenantID+"\x00"+scope)))
	prompt := guestKnowledgePrompt(tenantID)
	if publicPrompt = strings.TrimSpace(publicPrompt); publicPrompt != "" {
		prompt += "\n\n" + TaskPromptText(tenantID, PromptPZGuestChannelLead) + "\n" + publicPrompt
	}
	if permission != "" {
		prompt += "\n\n" + TaskPromptText(tenantID, PromptPZGuestComplaint) + "\n\n" + guide
	}
	input, prompt := addKnowledgeImageInstruction(text, prompt, TaskPromptText(tenantID, imagePromptKey))
	result, err := client.RunFlowWithCustomerUsage(ctx, session, session, input,
		flow.APIURL, flow.APIKey, flow.PublicFlowID, "", permission, token, prompt)
	if err != nil {
		return LangflowResult{}, err
	}
	switch {
	case strings.Contains(result.Text, GuestVerificationMarker):
		result.Text = GuestKnowledgeVerificationReply
	case strings.TrimSpace(result.Text) == "", strings.Contains(result.Text, "[RICH_MESSAGE_SENT]"):
		result.Text = GuestKnowledgeUnavailableReply
	}
	return result, nil
}

func sameGuestFlowID(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if strings.EqualFold(a, b) {
		return true
	}
	aID, aErr := uuid.Parse(a)
	bID, bErr := uuid.Parse(b)
	return aErr == nil && bErr == nil && aID == bID
}
