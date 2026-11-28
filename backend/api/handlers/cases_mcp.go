package handlers

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/customercases"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
)

// guestNote is the tool's answer for someone reporting in a group they are not
// a member of: the reply is read by the whole group.
const guestNote = "Đã ghi nhận. Tin trả lời sẽ hiện cho cả nhóm: chỉ cảm ơn và nói em đã ghi nhận, không nhắc lại chi tiết hay các vấn đề trước đây của người này, không hứa nhân viên liên hệ ngay."

// complaintsOffMessage is the tool's answer when complaint handling is off.
const complaintsOffMessage = "Kênh này chưa bật ghi nhận khiếu nại. Hãy nói với khách em sẽ chuyển thông tin cho nhân viên phụ trách."

const (
	complaintMCPToolName = "complaint_record"
	resourceComplaints   = "complaints"
	// complaintImageWindow: images the person sent this recently, and after
	// the chat's last recorded report, are attached to the case.
	complaintImageWindow = 2 * time.Hour
)

// complaintMCPTool records a problem the person in the chat reports. The
// backend compares it with their earlier cases (decision model, task prompt
// complaint_case_match) and either opens a case or adds to the old one, and
// tells staff. How the bot talks about it is in the editable task prompt
// complaint_bot_guide.
func complaintMCPTool() erpMCPTool {
	str := func(desc string) gin.H { return gin.H{"type": "string", "description": desc} }
	return erpMCPTool{
		Name:        complaintMCPToolName,
		Description: "Ghi nhận vấn đề hoặc khiếu nại người đang chat báo (kể cả khi họ hỏi lại vấn đề cũ) để nhân viên xử lý. Hệ thống tự quyết định đây là vấn đề cũ hay mới và báo nhân viên. Kết quả: action là created (vấn đề mới), followup (vấn đề cũ đang xử lý) hoặc reopened (vấn đề đã xử lý nhưng vẫn còn). Không bao giờ đưa mã hay số nào của hồ sơ cho khách.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type":          gin.H{"type": "string", "enum": customercases.Types, "description": "complaint (phàn nàn dịch vụ/thái độ), delivery (giao sai/thiếu/chậm), product_defect (hàng lỗi/hỏng), price, payment (tiền, công nợ, chứng từ), staff_request (cần nhân viên), other."},
				"severity":      gin.H{"type": "string", "enum": []string{"low", "medium", "high"}, "description": "high khi khách bức xúc, sai tiền, có nguy cơ mất đơn hoặc cần xử lý trong ngày."},
				"title":         str("Một câu ngắn nêu vấn đề."),
				"detail":        str("Chi tiết: đơn nào, sản phẩm nào, chuyện gì xảy ra, khách muốn gì, và những gì đọc được trong ảnh khách gửi."),
				"evidence":      str("Nguyên văn lời khách."),
				"customer_code": str("Chỉ dùng khi nhân viên báo vấn đề thay cho một khách: mã khách hàng đó."),
			},
			"required":             []string{"title", "detail"},
			"additionalProperties": false,
		},
		resource: resourceComplaints,
	}
}

type complaintArgs struct {
	Type         string `json:"type"`
	Severity     string `json:"severity"`
	Title        string `json:"title"`
	Detail       string `json:"detail"`
	Evidence     string `json:"evidence"`
	CustomerCode string `json:"customer_code"`
}

// recordComplaint is customercases.Record; replaced in tests.
var recordComplaint = customercases.Record

