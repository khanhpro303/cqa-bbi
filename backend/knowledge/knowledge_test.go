package knowledge

import (
	"archive/zip"
	"bytes"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

func TestExtractTextPlainAndHTML(t *testing.T) {
	got, err := ExtractText("a.txt", []byte("\xEF\xBB\xBFChính sách\r\n\r\n\r\n\r\nbảo hành   12 tháng"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "Chính sách\n\nbảo hành 12 tháng" {
		t.Fatalf("txt: %q", got)
	}
	got, err = ExtractText("p.html", []byte(`<html><head><title>x</title><script>var a=1</script></head><body><h1>Đổi trả</h1><p>Trong 7 ngày</p><table><tr><td>A</td><td>B</td></tr></table></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Đổi trả\nTrong 7 ngày") || strings.Contains(got, "var a") || !strings.Contains(got, "A | B") {
		t.Fatalf("html: %q", got)
	}
	if _, err := ExtractText("x.exe", []byte("abc")); err != ErrUnsupportedType {
		t.Fatalf("want ErrUnsupportedType, got %v", err)
	}
	if _, err := ExtractText("empty.txt", []byte("  \n ")); err == nil {
		t.Fatal("empty text must fail")
	}
}

func TestExtractTextDOCX(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("word/document.xml")
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
<w:p><w:r><w:t>Giao hàng</w:t></w:r><w:r><w:t xml:space="preserve"> toàn quốc</w:t></w:r></w:p>
<w:p><w:r><w:t>Phí ship 30k</w:t></w:r></w:p>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Vùng</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Phí</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
</w:body></w:document>`))
	zw.Close()
	got, err := ExtractText("policy.docx", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Giao hàng toàn quốc\nPhí ship 30k") || !strings.Contains(got, "Vùng | Phí") {
		t.Fatalf("docx: %q", got)
	}
}

func TestExtractTextXLSXLabelsCellsWithHeader(t *testing.T) {
	f := excelize.NewFile()
	f.SetSheetRow("Sheet1", "A1", &[]interface{}{"Sản phẩm", "Bảo hành"})
	f.SetSheetRow("Sheet1", "A2", &[]interface{}{"FF800", "24 tháng"})
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	got, err := ExtractText("bh.xlsx", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Sản phẩm: FF800 | Bảo hành: 24 tháng") {
		t.Fatalf("xlsx: %q", got)
	}
}

func TestSplitChunksKeepsParagraphsAndOverlaps(t *testing.T) {
	if SplitChunks("  ", 100, 10) != nil {
		t.Fatal("blank text must give no chunks")
	}
	short := SplitChunks("Một đoạn ngắn.", 100, 10)
	if len(short) != 1 || short[0] != "Một đoạn ngắn." {
		t.Fatalf("short: %q", short)
	}
	var paras []string
	for i := 0; i < 30; i++ {
		paras = append(paras, strings.Repeat("Bảo hành mũ bảo hiểm chính hãng. ", 4))
	}
	chunks := SplitChunks(strings.Join(paras, "\n\n"), 400, 60)
	if len(chunks) < 5 {
		t.Fatalf("want several chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if n := utf8.RuneCountInString(c); n > 400+60+2+200 {
			t.Fatalf("chunk %d too long: %d", i, n)
		}
	}
	// One long paragraph without breaks is cut at sentence ends.
	long := strings.Repeat("Câu số một rất dài. ", 200)
	parts := SplitChunks(long, 300, 50)
	if len(parts) < 10 {
		t.Fatalf("long paragraph not split: %d", len(parts))
	}
	for _, p := range parts[:len(parts)-1] {
		if !strings.HasSuffix(strings.TrimSpace(p), ".") {
			t.Fatalf("cut mid-sentence: %q", p[len(p)-20:])
		}
	}
}

func TestQueryWords(t *testing.T) {
	got := queryWords("Chính sách đổi trả mũ FF800 thế nào?")
	want := map[string]bool{"chính": true, "sách": true, "đổi": true, "trả": true, "mũ": true, "ff800": true, "thế": true, "nào": true}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for _, w := range got {
		if !want[w] {
			t.Fatalf("unexpected word %q in %v", w, got)
		}
	}
	if got[len(got)-1] != "mũ" {
		t.Fatalf("longest word first: %v", got)
	}
}

func TestValidateAstraEndpoint(t *testing.T) {
	ok := "https://38ed3530-bbdb-4f20-bb15-5828c4301f7d-us-east-2.apps.astra.datastax.com/"
	if got, err := ValidateAstraEndpoint(ok); err != nil || got != strings.TrimSuffix(ok, "/") {
		t.Fatalf("valid endpoint rejected: %q %v", got, err)
	}
	for _, bad := range []string{
		"http://x.apps.astra.datastax.com",
		"https://169.254.169.254",
		"https://evil.com/.apps.astra.datastax.com",
		"https://x.apps.astra.datastax.com.evil.com",
		"https://user:pw@x.apps.astra.datastax.com",
		"https://x.apps.astra.datastax.com:8443",
	} {
		if _, err := ValidateAstraEndpoint(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestSourceOf(t *testing.T) {
	key, title := sourceOf(map[string]interface{}{"file_path": "/app/data/.cache/langflow/abc/2025-06-01_Chinh sach bao hanh.pdf"})
	if title != "2025-06-01_Chinh sach bao hanh.pdf" || key == "" {
		t.Fatalf("got %q %q", key, title)
	}
	if _, title := sourceOf(nil); title != "Tài liệu nhập từ Astra" {
		t.Fatalf("fallback title %q", title)
	}
}

func TestExtractTextPDFReadsTopDown(t *testing.T) {
	data, err := os.ReadFile("testdata/bao-hanh.pdf")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ExtractText("bao-hanh.pdf", data)
	if err != nil {
		t.Fatal(err)
	}
	want := "Chính sách bảo hành\nMũ bảo hiểm LS2 được bảo hành 12 tháng cho lỗi sản xuất.\nĐổi trả trong 7 ngày nếu còn tem."
	if got != want {
		t.Fatalf("pdf: %q", got)
	}
}
