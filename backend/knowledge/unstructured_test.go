package knowledge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeUnstructured(t *testing.T, status int, elements []map[string]any) (*httptest.Server, *http.Request) {
	t.Helper()
	got := &http.Request{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			t.Errorf("multipart: %v", err)
		}
		*got = *r
		f, fh, err := r.FormFile("files")
		if err != nil || fh.Filename == "" {
			t.Errorf("files field missing: %v", err)
		} else {
			io.ReadAll(f)
		}
		w.WriteHeader(status)
		if status == http.StatusOK {
			json.NewEncoder(w).Encode(elements)
		} else {
			w.Write([]byte(`{"detail":"quota exceeded"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func TestUnstructuredExtractBuildsTextFromElements(t *testing.T) {
	srv, got := fakeUnstructured(t, http.StatusOK, []map[string]any{
		{"type": "Header", "text": "CÔNG TY BBI"},
		{"type": "Title", "text": "Chính sách bảo hành"},
		{"type": "NarrativeText", "text": "Mũ bảo hành 12 tháng."},
		{"type": "ListItem", "text": "Lỗi sơn"},
		{"type": "ListItem", "text": "Lỗi khoá"},
		{"type": "Table", "text": "Dòng Hạn FF800 24", "metadata": map[string]any{"text_as_html": "<table><tr><td>Dòng</td><td>Hạn</td></tr><tr><td>FF800</td><td>24 tháng</td></tr></table>"}},
		{"type": "PageNumber", "text": "1"},
	})
	p := ParserSettings{APIURL: srv.URL, APIKey: "k-123", Strategy: "hi_res", Languages: "vie, eng"}
	text, err := UnstructuredExtract(context.Background(), p, "bh.pdf", []byte("%PDF"))
	if err != nil {
		t.Fatal(err)
	}
	want := "Chính sách bảo hành\n\nMũ bảo hành 12 tháng.\n\n- Lỗi sơn\n- Lỗi khoá\n\nDòng | Hạn\nFF800 | 24 tháng"
	if text != want {
		t.Fatalf("text:\n%q\nwant\n%q", text, want)
	}
	if got.Header.Get("unstructured-api-key") != "k-123" {
		t.Fatal("api key header not sent")
	}
	if got.FormValue("strategy") != "hi_res" || strings.Join(got.MultipartForm.Value["languages"], ",") != "vie,eng" {
		t.Fatalf("form: %v", got.MultipartForm.Value)
	}
}

func TestExtractDocumentFallsBackToBuiltInReader(t *testing.T) {
	srv, _ := fakeUnstructured(t, http.StatusTooManyRequests, nil)
	p := ParserSettings{APIURL: srv.URL, APIKey: "k"}

	text, parser, notice, err := ExtractDocument(context.Background(), p, "a.txt", []byte("Đổi trả 7 ngày"))
	if err != nil || parser != ParserLocal || text != "Đổi trả 7 ngày" || !strings.Contains(notice, "Unstructured lỗi") {
		t.Fatalf("fallback: %q %q %q %v", text, parser, notice, err)
	}
	// An image only Unstructured can read fails with its error.
	if _, _, _, err := ExtractDocument(context.Background(), p, "scan.png", []byte{0x89}); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("image without Unstructured must fail with its error, got %v", err)
	}
	// Without Unstructured the built-in reader is used and images are refused.
	if _, parser, _, err := ExtractDocument(context.Background(), ParserSettings{}, "a.md", []byte("x y")); err != nil || parser != ParserLocal {
		t.Fatalf("local: %q %v", parser, err)
	}
	if SupportsExtension(ParserSettings{}, "scan.png") || !SupportsExtension(p, "scan.PNG") {
		t.Fatal("image support must follow the Unstructured setup")
	}
}

func TestParserSettingsEnabled(t *testing.T) {
	if (ParserSettings{}).Enabled() || (ParserSettings{APIURL: DefaultUnstructuredURL}).Enabled() {
		t.Fatal("hosted URL without key must stay off")
	}
	if !(ParserSettings{APIKey: "k"}).Enabled() || !(ParserSettings{APIURL: "http://unstructured:8000/general/v0/general"}).Enabled() {
		t.Fatal("key or self-hosted URL must turn it on")
	}
	if ValidateUnstructuredURL("ftp://x") == nil || ValidateUnstructuredURL("https://u:p@x") == nil || ValidateUnstructuredURL("") != nil {
		t.Fatal("url validation")
	}
}
