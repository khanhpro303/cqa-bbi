package channels

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"math/rand/v2"
	"testing"
)

func TestPrepareZaloProductImageCompressesAndConverts(t *testing.T) {
	large := image.NewNRGBA(image.Rect(0, 0, 1100, 1100))
	rng := rand.NewPCG(1, 2)
	for i := 0; i < len(large.Pix); i += 4 {
		pixel := rng.Uint64()
		large.Pix[i], large.Pix[i+1], large.Pix[i+2], large.Pix[i+3] = byte(pixel), byte(pixel>>8), byte(pixel>>16), 255
	}
	var pngInput, gifInput bytes.Buffer
	if err := png.Encode(&pngInput, large); err != nil || pngInput.Len() <= maxZaloProductImageBytes {
		t.Fatalf("create oversized, valid PNG: %d bytes, %v", pngInput.Len(), err)
	}
	if err := gif.Encode(&gifInput, image.NewPaletted(image.Rect(0, 0, 10, 10), color.Palette{color.White}), nil); err != nil {
		t.Fatal(err)
	}
	for _, input := range [][]byte{pngInput.Bytes(), gifInput.Bytes()} {
		result, filename, err := PrepareZaloProductImage(input)
		if err != nil || filename != "product-image.jpg" || len(result) > maxZaloProductImageBytes {
			t.Fatalf("invalid attachment: %s, %d bytes, %v", filename, len(result), err)
		}
		if _, format, err := image.Decode(bytes.NewReader(result)); err != nil || format != "jpeg" {
			t.Fatalf("invalid JPEG: %s, %v", format, err)
		}
	}
}
