package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// Unstructured (https://unstructured.io) reads documents the way the old
// Langflow ingestion flows did: layout-aware PDF parsing, tables, OCR of
// scanned pages and images, Office formats. CQA calls its partition API
// when the tenant has set it up in AI Engine; the built-in Go extractor in
// extract.go is the fallback.

// DefaultUnstructuredURL is the hosted (serverless) partition endpoint.
const DefaultUnstructuredURL = "https://api.unstructuredapp.io/general/v0/general"

// app_settings keys.
const (
	SettingUnstructuredURL       = "knowledge_unstructured_url"
	SettingUnstructuredKey       = "knowledge_unstructured_key" // encrypted
	SettingUnstructuredStrategy  = "knowledge_unstructured_strategy"
	SettingUnstructuredLanguages = "knowledge_unstructured_languages"
)

// Parser names stored on a document.
const (
	ParserUnstructured = "unstructured"
	ParserLocal        = "local"
)

// UnstructuredExtensions are the types sent to Unstructured. Images and
// legacy Office files are only readable when Unstructured is set up.
var UnstructuredExtensions = []string{
	".pdf", ".docx", ".doc", ".odt", ".rtf", ".pptx", ".ppt", ".xlsx", ".xls", ".csv", ".tsv",
	".html", ".htm", ".md", ".txt", ".epub", ".eml", ".msg",
	".png", ".jpg", ".jpeg", ".tiff", ".tif", ".bmp", ".heic",
}

// ParserSettings is a tenant's Unstructured setup.
type ParserSettings struct {
	APIURL    string `json:"api_url"`
	APIKey    string `json:"-"`
	Strategy  string `json:"strategy"`  // auto | fast | hi_res | ocr_only
	Languages string `json:"languages"` // comma separated ISO 639-3, e.g. vie,eng
}

// Enabled reports whether documents go to Unstructured: a key for the hosted
// API, or any URL for a self-hosted one (which needs no key).
func (p ParserSettings) Enabled() bool {
	if p.APIKey != "" {
		return true
	}
	u := strings.TrimSpace(p.APIURL)
	return u != "" && u != DefaultUnstructuredURL
}

func (p ParserSettings) url() string {
	if u := strings.TrimSpace(p.APIURL); u != "" {
		return u
	}
	return DefaultUnstructuredURL
}

func (p ParserSettings) strategy() string {
	switch p.Strategy {
	case "fast", "hi_res", "ocr_only", "auto":
		return p.Strategy
	}
	return "auto"
}

func (p ParserSettings) languages() []string {
	raw := p.Languages
	if strings.TrimSpace(raw) == "" {
		raw = "vie,eng"
	}
	var out []string
	for _, l := range strings.Split(raw, ",") {
		if l = strings.TrimSpace(strings.ToLower(l)); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// ValidateUnstructuredURL accepts an http(s) URL without credentials.
func ValidateUnstructuredURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return errors.New("Unstructured URL must be an http(s) URL")
	}
	return nil
}

// LoadParserSettings reads the tenant's settings, falling back to the
// UNSTRUCTURED_API_URL / UNSTRUCTURED_API_KEY environment variables.
func LoadParserSettings(tenantID, encryptionKey string) ParserSettings {
	get := func(key string) models.AppSetting {
		var s models.AppSetting
		if db.DB != nil {
			db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, key).First(&s)
		}
		return s
	}
	p := ParserSettings{
		APIURL:    get(SettingUnstructuredURL).ValuePlain,
		Strategy:  get(SettingUnstructuredStrategy).ValuePlain,
		Languages: get(SettingUnstructuredLanguages).ValuePlain,
	}
	if s := get(SettingUnstructuredKey); len(s.ValueEncrypted) > 0 && encryptionKey != "" {
		if plain, err := pkg.Decrypt(s.ValueEncrypted, encryptionKey); err == nil {
			p.APIKey = string(plain)
		}
	} else if s.ValuePlain != "" {
		p.APIKey = s.ValuePlain
	}
	if p.APIURL == "" {
		p.APIURL = envUnstructuredURL
	}
	if p.APIKey == "" {
		p.APIKey = envUnstructuredKey
	}
	return p
}

var (
	envUnstructuredURL string
	envUnstructuredKey string
)

// SetEnvDefaults sets the environment fallbacks (from config).
func SetEnvDefaults(apiURL, apiKey string) {
	envUnstructuredURL, envUnstructuredKey = strings.TrimSpace(apiURL), strings.TrimSpace(apiKey)
}

