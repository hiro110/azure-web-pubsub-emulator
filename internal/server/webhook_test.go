package server_test

// Webhook E2E integration tests.
//
// Each test spins up a mock upstream server (httptest.Server) and the emulator
// (server.New + httptest.Server) in-process, then connects real WebSocket
// clients to exercise the full CloudEvents delivery path.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/hiro110/azure-web-pubsub-emulator/internal/auth"
	"github.com/hiro110/azure-web-pubsub-emulator/internal/config"
	"github.com/hiro110/azure-web-pubsub-emulator/internal/server"
)

// ── Mock upstream helpers ──────────────────────────────────────────────────────

type capturedEvent struct {
	Type         string
	ConnectionID string
	Body         []byte
}

// upstreamMock records all incoming CloudEvents and allows per-event-type
// response overrides.
type upstreamMock struct {
	mu        sync.Mutex
	events    []capturedEvent
	onConnect func(http.ResponseWriter, *http.Request) // nil → 204
	onMessage func(http.ResponseWriter, *http.Request) // nil → 204
}

func newUpstreamMock(t *testing.T) (*upstreamMock, *httptest.Server) {
	t.Helper()
	m := &upstreamMock{}
	ts := httptest.NewServer(http.HandlerFunc(m.handle))
	t.Cleanup(ts.Close)
	return m, ts
}

