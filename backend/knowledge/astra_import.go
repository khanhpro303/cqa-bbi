package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// AstraImportInput points at an existing Astra DB collection written by the
// old Langflow ingestion flows (for example database "bbi", keyspace and
// collection "bbi_rag"). The token is used for this import only and is not
// stored.
type AstraImportInput struct {
	TenantID   string
	Endpoint   string
	Token      string
	Keyspace   string
	Collection string
	Audience   string
	CreatedBy  string
}

// AstraImportResult reports what was imported.
type AstraImportResult struct {
	Documents int      `json:"documents"`
	Chunks    int      `json:"chunks"`
	Titles    []string `json:"titles"`
}

// ErrInvalidImport wraps an input problem (bad endpoint, missing token...).
var ErrInvalidImport = errors.New("invalid import request")

// MaxAstraImportChunks caps one import.
const MaxAstraImportChunks = 20000

var astraImportHTTP = &http.Client{Timeout: 60 * time.Second}

// ValidateAstraEndpoint accepts only an Astra Data API endpoint, so the
// import cannot be pointed at an internal address.
func ValidateAstraEndpoint(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Port() != "" {
		return "", errors.New("api_endpoint must be an https Astra DB endpoint")
	}
	host := strings.ToLower(u.Hostname())
	if !strings.HasSuffix(host, ".apps.astra.datastax.com") {
		return "", errors.New("api_endpoint must end with .apps.astra.datastax.com")
	}
	return "https://" + host, nil
}

// ImportAstra reads every document of the collection, groups the passages by
// their source file (metadata file_path / source / file_name), and creates
// one knowledge document per file. Embeddings are recomputed with the CQA
// embedding model, so the old collection's model does not matter.
func (s *Service) ImportAstra(ctx context.Context, in AstraImportInput) (*AstraImportResult, error) {
	endpoint, err := ValidateAstraEndpoint(in.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidImport, err)
	}
	if strings.TrimSpace(in.Token) == "" || strings.TrimSpace(in.Collection) == "" {
		return nil, fmt.Errorf("%w: token and collection are required", ErrInvalidImport)
	}
	keyspace := strings.TrimSpace(in.Keyspace)
	if keyspace == "" {
		keyspace = "default_keyspace"
	}
	if !safeName(keyspace) || !safeName(in.Collection) {
		return nil, fmt.Errorf("%w: invalid keyspace or collection name", ErrInvalidImport)
	}
	collURL := fmt.Sprintf("%s/api/json/v1/%s/%s", endpoint, keyspace, strings.TrimSpace(in.Collection))

	type group struct {
		title  string
		chunks []string
	}
	groups := map[string]*group{}
	var order []string
	total := 0
	pageState := ""
	for {
		opts := map[string]interface{}{}
		if pageState != "" {
			opts["pageState"] = pageState
		}
		cmd := map[string]interface{}{"find": map[string]interface{}{
			"filter":     map[string]interface{}{},
			"projection": map[string]interface{}{"$vector": 0},
			"options":    opts,
		}}
		var resp struct {
			Data struct {
				Documents     []map[string]interface{} `json:"documents"`
				NextPageState *string                  `json:"nextPageState"`
			} `json:"data"`
			Errors []struct {
				Message   string `json:"message"`
				ErrorCode string `json:"errorCode"`
			} `json:"errors"`
		}
		if err := astraPost(ctx, collURL, in.Token, cmd, &resp); err != nil {
			return nil, err
		}
		if len(resp.Errors) > 0 {
			return nil, fmt.Errorf("astra error %s: %s", resp.Errors[0].ErrorCode, resp.Errors[0].Message)
		}
		for _, d := range resp.Data.Documents {
			text := firstString(d, "content", "page_content", "text")
			meta, _ := d["metadata"].(map[string]interface{})
			if text == "" && meta != nil {
				text = firstString(meta, "content", "page_content", "text")
			}
			if strings.TrimSpace(text) == "" {
				continue
			}
			key, title := sourceOf(meta)
			g := groups[key]
			if g == nil {
				g = &group{title: title}
				groups[key] = g
				order = append(order, key)
			}
			g.chunks = append(g.chunks, text)
			total++
			if total >= MaxAstraImportChunks {
				break
			}
		}
		if total >= MaxAstraImportChunks || resp.Data.NextPageState == nil || *resp.Data.NextPageState == "" {
			break
		}
		pageState = *resp.Data.NextPageState
	}
	if total == 0 {
		return nil, errors.New("the collection has no text passages")
	}
	res := &AstraImportResult{}
	for _, key := range order {
		g := groups[key]
		doc, err := s.CreateDocument(NewDocumentInput{
			TenantID: in.TenantID, Title: g.title, FileName: g.title, Source: "astra_import",
			Audience: in.Audience, CreatedBy: in.CreatedBy, Chunks: g.chunks,
		})
		if err != nil {
			return res, fmt.Errorf("save %q: %w", g.title, err)
		}
		res.Documents++
		res.Chunks += doc.ChunkCount
		res.Titles = append(res.Titles, doc.Title)
	}
	return res, nil
}

func astraPost(ctx context.Context, url, token string, cmd interface{}, out interface{}) error {
	body, _ := json.Marshal(cmd)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Token", token)
	resp, err := astraImportHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("astra request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("astra status %d: %s", resp.StatusCode, truncateRunes(string(raw), 300))
	}
	return json.Unmarshal(raw, out)
}

func firstString(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// sourceOf names the file a passage came from.
func sourceOf(meta map[string]interface{}) (key, title string) {
	if meta != nil {
		for _, k := range []string{"file_path", "source", "file_name", "filename", "title", "document_name"} {
			if v, ok := meta[k].(string); ok && strings.TrimSpace(v) != "" {
				v = strings.TrimSpace(v)
				base := path.Base(strings.ReplaceAll(v, `\`, "/"))
				if base == "." || base == "/" || base == "" {
					base = v
				}
				return v, base
			}
		}
	}
	return "", "Tài liệu nhập từ Astra"
}

func safeName(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 48 {
		return false
	}
	for _, r := range s {
		if !(r == '_' || ('0' <= r && r <= '9') || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z')) {
			return false
		}
	}
	return true
}
