package server_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/hiro110/azure-web-pubsub-emulator/internal/auth"
)

// wsConnect dials a WebSocket connection to the test server for the given hub,
// generating a valid JWT access token from testAccessKey.
func wsConnect(t *testing.T, ts *httptest.Server, hubName, userID string, groups []string) *websocket.Conn {
	t.Helper()
	accessKeyBytes := mustDecodeBase64(t, testAccessKey)

	// Audience must reference the test server URL
	audience := fmt.Sprintf("http://%s/client/hubs/%s", ts.Listener.Addr().String(), hubName)
	tokenStr, err := auth.GenerateClientToken(accessKeyBytes, audience, userID, nil, groups, 0)
	if err != nil {
		t.Fatalf("GenerateClientToken: %v", err)
	}

	wsURL := fmt.Sprintf("ws://%s/client/hubs/%s?access_token=%s",
		ts.Listener.Addr().String(), hubName, tokenStr)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// readSystemMsg reads one message from conn and unmarshals it.
func readSystemMsg(t *testing.T, conn *websocket.Conn) map[string]interface{} {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	var msg map[string]interface{}
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	return msg
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestWebSocket_Connect_ReceivesSystemConnected(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := wsConnect(t, ts, "hub1", "user1", nil)

	msg := readSystemMsg(t, conn)
	if msg["type"] != "system" {
		t.Errorf("type: got %q, want system", msg["type"])
	}
	if msg["event"] != "connected" {
		t.Errorf("event: got %q, want connected", msg["event"])
	}
	if msg["userId"] != "user1" {
		t.Errorf("userId: got %q, want user1", msg["userId"])
	}
	if msg["connectionId"] == "" {
		t.Error("connectionId must not be empty")
	}
}

func TestWebSocket_Connect_AnonymousUser(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := wsConnect(t, ts, "hub1", "", nil)

	msg := readSystemMsg(t, conn)
	if msg["event"] != "connected" {
		t.Fatalf("expected connected event, got %q", msg["event"])
	}
	// userId must be absent (omitempty)
	if _, ok := msg["userId"]; ok {
		t.Error("userId should be omitted for anonymous users")
	}
}

func TestWebSocket_Connect_InvalidToken(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	wsURL := fmt.Sprintf("ws://%s/client/hubs/hub1?access_token=not.a.valid.jwt",
		ts.Listener.Addr().String())
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Error("expected dial to fail with invalid token")
		return
	}
	if resp == nil || resp.StatusCode != 401 {
		t.Errorf("expected 401, got: %v", resp)
	}
}

func TestWebSocket_Connect_MissingToken(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	wsURL := fmt.Sprintf("ws://%s/client/hubs/hub1", ts.Listener.Addr().String())
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Error("expected dial to fail without token")
		return
	}
	if resp == nil || resp.StatusCode != 401 {
		t.Errorf("expected 401, got: %v", resp)
	}
}

func TestWebSocket_Connect_WrongHub(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	accessKeyBytes := mustDecodeBase64(t, testAccessKey)
	// Token issued for hub1, but connecting to hub2
	audience := fmt.Sprintf("http://%s/client/hubs/hub1", ts.Listener.Addr().String())
	tokenStr, _ := auth.GenerateClientToken(accessKeyBytes, audience, "", nil, nil, 0)

	wsURL := fmt.Sprintf("ws://%s/client/hubs/hub2?access_token=%s",
		ts.Listener.Addr().String(), tokenStr)
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Error("expected dial to fail with mismatched hub in audience")
		return
	}
	if resp == nil || resp.StatusCode != 401 {
		t.Errorf("expected 401, got: %v", resp)
	}
}

func TestWebSocket_Disconnect_RemovesConnection(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := wsConnect(t, ts, "hub1", "user1", nil)
	msg := readSystemMsg(t, conn)
	connID := msg["connectionId"].(string)

	h := srv.Manager().GetOrCreate("hub1")
	if !h.ConnectionExists(connID) {
		t.Fatal("connection should be registered after connect")
	}

	// Close from client side
	conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	conn.Close()

	// Give the read pump time to clean up
	time.Sleep(100 * time.Millisecond)

	if h.ConnectionExists(connID) {
		t.Error("connection should be removed after disconnect")
	}
}

func TestWebSocket_PreJoinGroups(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := wsConnect(t, ts, "hub1", "user1", []string{"room1", "room2"})
	msg := readSystemMsg(t, conn)
	connID := msg["connectionId"].(string)

	h := srv.Manager().GetOrCreate("hub1")
	for _, group := range []string{"room1", "room2"} {
		if !h.GroupExists(group) {
			t.Errorf("should be pre-joined to group %q", group)
		}
		_ = connID
	}
}

func TestWebSocket_MultipleClients_ReceiveConnectedWithUniqueIDs(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	c1 := wsConnect(t, ts, "hub1", "user1", nil)
	c2 := wsConnect(t, ts, "hub1", "user2", nil)

	m1 := readSystemMsg(t, c1)
	m2 := readSystemMsg(t, c2)

	id1 := m1["connectionId"].(string)
	id2 := m2["connectionId"].(string)
	if id1 == "" || id2 == "" {
		t.Error("connection IDs must not be empty")
	}
	if id1 == id2 {
		t.Errorf("connection IDs must be unique, both got %q", id1)
	}

	// userIds must match
	if m1["userId"] != "user1" {
		t.Errorf("c1 userId: got %q, want user1", m1["userId"])
	}
	if !strings.Contains(fmt.Sprintf("%v", m2["userId"]), "user2") {
		t.Errorf("c2 userId: got %q, want user2", m2["userId"])
	}
}
