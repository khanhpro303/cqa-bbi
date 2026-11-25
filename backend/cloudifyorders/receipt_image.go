package cloudifyorders

import (
	"bytes"
	_ "embed"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strconv"
	"strings"

	imagedraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/text/unicode/norm"
)

// ReceiptLine holds the details of a single order line and its optional cached image.
type ReceiptLine struct {
	SKU       string
	Name      string
	Unit      string
	Quantity  float64
	UnitPrice float64
	Image     image.Image `json:"-"`
}

// ReceiptPayment supplies the transfer details shown beside the VietQR image.
type ReceiptPayment struct {
	QR           image.Image
	AccountNo    string
	AccountName  string
	TransferMemo string
}

const (
	receiptWidth     = 880
	receiptMaxHeight = 12000
	receiptMaxBytes  = 2 << 20
	receiptMargin    = 24
	receiptLeft      = 56
	receiptRight     = receiptWidth - receiptLeft
	receiptTextWidth = receiptRight - receiptLeft
	receiptQRSize    = 228
)

var (
	receiptInk        = color.RGBA{R: 20, G: 32, B: 45, A: 255}
	receiptMuted      = color.RGBA{R: 73, G: 87, B: 101, A: 255}
	receiptNavy       = color.RGBA{R: 17, G: 38, B: 56, A: 255}
	receiptLightBlue  = color.RGBA{R: 201, G: 222, B: 232, A: 255}
	receiptBackground = color.RGBA{R: 241, G: 245, B: 248, A: 255}
	receiptBorder     = color.RGBA{R: 224, G: 232, B: 237, A: 255}
	receiptTeal       = color.RGBA{R: 0, G: 91, B: 80, A: 255}
	receiptMint       = color.RGBA{R: 230, G: 246, B: 241, A: 255}
)

//go:embed receiptfonts/NotoSans-Regular.ttf
var receiptRegularData []byte

//go:embed receiptfonts/NotoSans-Bold.ttf
var receiptBoldData []byte

var receiptRegularFont, receiptRegularFontErr = opentype.Parse(receiptRegularData)
var receiptBoldFont, receiptBoldFontErr = opentype.Parse(receiptBoldData)

type receiptFaces struct {
	small   font.Face
	body    font.Face
	strong  font.Face
	total   font.Face
	heading font.Face
}

func newReceiptFaces() (receiptFaces, error) {
	if err := errors.Join(receiptRegularFontErr, receiptBoldFontErr); err != nil {
		return receiptFaces{}, err
	}
	var faces receiptFaces
	for _, style := range []struct {
		dest *font.Face
		ttf  *opentype.Font
		size float64
	}{
		{&faces.small, receiptRegularFont, 22},
		{&faces.body, receiptRegularFont, 26},
		{&faces.strong, receiptBoldFont, 26},
		{&faces.total, receiptBoldFont, 34},
		{&faces.heading, receiptBoldFont, 38},
	} {
		face, err := opentype.NewFace(style.ttf, &opentype.FaceOptions{
			Size: style.size, DPI: 72, Hinting: font.HintingNone,
		})
		if err != nil {
			faces.close()
			return receiptFaces{}, err
		}
		*style.dest = face
	}
	return faces, nil
}

func (f receiptFaces) close() {
	for _, face := range []font.Face{f.small, f.body, f.strong, f.total, f.heading} {
		if face != nil {
			face.Close()
		}
	}
}

type receiptLineLayout struct {
	line          ReceiptLine
	y, height, x  int
	name, details []string
	amount        string
}