func handleComplaintTool(ctx context.Context, tenantID, agentType, permissionToken string, rawArgs map[string]json.RawMessage) gin.H {
	result := func(body any, failed bool) gin.H {
		data, _ := json.Marshal(body)
		return erpMCPResult(data, failed)
	}
	if permissionToken == "" {
		return result(gin.H{"error": "forbidden_token"}, true)
	}
	cfg, _ := config.Load()
	permissions, err := engine.VerifyPermissionToken(permissionToken, cfg.EncryptionKey)
	if err != nil || permissions.TenantID != tenantID || permissions.AgentType != agentType {
		return result(gin.H{"error": "forbidden_token"}, true)
	}
	var args complaintArgs
	if data, err := json.Marshal(rawArgs); err != nil || json.Unmarshal(data, &args) != nil {
		return result(gin.H{"error": "invalid_arguments", "message": "Tham số không hợp lệ."}, true)
	}
	report, refusal := complaintReport(ctx, permissions, args)
	if refusal != "" {
		if permissions.PersonalZalo && permissions.ZaloGroupID == "" && permissions.AgentType == "private" &&
			permissions.Complaints && permissions.ComplaintTurnID != "" && db.RedisClient != nil {
			_ = db.RedisClient.Set(ctx, personalZaloComplaintRefusalKey(permissions.ComplaintTurnID), refusal, time.Minute).Err()
		}
		return result(gin.H{"status": "error", "message": refusal}, true)
	}
	out, err := recordComplaint(ctx, report)
	if err != nil {
		log.Printf("[cases] complaint_record failed for %s/%s: %v", report.SubjectType, report.SubjectID, err)
		auditComplaint(tenantID, permissions, 500, 0)
		return result(gin.H{"error": "complaint_unavailable", "message": "Chưa ghi nhận được lúc này. Hãy xin lỗi khách và nói em sẽ chuyển ngay cho nhân viên."}, true)
	}
	auditComplaint(tenantID, permissions, 200, 1)
	log.Printf("[cases] %s case %s for %s/%s (staff_notified=%t, %s)", out.Action, out.Case.ID, report.SubjectType, report.SubjectID, out.StaffNotified, out.Reason)
	body := gin.H{"status": "success", "action": out.Action, "staff_notified": out.StaffNotified}
	groupReply := permissions.GroupGuest || (permissions.PersonalZalo && permissions.ZaloGroupID != "")
	if groupReply {
		// The reply goes to the whole group: nothing about the case
		// (whether it is new or old, title, status, staff updates) is
		// returned, only that it was recorded.
		body["action"] = "recorded"
		body["note"] = guestNote
	} else {
		body["case"] = customercases.ViewForBot(out.Case)
	}
	if !out.StaffNotified && !groupReply {
		body["note"] = "Đã lưu vấn đề nhưng chưa báo trực tiếp được nhân viên. Chỉ nói với khách là em đã ghi nhận; không hứa nhân viên sẽ liên hệ ngay."
	}
	return result(body, false)
}

