package handlers

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func knowledgeTestPNG(t *testing.T) []byte {
	t.Helper()
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 5, 7))); err != nil {
		t.Fatal(err)
	}
	return imageData.Bytes()
}

func knowledgeImageMultipart(t *testing.T, files [][]byte, captions string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, data := range files {
		file, err := w.CreateFormFile("images", "Sơ đồ.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if captions != "" {
		if err := w.WriteField("captions", captions); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, w.FormDataContentType()
}

func TestKnowledgeImageUploadValidation(t *testing.T) {
	validPNG := knowledgeTestPNG(t)
	tooManyPixels := bytes.Clone(validPNG)
	// Adjust the IHDR width, height and CRC while leaving the small pixel payload.
	binary.BigEndian.PutUint32(tooManyPixels[16:20], 5001)
	binary.BigEndian.PutUint32(tooManyPixels[20:24], 4000)
	binary.BigEndian.PutUint32(tooManyPixels[29:33], crc32.ChecksumIEEE(tooManyPixels[12:29]))

	for _, tc := range []struct {
		name     string
		files    [][]byte
		captions string
		code     string
		status   int
	}{
		{"empty", nil, "", "invalid_image_count", http.StatusBadRequest},
		{"too many files", make([][]byte, maxKnowledgeImageUploadFiles+1), "", "invalid_image_count", http.StatusBadRequest},
		{"invalid captions", [][]byte{validPNG}, `[]`, "invalid_captions", http.StatusBadRequest},
		{"long Vietnamese caption", [][]byte{validPNG}, `["` + strings.Repeat("ế", maxKnowledgeImageCaption+1) + `"]`, "caption_too_long", http.StatusBadRequest},
		{"SVG renamed as PNG", [][]byte{[]byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)}, "", "unsupported_image_type", http.StatusBadRequest},
		{"corrupt PNG", [][]byte{validPNG[:len(validPNG)-15]}, "", "invalid_image", http.StatusBadRequest},
		{"pixel cap", [][]byte{tooManyPixels}, "", "image_dimensions_too_large", http.StatusBadRequest},
		{"individual byte cap", [][]byte{make([]byte, maxKnowledgeImageBytes+1)}, "", "image_too_large", http.StatusRequestEntityTooLarge},
		{"request byte cap", [][]byte{make([]byte, maxKnowledgeImageUploadRequest)}, "", "request_too_large", http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, contentType := knowledgeImageMultipart(t, tc.files, tc.captions)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/", body)
			c.Request.Header.Set("Content-Type", contentType)
			UploadKnowledgeDocumentImages(c)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"error":"`+tc.code+`"`) {
				t.Fatalf("got %d %s, want %d %s", rec.Code, rec.Body.String(), tc.status, tc.code)
			}
		})
	}

	body, contentType := knowledgeImageMultipart(t, [][]byte{validPNG}, "")
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", contentType)
	if err := req.ParseMultipartForm(1024); err != nil {
		t.Fatal(err)
	}
	defer req.MultipartForm.RemoveAll()
	img, err := readKnowledgeImageUpload(req.MultipartForm.File["images"][0], "Chú thích mẫu")
	if err != nil || img.Width != 5 || img.Height != 7 || img.MediaType != "image/png" ||
		img.FileName != "Sơ đồ.png" || img.Caption != "Chú thích mẫu" || !img.Enabled || !bytes.Equal(img.Data, validPNG) {
		t.Fatalf("unexpected parsed image: %+v, %v", img, err)
	}
}
