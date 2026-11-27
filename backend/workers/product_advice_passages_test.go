package workers

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/vietbui/chat-quality-agent/db/models"
)

const gobyQuestion = "Bull ơi, cho tôi thông tin giày goby với"

const gobyProductDetails = `## Những đặc điểm của giày LS2 Goby Man

Thiết kế cổ cao giúp bảo vệ ngón chân, gót chân và mắt cá chân.

### CE EN 13634:2017 — 2|2|2|2 WR

## Chất liệu của giày bảo hộ LS2 Goby Man

Da bò, vải phản quang giúp tăng nhận diện khi thiếu sáng.

Lớp lót bằng nylon co giãn, thoáng khí.

## Sản phẩm liên quan

### Chứng nhận EN 99999 của mẫu khác

Mẫu khác có màng chống thấm Gore-Tex.
`

func TestProductAdviceGobyPassages(t *testing.T) {
	intro := strings.Repeat("LS2 Goby Man là sản phẩm chất lượng cao. ", 75)
	advice := productAdvice{ProductAdvisory: models.ProductAdvisory{
		WebName: "Giày Bảo Hộ Xe Máy LS2 Goby Man", ScrapedContent: intro + "\n\n" + gobyProductDetails,
		SourceSummary: "Thông số AI chưa được đối chiếu.",
	}}
	prompt, _ := productAdviceContext([]productAdvice{advice}, false)
	for _, want := range []string{
		"CE EN 13634:2017 — 2|2|2|2 WR", "cổ cao giúp bảo vệ ngón chân, gót chân và mắt cá chân",
		"Da bò, vải phản quang", "nylon co giãn, thoáng khí",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("website context lost %q: %s", want, prompt)
		}
	}
	for _, unwanted := range []string{"chất lượng cao", "EN 99999", "Gore-Tex", advice.SourceSummary} {
		if strings.Contains(prompt, unwanted) {
			t.Errorf("website context contains unrelated or unverified data %q", unwanted)
		}
	}
}

func TestProductAdvicePassageBudgetAndFallback(t *testing.T) {
	if got := productAdvicePassages("Một đoạn mô tả ngắn."); len(got) != 1 || got[0] != "Một đoạn mô tả ngắn." {
		t.Fatalf("short unstructured page lost its description: %q", got)
	}
	source := strings.Repeat("Cấu tạo dùng vật liệu bền, phần lót thoáng khí. ", 90)
	excerpts := productAdvicePassages(source)
	totalRunes := 0
	for _, excerpt := range excerpts {
		size := utf8.RuneCountInString(excerpt)
		if size > advicePassageRunes || !strings.Contains(source, excerpt) {
			t.Fatalf("invalid source passage of %d characters: %q", size, excerpt)
		}
		totalRunes += size
	}
	if len(excerpts) == 0 || len(excerpts) > advicePassageLimit || totalRunes > adviceWebsiteRunes {
		t.Fatalf("got %d excerpts and %d characters", len(excerpts), totalRunes)
	}
}