func renderReceiptPNG(order Order, lines []ReceiptLine, discount float64, payment ReceiptPayment) ([]byte, error) {
	faces, err := newReceiptFaces()
	if err != nil {
		return nil, err
	}
	defer faces.close()

	code := order.Code
	if code == "" {
		code = "#" + strconv.FormatInt(order.ID, 10)
	}
	orderCode := wrapReceiptText(faces.total, code, receiptTextWidth)
	headerBottom := 125 + 46*len(orderCode)

	customer := order.CustomerName
	if customer == "" {
		customer = order.CustomerCode
	} else if order.CustomerCode != "" {
		customer += "  ·  " + order.CustomerCode
	}
	customerLines := wrapReceiptText(faces.body, customer, receiptTextWidth)
	var metadata []string
	if date := FormatDate(order.Date); date != "" {
		metadata = append(metadata, "Ngày đặt "+date)
	}
	if order.Status != "" {
		metadata = append(metadata, StatusName(order.Status))
	}
	metaLines := wrapReceiptText(faces.small, strings.Join(metadata, "  ·  "), receiptTextWidth)
	tableHeader := headerBottom + 82 + 36*len(customerLines) + 30*len(metaLines)
	y := tableHeader + 52

	// Smaller thumbnails leave space in the PNG upload budget for long orders.
	photoSize := min(76, max(48, 7200/max(1, len(lines))))
	layout := make([]receiptLineLayout, 0, min(len(lines), receiptMaxHeight/92))
	subtotal := 0.0
	for _, line := range lines {
		x := receiptLeft
		if line.Image != nil {
			x += photoSize + 18
		}
		name := strings.TrimSpace(line.Name)
		if name == "" {
			name = line.SKU
		}
		if name == "" {
			name = "Sản phẩm"
		}
		lineTotal := line.Quantity * line.UnitPrice
		if !receiptValidAmount(line.UnitPrice) || !receiptValidAmount(lineTotal) {
			return nil, errors.New("order line contains an invalid price or quantity")
		}
		detail := strings.TrimSpace(receiptQuantity(line.Quantity)+" "+line.Unit) + " × " + receiptMoney(line.UnitPrice)
		if line.SKU != "" && line.SKU != name {
			detail = line.SKU + "  ·  " + detail
		}
		amount := receiptMoney(lineTotal)
		amountWidth := max(244, font.MeasureString(faces.strong, amount).Ceil()+24)
		available := receiptRight - amountWidth - x
		item := receiptLineLayout{
			line: line, y: y, x: x,
			name:    wrapReceiptText(faces.strong, name, available),
			details: wrapReceiptText(faces.small, detail, available),
			amount:  amount,
		}
		item.height = max(100, 35+35*len(item.name)+29*len(item.details))
		y += item.height
		if y > receiptMaxHeight {
			return nil, errors.New("order items exceed the confirmation image height limit")
		}
		layout = append(layout, item)
		subtotal += lineTotal
	}
	if !receiptValidAmount(subtotal) || !receiptValidAmount(discount) || !receiptValidAmount(order.Total) {
		return nil, errors.New("order contains an invalid total or discount")
	}
	if len(layout) == 0 {
		y += 68
	}
	summaryTop := y
	if discount != 0 {
		y += 78
	}
	totalTop := y + 16
	y = totalTop + 84

	paymentTop := y + 22
	var paymentLines []string
	if payment.QR != nil {
		paymentLines = wrapReceiptText(faces.body, payment.AccountName, receiptTextWidth-receiptQRSize-40)
		if payment.AccountNo != "" {
			paymentLines = append(paymentLines, wrapReceiptText(faces.body, "STK "+payment.AccountNo, receiptTextWidth-receiptQRSize-40)...)
		}
		if payment.TransferMemo != "" {
			paymentLines = append(paymentLines, wrapReceiptText(faces.body, "Nội dung: "+payment.TransferMemo, receiptTextWidth-receiptQRSize-40)...)
		}
		y = paymentTop + max(receiptQRSize+36, 91+36*len(paymentLines))
	}
	height := y + receiptMargin + 24
	if height > receiptMaxHeight {
		return nil, errors.New("order details exceed the confirmation image height limit")
	}

	canvas := image.NewRGBA(image.Rect(0, 0, receiptWidth, height))
	receiptFill(canvas, 0, 0, receiptWidth, height, receiptBackground)
	receiptFill(canvas, receiptMargin, receiptMargin, receiptWidth-receiptMargin, height-receiptMargin, color.White)
	receiptFill(canvas, receiptMargin, receiptMargin, receiptWidth-receiptMargin, headerBottom, receiptNavy)
	receiptText(canvas, faces.heading, receiptLeft, 89, color.White, "PHIẾU XÁC NHẬN ĐƠN HÀNG")
	receiptTextLines(canvas, faces.total, receiptLeft, 141, 46, receiptLightBlue, orderCode)
	receiptText(canvas, faces.strong, receiptLeft, headerBottom+52, receiptInk, "Khách hàng")
	receiptTextLines(canvas, faces.body, receiptLeft, headerBottom+88, 36, receiptInk, customerLines)
	receiptTextLines(canvas, faces.small, receiptLeft, headerBottom+88+36*len(customerLines), 30, receiptMuted, metaLines)

	receiptFill(canvas, receiptLeft-12, tableHeader, receiptRight+12, tableHeader+44, receiptBackground)
	receiptText(canvas, faces.strong, receiptLeft, tableHeader+30, receiptInk, "Sản phẩm")
	receiptRightText(canvas, faces.strong, tableHeader+30, receiptInk, "Thành tiền")
	if len(layout) == 0 {
		receiptText(canvas, faces.body, receiptLeft, tableHeader+86, receiptMuted, "Chưa có chi tiết sản phẩm")
	}
	for _, item := range layout {
		if item.line.Image != nil {
			photoTop := item.y + 12
			bounds := image.Rect(receiptLeft, photoTop, receiptLeft+photoSize, photoTop+photoSize)
			drawReceiptImage(canvas, item.line.Image, bounds, imagedraw.CatmullRom)
		}
		receiptTextLines(canvas, faces.strong, item.x, item.y+32, 35, receiptInk, item.name)
		receiptTextLines(canvas, faces.small, item.x, item.y+32+35*len(item.name), 29, receiptMuted, item.details)
		receiptRightText(canvas, faces.strong, item.y+32, receiptInk, item.amount)
		receiptFill(canvas, receiptLeft, item.y+item.height-1, receiptRight, item.y+item.height, receiptBorder)
	}
	if discount != 0 {
		receiptText(canvas, faces.body, receiptLeft, summaryTop+35, receiptMuted, "Tạm tính")
		receiptRightText(canvas, faces.body, summaryTop+35, receiptMuted, receiptMoney(subtotal))
		receiptText(canvas, faces.body, receiptLeft, summaryTop+71, receiptMuted, "Giảm giá")
		receiptRightText(canvas, faces.body, summaryTop+71, receiptMuted, receiptMoney(-discount))
	}
	receiptFill(canvas, receiptLeft-12, totalTop, receiptRight+12, totalTop+84, receiptMint)
	receiptText(canvas, faces.strong, receiptLeft, totalTop+53, receiptTeal, "Tổng tiền đơn hàng")
	receiptRightText(canvas, faces.total, totalTop+53, receiptTeal, receiptMoney(order.Total))
	if payment.QR != nil {
		left := receiptLeft + receiptQRSize + 35
		receiptFill(canvas, receiptLeft-12, paymentTop, receiptRight+12, y, receiptBackground)
		box := image.Rect(receiptLeft, paymentTop+18, receiptLeft+receiptQRSize, paymentTop+18+receiptQRSize)
		draw.Draw(canvas, box, image.NewUniform(color.White), image.Point{}, draw.Src)
		drawReceiptImage(canvas, payment.QR, box.Inset(18), imagedraw.NearestNeighbor)
		receiptText(canvas, faces.strong, left, paymentTop+59, receiptInk, "Quét VietQR để chuyển khoản")
		receiptTextLines(canvas, faces.body, left, paymentTop+101, 36, receiptMuted, paymentLines)
	}

	var pngBuffer bytes.Buffer
	if err := png.Encode(&pngBuffer, canvas); err != nil {
		return nil, err
	}
	if pngBuffer.Len() > receiptMaxBytes {
		return nil, errors.New("confirmation image exceeds the upload size limit")
	}
	return pngBuffer.Bytes(), nil
}

