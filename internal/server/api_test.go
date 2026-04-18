package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/hiro110/azure-web-pubsub-emulator/internal/auth"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// apiDo signs and executes an authenticated data-plane request.
func apiDo(t *testing.T, ts *httptest.Server, method, path, contentType, body string) *http.Response {
	t.Helper()
	var bodyReader *strings.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	var req *http.Request
	var err error
	if bodyReader != nil {
		req, err = http.NewRequest(method, ts.URL+path, bodyReader)
	} else {
		req, err = http.NewRequest(method, ts.URL+path, nil)
	}
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	accessKeyBytes := mustDecodeBase64(t, testAccessKey)
	if err := auth.SignRequest(req, accessKeyBytes); err != nil {
		t.Fatalf("SignRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do %s %s: %v", method, path, err)
	}
	return resp
}

// connectWS connects a WebSocket client and drains the system.connected message.
// Returns connID.
func connectWS(t *testing.T, ts *httptest.Server, hubName, userID string) (*websocket.Conn, string) {
	t.Helper()
	conn := wsConnect(t, ts, hubName, userID, nil)
	msg := readSystemMsg(t, conn)
	return conn, msg["connectionId"].(string)
}

// ── Send To All ───────────────────────────────────────────────────────────────

func TestAPI_SendToAll_202(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	wsConn, _ := connectWS(t, ts, "hub1", "user1")
	resp := apiDo(t, ts, http.MethodPost, "/api/hubs/hub1/:send", "text/plain", "hello")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status: got %d, want 202", resp.StatusCode)
	}

	// WebSocket client should receive the broadcast
	wsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("payload: got %q, want hello", data)
	}
}

func TestAPI_SendToAll_ExcludeConnection(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	c1, id1 := connectWS(t, ts, "hub1", "user1")
	c2, _ := connectWS(t, ts, "hub1", "user2")

	path := fmt.Sprintf("/api/hubs/hub1/:send?excluded=%s", id1)
	resp := apiDo(t, ts, http.MethodPost, path, "text/plain", "broadcast")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status: got %d, want 202", resp.StatusCode)
	}

	// c1 (excluded) should not receive; c2 should
	c1.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, _, err := c1.ReadMessage(); err == nil {
		t.Error("excluded connection should not receive message")
	}

	c2.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := c2.ReadMessage()
	if err != nil {
		t.Fatalf("c2 ReadMessage: %v", err)
	}
	if string(data) != "broadcast" {
		t.Errorf("c2 payload: got %q, want broadcast", data)
	}
}

func TestAPI_SendToAll_FilterByUserId(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	c1, _ := connectWS(t, ts, "hub1", "listener-1")
	c2, _ := connectWS(t, ts, "hub1", "sender-1")

	resp := apiDo(t, ts, http.MethodPost,
		"/api/hubs/hub1/:send?filter=userId+eq+'listener-1'",
		"text/plain", "filtered")
	defer resp.Body.Close()

	// c1 (listener-1) should receive
	c1.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := c1.ReadMessage()
	if err != nil {
		t.Fatalf("c1 ReadMessage: %v", err)
	}
	if string(data) != "filtered" {
		t.Errorf("c1 payload: got %q, want filtered", data)
	}

	// c2 (sender-1) should not receive
	c2.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, _, err := c2.ReadMessage(); err == nil {
		t.Error("non-matching connection should not receive filtered message")
	}
}

// ── Send To Connection ────────────────────────────────────────────────────────

func TestAPI_SendToConnection_202(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	wsConn, connID := connectWS(t, ts, "hub1", "user1")
	path := fmt.Sprintf("/api/hubs/hub1/connections/%s/:send", connID)
	resp := apiDo(t, ts, http.MethodPost, path, "text/plain", "direct")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status: got %d, want 202", resp.StatusCode)
	}

	wsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(data) != "direct" {
		t.Errorf("payload: got %q, want direct", data)
	}
}

func TestAPI_SendToConnection_404_UnknownConn(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp := apiDo(t, ts, http.MethodPost, "/api/hubs/hub1/connections/no-such-conn/:send", "text/plain", "x")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
}

// ── Send To Group ─────────────────────────────────────────────────────────────

