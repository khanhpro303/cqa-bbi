package channels

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// PersonalZaloAttachmentDirectory identifies personal Zalo copies for the
	// authenticated file-serving route.
	PersonalZaloAttachmentDirectory = "personal-zalo"
	personalZaloAttachmentByteLimit = 100 << 20
)

// ErrPersonalZaloAttachmentUnavailable indicates that the source cannot be
// archived. Network and storage failures are reported separately for retry.
var ErrPersonalZaloAttachmentUnavailable = errors.New("personal Zalo attachment unavailable")

var personalZaloImageExtensions = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
	"image/gif":  ".gif",
	"image/bmp":  ".bmp",
}

// PersonalZaloAttachmentArchive stores copies in the existing conversation
// file directory. Files use a .bin suffix for download-only serving.
type PersonalZaloAttachmentArchive struct {
	root     string
	client   *http.Client
	maxBytes int64
}

func NewPersonalZaloAttachmentArchive(root string) *PersonalZaloAttachmentArchive {
	if root == "" {
		root = "/var/lib/cqa/files"
	}
	return &PersonalZaloAttachmentArchive{
		root:     root,
		maxBytes: personalZaloAttachmentByteLimit,
		client: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				DialContext:           dialPublicAddress,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          16,
				MaxIdleConnsPerHost:   4,
				IdleConnTimeout:       60 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 10 * time.Second,
			},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 4 || !allowedPersonalZaloAttachmentURL(req.URL) {
					return ErrPersonalZaloAttachmentUnavailable
				}
				req.Header.Del("Referer")
				return nil
			},
		},
	}
}

// AllowedPersonalZaloAttachmentURL reports whether a source URL is on a Zalo
// host and uses HTTPS on the standard port.
func AllowedPersonalZaloAttachmentURL(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	return err == nil && allowedPersonalZaloAttachmentURL(u)
}

func allowedPersonalZaloAttachmentURL(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, suffix := range []string{"zdn.vn", "zadn.vn", "zalo.me", "zaloapp.com", "zalo.vn"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

// Store copies the attachment to disk and returns a path relative to the
// configured file directory. Retries reuse a completed archive for this slot.
func (a *PersonalZaloAttachmentArchive) Store(ctx context.Context, tenantID, conversationID, externalMessageID string, index int, att Attachment) (string, error) {
	if !archivePathSegment(tenantID) || !archivePathSegment(conversationID) || externalMessageID == "" || index < 0 {
		return "", errors.New("invalid personal Zalo attachment identifiers")
	}
	if att.Type != "file" && att.Type != "image" {
		return "", ErrPersonalZaloAttachmentUnavailable
	}
	if err := os.MkdirAll(a.root, 0750); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(a.root)
	if err != nil {
		return "", err
	}
	defer root.Close()

	dir := filepath.Join(tenantID, conversationID, PersonalZaloAttachmentDirectory)
	key := sha256.Sum256([]byte(externalMessageID + ":" + strconv.Itoa(index)))
	base := filepath.Join(dir, fmt.Sprintf("%x", key))
	if att.Type == "file" {
		if archived, err := existingPersonalZaloArchive(root, base+".bin"); archived != "" || err != nil {
			return archived, err
		}
	} else {
		for _, ext := range personalZaloImageExtensions {
			if archived, err := existingPersonalZaloArchive(root, base+ext); archived != "" || err != nil {
				return archived, err
			}
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(att.URL), nil)
	if err != nil || !allowedPersonalZaloAttachmentURL(req.URL) {
		return "", ErrPersonalZaloAttachmentUnavailable
	}
	resp, err := a.client.Do(req)
	if err != nil {
		if errors.Is(err, ErrPersonalZaloAttachmentUnavailable) {
			return "", ErrPersonalZaloAttachmentUnavailable
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		// HTTP errors may contain the source URL and its signed query.
		return "", errors.New("personal Zalo attachment request failed")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone:
		return "", fmt.Errorf("%w (HTTP %d)", ErrPersonalZaloAttachmentUnavailable, resp.StatusCode)
	default:
		return "", fmt.Errorf("personal Zalo attachment request returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > a.maxBytes || resp.ContentLength == 0 {
		return "", ErrPersonalZaloAttachmentUnavailable
	}

	limited := &io.LimitedReader{R: resp.Body, N: a.maxBytes + 1}
	body := bufio.NewReader(limited)
	extension := ".bin"
	if att.Type == "image" {
		header, err := body.Peek(512)
		if err != nil && !errors.Is(err, io.EOF) {
			return "", errors.New("could not read personal Zalo image")
		}
		extension = personalZaloImageExtensions[http.DetectContentType(header)]
		if extension == "" {
			return "", ErrPersonalZaloAttachmentUnavailable
		}
	}
	return a.write(root, dir, base+extension, body, resp.ContentLength)
}

func (a *PersonalZaloAttachmentArchive) write(root *os.Root, dir, final string, body io.Reader, contentLength int64) (string, error) {
	if err := root.MkdirAll(dir, 0750); err != nil {
		return "", err
	}
	tmp := filepath.Join(dir, ".part-"+rand.Text())
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer root.Remove(tmp)
	written, copyErr := io.Copy(f, body)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		return "", errors.New("could not write complete personal Zalo attachment")
	}
	if written == 0 || written > a.maxBytes {
		return "", ErrPersonalZaloAttachmentUnavailable
	}
	if contentLength >= 0 && contentLength != written {
		return "", errors.New("could not write complete personal Zalo attachment")
	}
	// A hard link publishes the complete file without replacing a copy
	// published by a concurrent download of the same attachment.
	if err := root.Link(tmp, final); err != nil {
		if archived, _ := existingPersonalZaloArchive(root, final); !errors.Is(err, os.ErrExist) || archived == "" {
			return "", err
		}
	}
	return filepath.ToSlash(final), nil
}

func existingPersonalZaloArchive(root *os.Root, name string) (string, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "", errors.New("invalid existing personal Zalo archive file")
	}
	return filepath.ToSlash(name), nil
}

// RemoveConversation removes copies left by a download racing conversation deletion.
func (a *PersonalZaloAttachmentArchive) RemoveConversation(tenantID, conversationID string) error {
	if !archivePathSegment(tenantID) || !archivePathSegment(conversationID) {
		return errors.New("invalid personal Zalo archive directory")
	}
	root, err := os.OpenRoot(a.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	dir := filepath.Join(tenantID, conversationID)
	if err := root.RemoveAll(filepath.Join(dir, PersonalZaloAttachmentDirectory)); err != nil {
		return err
	}
	// Remove only empty parent directories. Other channel data may use them.
	_ = root.Remove(dir)
	_ = root.Remove(tenantID)
	return nil
}

func archivePathSegment(value string) bool {
	return value != "" && value != "." && value != ".." &&
		!strings.ContainsAny(value, "/\\\x00:")
}
