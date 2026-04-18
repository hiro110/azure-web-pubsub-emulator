package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/hiro110/azure-web-pubsub-emulator/internal/hub"
)

// ── Dispatch helpers ──────────────────────────────────────────────────────────

// handleHubAction dispatches hub-level action endpoints.
// The {action} URL parameter carries the Azure-style colon-prefixed verb,
// e.g. ":send", ":closeConnections", ":addToGroups", etc.
func (s *Server) handleHubAction(w http.ResponseWriter, r *http.Request) {
	switch chi.URLParam(r, "action") {
	case ":send":
		s.handleSendToAll(w, r)
	case ":closeConnections":
		s.handleCloseAllConnections(w, r)
	case ":addToGroups":
		s.handleAddConnectionsToGroups(w, r)
	case ":removeFromGroups":
		s.handleRemoveConnectionsFromGroups(w, r)
	case ":generateClientAccessToken":
		s.handleGenerateClientToken(w, r)
	default:
		http.Error(w, "Not Found", http.StatusNotFound)
	}
}

// handleConnectionAction dispatches connection-level action endpoints.
func (s *Server) handleConnectionAction(w http.ResponseWriter, r *http.Request) {
	switch chi.URLParam(r, "action") {
	case ":send":
		s.handleSendToConnection(w, r)
	default:
		http.Error(w, "Not Found", http.StatusNotFound)
	}
}

// handleGroupAction dispatches group-level action endpoints.
func (s *Server) handleGroupAction(w http.ResponseWriter, r *http.Request) {
	switch chi.URLParam(r, "action") {
	case ":send":
		s.handleSendToGroup(w, r)
	case ":closeConnections":
		s.handleCloseGroupConnections(w, r)
	default:
		http.Error(w, "Not Found", http.StatusNotFound)
	}
}

// handleUserAction dispatches user-level action endpoints.
func (s *Server) handleUserAction(w http.ResponseWriter, r *http.Request) {
	switch chi.URLParam(r, "action") {
	case ":send":
		s.handleSendToUser(w, r)
	case ":closeConnections":
		s.handleCloseUserConnections(w, r)
	default:
		http.Error(w, "Not Found", http.StatusNotFound)
	}
}

// ── Send operations ───────────────────────────────────────────────────────────