func TestAPI_SendToGroup_202(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	wsConn, connID := connectWS(t, ts, "hub1", "user1")
	// Add connection to group
	apiDo(t, ts, http.MethodPut,
		fmt.Sprintf("/api/hubs/hub1/connections/%s/groups/room1", connID), "", "").Body.Close()

	resp := apiDo(t, ts, http.MethodPost, "/api/hubs/hub1/groups/room1/:send", "text/plain", "group-msg")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status: got %d, want 202", resp.StatusCode)
	}

	wsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(data) != "group-msg" {
		t.Errorf("payload: got %q, want group-msg", data)
	}
}

// ── Send To User ──────────────────────────────────────────────────────────────

func TestAPI_SendToUser_202(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	wsConn, _ := connectWS(t, ts, "hub1", "alice")

	resp := apiDo(t, ts, http.MethodPost, "/api/hubs/hub1/users/alice/:send", "text/plain", "hey alice")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status: got %d, want 202", resp.StatusCode)
	}

	wsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(data) != "hey alice" {
		t.Errorf("payload: got %q, want 'hey alice'", data)
	}
}

// ── Close operations ──────────────────────────────────────────────────────────

func TestAPI_CloseConnection_204(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	_, connID := connectWS(t, ts, "hub1", "user1")
	resp := apiDo(t, ts, http.MethodDelete,
		fmt.Sprintf("/api/hubs/hub1/connections/%s", connID), "", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status: got %d, want 204", resp.StatusCode)
	}
}

