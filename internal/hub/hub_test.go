package hub_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/hiro110/azure-web-pubsub-emulator/internal/hub"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newHub(t *testing.T) (*hub.Manager, *hub.Hub) {
	t.Helper()
	mgr := hub.NewManager()
	h := mgr.GetOrCreate("test")
	return mgr, h
}

func addConn(t *testing.T, h *hub.Hub, id, userID string) *hub.Connection {
	t.Helper()
	conn := hub.ExportNewConnection(id, userID)
	h.AddConnection(conn)
	return conn
}

func drainMessages(conn *hub.Connection) []hub.Message {
	var msgs []hub.Message
	for {
		select {
		case msg, ok := <-conn.Messages():
			if !ok {
				return msgs
			}
			msgs = append(msgs, msg)
		default:
			return msgs
		}
	}
}

func textMsg(data string) hub.Message {
	return hub.Message{Type: hub.MessageTypeText, Data: []byte(data)}
}

// ── Connection tests ──────────────────────────────────────────────────────────

func TestConnection_SendAndReceive(t *testing.T) {
	conn := hub.ExportNewConnection("c1", "user1")
	msg := textMsg("hello")

	if !conn.Send(msg) {
		t.Fatal("Send returned false unexpectedly")
	}
	msgs := drainMessages(conn)
	if len(msgs) != 1 || string(msgs[0].Data) != "hello" {
		t.Errorf("unexpected messages: %v", msgs)
	}
}

func TestConnection_SendAfterClose(t *testing.T) {
	conn := hub.ExportNewConnection("c1", "")
	conn.Close()

	if conn.Send(textMsg("should drop")) {
		t.Error("Send should return false after Close")
	}
	if !conn.IsClosed() {
		t.Error("IsClosed should return true")
	}
}

func TestConnection_DoubleClose(t *testing.T) {
	conn := hub.ExportNewConnection("c1", "")
	conn.Close()
	conn.Close() // must not panic
}

func TestConnection_SendBufferFull(t *testing.T) {
	conn := hub.ExportNewConnection("c1", "")
	// Fill the buffer
	for i := 0; i < hub.SendBufferSize; i++ {
		conn.Send(textMsg("x"))
	}
	// Next send should return false (drop)
	if conn.Send(textMsg("overflow")) {
		t.Error("expected Send to return false when buffer is full")
	}
}

// ── Connection lifecycle ──────────────────────────────────────────────────────

func TestHub_AddAndRemoveConnection(t *testing.T) {
	_, h := newHub(t)

	c := addConn(t, h, "c1", "user1")
	if !h.ConnectionExists(c.ID) {
		t.Error("connection should exist after AddConnection")
	}
	if !h.UserExists("user1") {
		t.Error("user should exist after AddConnection")
	}

	h.RemoveConnection(c.ID)
	if h.ConnectionExists(c.ID) {
		t.Error("connection should not exist after RemoveConnection")
	}
	if h.UserExists("user1") {
		t.Error("user should not exist after last connection removed")
	}
}

func TestHub_RemoveConnection_NotFound(t *testing.T) {
	_, h := newHub(t)
	h.RemoveConnection("nonexistent") // must not panic
}

func TestHub_RemoveConnection_CleansGroups(t *testing.T) {
	_, h := newHub(t)
	c := addConn(t, h, "c1", "")
	h.AddConnectionToGroup(c.ID, "g1") //nolint

	h.RemoveConnection(c.ID)
	if h.GroupExists("g1") {
		t.Error("group should be empty after its last member is removed")
	}
}

// ── Group management ──────────────────────────────────────────────────────────

func TestHub_GroupOperations(t *testing.T) {
	_, h := newHub(t)
	c1 := addConn(t, h, "c1", "")
	c2 := addConn(t, h, "c2", "")

	if err := h.AddConnectionToGroup(c1.ID, "g1"); err != nil {
		t.Fatalf("AddConnectionToGroup: %v", err)
	}
	if err := h.AddConnectionToGroup(c2.ID, "g1"); err != nil {
		t.Fatalf("AddConnectionToGroup: %v", err)
	}
	if !h.GroupExists("g1") {
		t.Error("group should exist")
	}

	h.RemoveConnectionFromGroup(c1.ID, "g1")
	if !h.GroupExists("g1") {
		t.Error("group should still exist with one member")
	}

	h.RemoveConnectionFromGroup(c2.ID, "g1")
	if h.GroupExists("g1") {
		t.Error("group should be gone when empty")
	}
}

func TestHub_AddConnectionToGroup_NotFound(t *testing.T) {
	_, h := newHub(t)
	err := h.AddConnectionToGroup("nonexistent", "g1")
	if err == nil {
		t.Error("expected error for unknown connection")
	}
}