// complaintReport builds the report for the chat the token pins. It returns
// a refusal message when the report cannot be recorded.
func complaintReport(ctx context.Context, p *engine.GroupPermissionContext, args complaintArgs) (customercases.Report, string) {
	if p.ChannelID == "" || p.ZaloUserID == "" {
		return customercases.Report{}, "Không xác định được cuộc trò chuyện để ghi nhận."
	}
	// The worker signs Complaints only when the channel switch is on and the
	// complaint guide rides on the system prompt; a flow calling the tool
	// outside that cannot write cases.
	if !p.Complaints {
		return customercases.Report{}, complaintsOffMessage
	}
	if strings.TrimSpace(args.Title) == "" && strings.TrimSpace(args.Detail) == "" {
		return customercases.Report{}, "Cần title hoặc detail mô tả vấn đề."
	}
	r := customercases.Report{
		TenantID: p.TenantID, ChannelID: p.ChannelID, ZaloUserID: p.ZaloUserID, ZaloGroupID: p.ZaloGroupID,
		SubjectType: "user", SubjectID: p.ZaloUserID, CustomerCode: p.CustomerCode,
		Source: customercases.SourceBot, TurnID: p.TurnID, SubjectOnly: p.GroupGuest, Retry: p.Retry, Type: args.Type, Severity: args.Severity,
		Title: args.Title, Detail: args.Detail, Evidence: args.Evidence,
	}
	personalGroupStaff := p.PersonalZalo && p.ZaloGroupID != "" && !p.GroupGuest
	personalDirectStaff := p.PersonalZalo && p.ZaloGroupID == "" && p.AgentType == "private"
	if p.PersonalZalo {
		if p.ComplaintTurnID == "" {
			return customercases.Report{}, "Không xác định được tin nhắn để ghi nhận."
		}
		r.TurnID, r.Retry = p.ComplaintTurnID, true
	}
	if personalDirectStaff && (p.ComplaintCustomerCode == "" || p.ComplaintStaffID == "" || db.DB == nil) {
		return customercases.Report{}, personalZaloComplaintNeedCode
	}
	// A group's cases belong to the group, except for a sender who is not a
	// CRM member of it (GroupGuest): theirs stay their own, matched only with
	// their own chat and customer code.
	if p.ZaloGroupID != "" && !p.GroupGuest {
		r.SubjectType, r.SubjectID = "group", p.ZaloGroupID
	}
	if db.DB != nil {
		var ch models.Channel
		if err := db.DB.WithContext(ctx).Select("id, tenant_id, external_id, metadata, channel_type, is_active, personal_zalo_auto_reply_enabled").Where("id = ? AND tenant_id = ?", p.ChannelID, p.TenantID).First(&ch).Error; err != nil || !customercases.EnabledFor(ch.Metadata) {
			return customercases.Report{}, complaintsOffMessage
		}
		if p.PersonalZalo {
			meta, _ := decodeMetadataObject(ch.Metadata)
			scope, _ := meta["sync_scope"].(string)
			groups, _ := meta["personal_zalo_group_reply_enabled"].(bool)
			if ch.ChannelType != "personal_zalo_import" || !ch.IsActive || !ch.PersonalZaloAutoReplyEnabled ||
				(p.ZaloGroupID == "" && scope == "group") || (p.ZaloGroupID != "" && (!groups || scope == "direct")) {
				return customercases.Report{}, complaintsOffMessage
			}
			settings, err := loadPersonalZaloChatbotSettings(ctx, p.TenantID, "chatbot_active")
			if err != nil || !personalZaloChatbotMasterEnabled(settings) {
				return customercases.Report{}, complaintsOffMessage
			}
		}
		if personalGroupStaff {
			if refusal := applyPersonalZaloStaffComplaintSource(ctx, ch, &r); refusal != "" {
				return customercases.Report{}, refusal
			}
		} else if personalDirectStaff {
			if refusal := applyPersonalZaloDirectComplaintSource(ctx, ch, p, &r); refusal != "" {
				return customercases.Report{}, refusal
			}
		} else {
			r.ReporterName = zaloPersonName(p.TenantID, p.ZaloUserID)
			r.CustomerName = r.ReporterName
		}
		if p.ZaloGroupID != "" && !personalGroupStaff {
			var g models.CRMGroup
			if err := db.DB.Select("name, customer_code").Where("tenant_id = ? AND zalo_group_id = ?", p.TenantID, p.ZaloGroupID).First(&g).Error; err == nil {
				r.ChatGroup = g.Name
				if !p.GroupGuest {
					r.CustomerName = g.Name
					if r.CustomerCode == "" {
						r.CustomerCode = g.CustomerCode
					}
				}
			}
		}
	}
	if code := strings.TrimSpace(args.CustomerCode); code != "" && p.AgentType == "private" && !personalDirectStaff {
		// Staff reporting for a customer: the case belongs to that customer.
		name, ok := cachedCustomer(p.TenantID, code)
		if !ok {
			return customercases.Report{}, "Không tìm thấy mã khách hàng " + code + "."
		}
		r.CustomerCode, r.CustomerName = code, name
	}
	if !p.PersonalZalo {
		r.SessionID = engine.ResolveActiveSessionID(ctx, p.ChannelID, p.ZaloUserID, p.ZaloGroupID)
	}

	since := time.Now().Add(-complaintImageWindow)
	if last := customercases.LastBotReportAt(ctx, r.TenantID, r.SubjectType, r.SubjectID, p.ZaloUserID); last.After(since) {
		since = last
	}
	r.Images = customercases.RecentImages(ctx, r.TenantID, r.ChannelID, conversationExtID(r), p.ZaloUserID, since)
	return r, ""
}

func conversationExtID(r customercases.Report) string {
	if r.ZaloGroupID != "" {
		return r.ZaloGroupID
	}
	return r.ZaloUserID
}

// zaloPersonName is the CRM name of a Zalo user: an approved customer or a
// whitelisted staff member.
func zaloPersonName(tenantID, zaloUserID string) string {
	var c models.ZaloCustomer
	if err := db.DB.Select("name").Where("tenant_id = ? AND zalo_user_id = ?", tenantID, zaloUserID).First(&c).Error; err == nil && c.Name != "" {
		return c.Name
	}
	var w models.ZaloWhitelist
	if err := db.DB.Select("name").Where("tenant_id = ? AND zalo_user_id = ?", tenantID, zaloUserID).First(&w).Error; err == nil {
		return w.Name
	}
	return ""
}

func auditComplaint(tenantID string, p *engine.GroupPermissionContext, status, count int) {
	if db.DB != nil {
		writeAuditLog(tenantID, p, resourceComplaints, "", nil, complaintMCPToolName, status, count, "")
	}
}
