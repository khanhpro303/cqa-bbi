package pancake

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func fakeJWT(uid string) string {
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"HS256"}`)) + "." + enc.EncodeToString([]byte(`{"uid":"`+uid+`"}`)) + ".sig"
}

func TestUserIDFromToken(t *testing.T) {
	if got := UserIDFromToken(fakeJWT("32f5b15b-08fe-4929-973d-687fcc266a93")); got != "32f5b15b-08fe-4929-973d-687fcc266a93" {
		t.Fatalf("got %q", got)
	}
	if UserIDFromToken("not-a-jwt") != "" {
		t.Fatal("expected empty")
	}
}

type phoenixServer struct {
	refuse   bool
	mu       sync.Mutex
	cookie   string
	joins    []string
	upgrader websocket.Upgrader
}

func (s *phoenixServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.upgrader.CheckOrigin = func(r *http.Request) bool { return r.Header.Get("Origin") == DefaultOrigin }
		s.mu.Lock()
		s.cookie = r.Header.Get("Cookie")
		s.mu.Unlock()
		if r.URL.Query().Get("vsn") != "2.0.0" {
			t.Errorf("missing vsn")
		}
		conn, err := s.upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			frame, err := DecodeFrame(data)
			if err != nil {
				t.Errorf("bad frame %s", data)
				return
			}
			if frame.Event != "phx_join" {
				continue
			}
			s.mu.Lock()
			s.joins = append(s.joins, frame.Topic)
			s.mu.Unlock()
			status := "ok"
			if s.refuse && strings.HasPrefix(frame.Topic, "pages:") {
				status = "error"
			}
			reply, _ := EncodeFrame(frame.JoinRef, frame.Ref, frame.Topic, "phx_reply", map[string]any{"status": status, "response": map[string]any{}})
			conn.WriteMessage(websocket.TextMessage, reply)
			if status == "ok" && frame.Topic == "pages:100" {
				conn.WriteMessage(websocket.TextMessage, []byte(tagFrame))
			}
		}
	}
}

func TestRunSocketJoinsAndDispatches(t *testing.T) {
	server := &phoenixServer{}
	ts := httptest.NewServer(server.handler(t))
	defer ts.Close()
	token := fakeJWT("user-uuid-123")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events := make(chan []ConversationUpdate, 1)
	connected := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- RunSocket(ctx, SocketConfig{
			URL: "ws" + strings.TrimPrefix(ts.URL, "http") + "/socket/websocket?vsn=2.0.0", UserToken: token, PageIDs: []string{"100"}, Dialer: &websocket.Dialer{},
			OnConnected: func() { connected <- struct{}{} },
			OnEvent: func(pageID string, frame Frame) {
				events <- ExtractUpdates(pageID, frame.Payload)
			},
		})
	}()
	select {
	case <-connected:
	case <-ctx.Done():
		t.Fatal("never connected")
	}
	select {
	case updates := <-events:
		if len(updates) != 1 || updates[0].PSID != "300" {
			t.Fatalf("updates %+v", updates)
		}
	case <-ctx.Done():
		t.Fatal("no event")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("cancel must return nil, got %v", err)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.cookie != "jwt="+token {
		t.Fatalf("cookie not sent")
	}
	if len(server.joins) != 2 || server.joins[0] != "users:user-uuid-123" || server.joins[1] != "pages:100" {
		t.Fatalf("joins %v", server.joins)
	}
}

func TestRunSocketJoinRefused(t *testing.T) {
	server := &phoenixServer{refuse: true}
	ts := httptest.NewServer(server.handler(t))
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := RunSocket(ctx, SocketConfig{URL: "ws" + strings.TrimPrefix(ts.URL, "http") + "/socket/websocket", UserToken: "opaque", PageIDs: []string{"100"}, Dialer: &websocket.Dialer{}})
	var socketErr *SocketError
	if !errors.As(err, &socketErr) || socketErr.Kind != "join_refused" || strings.Contains(err.Error(), "opaque") {
		t.Fatalf("got %v", err)
	}
}

func TestRunSocketAuthFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer ts.Close()
	err := RunSocket(context.Background(), SocketConfig{URL: "ws" + strings.TrimPrefix(ts.URL, "http"), UserToken: "t", PageIDs: []string{"100"}, Dialer: &websocket.Dialer{}})
	var socketErr *SocketError
	if !errors.As(err, &socketErr) || socketErr.Kind != "auth" {
		t.Fatalf("got %v", err)
	}
	if err := RunSocket(context.Background(), SocketConfig{UserToken: "", PageIDs: []string{"100"}}); err == nil {
		t.Fatal("empty token must fail")
	}
}
