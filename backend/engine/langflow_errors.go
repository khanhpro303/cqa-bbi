package engine

import (
	"errors"
	"fmt"
	"net/http"
)

// LangflowAPIError describes a non-200 response from a Langflow client call.
type LangflowAPIError struct {
	StatusCode int
	prefix     string
	body       string
}

func (e *LangflowAPIError) Error() string {
	return fmt.Sprintf("%s (status %d): %s", e.prefix, e.StatusCode, e.body)
}

// IsLangflowConfigError identifies credential or missing-flow errors that can
// trigger a retry with the AI Engine configuration.
func IsLangflowConfigError(err error) bool {
	var apiErr *LangflowAPIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return true
	default:
		return false
	}
}

// IsPermanentLangflowError reports failures for which repeating the same
// request cannot help.
func IsPermanentLangflowError(err error) bool {
	if IsLangflowConfigError(err) {
		return true
	}
	var apiErr *LangflowAPIError
	return errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusBadRequest || apiErr.StatusCode == http.StatusUnprocessableEntity)
}
