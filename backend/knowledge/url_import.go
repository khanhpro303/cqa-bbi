package knowledge

import (
	"context"
	"net/url"
	"strings"

	"github.com/vietbui/chat-quality-agent/db/models"
)

// CreateFromURL accepts a link validated by ParseSourceURL. It scrapes and
// indexes the document in the background via the same pipeline as uploads.
func (s *Service) CreateFromURL(in NewDocumentInput, source *url.URL, client *FirecrawlClient) (*models.KnowledgeDocument, error) {
	usePageTitle := strings.TrimSpace(in.Title) == ""
	if usePageTitle {
		in.Title = source.Hostname()
	}
	in.Source, in.MimeType = "url", "text/markdown"
	doc := newDocument(in)
	doc.SourceURL, doc.Parser = source.String(), ParserFirecrawl
	return s.createFromReader(doc, firecrawlHTTPBudget, func(ctx context.Context) (parsedDocument, error) {
		text, title, err := client.scrape(ctx, doc.SourceURL)
		if !usePageTitle {
			title = ""
		}
		return parsedDocument{text: text, title: title, parser: ParserFirecrawl}, err
	})
}
