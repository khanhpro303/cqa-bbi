package workers

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/ai"
)

func TestAllowedImageURL(t *testing.T) {
	for u, want := range map[string]bool{
		"https://f20-zpc.zdn.vn/jpg/abc.jpg":    true,
		"https://s240-ava-talk.zadn.vn/a/b.jpg": true,
		"https://photo.zalo.me/x":               true,
		"http://f20-zpc.zdn.vn/jpg/abc.jpg":     false,
		"https://evil.com/zdn.vn":               false,
		"https://zdn.vn.evil.com/a.jpg":         false,
		"https://user:pw@f20.zdn.vn/a.jpg":      false,
		"https://f20.zdn.vn:8443/a.jpg":         false,
		"https://169.254.169.254/latest/meta":   false,
		"file:///etc/passwd":                    false,
	} {
		if got := allowedImageURL(u); got != want {
			t.Errorf("allowedImageURL(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestImageURLsTakesImagesOnly(t *testing.T) {
	var atts []ZaloWebhookAttachment
	add := func(typ, url, thumb string) {
		var a ZaloWebhookAttachment
		a.Type, a.Payload.URL, a.Payload.Thumbnail = typ, url, thumb
		atts = append(atts, a)
	}
	add("image", "https://f1.zdn.vn/1.jpg", "")
	add("file", "https://f1.zdn.vn/doc.pdf", "")
	add("image", "", "https://f1.zdn.vn/thumb.jpg")
	for i := 0; i < 5; i++ {
		add("image", "https://f1.zdn.vn/more.jpg", "")
	}
	got := imageURLs(atts)
	if len(got) != imageMaxPerMessage || got[0] != "https://f1.zdn.vn/1.jpg" || got[1] != "https://f1.zdn.vn/thumb.jpg" {
		t.Fatalf("imageURLs = %v", got)
	}
}

func TestReadInboundImagesKeepsUnreadImages(t *testing.T) {
	origDownload, origDescribe := downloadImage, describeImage
	t.Cleanup(func() { downloadImage, describeImage = origDownload, origDescribe })
	downloadImage = func(_ context.Context, u string) (ai.ImageInput, error) {
		if strings.Contains(u, "broken") {
			return ai.ImageInput{}, errors.New("404")
		}
		return ai.ImageInput{MediaType: "image/jpeg", Data: []byte(u)}, nil
	}
	var captions []string
	describeImage = func(_ context.Context, _, _ string, img ai.ImageInput, caption string) (string, error) {
		captions = append(captions, caption)
		return "Màn hình báo: Đơn hàng số <> đã được xử lý bởi chứng từ bán hàng <>", nil
	}
	got := readInboundImages(context.Background(), "t", "c", []string{"https://f1.zdn.vn/ok.jpg", "https://f1.zdn.vn/broken.jpg"}, "đang bị gì ạ")
	if len(got) != 2 || got[0].Description == "" || got[1].Description != "" || got[1].URL != "https://f1.zdn.vn/broken.jpg" {
		t.Fatalf("images = %+v", got)
	}
	if len(captions) != 1 || captions[0] != "đang bị gì ạ" {
		t.Fatalf("caption passed = %v", captions)
	}
	text := imagesText(got)
	if !strings.Contains(text, "[Ảnh khách gửi: Màn hình báo: Đơn hàng số") || !strings.Contains(text, "[Ảnh khách gửi: chưa đọc được nội dung ảnh]") {
		t.Fatalf("text = %q", text)
	}
	atts := imageAttachments(got)
	if len(atts) != 2 || atts[0].Type != "image" || atts[0].Description == "" {
		t.Fatalf("attachments = %+v", atts)
	}
}

func TestSniffImageType(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n0000")
	if sniffImageType(png) != "image/png" || sniffImageType([]byte("<html>")) != "" {
		t.Fatal("sniff")
	}
}

func TestImageDialerRefusesPrivateAddresses(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "192.168.1.1", "172.16.0.5", "169.254.169.254", "100.64.0.1", "0.0.0.0", "::1", "fe80::1", "fc00::1"} {
		if publicAddress(net.ParseIP(ip)) {
			t.Errorf("%s must be refused", ip)
		}
		if err := imageDialer.Control("tcp", net.JoinHostPort(ip, "443"), nil); err == nil {
			t.Errorf("dialer allowed %s", ip)
		}
	}
	for _, ip := range []string{"14.225.1.1", "2001:4860::8888"} {
		if !publicAddress(net.ParseIP(ip)) {
			t.Errorf("%s must be allowed", ip)
		}
	}
}

func TestImageURLsKeepsOnlyZaloImages(t *testing.T) {
	var atts []ZaloWebhookAttachment
	for _, u := range []string{"https://f1.zdn.vn/a.jpg", "http://f1.zdn.vn/b.jpg", "https://evil.example/c.jpg", "https://f1.zdn.vn.evil.example/d.jpg"} {
		var a ZaloWebhookAttachment
		a.Type = "image"
		a.Payload.URL = u
		atts = append(atts, a)
	}
	if got := imageURLs(atts); len(got) != 1 || got[0] != "https://f1.zdn.vn/a.jpg" {
		t.Fatalf("imageURLs = %v", got)
	}
}

func TestComplaintTurnIDStableWithoutMsgID(t *testing.T) {
	var p ZaloWebhookPayload
	p.Sender.ID, p.Timestamp, p.EventName, p.Message.Text = "u1", "1700000000000", "user_send_text", "hàng lỗi"
	a, b := complaintTurnID("ch", p), complaintTurnID("ch", p)
	if a != b || len(a) > 36 {
		t.Fatalf("turn ids %q %q", a, b)
	}
	p.Message.Text = "khác"
	if complaintTurnID("ch", p) == a {
		t.Fatal("different message, same turn id")
	}
	p.Message.MsgID = "m1"
	if complaintTurnID("ch", p) != complaintTurnID("ch", p) {
		t.Fatal("msg_id turn id not stable")
	}
}
