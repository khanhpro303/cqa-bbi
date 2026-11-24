package engine

import (
	"net/http"

	"github.com/vietbui/chat-quality-agent/config"
)

// NewLangflowPreviewClient keeps preview questions and credentials on the
// configured endpoint even when its response redirects.
func NewLangflowPreviewClient(cfg *config.Config) *LangflowClient {
	client := NewLangflowClient(cfg)
	client.client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client
}
