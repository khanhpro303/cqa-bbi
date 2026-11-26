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

type ZaloImage struct {
	Data      []byte `json:"data_base64"`
	FileName  string `json:"filename"`
	MediaType string `json:"media_type"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

// PrepareZaloProductImage converts a supported catalog image to an uploadable
// static attachment. Check dimensions before decoding untrusted image data.
func PrepareZaloProductImage(data []byte) ([]byte, string, error) {
	attachment, err := PrepareZaloImage(data)
	if err != nil {
		return nil, "", err
	}
	return attachment.Data, "product-" + attachment.FileName, nil
}

// PrepareZaloImage returns a static attachment and its final dimensions.
func PrepareZaloImage(data []byte) (ZaloImage, error) {
	if len(data) == 0 || len(data) > maxRemoteProductImageBytes {
		return ZaloImage{}, errors.New("image size is unsupported")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 20_000_000 {
		return ZaloImage{}, errors.New("image cannot be decoded or is too large")
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return ZaloImage{}, errors.New("image is invalid")
	}
	if len(data) <= maxZaloProductImageBytes {
		switch format {
		case "jpeg":
			return ZaloImage{data, "image.jpg", "image/jpeg", config.Width, config.Height}, nil
		case "png":
			return ZaloImage{data, "image.png", "image/png", config.Width, config.Height}, nil
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
			return ZaloImage{}, errors.New("could not encode Zalo image")
		}
		if encoded.Len() <= maxZaloProductImageBytes {
			return ZaloImage{encoded.Bytes(), "image.jpg", "image/jpeg", bounds.Dx(), bounds.Dy()}, nil
		}
	}
	return ZaloImage{}, errors.New("could not fit image within Zalo attachment limit")
}
