package hub

import (
	"fmt"
	"sync"
)

// Hub manages connections, group memberships, and user→connection mappings
// for a single Azure Web PubSub hub.
//
// Concurrency model:
//   - mu (RWMutex) guards connections, groups, and users maps, plus each
//     Connection's groups and permissions fields.
//   - conn.mu guards conn.closed and the send channel — callers must NOT hold
//     Hub.mu when calling conn.Send() or conn.Close() to avoid lock inversion.
type Hub struct {
	name string
	mu   sync.RWMutex

	connections map[string]*Connection         // connectionId → *Connection
	groups      map[string]map[string]struct{} // groupName → set of connectionIds
	users       map[string]map[string]struct{} // userId    → set of connectionIds
}

func newHub(name string) *Hub {
	return &Hub{
		name:        name,
		connections: make(map[string]*Connection),
		groups:      make(map[string]map[string]struct{}),
		users:       make(map[string]map[string]struct{}),
	}
}

// ── Connection lifecycle ──────────────────────────────────────────────────────

// AddConnection registers conn in the hub.
func (h *Hub) AddConnection(conn *Connection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.connections[conn.ID] = conn
	if conn.UserID != "" {
		if h.users[conn.UserID] == nil {
			h.users[conn.UserID] = make(map[string]struct{})
		}
		h.users[conn.UserID][conn.ID] = struct{}{}
	}
}

// RemoveConnection unregisters conn and removes it from all groups/users.
func (h *Hub) RemoveConnection(connectionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	conn, ok := h.connections[connectionID]
	if !ok {
		return
	}
	for group := range conn.groups {
		delete(h.groups[group], connectionID)
		if len(h.groups[group]) == 0 {
			delete(h.groups, group)
		}
	}
	if conn.UserID != "" {
		delete(h.users[conn.UserID], connectionID)
		if len(h.users[conn.UserID]) == 0 {
			delete(h.users, conn.UserID)
		}
	}
	delete(h.connections, connectionID)
}

// ConnectionExists returns true if connectionID is registered.
func (h *Hub) ConnectionExists(connectionID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.connections[connectionID]
	return ok
}

// UserExists returns true if any active connection belongs to userID.
func (h *Hub) UserExists(userID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.users[userID]) > 0
}

// GroupExists returns true if the group has at least one connection.
func (h *Hub) GroupExists(group string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.groups[group]) > 0
}

// ── Group management ──────────────────────────────────────────────────────────

// AddConnectionToGroup adds connectionID to group. Returns an error if
// the connection is not registered.
func (h *Hub) AddConnectionToGroup(connectionID, group string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	conn, ok := h.connections[connectionID]
	if !ok {
		return fmt.Errorf("connection %q not found", connectionID)
	}
	if h.groups[group] == nil {
		h.groups[group] = make(map[string]struct{})
	}
	h.groups[group][connectionID] = struct{}{}
	conn.groups[group] = struct{}{}
	return nil
}

// RemoveConnectionFromGroup removes connectionID from group. No-op if either
// the connection or group does not exist.
func (h *Hub) RemoveConnectionFromGroup(connectionID, group string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	conn, ok := h.connections[connectionID]
	if !ok {
		return
	}
	delete(conn.groups, group)
	if h.groups[group] != nil {
		delete(h.groups[group], connectionID)
		if len(h.groups[group]) == 0 {
			delete(h.groups, group)
		}
	}
}

// RemoveConnectionFromAllGroups removes connectionID from every group it belongs to.
func (h *Hub) RemoveConnectionFromAllGroups(connectionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	conn, ok := h.connections[connectionID]
	if !ok {
		return
	}
	for group := range conn.groups {
		delete(h.groups[group], connectionID)
		if len(h.groups[group]) == 0 {
			delete(h.groups, group)
		}
	}
	conn.groups = make(map[string]struct{})
}

// AddUserToGroup adds all connections for userID to group.
func (h *Hub) AddUserToGroup(userID, group string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.groups[group] == nil {
		h.groups[group] = make(map[string]struct{})
	}
	for connID := range h.users[userID] {
		conn, ok := h.connections[connID]
		if !ok {
			continue
		}
		h.groups[group][connID] = struct{}{}
		conn.groups[group] = struct{}{}
	}
}

