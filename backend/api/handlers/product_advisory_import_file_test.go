package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
)

func TestParseProductAdvisoryImportRowsRecognizesHeaders(t *testing.T) {
	rows, err := parseProductAdvisoryImportRows([][]string{
		{"Tên đồng bộ web", "Mã SKU (mã cha)", "Link sản phẩm"},
		{"Storm II", " SP001 ", " https://example.com/storm "},
		{"", "", ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].RowNumber != 2 || rows[0].InputWebName != "Storm II" || rows[0].InputParentCode != "SP001" || rows[0].SourceURL != "https://example.com/storm" {
		t.Fatalf("unexpected rows: %#v", rows)
	}
}

func TestParseProductAdvisoryImportRowsSupportsOneIdentifier(t *testing.T) {
	for _, header := range [][]string{{"parent_code", "url"}, {"web_name", "source_url"}} {
		t.Run(strings.Join(header, "_"), func(t *testing.T) {
			rows, err := parseProductAdvisoryImportRows([][]string{header, {"Storm II", "https://example.com/storm"}})
			if err != nil || len(rows) != 1 {
				t.Fatalf("rows=%#v err=%v", rows, err)
			}
		})
	}
}

func TestParseProductAdvisoryImportRowsRejectsUnrelatedThreeColumns(t *testing.T) {
	_, err := parseProductAdvisoryImportRows([][]string{{"name", "code", "website"}, {"Storm", "SP1", "https://example.com"}})
	if !errors.Is(err, errProductImportInvalidHeader) {
		t.Fatalf("got %v, want invalid_header", err)
	}
}

func TestParseProductAdvisoryImportRowsEnforcesPhysicalRowLimit(t *testing.T) {
	raw := make([][]string, maxProductAdvisoryImportRows+2)
	raw[0] = []string{"ma_cha", "link"}
	for i := 1; i < len(raw); i++ {
		raw[i] = []string{"", ""}
	}
	_, err := parseProductAdvisoryImportRows(raw)
	if !errors.Is(err, errProductImportTooManyRows) {
		t.Fatalf("got %v, want too_many_rows", err)
	}
}

func TestReadProductAdvisoryImportCSVVariants(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
	}{
		{name: "comma_bom", content: "\ufefften_dong_bo_web,ma_cha,link\nStorm II,SP1,https://example.com/storm\n"},
		{name: "semicolon", content: "Tên đồng bộ web;Mã cha;URL\nStorm II;SP1;https://example.com/storm\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := productImportMultipartContext(t, "../links.csv", []byte(test.content))
			fileName, rows, err := readProductAdvisoryImportFile(c)
			if err != nil {
				t.Fatal(err)
			}
			if fileName != "links.csv" || len(rows) != 1 || rows[0].InputParentCode != "SP1" {
				t.Fatalf("file=%q rows=%#v", fileName, rows)
			}
		})
	}
}

func TestReadProductAdvisoryImportXLSX(t *testing.T) {
	f := excelize.NewFile()
	t.Cleanup(func() { _ = f.Close() })
	_ = f.SetSheetRow("Sheet1", "A1", &[]string{"web_name", "parent_code", "product_url"})
	_ = f.SetSheetRow("Sheet1", "A2", &[]string{"Storm II", "SP1", "https://example.com/storm"})
	var data bytes.Buffer
	if err := f.Write(&data); err != nil {
		t.Fatal(err)
	}
	c := productImportMultipartContext(t, "links.xlsx", data.Bytes())
	_, rows, err := readProductAdvisoryImportFile(c)
	if err != nil || len(rows) != 1 || rows[0].SourceURL != "https://example.com/storm" {
		t.Fatalf("rows=%#v err=%v", rows, err)
	}
}

func TestReadProductAdvisoryImportRejectsLegacyExcel(t *testing.T) {
	c := productImportMultipartContext(t, "links.xls", []byte("legacy"))
	_, _, err := readProductAdvisoryImportFile(c)
	if !errors.Is(err, errProductImportUnsupportedType) {
		t.Fatalf("got %v, want unsupported_file_type", err)
	}
}

