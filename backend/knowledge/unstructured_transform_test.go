package knowledge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTransformURLDetection(t *testing.T) {
	cases := map[string]string{
		"https://transform.unstructured.io/api/v2":        "https://transform.unstructured.io/api/v2",
		"https://transform.unstructured.io/api/v2/":       "https://transform.unstructured.io/api/v2",
		"https://transform.unstructured.io/api/v2/parse":  "https://transform.unstructured.io/api/v2",
		"https://transform.unstructured.io":               "https://transform.unstructured.io/api/v2",
		"https://Transform.Unstructured.io/api/v2/jobs?x": "https://Transform.Unstructured.io/api/v2",
		"http://10.0.0.5:9000/api/v2":                     "http://10.0.0.5:9000/api/v2",
	}
	for in, want := range cases {
		p := ParserSettings{APIURL: in}
		if !p.IsTransform() || p.transformBase() != want {
			t.Errorf("%s: transform=%v base=%s want %s", in, p.IsTransform(), p.transformBase(), want)
		}
	}
	for _, in := range []string{"", DefaultUnstructuredURL, "http://unstructured:8000/general/v0/general"} {
		if (ParserSettings{APIURL: in}).IsTransform() {
			t.Errorf("%s must not be Transform", in)
		}
	}
}

func TestTransformExtensions(t *testing.T) {
	p := ParserSettings{APIURL: TransformBaseURL, APIKey: "k"}
	if !SupportsExtension(p, "scan.png") || !SupportsExtension(p, "a.pptx") || SupportsExtension(p, "a.doc") || SupportsExtension(p, "a.epub") {
		t.Fatal("Transform extensions")
	}
	if !SupportsExtension(p, "a.txt") {
		t.Fatal("built-in types stay accepted")
	}
	// A type Transform does not take is read by the built-in reader, without
	// calling the API.
	text, parser, notice, err := ExtractDocument(context.Background(), ParserSettings{APIURL: "http://127.0.0.1:1/api/v2", APIKey: "k"}, "a.md", []byte("Đổi trả 7 ngày"))
	if err != nil || parser != ParserLocal || notice != "" || text != "Đổi trả 7 ngày" {
		t.Fatalf("md with Transform: %q %q %q %v", text, parser, notice, err)
	}
}

func TestTransformExtractSync(t *testing.T) {
	var got *http.Request
	var form map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			t.Errorf("multipart: %v", err)
		}
		form = r.MultipartForm.Value
		f, fh, err := r.FormFile("input")
		if err != nil || fh.Filename != "scan.tiff" {
			t.Errorf("input field: %v %v", fh, err)
		} else {
			io.ReadAll(f)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"id": "j1", "status": "completed", "markdown": nil,
			"elements": []map[string]any{
				{"element_id": "1", "type": "Title", "text": "Chính sách", "metadata": map[string]any{"page_number": 1, "coordinates": nil, "text_as_html": nil}},
				{"element_id": "2", "type": "NarrativeText", "text": nil, "metadata": map[string]any{"page_number": nil}},
				{"element_id": "3", "type": "Table", "text": "x", "metadata": map[string]any{"text_as_html": "<table><tr><td>A</td><td>B</td></tr></table>"}},
			},
		})
	}))
	defer srv.Close()
	p := ParserSettings{APIURL: srv.URL + "/api/v2/", APIKey: "tk", Strategy: "hi_res", Languages: "vie"}
	text, err := UnstructuredExtract(context.Background(), p, "scan.tif", []byte("II*"))
	if err != nil {
		t.Fatal(err)
	}
	if text != "Chính sách\n\nA | B" {
		t.Fatalf("text %q", text)
	}
	if got.URL.Path != "/api/v2/parse" || got.URL.Query().Get("include") != "table_html" {
		t.Fatalf("url %s", got.URL)
	}
	if got.Header.Get("unstructured-api-key") != "tk" || !strings.HasPrefix(got.Header.Get("Prefer"), "wait=") {
		t.Fatalf("headers %v", got.Header)
	}
	if form["output"][0] != "elements" || form["profile"][0] != "best" || form["languages"] != nil || form["strategy"] != nil {
		t.Fatalf("form %v", form)
	}
}

func TestTransformExtractPollsAcceptedJob(t *testing.T) {
	old := transformPollInterval
	transformPollInterval = 10 * time.Millisecond
	defer func() { transformPollInterval = old }()
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("unstructured-api-key") != "tk" {
			t.Errorf("key missing on %s", r.URL)
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/parse":
			w.Header().Set("Location", "/api/v2/jobs/j9")
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"id":"j9","status":"queued","source":null}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/jobs/j9":
			if r.URL.Query().Get("output") != "elements" {
				t.Errorf("poll without output=elements: %s", r.URL)
			}
			polls++
			if polls < 2 {
				w.Write([]byte(`{"id":"j9","status":"processing","result":null}`))
				return
			}
			w.Write([]byte(`{"id":"j9","status":"completed","result":{"id":"j9","status":"completed","elements":[{"element_id":"1","type":"NarrativeText","text":"Bảo hành 12 tháng","metadata":{}}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p := ParserSettings{APIURL: srv.URL + "/api/v2", APIKey: "tk"}
	text, err := UnstructuredExtract(context.Background(), p, "a.pdf", []byte("%PDF"))
	if err != nil || text != "Bảo hành 12 tháng" || polls != 2 {
		t.Fatalf("%q %v polls=%d", text, err, polls)
	}
}

func TestTransformJobFailureAndForeignPollURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/parse":
			if r.URL.Query().Get("foreign") != "" {
				return
			}
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"id":"j1","status":"queued","poll_url":"` + "http://" + r.Host + `/api/v2/jobs/j1"}`))
		case "/api/v2/jobs/j1":
			w.Write([]byte(`{"id":"j1","status":"failed","error":{"code":"document_encrypted","message":"The document is password-protected."}}`))
		}
	}))
	defer srv.Close()
	p := ParserSettings{APIURL: srv.URL + "/api/v2", APIKey: "tk"}
	if _, err := UnstructuredExtract(context.Background(), p, "a.pdf", []byte("%PDF")); err == nil || !strings.Contains(err.Error(), "document_encrypted") {
		t.Fatalf("failed job: %v", err)
	}
	if _, err := p.transformPoll(context.Background(), p.transformBase(), "https://evil.example/api/v2/jobs/j1"); err == nil {
		t.Fatal("poll URL on another host must be refused")
	}
}

func TestCheckUnstructured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v2/jobs" && r.URL.Query().Get("limit") == "1":
			if r.Header.Get("unstructured-api-key") != "good" {
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"code":"unauthorized","message":"Authentication required"}`))
				return
			}
			w.Write([]byte(`{"jobs":[],"next_cursor":null,"total_count":0}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"code":"not_found","message":"Not Found"}`))
		}
	}))
	defer srv.Close()
	if err := CheckUnstructured(context.Background(), ParserSettings{APIURL: srv.URL + "/api/v2", APIKey: "good"}); err != nil {
		t.Fatal(err)
	}
	err := CheckUnstructured(context.Background(), ParserSettings{APIURL: srv.URL + "/api/v2", APIKey: "bad"})
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("bad key: %v", err)
	}
	// A partition URL that answers 404 says what URL to use.
	err = CheckUnstructured(context.Background(), ParserSettings{APIURL: srv.URL + "/general/v0/general", APIKey: "good"})
	if err == nil || !strings.Contains(err.Error(), TransformBaseURL) {
		t.Fatalf("404 hint: %v", err)
	}
}