// RemoveUserFromGroup removes all connections for userID from group.
func (h *Hub) RemoveUserFromGroup(userID, group string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for connID := range h.users[userID] {
		conn, ok := h.connections[connID]
		if !ok {
			continue
		}
		delete(conn.groups, group)
		if h.groups[group] != nil {
			delete(h.groups[group], connID)
		}
	}
	if len(h.groups[group]) == 0 {
		delete(h.groups, group)
	}
}

// RemoveUserFromAllGroups removes all connections for userID from every group.
func (h *Hub) RemoveUserFromAllGroups(userID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for connID := range h.users[userID] {
		conn, ok := h.connections[connID]
		if !ok {
			continue
		}
		for group := range conn.groups {
			delete(h.groups[group], connID)
			if len(h.groups[group]) == 0 {
				delete(h.groups, group)
			}
		}
		conn.groups = make(map[string]struct{})
	}
}

// ── Permission management ─────────────────────────────────────────────────────

// GrantPermission adds permission to the named connection.
func (h *Hub) GrantPermission(connectionID, permission string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	conn, ok := h.connections[connectionID]
	if !ok {
		return fmt.Errorf("connection %q not found", connectionID)
	}
	conn.permissions[permission] = struct{}{}
	return nil
}

// RevokePermission removes permission from the named connection.
func (h *Hub) RevokePermission(connectionID, permission string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	conn, ok := h.connections[connectionID]
	if !ok {
		return fmt.Errorf("connection %q not found", connectionID)
	}
	delete(conn.permissions, permission)
	return nil
}

// HasPermission returns true if the connection holds the given permission.
func (h *Hub) HasPermission(connectionID, permission string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	conn, ok := h.connections[connectionID]
	if !ok {
		return false
	}
	_, has := conn.permissions[permission]
	return has
}

// ── Close ─────────────────────────────────────────────────────────────────────

// CloseConnection closes the send channel of connectionID, signalling its
// WebSocket write loop to shut down. Returns false if not found.
func (h *Hub) CloseConnection(connectionID string) bool {
	h.mu.RLock()
	conn, ok := h.connections[connectionID]
	h.mu.RUnlock()
	if !ok {
		return false
	}
	conn.Close()
	return true
}

// CloseUserConnections closes all connections for userID.
func (h *Hub) CloseUserConnections(userID string) {
	h.mu.RLock()
	ids := make([]string, 0, len(h.users[userID]))
	for id := range h.users[userID] {
		ids = append(ids, id)
	}
	h.mu.RUnlock()

	for _, id := range ids {
		h.mu.RLock()
		conn, ok := h.connections[id]
		h.mu.RUnlock()
		if ok {
			conn.Close()
		}
	}
}

// ── Message broadcasting ──────────────────────────────────────────────────────

// SendToConnection delivers msg to connectionID. Returns false if not found.
func (h *Hub) SendToConnection(connectionID string, msg Message) bool {
	h.mu.RLock()
	conn, ok := h.connections[connectionID]
	h.mu.RUnlock()
	if !ok {
		return false
	}
	return conn.Send(msg)
}

// SendToGroup delivers msg to all connections in group, skipping excluded IDs.
func (h *Hub) SendToGroup(group string, msg Message, excluded map[string]struct{}) {
	h.mu.RLock()
	conns := h.collectGroupConns(group, excluded)
	h.mu.RUnlock()
	for _, c := range conns {
		c.Send(msg)
	}
}

