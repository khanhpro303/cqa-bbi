package cloudifyorders

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strings"
	"testing"

	"golang.org/x/image/font"
)

func TestRenderReceiptPNGWithCachedPhotoAndQR(t *testing.T) {
	product := image.NewRGBA(image.Rect(0, 0, 42, 24))
	photoColor := color.RGBA{R: 172, G: 44, B: 31, A: 255}
	draw.Draw(product, product.Bounds(), image.NewUniform(photoColor), image.Point{}, draw.Src)
	qr := image.NewGray(image.Rect(0, 0, 48, 48))
	order := Order{
		ID: 16, Code: "ĐH000016", CustomerCode: "S084", CustomerName: "Chợ Bảo Hộ",
		Status: "0", Date: "2021-08-31", Total: 2860000,
	}
	lines := []ReceiptLine{
		{SKU: "AQ12-7.5", Name: "Bình ắc quy 12V-7.5Ah", Unit: "Bình", Quantity: 6, UnitPrice: 485000, Image: product},
		{SKU: "BBI01", Name: strings.Repeat("Tên hàng rất dài ", 14), Unit: "Cái", Quantity: 1.5},
	}
	payment := ReceiptPayment{QR: qr, AccountNo: "0000123456", AccountName: "CÔNG TY BÌNH MINH", TransferMemo: "S084"}

	encoded, err := renderReceiptPNG(order, lines, 50000, payment)
	if err != nil {
		t.Fatal(err)
	}
	canvas, err := png.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	textOnly, err := renderReceiptPNG(order, lines, 50000, ReceiptPayment{})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := png.DecodeConfig(bytes.NewReader(textOnly))
	if err != nil {
		t.Fatal(err)
	}
	if canvas.Bounds().Dx() != receiptWidth || canvas.Bounds().Dy() <= plain.Height+receiptQRSize {
		t.Fatalf("unexpected dimensions, with payment %v and without %dx%d", canvas.Bounds(), plain.Width, plain.Height)
	}

	photoFound, qrFound := false, false
	for y := 0; y < canvas.Bounds().Dy(); y++ {
		for x := receiptLeft; x < receiptLeft+receiptQRSize; x++ {
			pixel := color.RGBAModel.Convert(canvas.At(x, y)).(color.RGBA)
			photoFound = photoFound || pixel == photoColor
			qrFound = qrFound || y > plain.Height && pixel == (color.RGBA{A: 255})
		}
	}
	if !photoFound || !qrFound {
		t.Fatalf("expected the composed product photo and QR pixels, got photo=%t QR=%t", photoFound, qrFound)
	}
}

func TestReceiptFontsAndLongVietnameseWords(t *testing.T) {
	faces, err := newReceiptFaces()
	if err != nil {
		t.Fatal(err)
	}
	defer faces.close()

	for _, face := range []font.Face{faces.body, faces.strong} {
		for _, letter := range "Đơn đặt hàng Chuyển khoản Huỳnh Thị Ngọc Ánh Ắc quy Ứng Tiền ĐỖ" {
			if _, ok := face.GlyphAdvance(letter); !ok {
				t.Errorf("missing Vietnamese glyph %q", letter)
			}
		}
		const width = 170
		longSKU := strings.Repeat("ẮCQUYĐIỆN", 20)
		lines := wrapReceiptText(face, longSKU, width)
		for _, line := range lines {
			if font.MeasureString(face, line).Ceil() > width {
				t.Fatalf("wrapped line exceeds the available width: %q", line)
			}
		}
		if strings.Join(lines, "") != longSKU {
			t.Fatal("wrapping lost part of the value")
		}
	}
}

func TestRenderReceiptPNGRejectsOversizedAndInvalidOrders(t *testing.T) {
	for _, test := range []struct {
		lines []ReceiptLine
		order Order
	}{
		{lines: make([]ReceiptLine, 200)},
		{order: Order{Total: math.NaN()}},
	} {
		if _, err := renderReceiptPNG(test.order, test.lines, 0, ReceiptPayment{}); err == nil {
			t.Error("expected render to reject the order")
		}
	}
}
