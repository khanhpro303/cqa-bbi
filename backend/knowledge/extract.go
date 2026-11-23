package knowledge

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	"github.com/xuri/excelize/v2"
	"golang.org/x/net/html"
)

// MaxUploadBytes caps one uploaded file.
const MaxUploadBytes = 20 << 20

// MaxTextChars caps the extracted text of one document.
const MaxTextChars = 2_000_000

// ErrUnsupportedType is returned for a file type ExtractText cannot read.
var ErrUnsupportedType = errors.New("unsupported file type")

// SupportedExtensions lists the file types ExtractText reads.
var SupportedExtensions = []string{".pdf", ".docx", ".xlsx", ".txt", ".md", ".csv", ".html", ".htm"}

// ExtractText returns the plain text of an uploaded file, picked by its
// extension. Scanned PDFs (images only) yield no text and return an error.
func ExtractText(fileName string, data []byte) (string, error) {
	var (
		text string
		err  error
	)
	switch strings.ToLower(filepath.Ext(fileName)) {
	case ".txt", ".md", ".csv":
		text = decodeText(data)
	case ".html", ".htm":
		text, err = htmlText(data)
	case ".docx":
		text, err = docxText(data)
	case ".xlsx":
		text, err = xlsxText(data)
	case ".pdf":
		text, err = pdfText(data)
	default:
		return "", ErrUnsupportedType
	}
	if err != nil {
		return "", err
	}
	text = NormalizeText(text)
	if strings.TrimSpace(text) == "" {
		return "", errors.New("no text found in the file (a scanned PDF needs OCR first)")
	}
	if utf8.RuneCountInString(text) > MaxTextChars {
		return "", fmt.Errorf("document is longer than %d characters; split it into smaller files", MaxTextChars)
	}
	return text, nil
}

func decodeText(data []byte) string {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if utf8.Valid(data) {
		return string(data)
	}
	return strings.ToValidUTF8(string(data), "")
}

var (
	spaceRun   = regexp.MustCompile(`[ \t\x{00A0}]+`)
	blankLines = regexp.MustCompile(`\n{3,}`)
)

// NormalizeText unifies line endings, trims trailing spaces and collapses
// runs of blank lines, keeping paragraph breaks.
func NormalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\x00", "")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(spaceRun.ReplaceAllString(l, " "))
	}
	s = strings.Join(lines, "\n")
	s = blankLines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func pdfText(data []byte) (out string, err error) {
	defer func() {
		// The PDF reader panics on some malformed files.
		if r := recover(); r != nil {
			out, err = "", fmt.Errorf("cannot read PDF: %v", r)
		}
	}()
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("cannot read PDF: %w", err)
	}
	var b strings.Builder
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		rows, err := p.GetTextByRow()
		if err != nil {
			continue
		}
		// Rows come bottom-up (PDF y grows upwards); read them top-down.
		for k := len(rows) - 1; k >= 0; k-- {
			row := rows[k]
			var line strings.Builder
			for _, w := range row.Content {
				line.WriteString(w.S)
			}
			b.WriteString(line.String())
			b.WriteByte('\n')
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

func docxText(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("cannot read DOCX: %w", err)
	}
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()
		return wordXMLText(io.LimitReader(rc, 64<<20))
	}
	return "", errors.New("cannot read DOCX: word/document.xml not found")
}

// wordXMLText walks WordprocessingML: one line per paragraph, cells of a
// table row joined with " | ".
func wordXMLText(r io.Reader) (string, error) {
	dec := xml.NewDecoder(r)
	var b strings.Builder
	inText := false
	cellStarted := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("cannot read DOCX: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "tab":
				b.WriteByte('\t')
			case "br", "cr":
				b.WriteByte('\n')
			case "tc":
				if cellStarted {
					b.WriteString(" | ")
				}
				cellStarted = true
			case "tr":
				cellStarted = false
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				if !cellStarted {
					b.WriteByte('\n')
				} else {
					b.WriteByte(' ')
				}
			case "tr":
				b.WriteByte('\n')
				cellStarted = false
			case "tbl":
				b.WriteByte('\n')
			}
		case xml.CharData:
			if inText {
				b.Write(t)
			}
		}
	}
	return b.String(), nil
}

func xlsxText(data []byte) (string, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("cannot read XLSX: %w", err)
	}
	defer f.Close()
	var b strings.Builder
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil || len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s\n", sheet)
		header := rows[0]
		for i, row := range rows {
			cells := make([]string, 0, len(row))
			for j, c := range row {
				c = strings.TrimSpace(c)
				if c == "" {
					continue
				}
				// Label data rows with the header so each line stands alone
				// once the sheet is chunked.
				if i > 0 && j < len(header) && strings.TrimSpace(header[j]) != "" {
					c = strings.TrimSpace(header[j]) + ": " + c
				}
				cells = append(cells, c)
			}
			if len(cells) > 0 {
				b.WriteString(strings.Join(cells, " | "))
				b.WriteByte('\n')
			}
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

func htmlText(data []byte) (string, error) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("cannot read HTML: %w", err)
	}
	var b strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "head":
				return
			}
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "p", "div", "br", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6", "section", "article", "table":
				b.WriteByte('\n')
			case "td", "th":
				b.WriteString(" | ")
			}
		}
	}
	walk(doc)
	lines := strings.Split(b.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(strings.TrimSpace(l), " |")
		lines[i] = strings.TrimSuffix(lines[i], "|")
	}
	return strings.Join(lines, "\n"), nil
}
