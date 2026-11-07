package pancake

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	DefaultSocketURL = "wss://pancake.vn/socket/websocket?vsn=2.0.0"
	DefaultOrigin    = "https://pancake.vn"
	heartbeatEvery   = 30 * time.Second
	replyTimeout     = 20 * time.Second
	readLimitBytes   = 8 << 20
)

// SocketError is a sanitized realtime failure. It never carries the token.
type SocketError struct {
	Kind string // auth | join_refused | transport | protocol
}

func (e *SocketError) Error() string { return "pancake socket error: " + e.Kind }

// SocketConfig describes one realtime session of one Pancake user.
type SocketConfig struct {
	URL       string
	Origin    string
	UserToken string
	UserID    string
	PageIDs   []string
	// OnConnected runs after every requested Page channel is joined.
	OnConnected func()
	// OnEvent runs for each server push on a pages:<id> topic, in order.
	OnEvent func(pageID string, frame Frame)
	Dialer  *websocket.Dialer
}

// UserIDFromToken reads the Pancake user id claim from a JWT without trusting
// it for authorization (the server checks the token). Empty if not found.
func UserIDFromToken(token string) string {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return ""
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims map[string]any
	if json.Unmarshal(data, &claims) != nil {
		return ""
	}
	for _, key := range []string{"uid", "user_id", "userId", "id", "sub"} {
		if value, ok := claims[key].(string); ok && len(value) >= 8 && len(value) <= 64 {
			return value
		}
	}
	return ""
}

func randomSession() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf)
}

func socketURL(raw, token string) (string, error) {
	if raw == "" {
		raw = DefaultSocketURL
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "wss" && parsed.Scheme != "ws") {
		return "", &SocketError{Kind: "protocol"}
	}
	query := parsed.Query()
	if query.Get("vsn") == "" {
		query.Set("vsn", "2.0.0")
	}
	// The web app authenticates the socket with its session cookie. The same
	// user token is also offered as a connect param for servers that read it.
	query.Set("access_token", token)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// RunSocket connects, joins every Page channel and dispatches events until the
// context ends or the connection fails. It returns nil only on cancellation.
func RunSocket(ctx context.Context, cfg SocketConfig) error {
	token := strings.TrimSpace(cfg.UserToken)
	if token == "" || len(cfg.PageIDs) == 0 {
		return &SocketError{Kind: "auth"}
	}
	endpoint, err := socketURL(cfg.URL, token)
	if err != nil {
		return err
	}
	origin := cfg.Origin
	if origin == "" {
		origin = DefaultOrigin
	}
	header := http.Header{}
	header.Set("Origin", origin)
	header.Set("Cookie", "jwt="+token)
	header.Set("User-Agent", "Mozilla/5.0 (compatible; CQA-Pancake-Listener/1.0)")
	dialer := cfg.Dialer
	if dialer == nil {
		dialer = &websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: 20 * time.Second, EnableCompression: true}
	}
	conn, resp, err := dialer.DialContext(ctx, endpoint, header)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			return &SocketError{Kind: "auth"}
		}
		return &SocketError{Kind: "transport"}
	}
	defer conn.Close()
	conn.SetReadLimit(readLimitBytes)

	var writeMu sync.Mutex
	write := func(data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
		return conn.WriteMessage(websocket.TextMessage, data)
	}
	ref := 0
	nextRef := func() *string { ref++; s := strconv.Itoa(ref); return &s }

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { <-ctx.Done(); _ = conn.Close() }()

	pending := map[string]string{} // join ref -> page id
	userID := cfg.UserID
	if userID == "" {
		userID = UserIDFromToken(token)
	}
	if userID != "" {
		joinRef := nextRef()
		data, _ := EncodeFrame(joinRef, joinRef, "users:"+userID, "phx_join", map[string]any{"userId": userID, "platform": "web"})
		if err := write(data); err != nil {
			return &SocketError{Kind: "transport"}
		}
		pending[*joinRef] = ""
	}
	session := randomSession()
	for _, pageID := range cfg.PageIDs {
		joinRef := nextRef()
		payload := map[string]any{"clientSession": session, "pageId": pageID, "platform": "web"}
		if userID != "" {
			payload["userId"] = userID
		}
		data, _ := EncodeFrame(joinRef, joinRef, "pages:"+pageID, "phx_join", payload)
		if err := write(data); err != nil {
			return &SocketError{Kind: "transport"}
		}
		pending[*joinRef] = pageID
	}
	pagesLeft := len(cfg.PageIDs)
	joinDeadline := time.Now().Add(replyTimeout)
	_ = conn.SetReadDeadline(joinDeadline)

	heartbeatErr := make(chan error, 1)
	var lastReply time.Time
	var replyMu sync.Mutex
	markReply := func() { replyMu.Lock(); lastReply = time.Now(); replyMu.Unlock() }
	markReply()
	go func() {
		ticker := time.NewTicker(heartbeatEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				replyMu.Lock()
				stale := time.Since(lastReply) > 3*heartbeatEvery
				replyMu.Unlock()
				if stale {
					heartbeatErr <- &SocketError{Kind: "transport"}
					cancel()
					return
				}
				data, _ := EncodeFrame(nil, nextRefLocked(&writeMu, &ref), "phoenix", "heartbeat", nil)
				if err := write(data); err != nil {
					heartbeatErr <- &SocketError{Kind: "transport"}
					cancel()
					return
				}
			}
		}
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			select {
			case hbErr := <-heartbeatErr:
				return hbErr
			default:
			}
			if ctx.Err() != nil {
				return nil
			}
			var netErr interface{ Timeout() bool }
			if pagesLeft > 0 && errors.As(err, &netErr) && netErr.Timeout() {
				return &SocketError{Kind: "join_refused"}
			}
			return &SocketError{Kind: "transport"}
		}
		markReply()
		frame, err := DecodeFrame(data)
		if err != nil {
			continue
		}
		switch frame.Event {
		case "phx_reply":
			if frame.Ref == nil {
				continue
			}
			pageID, isJoin := pending[*frame.Ref]
			if !isJoin {
				continue
			}
			delete(pending, *frame.Ref)
			var reply struct {
				Status string `json:"status"`
			}
			_ = json.Unmarshal(frame.Payload, &reply)
			if reply.Status != "ok" {
				if pageID == "" {
					// The user channel is optional; Page channels carry tags.
					continue
				}
				return &SocketError{Kind: "join_refused"}
			}
			if pageID != "" {
				pagesLeft--
				if pagesLeft == 0 {
					_ = conn.SetReadDeadline(time.Time{})
					if cfg.OnConnected != nil {
						cfg.OnConnected()
					}
				}
			}
		case "phx_error", "phx_close":
			if PageIDFromTopic(frame.Topic) != "" {
				return &SocketError{Kind: "join_refused"}
			}
		default:
			if pageID := PageIDFromTopic(frame.Topic); pageID != "" && cfg.OnEvent != nil {
				cfg.OnEvent(pageID, frame)
			}
		}
		if pagesLeft > 0 && time.Now().After(joinDeadline) {
			return &SocketError{Kind: "join_refused"}
		}
	}
}

func nextRefLocked(mu *sync.Mutex, ref *int) *string {
	mu.Lock()
	defer mu.Unlock()
	*ref++
	s := strconv.Itoa(*ref)
	return &s
}
