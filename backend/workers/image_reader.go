package workers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/customercases"
	"github.com/vietbui/chat-quality-agent/engine"
)

const (
	imageReadTimeout   = 25 * time.Second
	imageMaxBytes      = 8 << 20
	imageMaxPerMessage = 4
)

// inboundImage is one image a customer sent and what the AI read in it.
type inboundImage struct {
	ImageID     string
	URL         string
	Description string
}

// imageURLs returns the image URLs of a webhook message.
func imageURLs(atts []ZaloWebhookAttachment) []string {
	var out []string
	for _, att := range atts {
		if att.Type != "image" {
			continue
		}
		u := strings.TrimSpace(att.Payload.URL)
		if u == "" {
			u = strings.TrimSpace(att.Payload.Thumbnail)
		}
		// Only Zalo CDN URLs are kept: anything else is neither fetched nor
		// stored (staff browsers would load it from the case page).
		if u != "" && allowedImageURL(u) && len(out) < imageMaxPerMessage {
			out = append(out, u)
		}
	}
	return out
}

// allowedImageURL reports whether u is an https URL on a Zalo image host.
func allowedImageURL(u string) bool { return customercases.AllowedImageURL(u) }

// publicAddress reports whether ip is a public unicast address: the image
// host's name may resolve anywhere, so the connection itself is checked.
func publicAddress(ip net.IP) bool {
	return ip != nil && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsUnspecified() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsInterfaceLocalMulticast() &&
		!ip.IsMulticast() && !cgnat.Contains(ip)
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// imageDialer refuses connections to non-public addresses, checked on the
// address actually dialled (after DNS), so a Zalo-looking host name that
// resolves to the server's own network is not reachable.
var imageDialer = &net.Dialer{
	Timeout: 10 * time.Second,
	Control: func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		if !publicAddress(net.ParseIP(host)) {
			return fmt.Errorf("image host resolves to a non-public address %s", host)
		}
		return nil
	},
}

var imageHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
	// No proxy: the dialer's address check must see the image host itself.
	Transport: &http.Transport{
		Proxy:                 nil,
		DialContext:           imageDialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          10,
		IdleConnTimeout:       60 * time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !allowedImageURL(req.URL.String()) {
			return errors.New("redirect to a non-Zalo host refused")
		}
		return nil
	},
}

// downloadImage fetches an image from Zalo's CDN.
var downloadImage = func(ctx context.Context, u string) (ai.ImageInput, error) {
	if !allowedImageURL(u) {
		return ai.ImageInput{}, fmt.Errorf("image host not allowed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ai.ImageInput{}, err
	}
	resp, err := imageHTTPClient.Do(req)
	if err != nil {
		return ai.ImageInput{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ai.ImageInput{}, fmt.Errorf("image download status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, imageMaxBytes+1))
	if err != nil {
		return ai.ImageInput{}, err
	}
	if len(data) > imageMaxBytes {
		return ai.ImageInput{}, fmt.Errorf("image larger than %d bytes", imageMaxBytes)
	}
	mediaType := sniffImageType(data)
	if mediaType == "" {
		return ai.ImageInput{}, fmt.Errorf("not a supported image (%s)", http.DetectContentType(data))
	}
	return ai.ImageInput{MediaType: mediaType, Data: data}, nil
}

// sniffImageType returns the media type of a JPEG, PNG, GIF or WebP image,
// or "" for anything else.
func sniffImageType(data []byte) string {
	switch ct := http.DetectContentType(data); ct {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return ct
	}
	return ""
}

// describeImage asks the tenant's AI model what an image shows, with the
// editable prompt chatbot_image_reader. Replaced in tests.
var describeImage = func(ctx context.Context, tenantID, channelID string, img ai.ImageInput, caption string) (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	resolved, err := engine.ResolveTenantAIProvider(tenantID, cfg, engine.PromptImageReader)
	if err != nil {
		return "", err
	}
	vision, ok := resolved.Client.(ai.VisionProvider)
	if resolved.Client == nil || !ok {
		return "", errors.New("tenant AI provider cannot read images")
	}
	text := "Lời khách: (khách không nhắn kèm)"
	if c := strings.TrimSpace(caption); c != "" {
		text = "Lời khách: " + c
	}
	resp, err := vision.AnalyzeImages(ctx, engine.BuildTaskPrompt(tenantID, engine.PromptImageReader, nil), text, []ai.ImageInput{img})
	if err != nil {
		return "", err
	}
	customercases.LogUsage(tenantID, channelID, resolved.Provider, resolved.Model, resp)
	return strings.TrimSpace(resp.Content), nil
}

// readInboundImages downloads and reads the images of a message, in
// parallel. An image that cannot be read keeps its URL with no description.
func readInboundImages(ctx context.Context, tenantID, channelID string, urls []string, caption string, archive func(context.Context, string, ai.ImageInput) (string, error)) []inboundImage {
	if len(urls) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, imageReadTimeout)
	defer cancel()
	out := make([]inboundImage, len(urls))
	var wg sync.WaitGroup
	for i, u := range urls {
		out[i].URL = u
		wg.Add(1)
		go func(i int, u string) {
			defer wg.Done()
			img, err := downloadImage(ctx, u)
			if err != nil {
				log.Printf("[image-reader] download failed: %v", err)
				return
			}
			if archive != nil {
				archiveCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				id, err := archive(archiveCtx, u, img)
				cancel()
				if err != nil {
					log.Printf("[image-reader] could not store image (%T)", err)
				}
				out[i].ImageID = id
			}
			desc, err := describeImage(ctx, tenantID, channelID, img, caption)
			if err != nil {
				log.Printf("[image-reader] read failed: %v", err)
				return
			}
			out[i].Description = truncateRunes(desc, 1200)
		}(i, u)
	}
	wg.Wait()
	return out
}

// imagesText is what the bot reads for the images in a message.
func imagesText(images []inboundImage) string {
	var lines []string
	for _, im := range images {
		desc := strings.Join(strings.Fields(im.Description), " ")
		if desc == "" {
			desc = "chưa đọc được nội dung ảnh"
		}
		lines = append(lines, "[Ảnh khách gửi: "+desc+"]")
	}
	return strings.Join(lines, "\n")
}

// imageAttachments is how the images are stored on the recorded message.
func imageAttachments(images []inboundImage) []customercases.ImageAttachment {
	out := make([]customercases.ImageAttachment, 0, len(images))
	for _, im := range images {
		out = append(out, customercases.ImageAttachment{Type: "image", URL: im.URL, Description: im.Description, ImageID: im.ImageID})
	}
	return out
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
