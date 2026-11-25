package handlers

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestReadOrderConfirmationScreenshotPreservesOriginalFile(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var pngImage, jpegImage bytes.Buffer
	if err := png.Encode(&pngImage, frame); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&jpegImage, frame, nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, wantType string
		content        []byte
		wantStatus     int
	}{
		{"zalo.png", "image/png", pngImage.Bytes(), http.StatusOK},
		{"zalo.jpg", "image/jpeg", jpegImage.Bytes(), http.StatusOK},
		{"corrupt.png", "", pngImage.Bytes()[:40], http.StatusBadRequest},
		{"document.png", "", []byte("<p>not an image</p>"), http.StatusBadRequest},
		{"large.png", "", make([]byte, crmOrderEvidenceMaxBytes+1), http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &bytes.Buffer{}
			writer := multipart.NewWriter(body)
			part, err := writer.CreateFormFile("image", tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write(tc.content); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/", body)
			c.Request.Header.Set("Content-Type", writer.FormDataContentType())

			got, media, status := readOrderConfirmationScreenshot(c)
			if status != tc.wantStatus || media != tc.wantType {
				t.Fatalf("status %d, media %q; wanted %d %q", status, media, tc.wantStatus, tc.wantType)
			}
			if status == http.StatusOK && !bytes.Equal(got, tc.content) {
				t.Fatal("the uploaded file was altered")
			}
		})
	}
}