// SupportsExtension reports whether a file type can be read with p.
func SupportsExtension(p ParserSettings, fileName string) bool {
	ext := strings.ToLower(filepath.Ext(fileName))
	if containsString(SupportedExtensions, ext) {
		return true
	}
	return p.Enabled() && containsString(UnstructuredExtensions, ext)
}

// AcceptedExtensions lists the types the upload accepts with p.
func AcceptedExtensions(p ParserSettings) []string {
	if !p.Enabled() {
		return SupportedExtensions
	}
	out := append([]string{}, UnstructuredExtensions...)
	for _, e := range SupportedExtensions {
		if !containsString(out, e) {
			out = append(out, e)
		}
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ExtractDocument reads a file with Unstructured when it is set up and
// supports the type, else with the built-in extractor. When Unstructured
// fails on a type the built-in extractor also reads, it falls back and
// returns a notice saying so.
func ExtractDocument(ctx context.Context, p ParserSettings, fileName string, data []byte) (text, parser, notice string, err error) {
	ext := strings.ToLower(filepath.Ext(fileName))
	if p.Enabled() && containsString(UnstructuredExtensions, ext) {
		text, uerr := UnstructuredExtract(ctx, p, fileName, data)
		if uerr == nil {
			return text, ParserUnstructured, "", nil
		}
		if !containsString(SupportedExtensions, ext) {
			return "", ParserUnstructured, "", fmt.Errorf("Unstructured: %w", uerr)
		}
		local, lerr := ExtractText(fileName, data)
		if lerr != nil {
			return "", ParserLocal, "", fmt.Errorf("Unstructured: %v; built-in reader: %v", uerr, lerr)
		}
		return local, ParserLocal, "Unstructured lỗi (" + truncateRunes(uerr.Error(), 200) + "), đã đọc bằng thư viện nội bộ.", nil
	}
	text, err = ExtractText(fileName, data)
	return text, ParserLocal, "", err
}

var unstructuredHTTP = &http.Client{Timeout: 10 * time.Minute}

// unstructuredElement is one element of the partition API's JSON answer.
type unstructuredElement struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Metadata struct {
		TextAsHTML string `json:"text_as_html"`
		PageNumber int    `json:"page_number"`
	} `json:"metadata"`
}

// UnstructuredExtract sends the file to the partition API and joins the
// returned elements into text: titles and paragraphs as blocks, list items
// as lines, tables as "a | b" rows, page headers/footers dropped.
func UnstructuredExtract(ctx context.Context, p ParserSettings, fileName string, data []byte) (string, error) {
	if err := ValidateUnstructuredURL(p.url()); err != nil {
		return "", err
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("files", filepath.Base(fileName))
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(data); err != nil {
		return "", err
	}
	_ = mw.WriteField("strategy", p.strategy())
	for _, l := range p.languages() {
		_ = mw.WriteField("languages", l)
	}
	_ = mw.WriteField("output_format", "application/json")
	_ = mw.WriteField("pdf_infer_table_structure", "true")
	if err := mw.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url(), &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	if p.APIKey != "" {
		req.Header.Set("unstructured-api-key", p.APIKey)
	}
	resp, err := unstructuredHTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 128<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, truncateRunes(strings.TrimSpace(string(raw)), 300))
	}
	var elements []unstructuredElement
	if err := json.Unmarshal(raw, &elements); err != nil {
		return "", fmt.Errorf("unexpected answer: %w", err)
	}
	text := NormalizeText(ElementsToText(elements))
	if strings.TrimSpace(text) == "" {
		return "", errors.New("no text found in the file")
	}
	return text, nil
}

// ElementsToText joins partition elements into plain text.
func ElementsToText(elements []unstructuredElement) string {
	var b strings.Builder
	prevList := false
	for _, e := range elements {
		t := strings.TrimSpace(e.Text)
		switch e.Type {
		case "Header", "Footer", "PageNumber", "PageBreak":
			continue
		case "Table":
			if e.Metadata.TextAsHTML != "" {
				if tbl, err := htmlText([]byte(e.Metadata.TextAsHTML)); err == nil && strings.TrimSpace(tbl) != "" {
					t = tbl
				}
			}
		}
		if t == "" {
			continue
		}
		isList := e.Type == "ListItem"
		switch {
		case isList && prevList:
			b.WriteString("\n")
		case b.Len() > 0:
			b.WriteString("\n\n")
		}
		if isList && !strings.HasPrefix(t, "-") && !strings.HasPrefix(t, "•") {
			t = "- " + t
		}
		b.WriteString(t)
		prevList = isList
	}
	return b.String()
}
