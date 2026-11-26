package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSendDirectImageUsesMediaTemplate(t *testing.T) {
	var requestBody map[string]interface{}
	adapter := newTestZaloAdapter(ZaloOACredentials{AccessToken: "access"}, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v3.0/oa/message/cs" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return jsonResponse(`{"error":0,"message":"success"}`), nil
	})

	if err := adapter.SendDirectImage(context.Background(), " user-1 ", " attachment-1 "); err != nil {
		t.Fatalf("SendDirectImage: %v", err)
	}
	recipient := requestBody["recipient"].(map[string]interface{})
	if recipient["user_id"] != "user-1" {
		t.Fatalf("recipient = %#v", recipient)
	}
	message := requestBody["message"].(map[string]interface{})
	attachment := message["attachment"].(map[string]interface{})
	if attachment["type"] != "template" {
		t.Fatalf("attachment type = %v", attachment["type"])
	}
	payload := attachment["payload"].(map[string]interface{})
	if payload["template_type"] != "media" {
		t.Fatalf("template type = %v", payload["template_type"])
	}
	elements := payload["elements"].([]interface{})
	element := elements[0].(map[string]interface{})
	if element["media_type"] != "image" || element["attachment_id"] != "attachment-1" {
		t.Fatalf("element = %#v", element)
	}
}

func TestSendDirectImageRequiresIdentifiers(t *testing.T) {
	adapter := NewZaloOAAdapter(ZaloOACredentials{})
	if err := adapter.SendDirectImage(context.Background(), "", "attachment"); err == nil {
		t.Fatal("expected empty user ID to fail")
	}
	if err := adapter.SendDirectImage(context.Background(), "user", " "); err == nil {
		t.Fatal("expected empty attachment ID to fail")
	}
}

func TestFetchPublicImageReturnsValidatedRasterImage(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 32)...)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"image/png"}},
			Body:       io.NopCloser(bytes.NewReader(png)),
			Request:    r,
		}, nil
	})}

	image, err := fetchPublicImageWithClient(context.Background(), "https://images.example.com/catalog/Ao%20Thun.jpeg", client)
	if err != nil {
		t.Fatalf("fetchPublicImageWithClient: %v", err)
	}
	if image.MediaType != "image/png" || image.FileName != "AoThun.png" || !bytes.Equal(image.Bytes, png) {
		t.Fatalf("unexpected image: type=%q name=%q len=%d", image.MediaType, image.FileName, len(image.Bytes))
	}
}

func TestFetchPublicImageRejectsNonImageResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/html"}},
			Body:       io.NopCloser(strings.NewReader("<html>not an image</html>")),
			Request:    r,
		}, nil
	})}

	if _, err := fetchPublicImageWithClient(context.Background(), "https://images.example.com/product", client); err == nil {
		t.Fatal("expected non-image response to fail")
	}
}

func TestFetchPublicImageDoesNotExposeSignedURLInError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("temporary transport failure")
	})}
	const secret = "private-signature-value"

	_, err := fetchPublicImageWithClient(context.Background(), "https://images.example.com/product.png?signature="+secret, client)
	if err == nil {
		t.Fatal("expected transport error")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "images.example.com") {
		t.Fatalf("error exposed request URL: %q", err)
	}
}

func TestFetchPublicImageRejectsOversizeResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			ContentLength: maxRemoteProductImageBytes + 1,
			Body:          io.NopCloser(strings.NewReader("unused")),
			Request:       r,
		}, nil
	})}

	if _, err := fetchPublicImageWithClient(context.Background(), "https://images.example.com/product.png", client); err == nil {
		t.Fatal("expected oversized response to fail")
	}
}

func TestPublicImageURLRequiresHTTPSDefaultPort(t *testing.T) {
	invalid := []string{
		"http://images.example.com/product.jpg",
		"https://images.example.com:8443/product.jpg",
		"https://user:secret@images.example.com/product.jpg",
		"https://127.0.0.1/product.jpg",
	}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("invalid URL must be rejected before an HTTP request")
		return nil, nil
	})}
	for _, rawURL := range invalid {
		if _, err := fetchPublicImageWithClient(context.Background(), rawURL, client); err == nil {
			t.Errorf("expected %q to fail", rawURL)
		}
	}
}