func receiptMoney(amount float64) string {
	return FormatVND(math.Round(amount))
}

func receiptValidAmount(amount float64) bool {
	return math.Abs(amount) < float64(math.MaxInt64)
}

func receiptQuantity(quantity float64) string {
	return strings.Replace(strconv.FormatFloat(quantity, 'f', -1, 64), ".", ",", 1)
}

func receiptFill(dst draw.Image, left, top, right, bottom int, ink color.Color) {
	draw.Draw(dst, image.Rect(left, top, right, bottom), image.NewUniform(ink), image.Point{}, draw.Src)
}

func receiptText(dst draw.Image, face font.Face, x, baseline int, ink color.Color, value string) {
	d := font.Drawer{Dst: dst, Src: image.NewUniform(ink), Face: face, Dot: fixed.P(x, baseline)}
	d.DrawString(value)
}

func receiptTextLines(dst draw.Image, face font.Face, x, baseline, spacing int, ink color.Color, lines []string) {
	for _, line := range lines {
		receiptText(dst, face, x, baseline, ink, line)
		baseline += spacing
	}
}

func receiptRightText(dst draw.Image, face font.Face, baseline int, ink color.Color, value string) {
	receiptText(dst, face, receiptRight-font.MeasureString(face, value).Ceil(), baseline, ink, value)
}

func wrapReceiptText(face font.Face, value string, width int) []string {
	const maxLines = receiptMaxHeight/29 + 1
	var lines []string
	current := ""
	for _, word := range strings.Fields(norm.NFC.String(value)) {
		if current != "" {
			current += " "
		}
		// Most words fit without measuring one rune at a time. An oversized
		// word (often a pasted identifier) still wraps inside the image.
		if font.MeasureString(face, current+word).Ceil() <= width {
			current += word
			continue
		}
		if current != "" {
			lines = append(lines, strings.TrimRight(current, " "))
			current = ""
		}
		for _, r := range word {
			candidate := current + string(r)
			if current != "" && font.MeasureString(face, candidate).Ceil() > width {
				lines = append(lines, current)
				current = string(r)
				if len(lines) >= maxLines {
					return lines
				}
			} else {
				current = candidate
			}
		}
		if len(lines) >= maxLines {
			return lines
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func drawReceiptImage(dst draw.Image, src image.Image, bounds image.Rectangle, scaler imagedraw.Scaler) {
	size := src.Bounds().Size()
	if size.X <= 0 || size.Y <= 0 {
		return
	}
	scale := min(float64(bounds.Dx())/float64(size.X), float64(bounds.Dy())/float64(size.Y))
	width, height := max(1, int(float64(size.X)*scale)), max(1, int(float64(size.Y)*scale))
	x := bounds.Min.X + (bounds.Dx()-width)/2
	y := bounds.Min.Y + (bounds.Dy()-height)/2
	scaler.Scale(dst, image.Rect(x, y, x+width, y+height), src, src.Bounds(), draw.Over, nil)
}
