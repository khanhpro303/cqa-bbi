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
)

// Unstructured Transform (https://transform.unstructured.io) is Unstructured's
// v2 API. Its keys are not accepted by the legacy partition endpoint, and it
// takes a different request: POST {base}/parse with the file in "input",
// answering with a parse result (or 202 and a job to poll) instead of a bare
// element list. Docs: https://docs.unstructured.io/transform/api-reference

// TransformBaseURL is the Transform v2 API base.
const TransformBaseURL = "https://transform.unstructured.io/api/v2"

// TransformExtensions are the document types Transform parses.
var TransformExtensions = []string{".pdf", ".docx", ".pptx", ".png", ".jpg", ".jpeg", ".tiff", ".tif", ".bmp", ".heic"}

// transformPollInterval is how often a pending Transform job is polled.
var transformPollInterval = 3 * time.Second

// IsTransform reports whether the URL is Unstructured Transform (v2) rather
// than a partition endpoint.
func (p ParserSettings) IsTransform() bool {
	u, err := url.Parse(strings.TrimSpace(p.APIURL))
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Hostname(), "transform.unstructured.io") || strings.Contains(u.Path, "/api/v2")
}

// transformBase normalises what the user pasted (the host, the /api/v2 base
// or the /parse endpoint) to the /api/v2 base without a trailing slash.
func (p ParserSettings) transformBase() string {
	u, err := url.Parse(strings.TrimSpace(p.APIURL))
	if err != nil {
		return TransformBaseURL
	}
	u.RawQuery, u.Fragment = "", ""
	path := strings.TrimRight(u.Path, "/")
	if i := strings.Index(path, "/api/v2"); i >= 0 {
		path = path[:i+len("/api/v2")]
	} else {
		path += "/api/v2"
	}
	u.Path = path
	return u.String()
}

// remoteExtensions are the types sent to the configured Unstructured API.
func (p ParserSettings) remoteExtensions() []string {
	if p.IsTransform() {
		return TransformExtensions
	}
	return UnstructuredExtensions
}

// transformError is Transform's error envelope.
type transformError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// transformResult is the part of a ParseResult CQA reads.
type transformResult struct {
	ID          string                `json:"id"`
	Status      string                `json:"status"`
	Elements    []unstructuredElement `json:"elements"`
	FailedPages []struct {
		Page int `json:"page"`
	} `json:"failed_pages"`
}

// transformJob is a JobAccepted (202) or JobResult (GET /jobs/{id}) answer.
type transformJob struct {
	ID      string           `json:"id"`
	Status  string           `json:"status"`
	PollURL string           `json:"poll_url"`
	Error   *transformError  `json:"error"`
	Result  *transformResult `json:"result"`
}

func (p ParserSettings) transformRequest(ctx context.Context, method, rawURL string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if p.APIKey != "" {
		req.Header.Set("unstructured-api-key", p.APIKey)
	}
	return req, nil
}

// transformStatusError turns a non-2xx answer into a readable error.
func transformStatusError(status int, raw []byte) error {
	var e transformError
	msg := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &e) == nil && e.Message != "" {
		msg = e.Code + ": " + e.Message
	}
	return fmt.Errorf("status %d: %s", status, truncateRunes(msg, 300))
}

// transformProfile maps the strategy setting onto Transform's profiles:
// hi_res and ocr_only ask for "best"; auto and fast keep the default
// ("balanced").
func (p ParserSettings) transformProfile() string {
	switch p.strategy() {
	case "hi_res", "ocr_only":
		return "best"
	}
	return ""
}

// transformWait is the Prefer wait sent with a parse: as long as the context
// allows, at most 10 minutes; a longer job comes back as 202 and is polled.
func transformWait(ctx context.Context) int {
	wait := 600
	if dl, ok := ctx.Deadline(); ok {
		if left := int(time.Until(dl).Seconds()) - 15; left < wait {
			wait = left
		}
	}
	if wait < 0 {
		wait = 0
	}
	return wait
}

