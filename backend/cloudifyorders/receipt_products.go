package cloudifyorders

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"gorm.io/gorm"
)

const (
	receiptImageTimeout = 3 * time.Second
	receiptImageBudget  = 8 * time.Second
	receiptImageWorkers = 4
	maxProductImageSize = 2 << 20
	maxProductPixels    = 4_000_000
	maxProductDimension = 4096
	productThumbnail    = 192
)

var receiptProductImageClient = &http.Client{
	Timeout: receiptImageTimeout,
	Transport: &http.Transport{
		DialContext:            dialReceiptImage,
		MaxIdleConns:           24,
		MaxConnsPerHost:        receiptImageWorkers,
		IdleConnTimeout:        30 * time.Second,
		MaxResponseHeaderBytes: 32 << 10,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || (via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https") {
			return errors.New("product image redirect rejected")
		}
		req.Header.Del("Referer")
		return validateReceiptImageURL(req.URL)
	},
}

// receiptProductImages resolves only the requested SKUs in the tenant's
// product cache. Individual missing and unavailable photos are optional.
func receiptProductImages(ctx context.Context, database *gorm.DB, tenantID string, lines []ReceiptLine) (map[string]image.Image, error) {
	images := make(map[string]image.Image)
	if tenantID == "" {
		return images, nil
	}
	seen := make(map[string]bool, len(lines))
	skus := make([]string, 0, len(lines))
	for _, line := range lines {
		sku := strings.TrimSpace(line.SKU)
		if sku != "" && !seen[sku] {
			seen[sku] = true
			skus = append(skus, sku)
		}
	}
	if len(skus) == 0 {
		return images, nil
	}

	ctx, cancel := context.WithTimeout(ctx, receiptImageBudget)
	defer cancel()
	var products []models.CachedProduct
	if err := database.WithContext(ctx).Select("ma", "link_anh").
		Where("tenant_id = ? AND ma IN ? AND link_anh <> ''", tenantID, skus).
		Order("updated_at DESC, id ASC").Find(&products).Error; err != nil {
		return nil, err
	}
	bySKU := make(map[string]string, len(skus))
	for _, product := range products {
		sku := strings.ToUpper(strings.TrimSpace(product.MA))
		if link := strings.TrimSpace(product.LINK_ANH); bySKU[sku] == "" && link != "" {
			bySKU[sku] = link
		}
	}

	// Preserve order of first use and download a shared URL only once.
	urls := make([]string, 0, len(skus))
	urlIndex := make(map[string]int, len(skus))
	for _, line := range lines {
		link := bySKU[strings.ToUpper(strings.TrimSpace(line.SKU))]
		if _, exists := urlIndex[link]; link != "" && !exists {
			urlIndex[link] = len(urls)
			urls = append(urls, link)
		}
	}
	thumbs := loadReceiptThumbnails(ctx, urls)
	for _, line := range lines {
		link := bySKU[strings.ToUpper(strings.TrimSpace(line.SKU))]
		if i, exists := urlIndex[link]; exists && thumbs[i] != nil {
			images[line.SKU] = thumbs[i]
		}
	}
	return images, nil
}

func loadReceiptThumbnails(ctx context.Context, urls []string) []image.Image {
	thumbs := make([]image.Image, len(urls))
	jobs := make(chan int, len(urls))
	for i := range urls {
		jobs <- i
	}
	close(jobs)

	var workers sync.WaitGroup
	for range min(receiptImageWorkers, len(urls)) {
		workers.Go(func() {
			for i := range jobs {
				if ctx.Err() != nil {
					return
				}
				thumbs[i] = fetchReceiptThumbnail(ctx, receiptProductImageClient, urls[i])
			}
		})
	}
	workers.Wait()
	return thumbs
}

func fetchReceiptThumbnail(ctx context.Context, client *http.Client, link string) image.Image {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil || validateReceiptImageURL(req.URL) != nil {
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ContentLength > maxProductImageSize {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxProductImageSize+1))
	if err != nil || len(data) > maxProductImageSize {
		return nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 ||
		cfg.Width > maxProductDimension || cfg.Height > maxProductDimension || cfg.Width > maxProductPixels/cfg.Height {
		return nil
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	bounds := src.Bounds()
	side := max(bounds.Dx(), bounds.Dy())
	if side <= productThumbnail {
		return src
	}
	width := max(1, bounds.Dx()*productThumbnail/side)
	height := max(1, bounds.Dy()*productThumbnail/side)
	thumb := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(thumb, thumb.Bounds(), src, bounds, draw.Src, nil)
	return thumb
}

func validateReceiptImageURL(u *url.URL) error {
	if u.Hostname() == "" || u.User != nil || strings.Contains(u.Hostname(), "%") {
		return errors.New("product image URL requires a public host")
	}
	port := u.Port()
	switch {
	case u.Scheme == "http" && (port == "" || port == "80"):
		return nil
	case u.Scheme == "https" && (port == "" || port == "443"):
		return nil
	default:
		return errors.New("product image URL requires HTTP or HTTPS on the default port")
	}
}

// Use the same resolved address for validation and dialing, including after
// redirects. The transport does not read proxy settings from the environment.
func dialReceiptImage(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: 2 * time.Second}
	lastErr := errors.New("product image has no reachable public address")
	for _, addr := range addresses {
		ip := addr.Unmap()
		if !publicReceiptImageIP(ip) {
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

var (
	publicImageIPv6Prefix = netip.MustParsePrefix("2000::/3")
	reservedImageNetworks = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("168.63.129.16/32"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("192.88.99.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("2001::/23"),
		netip.MustParsePrefix("2001:db8::/32"),
		netip.MustParsePrefix("2002::/16"),
		netip.MustParsePrefix("3fff::/20"),
	}
)

func publicReceiptImageIP(ip netip.Addr) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || (ip.Is6() && !publicImageIPv6Prefix.Contains(ip)) {
		return false
	}
	for _, prefix := range reservedImageNetworks {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}
