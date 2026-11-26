package channels

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Zalo support messages accept static PNG/JPEG attachments up to 1 MB.
const maxZaloProductImageBytes = 1_000_000

// PrepareZaloProductImage converts a supported catalog image to an uploadable
// static attachment. Check dimensions before decoding untrusted image data.
func PrepareZaloProductImage(data []byte) ([]byte, string, error) {
	if len(data) == 0 || len(data) > maxRemoteProductImageBytes {
		return nil, "", errors.New("product image size is unsupported")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 20_000_000 {
		return nil, "", errors.New("product image cannot be decoded or is too large")
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", errors.New("product image is invalid")
	}
	if len(data) <= maxZaloProductImageBytes {
		switch format {
		case "jpeg":
			return data, "product-image.jpg", nil
		case "png":
			return data, "product-image.png", nil
		}
	}
	longest := max(config.Width, config.Height)
	for _, maxSide := range []int{1600, 1200, 800} {
		side := min(longest, maxSide)
		bounds := image.Rect(0, 0, max(1, config.Width*side/longest), max(1, config.Height*side/longest))
		canvas := image.NewRGBA(bounds)
		draw.Draw(canvas, bounds, image.NewUniform(color.White), image.Point{}, draw.Src)
		draw.CatmullRom.Scale(canvas, bounds, decoded, decoded.Bounds(), draw.Over, nil)
		var encoded bytes.Buffer
		if err := jpeg.Encode(&encoded, canvas, &jpeg.Options{Quality: 84}); err != nil {
			return nil, "", errors.New("could not encode Zalo product image")
		}
		if encoded.Len() <= maxZaloProductImageBytes {
			return encoded.Bytes(), "product-image.jpg", nil
		}
	}
	return nil, "", errors.New("could not fit product image within Zalo attachment limit")
}