// transformExtract parses a file with Transform and joins its elements.
func transformExtract(ctx context.Context, p ParserSettings, fileName string, data []byte) (string, error) {
	base := p.transformBase()
	name := filepath.Base(fileName)
	if strings.EqualFold(filepath.Ext(name), ".tif") {
		name = strings.TrimSuffix(name, filepath.Ext(name)) + ".tiff"
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("input", name)
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(data); err != nil {
		return "", err
	}
	_ = mw.WriteField("output", "elements")
	if prof := p.transformProfile(); prof != "" {
		_ = mw.WriteField("profile", prof)
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	req, err := p.transformRequest(ctx, http.MethodPost, base+"/parse?include=table_html", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Prefer", fmt.Sprintf("wait=%d", transformWait(ctx)))
	resp, err := unstructuredHTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 128<<20))
	resp.Body.Close()

	var result *transformResult
	switch resp.StatusCode {
	case http.StatusOK:
		var r transformResult
		if err := json.Unmarshal(raw, &r); err != nil {
			return "", fmt.Errorf("unexpected answer: %w", err)
		}
		result = &r
		if r.Status == "processing" && len(r.Elements) == 0 && r.ID != "" {
			if result, err = p.transformPoll(ctx, base, base+"/jobs/"+url.PathEscape(r.ID)); err != nil {
				return "", err
			}
		}
	case http.StatusAccepted:
		var job transformJob
		_ = json.Unmarshal(raw, &job)
		poll := resp.Header.Get("Location")
		if poll == "" {
			poll = job.PollURL
		}
		if poll == "" && job.ID != "" {
			poll = base + "/jobs/" + url.PathEscape(job.ID)
		}
		if poll == "" {
			return "", errors.New("Transform accepted the file but gave no job to follow")
		}
		if result, err = p.transformPoll(ctx, base, poll); err != nil {
			return "", err
		}
	default:
		return "", transformStatusError(resp.StatusCode, raw)
	}

	text := NormalizeText(ElementsToText(result.Elements))
	if strings.TrimSpace(text) == "" {
		if len(result.FailedPages) > 0 {
			return "", fmt.Errorf("Transform could not read %d page(s) and found no text", len(result.FailedPages))
		}
		return "", errors.New("no text found in the file")
	}
	return text, nil
}

// transformPoll follows a job until it ends. The poll URL must stay on the
// API's own host, so the key is never sent elsewhere.
func (p ParserSettings) transformPoll(ctx context.Context, base, poll string) (*transformResult, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	pollURL, err := baseURL.Parse(poll)
	if err != nil || !strings.EqualFold(pollURL.Host, baseURL.Host) || pollURL.Scheme != baseURL.Scheme {
		return nil, fmt.Errorf("Transform gave an unexpected job URL: %s", truncateRunes(poll, 200))
	}
	q := pollURL.Query()
	q.Set("output", "elements")
	q.Set("include", "table_html")
	pollURL.RawQuery = q.Encode()

	for {
		req, err := p.transformRequest(ctx, http.MethodGet, pollURL.String(), nil)
		if err != nil {
			return nil, err
		}
		resp, err := unstructuredHTTP.Do(req)
		if err != nil {
			return nil, fmt.Errorf("job poll failed: %w", err)
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 128<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, transformStatusError(resp.StatusCode, raw)
		}
		var job transformJob
		if err := json.Unmarshal(raw, &job); err != nil {
			return nil, fmt.Errorf("unexpected job answer: %w", err)
		}
		switch job.Status {
		case "completed", "completed_with_warnings":
			if job.Result == nil {
				return nil, errors.New("Transform finished the job without a result")
			}
			return job.Result, nil
		case "failed", "cancelled":
			if job.Error != nil && job.Error.Message != "" {
				return nil, fmt.Errorf("Transform job %s: %s: %s", job.Status, job.Error.Code, job.Error.Message)
			}
			return nil, fmt.Errorf("Transform job %s", job.Status)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("Transform job %s still running: %w", job.ID, ctx.Err())
		case <-time.After(transformPollInterval):
		}
	}
}

// transformCheck lists one job: it proves the URL and key work without
// parsing (and paying for) a document.
func transformCheck(ctx context.Context, p ParserSettings) error {
	req, err := p.transformRequest(ctx, http.MethodGet, p.transformBase()+"/jobs?limit=1", nil)
	if err != nil {
		return err
	}
	resp, err := unstructuredHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return transformStatusError(resp.StatusCode, raw)
	}
	return nil
}

// CheckUnstructured tests the tenant's setup: Transform is asked for its job
// list; a partition endpoint reads a tiny text file.
func CheckUnstructured(ctx context.Context, p ParserSettings) error {
	if err := ValidateUnstructuredURL(p.url()); err != nil {
		return err
	}
	if p.IsTransform() {
		return transformCheck(ctx, p)
	}
	_, err := UnstructuredExtract(ctx, p, "cqa-test.txt", []byte("Kiểm tra kết nối Unstructured từ CQA."))
	return err
}
