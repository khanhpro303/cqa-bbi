package channels

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPersonalZaloAttachmentArchiveStoresAndReusesCopies(t *testing.T) {
	root := t.TempDir()
	archive := NewPersonalZaloAttachmentArchive(root)
	photo := []byte("\x89PNG\r\n\x1a\nimage bytes")
	file := []byte("%PDF-1.7 document bytes")
	requests := 0
	archive.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		data := photo
		if r.URL.Path == "/file" {
			data = file
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/octet-stream"}},
			ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data)),
		}, nil
	})
	ctx := context.Background()
	for _, tc := range []struct {
		attachment Attachment
		extension  string
		data       []byte
	}{
		{Attachment{Type: "image", URL: "https://photo.zdn.vn/photo?sign=original"}, ".png", photo},
		{Attachment{Type: "file", URL: "https://zbox.zadn.vn/file?sign=original", Name: "../../document.pdf"}, ".bin", file},
	} {
		t.Run(tc.attachment.Type, func(t *testing.T) {
			name, err := archive.Store(ctx, "tenant", "conversation", "message", 0, tc.attachment)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(name, "tenant/conversation/personal-zalo/") || filepath.Ext(name) != tc.extension {
				t.Fatalf("unexpected relative path %q", name)
			}
			data, err := os.ReadFile(filepath.Join(root, name))
			if err != nil || !bytes.Equal(data, tc.data) {
				t.Fatalf("archived data = %q, err = %v", data, err)
			}
			tc.attachment.URL = "https://zbox.zadn.vn/refreshed"
			reused, err := archive.Store(ctx, "tenant", "conversation", "message", 0, tc.attachment)
			if err != nil || reused != name {
				t.Fatalf("archive retry path = %q, err = %v", reused, err)
			}
		})
	}
	if requests != 2 {
		t.Fatalf("requests = %d, expected one per attachment", requests)
	}
}

func TestPersonalZaloAttachmentArchiveRejectsUnsafeSources(t *testing.T) {
	archive := NewPersonalZaloAttachmentArchive(t.TempDir())
	archive.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "zdn.vn" {
			t.Fatalf("requested unsafe host %q", r.URL.Host)
		}
		return &http.Response{
			StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://127.0.0.1/private"}},
			Body: io.NopCloser(strings.NewReader("")), ContentLength: 0,
		}, nil
	})
	for _, source := range []string{
		"http://photo.zdn.vn/image",
		"https://photo.zdn.vn:8443/image",
		"https://user@photo.zdn.vn/image",
		"https://photo.zdn.vn.example.com/image",
		"https://notzdn.vn/image",
		"https://zdn.vn/redirect?sign=private-token",
	} {
		t.Run(source, func(t *testing.T) {
			path, err := archive.Store(context.Background(), "tenant", "conversation", "message", 0, Attachment{Type: "image", URL: source})
			if path != "" || !errors.Is(err, ErrPersonalZaloAttachmentUnavailable) {
				t.Fatalf("path = %q, err = %v", path, err)
			}
			if strings.Contains(err.Error(), "private-token") {
				t.Fatalf("error exposed signed URL: %v", err)
			}
		})
	}
}

func TestPersonalZaloAttachmentArchiveRejectsIncompleteAndUnsafeContent(t *testing.T) {
	for _, tc := range []struct {
		name     string
		kind     string
		body     string
		length   int64
		status   int
		terminal bool
	}{
		{"expired URL", "file", "", 0, http.StatusGone, true},
		{"temporary CDN failure", "file", "", 0, http.StatusServiceUnavailable, false},
		{"active content presented as image", "image", "<svg></svg>", -1, http.StatusOK, true},
		{"limit with known length", "file", "", 33, http.StatusOK, true},
		{"limit with unknown length", "file", strings.Repeat("a", 33), -1, http.StatusOK, true},
		{"incomplete file", "file", "partial body", 20, http.StatusOK, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			archive := NewPersonalZaloAttachmentArchive(root)
			archive.maxBytes = 32
			archive.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), ContentLength: tc.length,
				}, nil
			})
			path, err := archive.Store(context.Background(), "tenant", "conversation", "message", 0, Attachment{
				Type: tc.kind, URL: "https://zbox.zadn.vn/file",
			})
			if path != "" || err == nil || errors.Is(err, ErrPersonalZaloAttachmentUnavailable) != tc.terminal {
				t.Fatalf("path = %q, err = %v", path, err)
			}
			files, err := filepath.Glob(filepath.Join(root, "tenant", "conversation", "personal-zalo", "*"))
			if err != nil || len(files) != 0 {
				t.Fatalf("partial copies remain: %v, err = %v", files, err)
			}
		})
	}
}

func TestPersonalZaloAttachmentArchiveDoesNotEscapeStorageRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "tenant")); err != nil {
		t.Fatal(err)
	}
	archive := NewPersonalZaloAttachmentArchive(root)
	archive.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("started download before rejecting traversal")
		return nil, nil
	})
	for _, tenant := range []string{"../outside", "tenant"} {
		path, err := archive.Store(context.Background(), tenant, "conversation", "message", 0, Attachment{
			Type: "file", URL: "https://zbox.zadn.vn/file",
		})
		if path != "" || err == nil {
			t.Fatalf("path = %q, err = %v", path, err)
		}
	}
}

func TestPersonalZaloAttachmentArchivePublishesOneCompleteConcurrentCopy(t *testing.T) {
	root := t.TempDir()
	archive := NewPersonalZaloAttachmentArchive(root)
	var started atomic.Int32
	ready := make(chan struct{})
	archive.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if started.Add(1) == 2 {
			close(ready)
		}
		select {
		case <-ready:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		data := strings.Repeat(r.URL.Query().Get("copy"), 1000)
		return &http.Response{
			StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(data)), ContentLength: int64(len(data)),
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type result struct {
		path string
		err  error
	}
	results := make(chan result, 2)
	for _, copyID := range []string{"a", "b"} {
		go func() {
			path, err := archive.Store(ctx, "tenant", "conversation", "message", 0, Attachment{
				Type: "file", URL: "https://zbox.zadn.vn/file?copy=" + copyID,
			})
			results <- result{path, err}
		}()
	}
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.path != second.path {
		t.Fatalf("concurrent results: %+v and %+v", first, second)
	}
	local := filepath.Join(root, first.path)
	data, err := os.ReadFile(local)
	if err != nil || (string(data) != strings.Repeat("a", 1000) && string(data) != strings.Repeat("b", 1000)) {
		t.Fatalf("archived byte count = %d, err = %v", len(data), err)
	}
	files, err := os.ReadDir(filepath.Dir(local))
	if err != nil || len(files) != 1 {
		t.Fatalf("archive directory entries: %v, err = %v", files, err)
	}
}
