package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

const (
	ParserFirecrawl     = "firecrawl"
	firecrawlAPI        = "https://api.firecrawl.dev/v2"
	firecrawlTimeout    = 60 * time.Second
	firecrawlHTTPBudget = firecrawlTimeout + 5*time.Second
)

var errInvalidSourceURL = errors.New("enter a public HTTP or HTTPS URL")

// ParseSourceURL accepts links to public websites. CQA passes the URL as data
// to Firecrawl and only connects to Firecrawl's fixed API host.
func ParseSourceURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > 2048 || strings.Contains(raw, "\\") {
		return nil, errInvalidSourceURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return nil, errInvalidSourceURL
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		sharedIPv4 := netip.PrefixFrom(netip.AddrFrom4([4]byte{100, 64, 0, 0}), 10)
		if ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() || sharedIPv4.Contains(ip) {
			return nil, errInvalidSourceURL
		}
	} else {
		host, err = idna.Lookup.ToASCII(host)
		if err != nil || !strings.Contains(host, ".") || strings.HasSuffix(host, ".localhost") ||
			strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".home.arpa") {
			return nil, errInvalidSourceURL
		}
		if port := u.Port(); port != "" {
			u.Host = net.JoinHostPort(host, port)
		} else {
			u.Host = host
		}
	}
	if len(u.String()) > 2048 {
		return nil, errInvalidSourceURL
	}
	return u, nil
}

// FirecrawlClient calls the hosted Firecrawl v2 API with a tenant's key.
type FirecrawlClient struct {
	key    string
	client *http.Client
}

var firecrawlHTTPClient = &http.Client{
	Timeout: firecrawlHTTPBudget,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func NewFirecrawlClient(key string) *FirecrawlClient {
	return &FirecrawlClient{key: key, client: firecrawlHTTPClient}
}

// Check validates the key with the team's credit-usage endpoint without scraping.
func (f *FirecrawlClient) Check(ctx context.Context) error {
	_, err := f.request(ctx, http.MethodGet, "/team/credit-usage", nil, 64<<10)
	return err
}

func (f *FirecrawlClient) scrape(ctx context.Context, sourceURL string) (text, title string, err error) {
	payload, err := json.Marshal(struct {
		URL             string   `json:"url"`
		Formats         []string `json:"formats"`
		OnlyMainContent bool     `json:"onlyMainContent"`
		Timeout         int64    `json:"timeout"`
		MaxAge          int      `json:"maxAge"`
	}{sourceURL, []string{"markdown"}, true, firecrawlTimeout.Milliseconds(), 0})
	if err != nil {
		return "", "", err
	}
	data, err := f.request(ctx, http.MethodPost, "/scrape", payload, 16<<20)
	if err != nil {
		return "", "", err
	}

	var page struct {
		Markdown string `json:"markdown"`
		Metadata struct {
			StatusCode int    `json:"statusCode"`
			Title      string `json:"title"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		return "", "", errors.New("Firecrawl returned an invalid page response")
	}
	if code := page.Metadata.StatusCode; code != 0 && code != http.StatusNotModified && (code < 200 || code >= 300) {
		return "", "", fmt.Errorf("Firecrawl: source page returned HTTP %d", code)
	}
	text = NormalizeText(page.Markdown)
	if text == "" {
		return "", "", errors.New("Firecrawl returned no readable text")
	}
	if utf8.RuneCountInString(text) > MaxTextChars {
		return "", "", fmt.Errorf("Firecrawl: document is longer than %d characters", MaxTextChars)
	}
	return text, strings.TrimSpace(page.Metadata.Title), nil
}

func (f *FirecrawlClient) request(ctx context.Context, method, path string, payload []byte, limit int64) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, method, firecrawlAPI+path, bytes.NewReader(payload))
	if err != nil {
		return nil, errors.New("could not build Firecrawl request")
	}
	req.Header.Set("Authorization", "Bearer "+f.key)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := f.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, errors.New("Firecrawl request timed out")
		}
		return nil, errors.New("could not connect to Firecrawl")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, errors.New("Firecrawl rejected the API key or its permissions")
	case http.StatusPaymentRequired:
		return nil, errors.New("Firecrawl credits are exhausted")
	case http.StatusTooManyRequests:
		return nil, errors.New("Firecrawl rate limit reached")
	default:
		return nil, fmt.Errorf("Firecrawl returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, errors.New("could not read Firecrawl response")
	}
	if int64(len(body)) > limit {
		return nil, errors.New("Firecrawl response is too large")
	}
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, errors.New("Firecrawl returned an invalid response")
	}
	if !envelope.Success || len(envelope.Data) == 0 || bytes.Equal(envelope.Data, []byte("null")) {
		return nil, errors.New("Firecrawl did not complete the request")
	}
	return envelope.Data, nil
}
