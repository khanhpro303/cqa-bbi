package cloudifyorders

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strconv"
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

	encoded, err := renderReceiptPages(order, lines, 50000, payment)
	if err != nil || len(encoded) != 1 {
		t.Fatalf("expected one page with payment, got %d: %v", len(encoded), err)
	}
	canvas, err := png.Decode(bytes.NewReader(encoded[0].png))
	if err != nil {
		t.Fatal(err)
	}
	textOnly, err := renderReceiptPages(order, lines, 50000, ReceiptPayment{})
	if err != nil || len(textOnly) != 1 {
		t.Fatalf("expected one page without payment, got %d: %v", len(textOnly), err)
	}
	plain, err := png.DecodeConfig(bytes.NewReader(textOnly[0].png))
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

func TestRenderReceiptPagesRejectsOversizedAndInvalidOrders(t *testing.T) {
	for _, order := range []Order{
		{CustomerName: strings.Repeat("Khách Hàng ", 4000)},
		{Total: math.NaN()},
	} {
		if _, err := renderReceiptPages(order, nil, 0, ReceiptPayment{}); err == nil {
			t.Error("expected render to reject the order")
		}
	}
}

func TestRenderReceiptPagesMultiPage(t *testing.T) {
	qr := image.NewGray(image.Rect(0, 0, 48, 48))
	order := Order{
		ID: 42, Code: "ĐH000042", CustomerCode: "KH200", CustomerName: "Khách Hàng Nhiều SP",
		Status: "0", Date: "2026-11-25", Total: 100000,
	}
	// Create enough items to force multiple pages.
	lines := make([]ReceiptLine, 40)
	for i := range lines {
		lines[i] = ReceiptLine{
			SKU:  "SP" + strings.Repeat("0", 3-len(strconv.Itoa(i+1))) + strconv.Itoa(i+1),
			Name: "Sản phẩm mẫu " + strconv.Itoa(i+1) + " với tên dài để test wrap",
			Unit: "Cái", Quantity: float64(i + 1), UnitPrice: 2500,
		}
	}
	payment := ReceiptPayment{QR: qr, AccountNo: "0000123456", AccountName: "CÔNG TY TEST", TransferMemo: "KH200"}

	pages, err := renderReceiptPages(order, lines, 0, payment)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 2 {
		t.Fatalf("expected multiple pages for %d items, got %d page(s)", len(lines), len(pages))
	}
	// Every page must be a valid PNG.
	for i, page := range pages {
		cfg, err := png.DecodeConfig(bytes.NewReader(page.png))
		if err != nil {
			t.Fatalf("page %d is not a valid PNG: %v", i+1, err)
		}
		if cfg.Width != receiptWidth {
			t.Fatalf("page %d width = %d, want %d", i+1, cfg.Width, receiptWidth)
		}
		// Pages should have roughly similar heights (not exact because
		// item counts per page can differ by one).
		if cfg.Height < 400 || cfg.Height > receiptPageHeight+500 {
			t.Fatalf("page %d height = %d out of reasonable range", i+1, cfg.Height)
		}
	}
	for i := range lines {
		lines[i].Image = qr
	}
	withPhotos, err := renderReceiptPages(order, lines, 0, payment)
	if err != nil || len(withPhotos) != len(pages) {
		t.Fatalf("product photos changed pagination: %d pages, want %d: %v", len(withPhotos), len(pages), err)
	}
	lastItem := 0
	for i, page := range pages {
		if page.itemEnd <= lastItem || page.itemEnd != withPhotos[i].itemEnd {
			t.Fatalf("unexpected item boundary on page %d: %d", i+1, page.itemEnd)
		}
		lastItem = page.itemEnd
	}
	if lastItem != len(lines) {
		t.Fatalf("ended after %d items, want %d", lastItem, len(lines))
	}

}

func TestRenderReceiptPagesSinglePageForSmallOrders(t *testing.T) {
	order := Order{ID: 1, Code: "ĐH001", CustomerCode: "KH1", Status: "0", Total: 10000}
	lines := []ReceiptLine{
		{SKU: "A", Name: "Sản phẩm A", Unit: "Cái", Quantity: 1, UnitPrice: 10000},
	}
	pages, err := renderReceiptPages(order, lines, 0, ReceiptPayment{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("small order should produce 1 page, got %d", len(pages))
	}
}

func TestRenderReceiptPagesWithoutItems(t *testing.T) {
	order := Order{Code: "ĐH001", CustomerName: strings.Repeat("Khách Hàng ", 600)}
	pages, err := renderReceiptPages(order, nil, 0, ReceiptPayment{})
	if err != nil || len(pages) != 1 {
		t.Fatalf("expected one page for an order without items, got %d: %v", len(pages), err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(pages[0].png))
	if err != nil || cfg.Height <= receiptPageHeight {
		t.Fatalf("expected a valid PNG taller than the page target, got height %d: %v", cfg.Height, err)
	}
}