func TestAPI_CloseConnection_404_Unknown(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp := apiDo(t, ts, http.MethodDelete, "/api/hubs/hub1/connections/no-such", "", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
}

func TestAPI_CloseAllConnections_204(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	connectWS(t, ts, "hub1", "user1")
	connectWS(t, ts, "hub1", "user2")

	resp := apiDo(t, ts, http.MethodPost, "/api/hubs/hub1/:closeConnections", "", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status: got %d, want 204", resp.StatusCode)
	}
	// Give write pumps time to close
	time.Sleep(100 * time.Millisecond)
	h := srv.Manager().GetOrCreate("hub1")
	if h.ConnectionExists("any") {
		t.Error("expected hub to have no connections after closeAllConnections")
	}
}

func TestAPI_CloseGroupConnections_204(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	_, id1 := connectWS(t, ts, "hub1", "user1")
	_, id2 := connectWS(t, ts, "hub1", "user2")

	// Add both to group
	apiDo(t, ts, http.MethodPut,
		fmt.Sprintf("/api/hubs/hub1/connections/%s/groups/room1", id1), "", "").Body.Close()
	apiDo(t, ts, http.MethodPut,
		fmt.Sprintf("/api/hubs/hub1/connections/%s/groups/room1", id2), "", "").Body.Close()

	resp := apiDo(t, ts, http.MethodPost, "/api/hubs/hub1/groups/room1/:closeConnections", "", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status: got %d, want 204", resp.StatusCode)
	}
}

func TestAPI_CloseUserConnections_204(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	connectWS(t, ts, "hub1", "alice")

	resp := apiDo(t, ts, http.MethodPost, "/api/hubs/hub1/users/alice/:closeConnections", "", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status: got %d, want 204", resp.StatusCode)
	}
}

// ── Exists checks ─────────────────────────────────────────────────────────────

func TestAPI_ConnectionExists_200(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	_, connID := connectWS(t, ts, "hub1", "user1")
	resp := apiDo(t, ts, http.MethodHead,
		fmt.Sprintf("/api/hubs/hub1/connections/%s", connID), "", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
}

func TestAPI_ConnectionExists_404(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp := apiDo(t, ts, http.MethodHead, "/api/hubs/hub1/connections/ghost", "", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
}

func TestAPI_UserExists_200_and_404(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	connectWS(t, ts, "hub1", "alice")

	r1 := apiDo(t, ts, http.MethodHead, "/api/hubs/hub1/users/alice", "", "")
	defer r1.Body.Close()
	if r1.StatusCode != http.StatusOK {
		t.Errorf("alice exists: got %d, want 200", r1.StatusCode)
	}

	r2 := apiDo(t, ts, http.MethodHead, "/api/hubs/hub1/users/nobody", "", "")
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusNotFound {
		t.Errorf("nobody: got %d, want 404", r2.StatusCode)
	}
}

func TestAPI_GroupExists_200_and_404(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	_, connID := connectWS(t, ts, "hub1", "user1")
	apiDo(t, ts, http.MethodPut,
		fmt.Sprintf("/api/hubs/hub1/connections/%s/groups/room1", connID), "", "").Body.Close()

	r1 := apiDo(t, ts, http.MethodHead, "/api/hubs/hub1/groups/room1", "", "")
	defer r1.Body.Close()
	if r1.StatusCode != http.StatusOK {
		t.Errorf("room1 exists: got %d, want 200", r1.StatusCode)
	}

	r2 := apiDo(t, ts, http.MethodHead, "/api/hubs/hub1/groups/empty", "", "")
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusNotFound {
		t.Errorf("empty: got %d, want 404", r2.StatusCode)
	}
}

// ── Group management ──────────────────────────────────────────────────────────

func TestAPI_AddAndRemoveConnectionFromGroup(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	_, connID := connectWS(t, ts, "hub1", "user1")

	// Add
	r1 := apiDo(t, ts, http.MethodPut,
		fmt.Sprintf("/api/hubs/hub1/connections/%s/groups/room1", connID), "", "")
	r1.Body.Close()
	if r1.StatusCode != http.StatusOK {
		t.Errorf("PUT group: got %d, want 200", r1.StatusCode)
	}
	if !srv.Manager().GetOrCreate("hub1").GroupExists("room1") {
		t.Error("group should exist after add")
	}

	// Remove
	r2 := apiDo(t, ts, http.MethodDelete,
		fmt.Sprintf("/api/hubs/hub1/connections/%s/groups/room1", connID), "", "")
	r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Errorf("DELETE group: got %d, want 200", r2.StatusCode)
	}
	if srv.Manager().GetOrCreate("hub1").GroupExists("room1") {
		t.Error("group should be empty after remove")
	}
}

func TestAPI_RemoveConnectionFromAllGroups(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	_, connID := connectWS(t, ts, "hub1", "user1")
	for _, g := range []string{"g1", "g2", "g3"} {
		apiDo(t, ts, http.MethodPut,
			fmt.Sprintf("/api/hubs/hub1/connections/%s/groups/%s", connID, g), "", "").Body.Close()
	}

	resp := apiDo(t, ts, http.MethodDelete,
		fmt.Sprintf("/api/hubs/hub1/connections/%s/groups", connID), "", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	h := srv.Manager().GetOrCreate("hub1")
	for _, g := range []string{"g1", "g2", "g3"} {
		if h.GroupExists(g) {
			t.Errorf("group %q should be empty after removeFromAll", g)
		}
	}
}

func TestAPI_AddUserToGroup_And_Remove(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	connectWS(t, ts, "hub1", "alice")

	r1 := apiDo(t, ts, http.MethodPut, "/api/hubs/hub1/users/alice/groups/vip", "", "")
	r1.Body.Close()
	if r1.StatusCode != http.StatusOK {
		t.Errorf("PUT user group: got %d, want 200", r1.StatusCode)
	}
	if !srv.Manager().GetOrCreate("hub1").GroupExists("vip") {
		t.Error("group should exist after addUserToGroup")
	}

	r2 := apiDo(t, ts, http.MethodDelete, "/api/hubs/hub1/users/alice/groups/vip", "", "")
	r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Errorf("DELETE user group: got %d, want 200", r2.StatusCode)
	}
	if srv.Manager().GetOrCreate("hub1").GroupExists("vip") {
		t.Error("group should be empty after removeUserFromGroup")
	}
}

func TestAPI_RemoveUserFromAllGroups(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	connectWS(t, ts, "hub1", "bob")
	for _, g := range []string{"a", "b"} {
		apiDo(t, ts, http.MethodPut,
			fmt.Sprintf("/api/hubs/hub1/users/bob/groups/%s", g), "", "").Body.Close()
	}

	resp := apiDo(t, ts, http.MethodDelete, "/api/hubs/hub1/users/bob/groups", "", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	h := srv.Manager().GetOrCreate("hub1")
	for _, g := range []string{"a", "b"} {
		if h.GroupExists(g) {
			t.Errorf("group %q should be empty after removeUserFromAllGroups", g)
		}
	}
}

// ── List connections in group ─────────────────────────────────────────────────

func TestAPI_ListConnectionsInGroup(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	_, id1 := connectWS(t, ts, "hub1", "u1")
	_, id2 := connectWS(t, ts, "hub1", "u2")
	for _, id := range []string{id1, id2} {
		apiDo(t, ts, http.MethodPut,
			fmt.Sprintf("/api/hubs/hub1/connections/%s/groups/room1", id), "", "").Body.Close()
	}

	resp := apiDo(t, ts, http.MethodGet, "/api/hubs/hub1/groups/room1/connections", "", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var result struct {
		Value []string `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Value) != 2 {
		t.Errorf("expected 2 connections, got %d", len(result.Value))
	}
}

// ── Batch group operations ────────────────────────────────────────────────────

func TestAPI_AddConnectionsToGroups_Filtered(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	connectWS(t, ts, "hub1", "listener-1")
	connectWS(t, ts, "hub1", "sender-1")

	body, _ := json.Marshal(map[string]interface{}{
		"groups": []string{"g1", "g2"},
		"filter": "userId eq 'listener-1'",
	})
	resp := apiDo(t, ts, http.MethodPost, "/api/hubs/hub1/:addToGroups",
		"application/json", string(body))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}

	h := srv.Manager().GetOrCreate("hub1")
	if !h.GroupExists("g1") || !h.GroupExists("g2") {
		t.Error("groups should exist after batch add")
	}
	if ids := h.ListConnectionsInGroup("g1"); len(ids) != 1 {
		t.Errorf("expected 1 connection in g1 (filtered), got %d", len(ids))
	}
}

func TestAPI_RemoveConnectionsFromGroups_Filtered(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	connectWS(t, ts, "hub1", "user1")
	connectWS(t, ts, "hub1", "user2")

	// Add all to group
	addBody, _ := json.Marshal(map[string]interface{}{
		"groups": []string{"room1"},
	})
	apiDo(t, ts, http.MethodPost, "/api/hubs/hub1/:addToGroups",
		"application/json", string(addBody)).Body.Close()

	// Remove only user1
	removeBody, _ := json.Marshal(map[string]interface{}{
		"groups": []string{"room1"},
		"filter": "userId eq 'user1'",
	})
	resp := apiDo(t, ts, http.MethodPost, "/api/hubs/hub1/:removeFromGroups",
		"application/json", string(removeBody))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}

	// user2 should still be in the group
	if ids := srv.Manager().GetOrCreate("hub1").ListConnectionsInGroup("room1"); len(ids) != 1 {
		t.Errorf("expected 1 connection remaining in room1, got %d", len(ids))
	}
}

// ── Permissions ───────────────────────────────────────────────────────────────

func TestAPI_Permissions_GrantCheckRevoke(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	_, connID := connectWS(t, ts, "hub1", "user1")
	permPath := fmt.Sprintf("/api/hubs/hub1/permissions/joinLeaveGroup/connections/%s?targetName=room1", connID)

	// Check — should be 404 initially
	r0 := apiDo(t, ts, http.MethodGet, permPath, "", "")
	r0.Body.Close()
	if r0.StatusCode != http.StatusNotFound {
		t.Errorf("initial check: got %d, want 404", r0.StatusCode)
	}

	// Grant
	r1 := apiDo(t, ts, http.MethodPut, permPath, "", "")
	r1.Body.Close()
	if r1.StatusCode != http.StatusOK {
		t.Errorf("grant: got %d, want 200", r1.StatusCode)
	}

	// Check — should be 200
	r2 := apiDo(t, ts, http.MethodGet, permPath, "", "")
	r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Errorf("check after grant: got %d, want 200", r2.StatusCode)
	}

	// Revoke
	r3 := apiDo(t, ts, http.MethodDelete, permPath, "", "")
	r3.Body.Close()
	if r3.StatusCode != http.StatusOK {
		t.Errorf("revoke: got %d, want 200", r3.StatusCode)
	}

	// Check — should be 404 again
	r4 := apiDo(t, ts, http.MethodGet, permPath, "", "")
	r4.Body.Close()
	if r4.StatusCode != http.StatusNotFound {
		t.Errorf("check after revoke: got %d, want 404", r4.StatusCode)
	}
}

// ── parseFilter helper ────────────────────────────────────────────────────────

func TestAPI_SendToAll_Binary(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	wsConn, _ := connectWS(t, ts, "hub1", "user1")

	body := []byte{0x01, 0x02, 0x03}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/hubs/hub1/:send", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	accessKeyBytes := mustDecodeBase64(t, testAccessKey)
	auth.SignRequest(req, accessKeyBytes)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	wsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, data, err := wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msgType != websocket.BinaryMessage {
		t.Errorf("message type: got %d, want BinaryMessage(%d)", msgType, websocket.BinaryMessage)
	}
	if !bytes.Equal(data, body) {
		t.Errorf("payload mismatch")
	}
}
