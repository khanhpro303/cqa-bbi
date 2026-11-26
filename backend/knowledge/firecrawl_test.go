package knowledge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type firecrawlTransport func(*http.Request) (*http.Response, error)

func (f firecrawlTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func firecrawlStub(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}

func TestParseSourceURL(t *testing.T) {
	for input, want := range map[string]string{
		" https://Example.com/bao-hanh?lang=vi#chinh-sach ": "https://example.com/bao-hanh?lang=vi#chinh-sach",
		"http://ví-dụ.vn/path":                              "http://xn--v-d-rma6749a.vn/path",
	} {
		got, err := ParseSourceURL(input)
		if err != nil || got.String() != want {
			t.Errorf("ParseSourceURL(%q) = %v, %v; want %q", input, got, err, want)
		}
	}
	withCredentials := url.URL{Scheme: "https", Host: "example.com", User: url.UserPassword("fixture-user", "fixture-password")}
	for _, input := range []string{
		"", "file:///etc/hosts", "javascript:alert(1)", withCredentials.String(),
		"https://localhost", "https://host.localhost", "http://assets.internal",
		"http://127.0.0.1", "http://169.254.169.254", "http://100.64.0.1",
		"http://[::ffff:10.0.0.1]", "https://example.com/" + strings.Repeat("a", 2048),
	} {
		if _, err := ParseSourceURL(input); err == nil {
			t.Errorf("accepted invalid source %q", input)
		}
	}
}

func TestFirecrawlScrapeContract(t *testing.T) {
	client := NewFirecrawlClient("fc-test")
	client.client = &http.Client{Transport: firecrawlTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.String() != "https://api.firecrawl.dev/v2/scrape" ||
			r.Header.Get("Authorization") != "Bearer fc-test" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected request: %v", r)
		}
		var body struct {
			URL             string   `json:"url"`
			Formats         []string `json:"formats"`
			OnlyMainContent bool     `json:"onlyMainContent"`
			Timeout         int      `json:"timeout"`
			MaxAge          *int     `json:"maxAge"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.URL != "https://example.com/vi" || len(body.Formats) != 1 || body.Formats[0] != "markdown" ||
			!body.OnlyMainContent || body.Timeout != 60000 || body.MaxAge == nil || *body.MaxAge != 0 {
			t.Fatalf("unexpected request body: %+v", body)
		}
		return firecrawlStub(http.StatusOK, `{"success":true,"data":{"markdown":" # Bảo hành\r\n\r\n 12 tháng ","metadata":{"statusCode":200,"title":" Chính sách "}}}`), nil
	})}
	text, title, err := client.scrape(context.Background(), "https://example.com/vi")
	if err != nil || text != "# Bảo hành\n\n12 tháng" || title != "Chính sách" {
		t.Fatalf("scrape: %q, %q, %v", text, title, err)
	}
}

func TestFirecrawlScrapePageExtractsOnlySendableImageURLs(t *testing.T) {
	client := NewFirecrawlClient("fc-test")
	client.client = &http.Client{Transport: firecrawlTransport(func(*http.Request) (*http.Response, error) {
		return firecrawlStub(http.StatusOK, `{"success":true,"data":{"markdown":"Mô tả ![front](/images/front.webp) ![insecure](http://cdn.example.com/no.jpg) ![fragment](https://cdn.example.com/side.png#crop)","metadata":{"statusCode":200,"title":"Mũ","ogImage":"https://cdn.example.com/cover.jpg"}}}`), nil
	})}
	page, err := client.ScrapePage(context.Background(), "https://shop.example.com/products/helmet")
	if err != nil {
		t.Fatal(err)
	}
	wantImages := []string{"https://cdn.example.com/cover.jpg", "https://shop.example.com/images/front.webp"}
	if strings.Contains(page.Text, "front.webp") || len(page.Images) != len(wantImages) {
		t.Fatalf("page = %+v", page)
	}
	for i := range wantImages {
		if page.Images[i] != wantImages[i] {
			t.Fatalf("images = %#v, want %#v", page.Images, wantImages)
		}
	}
}

func TestFirecrawlScrapeRejectsErrors(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"auth", 401, `{"error":"fc-test"}`, "rejected"},
		{"credits", 402, "", "credits"},
		{"rate", 429, "", "rate limit"},
		{"response format", 200, "<html/>", "invalid response"},
		{"api failure", 200, `{"success":false,"error":"fc-test"}`, "did not complete"},
		{"page failure", 200, `{"success":true,"data":{"markdown":"Not found","metadata":{"statusCode":404}}}`, "source page returned HTTP 404"},
		{"empty page", 200, `{"success":true,"data":{"markdown":" "}}`, "no readable text"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := NewFirecrawlClient("fc-test")
			client.client = &http.Client{Transport: firecrawlTransport(func(*http.Request) (*http.Response, error) {
				return firecrawlStub(tt.status, tt.body), nil
			})}
			_, _, err := client.scrape(context.Background(), "https://example.com/")
			if err == nil || !strings.Contains(err.Error(), tt.want) || strings.Contains(err.Error(), "fc-test") {
				t.Fatalf("scrape error = %v, want %q without the key", err, tt.want)
			}
		})
	}
}

func TestFirecrawlCheckUsesCreditEndpoint(t *testing.T) {
	client := NewFirecrawlClient("fc-test")
	client.client = &http.Client{Transport: firecrawlTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.String() != "https://api.firecrawl.dev/v2/team/credit-usage" ||
			r.Header.Get("Authorization") != "Bearer fc-test" {
			t.Fatalf("unexpected check request: %v", r)
		}
		return firecrawlStub(http.StatusOK, `{"success":true,"data":{"remainingCredits":0}}`), nil
	})}
	if err := client.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
}