// SendToUser delivers msg to all connections belonging to userID.
func (h *Hub) SendToUser(userID string, msg Message) {
	h.mu.RLock()
	conns := make([]*Connection, 0, len(h.users[userID]))
	for id := range h.users[userID] {
		if c, ok := h.connections[id]; ok {
			conns = append(conns, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range conns {
		c.Send(msg)
	}
}

// SendToAll delivers msg to every connection in the hub, skipping excluded IDs.
func (h *Hub) SendToAll(msg Message, excluded map[string]struct{}) {
	h.mu.RLock()
	conns := make([]*Connection, 0, len(h.connections))
	for id, c := range h.connections {
		if _, skip := excluded[id]; !skip {
			conns = append(conns, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range conns {
		c.Send(msg)
	}
}

// collectGroupConns collects connections in a group minus excluded; must be
// called with h.mu.RLock held.
func (h *Hub) collectGroupConns(group string, excluded map[string]struct{}) []*Connection {
	conns := make([]*Connection, 0, len(h.groups[group]))
	for id := range h.groups[group] {
		if _, skip := excluded[id]; skip {
			continue
		}
		if c, ok := h.connections[id]; ok {
			conns = append(conns, c)
		}
	}
	return conns
}

// ── Filter support ────────────────────────────────────────────────────────────

// FilterFunc is a predicate that tests a connection's UserID.
// Return true to include the connection in the operation.
type FilterFunc func(userID string) bool

// NoFilter matches every connection.
func NoFilter(_ string) bool { return true }

// ── Extended operations (REST API) ────────────────────────────────────────────

// ListConnectionsInGroup returns the connection IDs in group.
func (h *Hub) ListConnectionsInGroup(group string) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ids := make([]string, 0, len(h.groups[group]))
	for id := range h.groups[group] {
		ids = append(ids, id)
	}
	return ids
}

// CloseAllConnections closes every connection except those in excluded.
func (h *Hub) CloseAllConnections(excluded map[string]struct{}) {
	h.mu.RLock()
	conns := make([]*Connection, 0, len(h.connections))
	for id, c := range h.connections {
		if _, skip := excluded[id]; !skip {
			conns = append(conns, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range conns {
		c.Close()
	}
}

// CloseGroupConnections closes every connection in group except those in excluded.
func (h *Hub) CloseGroupConnections(group string, excluded map[string]struct{}) {
	h.mu.RLock()
	conns := h.collectGroupConns(group, excluded)
	h.mu.RUnlock()
	for _, c := range conns {
		c.Close()
	}
}

// SendToAllFiltered broadcasts msg to connections that pass filter and are not
// in excluded.
func (h *Hub) SendToAllFiltered(msg Message, excluded map[string]struct{}, filter FilterFunc) {
	h.mu.RLock()
	conns := make([]*Connection, 0, len(h.connections))
	for id, c := range h.connections {
		if _, skip := excluded[id]; skip {
			continue
		}
		if filter(c.UserID) {
			conns = append(conns, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range conns {
		c.Send(msg)
	}
}

// SendToGroupFiltered delivers msg to group connections that pass filter and
// are not in excluded.
func (h *Hub) SendToGroupFiltered(group string, msg Message, excluded map[string]struct{}, filter FilterFunc) {
	h.mu.RLock()
	conns := make([]*Connection, 0, len(h.groups[group]))
	for id := range h.groups[group] {
		if _, skip := excluded[id]; skip {
			continue
		}
		if c, ok := h.connections[id]; ok && filter(c.UserID) {
			conns = append(conns, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range conns {
		c.Send(msg)
	}
}

// AddConnectionsToGroupsFiltered adds every connection that passes filter to
// each of the given groups.
func (h *Hub) AddConnectionsToGroupsFiltered(filter FilterFunc, groups []string) {
	if len(groups) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, group := range groups {
		if h.groups[group] == nil {
			h.groups[group] = make(map[string]struct{})
		}
	}
	for id, conn := range h.connections {
		if !filter(conn.UserID) {
			continue
		}
		for _, group := range groups {
			h.groups[group][id] = struct{}{}
			conn.groups[group] = struct{}{}
		}
	}
}

// RemoveConnectionsFromGroupsFiltered removes every connection that passes
// filter from each of the given groups.
func (h *Hub) RemoveConnectionsFromGroupsFiltered(filter FilterFunc, groups []string) {
	if len(groups) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, group := range groups {
		for id := range h.groups[group] {
			conn, ok := h.connections[id]
			if !ok || !filter(conn.UserID) {
				continue
			}
			delete(h.groups[group], id)
			delete(conn.groups, group)
		}
		if len(h.groups[group]) == 0 {
			delete(h.groups, group)
		}
	}
}
