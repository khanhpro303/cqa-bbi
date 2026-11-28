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
	guide += "\n" + engine.TaskPromptText(req.TenantID, engine.PromptPZComplaintRules)
	if req.ThreadType == personalZaloThreadGroup {
		guide += "\n" + engine.TaskPromptText(req.TenantID, engine.PromptPZComplaintGroup)
	}
	return guide
}

func (g *personalZaloDirectGrant) allowComplaints(ctx context.Context, key string, req personalZaloChatbotRequest, identity personalZaloERPIdentity, target personalZaloComplaintTarget) error {
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
	if identity.AgentType == "private" && req.ThreadType == personalZaloThreadUser {
		p.ComplaintStaffID = identity.ID
		p.ComplaintCustomerCode = target.code
		if target.raw != "" {
			p.ComplaintTurnID = target.pending.TurnID
		}
	}
	permission, err := engine.SignPermissionToken(p, key)
	if err != nil {
		return err
	}
	g.permission = permission
	if p.ComplaintStaffID != "" {
		// A staff chat covers multiple customers. Omit the sender's case history.
		guideReq := req
		guideReq.SenderID = ""
		g.complaintGuide = personalZaloComplaintGuide(ctx, guideReq, "")
		if target.code == "" {
			g.complaintGuide += "\n" + engine.TaskPromptText(req.TenantID, engine.PromptPZStaffNoCode)
		} else {
			// target.code passed complaintCodeFormat, so it is a plain code.
			g.complaintGuide += "\n" + engine.BuildTaskPrompt(req.TenantID, engine.PromptPZStaffCode, map[string]string{"customer_code": target.code})
		}
	} else {
		g.complaintGuide = personalZaloComplaintGuide(ctx, req, p.CustomerCode)
	}
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
		guide += "\n" + engine.TaskPromptText(req.TenantID, engine.PromptPZStaffGroup)
	}
	permission, err := engine.SignPermissionToken(p, cfg.EncryptionKey)
	if err != nil {
		return engine.LangflowResult{}, err
	}
	return engine.RunPersonalZaloGuestKnowledge(ctx, client, flow, cfg.EncryptionKey, req.TenantID, scope,
		req.Text, permission, guide)
}

func finishPersonalZaloComplaint(ctx context.Context, cfg *config.Config, channel models.Channel, req personalZaloChatbotRequest, turnID, reply string, forceHandover, staffGroup bool) (string, bool) {
	if turnID == "" {
		turnID = personalZaloComplaintTurnID(req)
	}
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
		return "Dạ, em chưa ghi nhận được khiếu nại thay khách. Anh/chị kiểm tra giúp liên kết và mã khách của nhóm trong CRM → Nhóm Zalo.", true
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