func TestHub_RemoveConnectionFromAllGroups(t *testing.T) {
	_, h := newHub(t)
	c := addConn(t, h, "c1", "")
	h.AddConnectionToGroup(c.ID, "g1") //nolint
	h.AddConnectionToGroup(c.ID, "g2") //nolint

	h.RemoveConnectionFromAllGroups(c.ID)
	if h.GroupExists("g1") || h.GroupExists("g2") {
		t.Error("all groups should be gone")
	}
}

func TestHub_AddUserToGroup(t *testing.T) {
	_, h := newHub(t)
	c1 := addConn(t, h, "c1", "user1")
	c2 := addConn(t, h, "c2", "user1")

	h.AddUserToGroup("user1", "g1")
	if !h.GroupExists("g1") {
		t.Error("group should exist after AddUserToGroup")
	}

	// Both connections should receive group messages
	h.SendToGroup("g1", textMsg("hi"), nil)
	if len(drainMessages(c1)) != 1 || len(drainMessages(c2)) != 1 {
		t.Error("both user connections should receive the message")
	}
}

func TestHub_RemoveUserFromGroup(t *testing.T) {
	_, h := newHub(t)
	c := addConn(t, h, "c1", "user1")
	h.AddUserToGroup("user1", "g1")

	h.RemoveUserFromGroup("user1", "g1")
	if h.GroupExists("g1") {
		t.Error("group should be empty after user removed")
	}
	_ = c
}

func TestHub_RemoveUserFromAllGroups(t *testing.T) {
	_, h := newHub(t)
	c := addConn(t, h, "c1", "user1")
	h.AddUserToGroup("user1", "g1")
	h.AddUserToGroup("user1", "g2")

	h.RemoveUserFromAllGroups("user1")
	if h.GroupExists("g1") || h.GroupExists("g2") {
		t.Error("all groups should be empty")
	}
	_ = c
}

// ── Permission management ─────────────────────────────────────────────────────

func TestHub_Permissions(t *testing.T) {
	_, h := newHub(t)
	c := addConn(t, h, "c1", "")

	if err := h.GrantPermission(c.ID, "webpubsub.sendToGroup"); err != nil {
		t.Fatalf("GrantPermission: %v", err)
	}
	if !h.HasPermission(c.ID, "webpubsub.sendToGroup") {
		t.Error("should have permission after grant")
	}

	if err := h.RevokePermission(c.ID, "webpubsub.sendToGroup"); err != nil {
		t.Fatalf("RevokePermission: %v", err)
	}
	if h.HasPermission(c.ID, "webpubsub.sendToGroup") {
		t.Error("should not have permission after revoke")
	}
}

func TestHub_Permissions_ConnectionNotFound(t *testing.T) {
	_, h := newHub(t)
	if err := h.GrantPermission("missing", "perm"); err == nil {
		t.Error("expected error for missing connection")
	}
	if err := h.RevokePermission("missing", "perm"); err == nil {
		t.Error("expected error for missing connection")
	}
	if h.HasPermission("missing", "perm") {
		t.Error("missing connection should not have permission")
	}
}

// ── Close ─────────────────────────────────────────────────────────────────────

func TestHub_CloseConnection(t *testing.T) {
	_, h := newHub(t)
	c := addConn(t, h, "c1", "")

	if !h.CloseConnection(c.ID) {
		t.Error("CloseConnection should return true for existing connection")
	}
	if !c.IsClosed() {
		t.Error("connection should be closed")
	}
	if h.CloseConnection("nonexistent") {
		t.Error("CloseConnection should return false for missing connection")
	}
}

func TestHub_CloseUserConnections(t *testing.T) {
	_, h := newHub(t)
	c1 := addConn(t, h, "c1", "user1")
	c2 := addConn(t, h, "c2", "user1")

	h.CloseUserConnections("user1")
	if !c1.IsClosed() || !c2.IsClosed() {
		t.Error("all user connections should be closed")
	}
}

// ── Message broadcasting ──────────────────────────────────────────────────────

func TestHub_SendToConnection(t *testing.T) {
	_, h := newHub(t)
	c := addConn(t, h, "c1", "")
	msg := textMsg("hello")

	if !h.SendToConnection(c.ID, msg) {
		t.Error("SendToConnection should return true")
	}
	if !h.SendToConnection("missing", msg) == false {
		t.Error("SendToConnection should return false for missing connection")
	}
	msgs := drainMessages(c)
	if len(msgs) != 1 {
		t.Errorf("expected 1 message, got %d", len(msgs))
	}
}

