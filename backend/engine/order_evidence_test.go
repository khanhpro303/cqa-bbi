package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
)

func orderEvidenceMessage(id, sender, content string, second int64) models.Message {
	return models.Message{ID: id, SenderType: sender, Content: content, SentAt: time.Unix(second, 0)}
}

func TestValidOrderConfirmation(t *testing.T) {
	phone := orderEvidenceMessage("phone", "customer", "SĐT của mình 0912 345 678", 1)
	address := orderEvidenceMessage("address", "customer", "Địa chỉ: 12 Nguyễn Trãi, phường 3", 2)
	tests := []struct {
		name     string
		message  models.Message
		messages []models.Message
		want     bool
	}{
		{name: "staff tracking code", message: orderEvidenceMessage("tracking", "agent", "Đơn đã gửi, mã vận đơn GHNABC123", 3), want: true},
		{name: "staff tracking no accents", message: orderEvidenceMessage("tracking", "agent", "Don gui roi nha, MVD: ghn-123456", 3), want: true},
		{name: "staff tracking natural filler", message: orderEvidenceMessage("tracking", "agent", "Mã bill của chị là SPXVN987654321", 3), want: true},
		{name: "staff tracking punctuation", message: orderEvidenceMessage("tracking", "agent", "Tracking no. = JT_12345678.", 3), want: true},
		{name: "staff numeric tracking", message: orderEvidenceMessage("tracking", "agent", "Mã vận đơn: 123456789", 3), want: true},
		{name: "price disguised as bill", message: orderEvidenceMessage("tracking", "agent", "Bill 450000", 3), want: false},
		{name: "staff created after phone and address", message: orderEvidenceMessage("created", "agent", "Shop đã tạo đơn cho mình rồi ạ", 3), messages: []models.Message{phone, address}, want: true},
		{name: "staff created colloquial", message: orderEvidenceMessage("created", "agent", "Đơn của mình lên xong rồi chị nhé", 3), messages: []models.Message{phone, address}, want: true},
		{name: "staff created with unlabeled street address", message: orderEvidenceMessage("created", "agent", "Tạo đơn thành công rồi ạ", 3), messages: []models.Message{phone, orderEvidenceMessage("address", "customer", "12 Nguyen Trai Q1", 2)}, want: true},
		{name: "staff created without address", message: orderEvidenceMessage("created", "agent", "Shop đã tạo đơn rồi ạ", 3), messages: []models.Message{phone}, want: false},
		{name: "staff created with pending address", message: orderEvidenceMessage("created", "agent", "Shop đã tạo đơn rồi ạ", 3), messages: []models.Message{phone, orderEvidenceMessage("address", "customer", "Địa chỉ chưa chốt, mình gửi sau", 2)}, want: false},
		{name: "customer bought in store", message: orderEvidenceMessage("store", "customer", "Mình đã đến cửa hàng và đã mua sản phẩm rồi", 3), want: true},
		{name: "customer picked up colloquial", message: orderEvidenceMessage("store", "customer", "Em vừa ghé shop lấy hàng xong nha", 3), want: true},
		{name: "customer bought direct no accents", message: orderEvidenceMessage("store", "customer", "minh mua truc tiep tai shop roi", 3), want: true},
		{name: "customer picked up with store between verb and completion", message: orderEvidenceMessage("store", "customer", "Em lấy hàng ở shop rồi ạ", 3), want: true},
		{name: "customer merely plans store visit", message: orderEvidenceMessage("plan", "customer", "Mai mình đến cửa hàng mua nhé", 3), want: false},
		{name: "customer only visited", message: orderEvidenceMessage("visit", "customer", "Mình vừa ghé shop xem thử", 3), want: false},
		{name: "bare acknowledgement", message: orderEvidenceMessage("ok", "customer", "Dạ ok", 3), want: false},
		{name: "qr", message: orderEvidenceMessage("qr", "agent", "Shop gửi mã QR thanh toán", 3), want: false},
		{name: "price quote", message: orderEvidenceMessage("price", "agent", "Báo giá sản phẩm 450.000đ", 3), want: false},
		{name: "shipping request", message: orderEvidenceMessage("request", "agent", "Cho xin số điện thoại và địa chỉ để lên đơn", 3), want: false},
		{name: "promise", message: orderEvidenceMessage("promise", "agent", "Shop sẽ lên đơn cho mình", 3), want: false},
		{name: "system event", message: orderEvidenceMessage("system", "system", "Đã tạo đơn", 3), want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := validOrderConfirmation(tc.message, append(tc.messages, tc.message)); got != tc.want {
				t.Fatalf("validOrderConfirmation() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidOrderTransition(t *testing.T) {
	for _, tc := range []struct {
		status, sender, content string
		want                    bool
	}{
		{status: "cancelled", sender: "customer", content: "Mình hủy đơn này nhé", want: true},
		{status: "cancelled", sender: "agent", content: "Đơn đã hủy thành công", want: true},
		{status: "cancelled", sender: "agent", content: "Khách không lấy nữa, shop huỷ đơn rồi", want: true},
		{status: "cancelled", sender: "customer", content: "huy don giup minh nha", want: true},
		{status: "cancelled", sender: "customer", content: "Đơn đã hủy chưa?", want: false},
		{status: "cancelled", sender: "agent", content: "Đơn không huỷ, vẫn giao", want: false},
		{status: "refunded", sender: "agent", content: "Shop đã hoàn tiền cho đơn", want: true},
		{status: "refunded", sender: "customer", content: "Mình đã nhận lại tiền", want: true},
		{status: "refunded", sender: "agent", content: "Refund thành công rồi chị nhé", want: true},
		{status: "refunded", sender: "customer", content: "tien ve tai khoan roi nha", want: true},
		{status: "refunded", sender: "agent", content: "Shop sẽ hoàn tiền", want: false},
		{status: "review", sender: "customer", content: "Đơn đã hủy", want: false},
	} {
		t.Run(tc.status+tc.content, func(t *testing.T) {
			message := orderEvidenceMessage("transition", tc.sender, tc.content, 1)
			if got := validOrderTransition(tc.status, message); got != tc.want {
				t.Fatalf("validOrderTransition() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidOrderAmount(t *testing.T) {
	confirmation := orderEvidenceMessage("confirm", "customer", "Mình đã đến cửa hàng và đã mua sản phẩm", 10)
	acceptedQuote := orderEvidenceMessage("accepted", "customer", "Mình chốt đơn theo mức giá này", 10)
	for _, tc := range []struct {
		name         string
		amount       models.Message
		confirmation models.Message
		value        int64
		want         bool
	}{
		{name: "total after confirmation", amount: orderEvidenceMessage("amount", "agent", "Tổng đơn là 450.000đ", 11), confirmation: confirmation, value: 450000, want: true},
		{name: "total no accents", amount: orderEvidenceMessage("amount", "agent", "tong tien don nay: 450,000 VND.", 11), confirmation: confirmation, value: 450000, want: true},
		{name: "total colloquial", amount: orderEvidenceMessage("amount", "agent", "Tổng bill = 450k nha chị", 11), confirmation: confirmation, value: 450000, want: true},
		{name: "amount to transfer", amount: orderEvidenceMessage("amount", "agent", "Số tiền cần ck 450 nghìn ạ", 11), confirmation: confirmation, value: 450000, want: true},
		{name: "remaining payment", amount: orderEvidenceMessage("amount", "agent", "Còn phải thanh toán 450 000đ!", 11), confirmation: confirmation, value: 450000, want: true},
		{name: "accepted prior total", amount: orderEvidenceMessage("amount", "agent", "Tổng đơn là 450.000đ", 9), confirmation: acceptedQuote, value: 450000, want: true},
		{name: "unaccepted prior quote", amount: orderEvidenceMessage("amount", "agent", "Tổng đơn là 450.000đ", 9), confirmation: confirmation, value: 450000, want: false},
		{name: "listed price", amount: orderEvidenceMessage("amount", "agent", "Giá sản phẩm 450.000đ", 11), confirmation: confirmation, value: 450000, want: false},
		{name: "unit price with total word elsewhere", amount: orderEvidenceMessage("amount", "agent", "Tổng hợp báo giá: giá sản phẩm 450.000đ", 11), confirmation: confirmation, value: 450000, want: false},
		{name: "other number in total message", amount: orderEvidenceMessage("amount", "agent", "Mã 999999, tổng đơn 450.000đ", 11), confirmation: confirmation, value: 999999, want: false},
		{name: "qr amount", amount: orderEvidenceMessage("amount", "agent", "Mã QR 450000, tổng đơn 450.000đ", 11), confirmation: confirmation, value: 450000, want: false},
		{name: "wrong total", amount: orderEvidenceMessage("amount", "agent", "Tổng đơn là 450.000đ", 11), confirmation: confirmation, value: 500000, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages := []models.Message{tc.amount, tc.confirmation}
			if got := validOrderAmount(tc.amount, tc.value, messages, tc.confirmation); got != tc.want {
				t.Fatalf("validOrderAmount() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOrderEvidenceRejectsNegationAndFalseTracking(t *testing.T) {
	for _, text := range []string{"Đơn chưa có mã vận đơn GHN123456", "Mã vận đơn: đang xử lý", "Mã vận đơn: 0912345678", "Tôi chưa đến cửa hàng và chưa mua sản phẩm"} {
		sender := "agent"
		if strings.Contains(text, "Tôi") {
			sender = "customer"
		}
		message := orderEvidenceMessage("id", sender, text, 10)
		if validOrderConfirmation(message, []models.Message{message}) {
			t.Errorf("false confirmation: %q", text)
		}
	}
	for _, text := range []string{"Đơn chưa hủy", "Đơn chưa được hoàn tiền", "Shop sẽ hoàn tiền", "Bạn hủy đơn chưa?"} {
		message := orderEvidenceMessage("id", "agent", text, 10)
		if validOrderTransition("cancelled", message) || validOrderTransition("refunded", message) {
			t.Errorf("false transition: %q", text)
		}
	}
}

func TestAgentOnlyTrackingIsAnalyzed(t *testing.T) {
	job := models.Job{JobType: "classification", RulesConfig: `{"profile":"messenger_insights","rules":[]}`}
	messages := []models.Message{orderEvidenceMessage("track", "agent", "Mã vận đơn GHN123456", 1)}
	if _, guarded := messengerInsightsGuardResponse(job, messages); guarded {
		t.Fatal("tracking evidence must reach AI")
	}
}
