package workers

import (
	"testing"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestOrderNoticeForReply(t *testing.T) {
	notices := []models.CloudifyOrderConfirmation{
		{ID: "newer", OrderCode: "ĐH0000160", OutboundMessageID: "out-2"},
		{ID: "older", OrderCode: "ĐH000016", OutboundMessageID: "out-1"},
	}
	for _, tc := range []struct {
		text, quote, id string
		history         bool
	}{
		{"ok", "out-1", "older", false},
		{"ok ĐH000016", "someone-else", "", false},
		{"duyệt đơn ĐH0000160", "", "newer", false},
		{"duyệt đơn ĐH000016.", "", "older", false},
		{"chốt ĐH000016 và ĐH0000160", "", "", false},
		{"ok", "", "newer", true},
	} {
		got, history := orderNoticeForReply(notices, tc.text, tc.quote)
		id := ""
		if got != nil {
			id = got.ID
		}
		if id != tc.id || history != tc.history {
			t.Errorf("reply %q quoting %q got %q and history=%v, want %q and history=%v", tc.text, tc.quote, id, history, tc.id, tc.history)
		}
	}
}

func TestOrderConfirmationRequiresSignedMatchingGMFReply(t *testing.T) {
	channel := &models.Channel{ID: "channel", TenantID: "tenant", ExternalID: "oa"}
	group := &models.CRMGroup{ID: "crm", TenantID: "tenant", ChannelID: "channel", ZaloGroupID: "group"}
	creds := channels.ZaloOACredentials{AppID: "app", OAId: "oa"}
	p := ZaloWebhookPayload{EventName: "user_send_group_text", OAID: "oa", AppID: "app", SignatureVerified: true}
	p.Message.MsgID = "reply"
	p.Sender.ID = "customer"
	p.Recipient.ID = "group"
	if !eligibleOrderReply(p, channel, group, creds) {
		t.Fatal("signed reply was rejected")
	}
	for _, change := range []func(*ZaloWebhookPayload){
		func(p *ZaloWebhookPayload) { p.SignatureVerified = false },
		func(p *ZaloWebhookPayload) { p.OAID = "other-oa" },
		func(p *ZaloWebhookPayload) { p.AppID = "other-app" },
		func(p *ZaloWebhookPayload) { p.Sender.ID = "oa" },
		func(p *ZaloWebhookPayload) { p.Recipient.ID = "other-group" },
		func(p *ZaloWebhookPayload) { p.EventName = "user_send_text" },
	} {
		copy := p
		change(&copy)
		if eligibleOrderReply(copy, channel, group, creds) {
			t.Errorf("ineligible reply accepted: %+v", copy)
		}
	}
}

func TestDirectOrderApprovalRequiresClearAffirmative(t *testing.T) {
	for _, msg := range []string{"OK", "Duyệt ĐH000016.", "Đồng ý!", "ok ạ"} {
		if !isDirectOrderApproval(msg, "ĐH000016") {
			t.Errorf("did not recognize %q", msg)
		}
	}
	for _, msg := range []string{"ok chưa?", "không duyệt", "ok nếu đổi số lượng", "ok ĐH000017", "cảm ơn", "ok, đã nhận tin"} {
		if isDirectOrderApproval(msg, "ĐH000016") {
			t.Errorf("wrongly recognized %q", msg)
		}
	}
}