func TestMatchProductImportGroupPrefersAuthoritativeCode(t *testing.T) {
	storm := productCatalogGroup{ParentCode: "SP1", WebName: "Storm"}
	other := productCatalogGroup{ParentCode: "SP2", WebName: "Storm"}
	byParent := map[string][]productCatalogGroup{"sp1": {storm}, "sp2": {other}}
	byChild := map[string][]productCatalogGroup{"sp1-red": {storm}}
	byName := map[string][]productCatalogGroup{"storm": {storm, other}}

	group, matchedBy, _ := matchProductImportGroup(productAdvisoryImportRow{InputParentCode: "SP1", InputWebName: "Wrong"}, byParent, byChild, byName)
	if group.ParentCode != "SP1" || matchedBy != "parent_code" {
		t.Fatalf("got %#v via %q", group, matchedBy)
	}
	group, matchedBy, _ = matchProductImportGroup(productAdvisoryImportRow{InputParentCode: "SP1-RED"}, byParent, byChild, byName)
	if group.ParentCode != "SP1" || matchedBy != "child_sku" {
		t.Fatalf("got %#v via %q", group, matchedBy)
	}
	group, _, candidates := matchProductImportGroup(productAdvisoryImportRow{InputWebName: "STÖRM"}, byParent, byChild, byName)
	if group.ParentCode != "" || len(candidates) != 2 {
		t.Fatalf("expected ambiguous name, got %#v candidates=%v", group, candidates)
	}
}

func TestMatchProductImportGroupWithSharedParent(t *testing.T) {
	helmet := productCatalogGroup{ParentCode: "SP462693", WebName: "LS2 FF818", GroupKey: "helmet"}
	accessory := productCatalogGroup{ParentCode: "SP462693", WebName: "Phụ Kiện LS2", GroupKey: "accessory"}
	byParent := map[string][]productCatalogGroup{"sp462693": {helmet, accessory}}
	byChild := map[string][]productCatalogGroup{"sp462696": {helmet}, "sp462552": {accessory}}
	for _, tc := range []struct {
		code, name, groupKey string
		ambiguous            bool
	}{
		{code: "SP462693", ambiguous: true},
		{code: "SP462693", name: "Phụ Kiện LS2", groupKey: "accessory"},
		{code: "SP462693", name: "LS2 FF818", groupKey: "helmet"},
		{code: "SP462696", groupKey: "helmet"},
		{code: "SP462552", groupKey: "accessory"},
	} {
		got, _, candidates := matchProductImportGroup(productAdvisoryImportRow{InputParentCode: tc.code, InputWebName: tc.name}, byParent, byChild, nil)
		if got.GroupKey != tc.groupKey || (len(candidates) > 1) != tc.ambiguous {
			t.Errorf("%s %s: group=%+v, candidates=%v", tc.code, tc.name, got, candidates)
		}
	}
}

func TestProductImportKeepsFullIdentifiersUntilMapping(t *testing.T) {
	long := strings.Repeat("á", 260)
	rows, err := parseProductAdvisoryImportRows([][]string{{"web_name", "parent_code", "link"}, {long, long, "https://example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].InputWebName != long || rows[0].InputParentCode != long {
		t.Fatal("identifiers must remain intact until mapping to avoid matching a truncated code")
	}
}

func productImportMultipartContext(t *testing.T, fileName string, content []byte) *gin.Context {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/imports", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	return c
}

func BenchmarkParseProductAdvisoryImportRows(b *testing.B) {
	raw := make([][]string, maxProductAdvisoryImportRows+1)
	raw[0] = []string{"web_name", "parent_code", "link"}
	for i := 1; i < len(raw); i++ {
		raw[i] = []string{fmt.Sprintf("Product %d", i), fmt.Sprintf("SP%d", i), "https://example.com"}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parseProductAdvisoryImportRows(raw)
	}
}
