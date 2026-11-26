package channels

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	maxRemoteProductImageBytes = 5 << 20
	maxRemoteImageRedirects    = 3
	remoteImageTimeout         = 20 * time.Second
)

// RemoteImage is a validated raster image fetched from a public HTTPS URL.
// Its filename is safe to pass to UploadGroupImage.
type RemoteImage struct {
	Bytes     []byte
	FileName  string
	MediaType string
}

// SendDirectImage sends an uploaded image to a 1:1 Zalo OA customer-support
// conversation. attachmentID must come from the OA upload/image endpoint.
func (z *ZaloOAAdapter) SendDirectImage(ctx context.Context, userID, attachmentID string) error {
	userID = strings.TrimSpace(userID)
	attachmentID = strings.TrimSpace(attachmentID)
	if userID == "" || attachmentID == "" {
		return errors.New("zalo direct image requires user and attachment IDs")
	}
	payload := map[string]interface{}{
		"recipient": map[string]interface{}{"user_id": userID},
		"message": map[string]interface{}{
			"attachment": map[string]interface{}{
				"type": "template",
				"payload": map[string]interface{}{
					"template_type": "media",
					"elements": []map[string]interface{}{
						{
							"media_type":    "image",
							"attachment_id": attachmentID,
						},
					},
				},
			},
		},
	}
	return z.sendCSPayload(ctx, payload)
}

// FetchPublicImage retrieves a catalog image without allowing access to local
// or private network addresses. Redirect targets and DNS results are validated
// on every connection, and only small raster images are returned.
func FetchPublicImage(ctx context.Context, rawURL string) (RemoteImage, error) {
	client := &http.Client{
		Timeout: remoteImageTimeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialPublicAddress,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          4,
			MaxIdleConnsPerHost:   2,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRemoteImageRedirects {
				return errors.New("product image redirected too many times")
			}
			return validatePublicImageURL(req.URL)
		},
	}
	defer client.CloseIdleConnections()
	return fetchPublicImageWithClient(ctx, rawURL, client)
}

func fetchPublicImageWithClient(ctx context.Context, rawURL string, client *http.Client) (RemoteImage, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || validatePublicImageURL(u) != nil {
		return RemoteImage{}, errors.New("product image must use a public HTTPS URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return RemoteImage{}, errors.New("could not build product image request")
	}
	req.Header.Set("Accept", "image/jpeg, image/png, image/webp")
	req.Header.Set("User-Agent", "CQA-Product-Image/1.0")

	resp, err := client.Do(req)
	if err != nil {
		// http.Client wraps transport failures in url.Error, whose Error string
		// includes the complete request URL. Catalog CDN links may carry signed
		// query parameters, so keep them out of worker logs and API responses.
		return RemoteImage{}, errors.New("could not fetch product image")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return RemoteImage{}, fmt.Errorf("product image returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxRemoteProductImageBytes {
		return RemoteImage{}, errors.New("product image is larger than 5 MiB")
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRemoteProductImageBytes+1))
	if err != nil {
		return RemoteImage{}, errors.New("could not read product image")
	}
	if len(data) == 0 {
		return RemoteImage{}, errors.New("product image is empty")
	}
	if len(data) > maxRemoteProductImageBytes {
		return RemoteImage{}, errors.New("product image is larger than 5 MiB")
	}

	mediaType := http.DetectContentType(data)
	extension, allowed := allowedProductImageTypes[mediaType]
	if !allowed {
		return RemoteImage{}, errors.New("product image must be JPEG, PNG, or WebP")
	}
	if declared, _, parseErr := mime.ParseMediaType(resp.Header.Get("Content-Type")); parseErr == nil &&
		strings.HasPrefix(declared, "image/") && declared != mediaType {
		return RemoteImage{}, errors.New("product image content does not match its media type")
	}

	finalURL := u
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL
	}
	return RemoteImage{
		Bytes:     data,
		FileName:  productImageFileName(finalURL, extension),
		MediaType: mediaType,
	}, nil
}

var allowedProductImageTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

func validatePublicImageURL(u *url.URL) error {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || u.Fragment != "" {
		return errors.New("invalid product image URL")
	}
	if port := u.Port(); port != "" && port != "443" {
		return errors.New("product image URL uses an unsupported port")
	}
	if strings.ContainsAny(u.Hostname(), "\\\x00") {
		return errors.New("invalid product image host")
	}
	if ip, err := netip.ParseAddr(strings.Trim(u.Hostname(), "[]")); err == nil && !isPublicImageAddress(ip) {
		return errors.New("product image host is not public")
	}
	return nil
}

func dialPublicAddress(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("invalid product image address")
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("could not resolve product image host")
	}
	for _, ip := range ips {
		if !isPublicImageAddress(ip) {
			return nil, errors.New("product image host resolved to a non-public address")
		}
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	var lastErr error
	for _, ip := range ips {
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	return nil, fmt.Errorf("could not connect to product image host: %w", lastErr)
}

func isPublicImageAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	sharedIPv4 := netip.MustParsePrefix("100.64.0.0/10")
	return ip.IsValid() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !sharedIPv4.Contains(ip)
}

func productImageFileName(source *url.URL, extension string) string {
	base := strings.TrimSpace(path.Base(source.Path))
	base = strings.TrimSuffix(base, path.Ext(base))
	var clean strings.Builder
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			clean.WriteRune(r)
		}
	}
	if clean.Len() == 0 {
		return "product-image" + extension
	}
	return clean.String() + extension
}