func (m *upstreamMock) handle(w http.ResponseWriter, r *http.Request) {
	// Abuse-protection handshake
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	ceType := r.Header.Get("ce-type")
	body, _ := io.ReadAll(r.Body)
	// Restore body so that onConnect/onMessage overrides can re-read it.
	r.Body = io.NopCloser(bytes.NewReader(body))

	m.mu.Lock()
	m.events = append(m.events, capturedEvent{
		Type:         ceType,
		ConnectionID: r.Header.Get("ce-connectionid"),
		Body:         body,
	})
	m.mu.Unlock()

	switch ceType {
	case "azure.webpubsub.sys.connect":
		if m.onConnect != nil {
			m.onConnect(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case "azure.webpubsub.user.message":
		if m.onMessage != nil {
			m.onMessage(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

func (m *upstreamMock) waitForEvent(ceType string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		for _, e := range m.events {
			if e.Type == ceType {
				m.mu.Unlock()
				return true
			}
		}
		m.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func (m *upstreamMock) countEvents(ceType string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, e := range m.events {
		if e.Type == ceType {
			n++
		}
	}
	return n
}

// ── Server factory ─────────────────────────────────────────────────────────────

func newServerWithEventHandler(t *testing.T, upstreamURL string) *httptest.Server {
	t.Helper()
	cfg := &config.Config{
		Server:  config.ServerConfig{Host: "0.0.0.0", Port: 0},
		Auth:    config.AuthConfig{AccessKey: testAccessKey},
		Logging: config.LoggingConfig{Level: "error", Format: "text"},
		Hubs:    []config.HubConfig{{Name: "hub1", EventHandlerURL: upstreamURL}},
	}
	srv, err := server.New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestWebhook_SysConnect_Fired(t *testing.T) {
	mock, upstreamTS := newUpstreamMock(t)
	ts := newServerWithEventHandler(t, upstreamTS.URL)

	conn := wsConnect(t, ts, "hub1", "user1", nil)
	readSystemMsg(t, conn) // consume system.connected

	if mock.countEvents("azure.webpubsub.sys.connect") != 1 {
		t.Error("sys.connect was not sent to upstream")
	}
}

func TestWebhook_SysConnect_PayloadContainsConnectionInfo(t *testing.T) {
	var captured capturedEvent
	mock, upstreamTS := newUpstreamMock(t)
	mock.onConnect = func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured = capturedEvent{
			Type:         r.Header.Get("ce-type"),
			ConnectionID: r.Header.Get("ce-connectionid"),
			Body:         body,
		}
		w.WriteHeader(http.StatusNoContent)
	}
	ts := newServerWithEventHandler(t, upstreamTS.URL)

	conn := wsConnect(t, ts, "hub1", "user1", nil)
	msg := readSystemMsg(t, conn)
	connID := msg["connectionId"].(string)

	if captured.ConnectionID != connID {
		t.Errorf("ce-connectionid: got %q, want %q", captured.ConnectionID, connID)
	}
	if captured.Type != "azure.webpubsub.sys.connect" {
		t.Errorf("ce-type: got %q", captured.Type)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(captured.Body, &payload); err != nil {
		t.Fatalf("unmarshal connect payload: %v", err)
	}
	if payload["claims"] == nil {
		t.Error("connect payload must include claims")
	}
}

func TestWebhook_SysConnect_Rejection_BlocksUpgrade(t *testing.T) {
	mock, upstreamTS := newUpstreamMock(t)
	mock.onConnect = func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden by upstream", http.StatusForbidden)
	}
	ts := newServerWithEventHandler(t, upstreamTS.URL)

	accessKeyBytes := mustDecodeBase64(t, testAccessKey)
	audience := fmt.Sprintf("http://%s/client/hubs/hub1", ts.Listener.Addr().String())
	tokenStr, _ := newToken(t, accessKeyBytes, audience, "user1")
	wsURL := fmt.Sprintf("ws://%s/client/hubs/hub1?access_token=%s",
		ts.Listener.Addr().String(), tokenStr)

	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Error("expected dial to fail when upstream rejects")
		return
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got: %v", resp)
	}
}

func TestWebhook_SysConnect_UserIDOverride(t *testing.T) {
	mock, upstreamTS := newUpstreamMock(t)
	mock.onConnect = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"userId": "overridden-by-upstream",
		})
	}
	ts := newServerWithEventHandler(t, upstreamTS.URL)

	conn := wsConnect(t, ts, "hub1", "original-user", nil)
	msg := readSystemMsg(t, conn)

	if msg["userId"] != "overridden-by-upstream" {
		t.Errorf("userId: got %q, want overridden-by-upstream", msg["userId"])
	}
}

func TestWebhook_SysConnected_Fired(t *testing.T) {
	mock, upstreamTS := newUpstreamMock(t)
	ts := newServerWithEventHandler(t, upstreamTS.URL)

	conn := wsConnect(t, ts, "hub1", "user1", nil)
	readSystemMsg(t, conn)

	if !mock.waitForEvent("azure.webpubsub.sys.connected", 3*time.Second) {
		t.Error("sys.connected was not sent to upstream within 3s")
	}
}

func TestWebhook_UserMessage_Forwarded(t *testing.T) {
	mock, upstreamTS := newUpstreamMock(t)
	ts := newServerWithEventHandler(t, upstreamTS.URL)

	conn := wsConnect(t, ts, "hub1", "user1", nil)
	readSystemMsg(t, conn)

	if err := conn.WriteMessage(websocket.TextMessage, []byte("hello upstream")); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	if !mock.waitForEvent("azure.webpubsub.user.message", 3*time.Second) {
		t.Error("user.message was not sent to upstream within 3s")
	}
}

func TestWebhook_UserMessage_Reply_ForwardedToClient(t *testing.T) {
	mock, upstreamTS := newUpstreamMock(t)
	mock.onMessage = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pong-from-upstream"))
	}
	ts := newServerWithEventHandler(t, upstreamTS.URL)

	conn := wsConnect(t, ts, "hub1", "user1", nil)
	readSystemMsg(t, conn)

	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(data) != "pong-from-upstream" {
		t.Errorf("reply: got %q, want pong-from-upstream", data)
	}
}

func TestWebhook_SysDisconnected_Fired(t *testing.T) {
	mock, upstreamTS := newUpstreamMock(t)
	ts := newServerWithEventHandler(t, upstreamTS.URL)

	conn := wsConnect(t, ts, "hub1", "user1", nil)
	readSystemMsg(t, conn)

	conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	conn.Close()

	if !mock.waitForEvent("azure.webpubsub.sys.disconnected", 3*time.Second) {
		t.Error("sys.disconnected was not sent to upstream within 3s")
	}
}

// ── Shared helper ──────────────────────────────────────────────────────────────

func newToken(t *testing.T, accessKey []byte, audience, userID string) (string, error) {
	t.Helper()
	return newTokenFull(accessKey, audience, userID, nil, nil)
}

func newTokenFull(accessKey []byte, audience, userID string, roles, groups []string) (string, error) {
	return auth.GenerateClientToken(accessKey, audience, userID, roles, groups, 0)
}