func (s *Server) handleSendToAll(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	msgType, data, err := readMessageBody(r)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	excluded := parseExcluded(r)
	filter := parseFilter(r.URL.Query().Get("filter"))

	s.manager.GetOrCreate(hubName).SendToAllFiltered(
		hub.Message{Type: msgType, Data: data},
		excluded,
		filter,
	)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleSendToConnection(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	connID := chi.URLParam(r, "connectionId")
	msgType, data, err := readMessageBody(r)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	h := s.manager.Get(hubName)
	if h == nil || !h.SendToConnection(connID, hub.Message{Type: msgType, Data: data}) {
		http.Error(w, "connection not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleSendToGroup(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	group := chi.URLParam(r, "group")
	msgType, data, err := readMessageBody(r)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	excluded := parseExcluded(r)
	filter := parseFilter(r.URL.Query().Get("filter"))

	s.manager.GetOrCreate(hubName).SendToGroupFiltered(
		group,
		hub.Message{Type: msgType, Data: data},
		excluded,
		filter,
	)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleSendToUser(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	userID := chi.URLParam(r, "userId")
	msgType, data, err := readMessageBody(r)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	s.manager.GetOrCreate(hubName).SendToUser(userID, hub.Message{Type: msgType, Data: data})
	w.WriteHeader(http.StatusAccepted)
}

// ── Close operations ──────────────────────────────────────────────────────────

func (s *Server) handleCloseAllConnections(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	excluded := parseExcluded(r)
	s.manager.GetOrCreate(hubName).CloseAllConnections(excluded)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCloseConnection(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	connID := chi.URLParam(r, "connectionId")
	h := s.manager.Get(hubName)
	if h == nil || !h.CloseConnection(connID) {
		http.Error(w, "connection not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCloseGroupConnections(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	group := chi.URLParam(r, "group")
	excluded := parseExcluded(r)
	s.manager.GetOrCreate(hubName).CloseGroupConnections(group, excluded)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCloseUserConnections(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	userID := chi.URLParam(r, "userId")
	s.manager.GetOrCreate(hubName).CloseUserConnections(userID)
	w.WriteHeader(http.StatusNoContent)
}

// ── Exists checks ─────────────────────────────────────────────────────────────

func (s *Server) handleConnectionExists(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	connID := chi.URLParam(r, "connectionId")
	h := s.manager.Get(hubName)
	if h == nil || !h.ConnectionExists(connID) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleGroupExists(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	group := chi.URLParam(r, "group")
	h := s.manager.Get(hubName)
	if h == nil || !h.GroupExists(group) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleUserExists(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	userID := chi.URLParam(r, "userId")
	h := s.manager.Get(hubName)
	if h == nil || !h.UserExists(userID) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ── Group management ──────────────────────────────────────────────────────────

func (s *Server) handleListConnectionsInGroup(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	group := chi.URLParam(r, "group")
	h := s.manager.Get(hubName)
	var ids []string
	if h != nil {
		ids = h.ListConnectionsInGroup(group)
	}
	if ids == nil {
		ids = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"value": ids})
}

func (s *Server) handleAddConnectionToGroup(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	connID := chi.URLParam(r, "connectionId")
	group := chi.URLParam(r, "group")
	h := s.manager.Get(hubName)
	if h == nil {
		http.Error(w, "connection not found", http.StatusNotFound)
		return
	}
	if err := h.AddConnectionToGroup(connID, group); err != nil {
		http.Error(w, "connection not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleRemoveConnectionFromGroup(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	connID := chi.URLParam(r, "connectionId")
	group := chi.URLParam(r, "group")
	s.manager.GetOrCreate(hubName).RemoveConnectionFromGroup(connID, group)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleRemoveConnectionFromAllGroups(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	connID := chi.URLParam(r, "connectionId")
	s.manager.GetOrCreate(hubName).RemoveConnectionFromAllGroups(connID)
	w.WriteHeader(http.StatusOK)
}

// ── User-group management ─────────────────────────────────────────────────────

func (s *Server) handleAddUserToGroup(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	userID := chi.URLParam(r, "userId")
	group := chi.URLParam(r, "group")
	s.manager.GetOrCreate(hubName).AddUserToGroup(userID, group)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleRemoveUserFromGroup(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	userID := chi.URLParam(r, "userId")
	group := chi.URLParam(r, "group")
	s.manager.GetOrCreate(hubName).RemoveUserFromGroup(userID, group)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleRemoveUserFromAllGroups(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	userID := chi.URLParam(r, "userId")
	s.manager.GetOrCreate(hubName).RemoveUserFromAllGroups(userID)
	w.WriteHeader(http.StatusOK)
}

// ── Batch group operations ────────────────────────────────────────────────────

type groupFilterRequest struct {
	Groups []string `json:"groups"`
	Filter string   `json:"filter"`
}

func (s *Server) handleAddConnectionsToGroups(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	var req groupFilterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	filter := parseFilter(req.Filter)
	s.manager.GetOrCreate(hubName).AddConnectionsToGroupsFiltered(filter, req.Groups)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleRemoveConnectionsFromGroups(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	var req groupFilterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	filter := parseFilter(req.Filter)
	s.manager.GetOrCreate(hubName).RemoveConnectionsFromGroupsFiltered(filter, req.Groups)
	w.WriteHeader(http.StatusOK)
}

// ── Permission operations ─────────────────────────────────────────────────────

func (s *Server) handleGrantPermission(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	permission := chi.URLParam(r, "permission")
	connID := chi.URLParam(r, "connectionId")
	targetName := r.URL.Query().Get("targetName")
	key := permKey(permission, targetName)
	h := s.manager.Get(hubName)
	if h == nil {
		http.Error(w, "connection not found", http.StatusNotFound)
		return
	}
	if err := h.GrantPermission(connID, key); err != nil {
		http.Error(w, "connection not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleRevokePermission(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	permission := chi.URLParam(r, "permission")
	connID := chi.URLParam(r, "connectionId")
	targetName := r.URL.Query().Get("targetName")
	key := permKey(permission, targetName)
	h := s.manager.Get(hubName)
	if h == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := h.RevokePermission(connID, key); err != nil {
		s.logger.Warn("revoking permission from unknown connection",
			zap.String("connectionId", connID),
			zap.Error(err),
		)
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleCheckPermission(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	permission := chi.URLParam(r, "permission")
	connID := chi.URLParam(r, "connectionId")
	targetName := r.URL.Query().Get("targetName")
	key := permKey(permission, targetName)
	h := s.manager.Get(hubName)
	if h == nil || !h.HasPermission(connID, key) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// permKey returns the storage key for a permission, combining the action name
// with an optional target (e.g. "joinLeaveGroup.room1").
func permKey(permission, targetName string) string {
	if targetName != "" {
		return permission + "." + targetName
	}
	return permission
}

// ── Request body helpers ──────────────────────────────────────────────────────

// readMessageBody reads the request body and determines the hub message type
// from the Content-Type header.
func readMessageBody(r *http.Request) (msgType int, data []byte, err error) {
	data, err = io.ReadAll(r.Body)
	if err != nil {
		return 0, nil, err
	}
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/octet-stream") {
		return hub.MessageTypeBinary, data, nil
	}
	return hub.MessageTypeText, data, nil
}

// parseExcluded extracts repeated "excluded" query parameters into a set.
func parseExcluded(r *http.Request) map[string]struct{} {
	vals := r.URL.Query()["excluded"]
	if len(vals) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(vals))
	for _, v := range vals {
		out[v] = struct{}{}
	}
	return out
}

// parseFilter parses a simplified OData filter string into a FilterFunc.
//
// Supported expressions:
//
//	userId eq 'value'  — include only connections with matching UserID
//	userId ne 'value'  — include only connections with non-matching UserID
//
// Any unsupported expression is silently treated as NoFilter (match all).
func parseFilter(expr string) hub.FilterFunc {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return hub.NoFilter
	}
	parts := strings.SplitN(expr, " ", 3)
	if len(parts) != 3 || parts[0] != "userId" {
		return hub.NoFilter
	}
	op := parts[1]
	val := strings.Trim(parts[2], "'")
	switch op {
	case "eq":
		return func(userID string) bool { return userID == val }
	case "ne":
		return func(userID string) bool { return userID != val }
	default:
		return hub.NoFilter
	}
}
