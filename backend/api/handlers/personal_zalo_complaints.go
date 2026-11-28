package handlers

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/customercases"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/workers"
)

const personalZaloCaseRecorded = "Dạ, em đã ghi nhận vấn đề anh/chị báo ạ."

func personalZaloComplaintTurnID(req personalZaloChatbotRequest) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(strings.Join([]string{"personal-zalo-case",
		req.TenantID, req.ChannelID, req.AccountExternalID, req.ThreadType, req.ThreadID, req.SenderID, req.MessageID,
	}, "\x00"))).String()
}

func attachPersonalZaloComplaintGrant(p *engine.GroupPermissionContext, req personalZaloChatbotRequest) {
	p.Complaints = true
	p.ComplaintTurnID = personalZaloComplaintTurnID(req)
	if req.ThreadType == personalZaloThreadGroup {
		p.ZaloGroupID = req.ThreadID
		p.GroupGuest = true
	}
}

func personalZaloComplaintGuide(ctx context.Context, req personalZaloChatbotRequest, customerCode string) string {
	subjectID := req.SenderID
	if req.ThreadType == personalZaloThreadGroup {
		// Personal groups cannot establish CRM membership through the OA.
		// The reply is public, so never include the sender's existing cases.
		subjectID = ""
	}
	guide := customercases.BotContext(ctx, req.TenantID, req.ChannelID, "user", subjectID, customerCode, func(cases string) string {
		return engine.BuildTaskPrompt(req.TenantID, engine.PromptComplaintBot, map[string]string{"cases": cases})
	})
	guide += "\nKhiếu nại phải gọi complaint_record trước khi xác nhận đã ghi nhận. Nếu công cụ không chạy được, chỉ trả " + personalZaloAIHandoverMarker + "."
	if req.ThreadType == personalZaloThreadGroup {
		guide += "\nCả nhóm thấy phản hồi. Không nhắc lại thông tin riêng tư hay nội dung khiếu nại cũ của người gửi."
	}
	return guide
}

func (g *personalZaloDirectGrant) allowComplaints(ctx context.Context, key string, req personalZaloChatbotRequest, identity personalZaloERPIdentity) error {
	_, source := g.flow.FlowFor(identity.AgentType)
	if g.flowID == "" || key == "" || source != engine.LangflowSourceTenant || g.flow.APIKeySource != engine.LangflowSourceTenant {
		return nil
	}
	p := engine.GroupPermissionContext{
		TenantID: req.TenantID, ChannelID: req.ChannelID, ZaloUserID: req.SenderID,
		AgentType: identity.AgentType, PersonalZalo: true, CustomerCode: identity.Customer.CustomerCode,
	}
	if g.permission != "" {
		current, err := engine.VerifyPermissionToken(g.permission, key)
		if err != nil {
			return err
		}
		p = *current
	}
	attachPersonalZaloComplaintGrant(&p, req)
	permission, err := engine.SignPermissionToken(p, key)
	if err != nil {
		return err
	}
	g.permission = permission
	g.complaintGuide = personalZaloComplaintGuide(ctx, req, p.CustomerCode)
	return nil
}

func runPersonalZaloGuest(ctx context.Context, client *engine.LangflowClient, cfg *config.Config, flow engine.LangflowConfig, req personalZaloChatbotRequest, scope string, complaints, staffGroup bool) (engine.LangflowResult, error) {
	if !complaints {
		return engine.RunPersonalZaloGuestKnowledge(ctx, client, flow, cfg.EncryptionKey, req.TenantID, scope, req.Text, "", "")
	}
	p := engine.GroupPermissionContext{
		TenantID: req.TenantID, ChannelID: req.ChannelID, ZaloUserID: req.SenderID, AgentType: "public", PersonalZalo: true,
	}
	attachPersonalZaloComplaintGrant(&p, req)
	guide := personalZaloComplaintGuide(ctx, req, "")
	if staffGroup && req.ThreadType == personalZaloThreadGroup {
		p.GroupGuest = false
		guide += "\nNhân viên đang gọi bot trong nhóm khách. Chỉ gọi complaint_record khi tin mới yêu cầu ghi nhận một vấn đề cụ thể cho khách của nhóm. Hệ thống tự tìm khách được gắn với nhóm. Nếu công cụ báo từ chối, nói chưa ghi nhận và nhắc đúng lý do công cụ trả về. Khi ghi nhận thành công, chỉ xác nhận đã ghi nhận."
	}
	permission, err := engine.SignPermissionToken(p, cfg.EncryptionKey)
	if err != nil {
		return engine.LangflowResult{}, err
	}
	return engine.RunPersonalZaloGuestKnowledge(ctx, client, flow, cfg.EncryptionKey, req.TenantID, scope,
		req.Text, permission, guide)
}

func finishPersonalZaloComplaint(ctx context.Context, cfg *config.Config, channel models.Channel, req personalZaloChatbotRequest, reply string, forceHandover, staffGroup bool) (string, bool) {
	turnID := personalZaloComplaintTurnID(req)
	recorded, notified := customercases.TurnCaseState(ctx, req.TenantID, turnID)
	cleaned := customercases.StripRefs(personalZaloStripHandoverMarker(reply))
	if notified {
		if cleaned == "" {
			return personalZaloCaseRecorded, true
		}
		return cleaned, false
	}
	if !recorded && !forceHandover && !workers.PersonalZaloComplaintNeedsHandover(ctx, req.TenantID, req.Text) {
		return cleaned, false
	}
	if staffGroup && !recorded {
		return "Dạ, em chưa ghi nhận được khiếu nại thay khách. Anh/chị kiểm tra giúp mã khách đã gắn với nhóm trong phần Khiếu nại của kênh.", true
	}
	log.Printf("[cases] personal Zalo turn channel=%s recorded=%t; sending fallback handover", channel.ID, recorded)
	alertCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var observe engine.HandoverDeliveryObserver
	if recorded {
		observe = customercases.CaseFallbackObserver(alertCtx, req.TenantID, turnID)
	}
	handoverReply := sendPersonalZaloHandover(alertCtx, cfg, channel, req, observe)
	if recorded {
		if handoverReply != personalZaloHandoverUnavailable {
			return personalZaloCaseRecorded + " Em đã chuyển thông tin cho nhân viên phụ trách ạ.", true
		}
		return personalZaloCaseRecorded, true
	}
	return handoverReply, true
}
