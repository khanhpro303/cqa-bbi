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
	return RunGuestKnowledgeWithScope(ctx, client, flow, key, tenantID, scope, text, "", "", GuestKnowledgeRules{Knowledge: true, ProductAdvice: true})
}

// GuestKnowledgeRules selects the guest prompts the answering agent can use.
type GuestKnowledgeRules struct {
	Knowledge     bool
	ProductAdvice bool
}

// RunGuestKnowledgeWithScope adds a specialist's signed scope and prompt to a public guest turn.
func RunGuestKnowledgeWithScope(ctx context.Context, client *LangflowClient, flow LangflowConfig, key, tenantID, scope, text, permission, prompt string, rules GuestKnowledgeRules) (LangflowResult, error) {
	return runGuestKnowledge(ctx, client, flow, key, tenantID, scope, text, permission, "", PromptKnowledgeImages, prompt, false, rules)
}

// RunPersonalZaloGuestKnowledge uses the public knowledge grant and the
// Personal Zalo attachment policy. The optional complaint permission is
// scoped to the current turn.
func RunPersonalZaloGuestKnowledge(ctx context.Context, client *LangflowClient, flow LangflowConfig, key, tenantID, scope, text, permission, guide string, complaints bool, rules GuestKnowledgeRules) (LangflowResult, error) {
	return runGuestKnowledge(ctx, client, flow, key, tenantID, scope, text, permission, guide, PromptPZKnowledgeImages, "", complaints, rules)
}

// RunPersonalZaloGuestDraft uses the guest knowledge grant with the tenant's
// public writing instructions. It cannot enable tools that change records.
func RunPersonalZaloGuestDraft(ctx context.Context, client *LangflowClient, flow LangflowConfig, key, tenantID, scope, text, permission, publicPrompt string, rules GuestKnowledgeRules) (LangflowResult, error) {
	return runGuestKnowledge(ctx, client, flow, key, tenantID, scope, text, permission, "", PromptPZKnowledgeImages, publicPrompt, false, rules)
}

func guestKnowledgePrompt(tenantID string, rules GuestKnowledgeRules) string {
	if !rules.Knowledge {
		// Only a complaint turn can reach Langflow without document access.
		return "Bạn là trợ lý ghi nhận khiếu nại. Trả lời bằng tiếng Việt. Chỉ dùng công cụ complaint_record theo hướng dẫn dưới đây. " +
			"Nếu khách chỉ muốn tra giá, tồn kho, đơn hàng hay công nợ, chỉ trả " + GuestVerificationMarker + " trên một dòng. " +
			"Nếu khách cần tư vấn ngoài phạm vi khiếu nại, chỉ trả câu sau: " + GuestKnowledgeUnavailableReply
	}
	prompt := TaskPromptText(tenantID, PromptGuestKnowledge)
	if rules.ProductAdvice {
		prompt += "\n\n" + TaskPromptText(tenantID, PromptProductAdvice)
	}
	return prompt
}

func runGuestKnowledge(ctx context.Context, client *LangflowClient, flow LangflowConfig, key, tenantID, scope, text, permission, guide, imagePromptKey, publicPrompt string, complaints bool, rules GuestKnowledgeRules) (LangflowResult, error) {
	if !complaints && guestKnowledgeRequiresVerification(text) {
		return LangflowResult{Text: GuestKnowledgeVerificationReply}, nil
	}
	if !rules.Knowledge && !complaints {
		return LangflowResult{Text: GuestKnowledgeUnavailableReply}, nil
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
	prompt := guestKnowledgePrompt(tenantID, rules)
	if publicPrompt = strings.TrimSpace(publicPrompt); publicPrompt != "" {
		if imagePromptKey == PromptPZKnowledgeImages {
			prompt += "\n\n" + TaskPromptText(tenantID, PromptPZGuestChannelLead) + "\n" + publicPrompt
		} else {
			prompt += "\n\n" + publicPrompt
		}
	}
	if complaints {
		prompt += "\n\n" + TaskPromptText(tenantID, PromptPZGuestComplaint) + "\n\n" + guide
	}
	input := text
	if rules.Knowledge {
		input, prompt = addKnowledgeImageInstruction(input, prompt, TaskPromptText(tenantID, imagePromptKey))
	}
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