func TestHub_SendToGroup(t *testing.T) {
	_, h := newHub(t)
	c1 := addConn(t, h, "c1", "")
	c2 := addConn(t, h, "c2", "")
	c3 := addConn(t, h, "c3", "") // not in group
	h.AddConnectionToGroup(c1.ID, "g1") //nolint
	h.AddConnectionToGroup(c2.ID, "g1") //nolint

	h.SendToGroup("g1", textMsg("hi"), nil)
	if len(drainMessages(c1)) != 1 {
		t.Error("c1 should receive message")
	}
	if len(drainMessages(c2)) != 1 {
		t.Error("c2 should receive message")
	}
	if len(drainMessages(c3)) != 0 {
		t.Error("c3 should not receive message")
	}
}

func TestHub_SendToGroup_WithExcluded(t *testing.T) {
	_, h := newHub(t)
	c1 := addConn(t, h, "c1", "")
	c2 := addConn(t, h, "c2", "")
	h.AddConnectionToGroup(c1.ID, "g1") //nolint
	h.AddConnectionToGroup(c2.ID, "g1") //nolint

	excluded := map[string]struct{}{c1.ID: {}}
	h.SendToGroup("g1", textMsg("hi"), excluded)
	if len(drainMessages(c1)) != 0 {
		t.Error("c1 should be excluded")
	}
	if len(drainMessages(c2)) != 1 {
		t.Error("c2 should receive message")
	}
}

func TestHub_SendToUser(t *testing.T) {
	_, h := newHub(t)
	c1 := addConn(t, h, "c1", "user1")
	c2 := addConn(t, h, "c2", "user1")
	c3 := addConn(t, h, "c3", "user2")

	h.SendToUser("user1", textMsg("hello"))
	if len(drainMessages(c1)) != 1 || len(drainMessages(c2)) != 1 {
		t.Error("both user1 connections should receive message")
	}
	if len(drainMessages(c3)) != 0 {
		t.Error("user2 should not receive message")
	}
}

func TestHub_SendToAll(t *testing.T) {
	_, h := newHub(t)
	c1 := addConn(t, h, "c1", "")
	c2 := addConn(t, h, "c2", "")
	c3 := addConn(t, h, "c3", "")

	excluded := map[string]struct{}{c3.ID: {}}
	h.SendToAll(textMsg("broadcast"), excluded)
	if len(drainMessages(c1)) != 1 || len(drainMessages(c2)) != 1 {
		t.Error("c1 and c2 should receive broadcast")
	}
	if len(drainMessages(c3)) != 0 {
		t.Error("c3 should be excluded")
	}
}

// ── Manager tests ─────────────────────────────────────────────────────────────

func TestManager_GetOrCreate(t *testing.T) {
	mgr := hub.NewManager()
	h1 := mgr.GetOrCreate("hub1")
	h2 := mgr.GetOrCreate("hub1")
	if h1 != h2 {
		t.Error("GetOrCreate should return the same hub instance")
	}
}

func TestManager_Get_NotFound(t *testing.T) {
	mgr := hub.NewManager()
	if mgr.Get("nonexistent") != nil {
		t.Error("Get should return nil for unknown hub")
	}
}

func TestManager_NewConnection_AssignsUUID(t *testing.T) {
	mgr := hub.NewManager()
	c1 := mgr.NewConnection("hub1", "user1")
	c2 := mgr.NewConnection("hub1", "user2")
	if c1.ID == "" || c2.ID == "" {
		t.Error("connection IDs should not be empty")
	}
	if c1.ID == c2.ID {
		t.Error("connection IDs should be unique")
	}
}

// ── Concurrent access ─────────────────────────────────────────────────────────

func TestHub_ConcurrentAddRemove(t *testing.T) {
	_, h := newHub(t)
	const goroutines = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("conn-%d", i)
			c := hub.ExportNewConnection(id, "")
			h.AddConnection(c)
			h.AddConnectionToGroup(id, "group1") //nolint
			h.RemoveConnection(id)
		}(i)
	}
	wg.Wait()

	if h.GroupExists("group1") {
		t.Error("group should be empty after all connections removed")
	}
}

func TestHub_ConcurrentSend(t *testing.T) {
	_, h := newHub(t)
	const goroutines = 20
	const msgsPerGoroutine = 10

	c := addConn(t, h, "c1", "")

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range msgsPerGoroutine {
				h.SendToConnection(c.ID, textMsg("x"))
			}
		}()
	}
	wg.Wait()
}

func TestManager_ConcurrentGetOrCreate(t *testing.T) {
	mgr := hub.NewManager()
	const goroutines = 100

	var wg sync.WaitGroup
	results := make([]*hub.Hub, goroutines)
	wg.Add(goroutines)
	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			results[i] = mgr.GetOrCreate("shared-hub")
		}(i)
	}
	wg.Wait()

	first := results[0]
	for _, h := range results[1:] {
		if h != first {
			t.Error("all goroutines should get the same hub instance")
			break
		}
	}
}
