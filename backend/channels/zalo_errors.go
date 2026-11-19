package channels

import (
	"errors"
	"fmt"
)

// Zalo OA error codes that concern the whole OA rather than one conversation.
const (
	zaloErrCallLimit     = -32  // "Your OA reached limit call api"
	zaloErrTokenInvalid  = -216 // access token invalid or expired
	zaloErrTokenExpired  = -124 // access token expired
	zaloErrNoPermission  = -230 // OA lacks the permission for this API
	zaloErrAppNotAllowed = -213 // app not linked to the OA / not approved
)

// zaloOAWideErrors lists codes after which every other request of the same
// sync fails too, so the sync must stop and retry the whole window later.
var zaloOAWideErrors = map[int]bool{
	zaloErrCallLimit:     true,
	zaloErrTokenInvalid:  true,
	zaloErrTokenExpired:  true,
	zaloErrNoPermission:  true,
	zaloErrAppNotAllowed: true,
}

// ZaloAPIError is an error Zalo reported in the body of an HTTP 200 response.
type ZaloAPIError struct {
	Code    int
	Message string
}

func (e *ZaloAPIError) Error() string {
	return fmt.Sprintf("zalo api error %d: %s", e.Code, e.Message)
}

// IsRateLimited reports whether err means the channel's API call quota is
// exhausted: every further request of this sync would fail the same way.
func IsRateLimited(err error) bool {
	var apiErr *ZaloAPIError
	return errors.As(err, &apiErr) && apiErr.Code == zaloErrCallLimit
}

// IsConversationScoped reports whether err concerns only the conversation
// being read. Zalo answers some single conversations with an error (for
// example -1 "Error fetching messages: -2") on every call while the rest of
// the OA reads fine; such a conversation must not hold back the others.
// Network failures, quota and token errors are never conversation scoped.
func IsConversationScoped(err error) bool {
	var apiErr *ZaloAPIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return !zaloOAWideErrors[apiErr.Code]
}
