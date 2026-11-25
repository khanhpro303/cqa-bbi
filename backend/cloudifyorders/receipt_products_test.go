package cloudifyorders

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"testing"
)

type productImageTransport func(*http.Request) (*http.Response, error)

func (fn productImageTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestFetchReceiptThumbnail(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 384, 192))
	for y := range source.Bounds().Dy() {
		for x := range source.Bounds().Dx() {
			source.SetNRGBA(x, y, color.NRGBA{R: 200, G: 15, B: 50, A: 255})
		}
	}
	client := productImageTestClient(t, source)
	got := fetchReceiptThumbnail(context.Background(), client, "https://cdn.example/products/item.png")
	if got == nil {
		t.Fatal("thumbnail is missing")
	}
	if want := image.Rect(0, 0, 192, 96); got.Bounds() != want {
		t.Fatalf("thumbnail bounds = %v, want %v", got.Bounds(), want)
	}
	if px := color.NRGBAModel.Convert(got.At(35, 42)); px != source.NRGBAAt(70, 84) {
		t.Fatalf("thumbnail color = %v, want %v", px, source.NRGBAAt(70, 84))
	}
}

func TestFetchReceiptThumbnailRejectsOversizedDimensions(t *testing.T) {
	client := productImageTestClient(t, image.NewGray(image.Rect(0, 0, maxProductDimension+1, 1)))
	if got := fetchReceiptThumbnail(context.Background(), client, "https://cdn.example/products/oversized.png"); got != nil {
		t.Fatalf("oversized thumbnail returned bounds %v", got.Bounds())
	}
}

func productImageTestClient(t *testing.T, source image.Image) *http.Client {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	return &http.Client{Transport: productImageTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("product image method = %q, want GET", req.Method)
		}
		return &http.Response{
			StatusCode:    http.StatusOK,
			ContentLength: int64(encoded.Len()),
			Body:          io.NopCloser(bytes.NewReader(encoded.Bytes())),
		}, nil
	})}
}
