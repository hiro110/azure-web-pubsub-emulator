package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/hiro110/azure-web-pubsub-emulator/internal/auth"
	"github.com/hiro110/azure-web-pubsub-emulator/internal/hub"
	"github.com/hiro110/azure-web-pubsub-emulator/internal/webhook"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 512 * 1024 // 512 KB
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// Allow all origins for local emulator use
	CheckOrigin: func(*http.Request) bool { return true },
}

// systemMsg is the JSON envelope for system-level messages sent to clients.
type systemMsg struct {
	Type         string `json:"type"`
	Event        string `json:"event"`
	UserID       string `json:"userId,omitempty"`
	ConnectionID string `json:"connectionId,omitempty"`
	Message      string `json:"message,omitempty"`
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	hubName := chi.URLParam(r, "hub")
	token := r.URL.Query().Get("access_token")

	claims, err := auth.ValidateClientToken(token, s.accessKey)
	if err != nil {
		s.logger.Warn("WebSocket: invalid access token",
			zap.String("hub", hubName),
			zap.Error(err),
		)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Audience must reference this hub
	if len(claims.Audience) > 0 && !claims.IsValidForHub(hubName) {
		s.logger.Warn("WebSocket: audience mismatch",
			zap.String("hub", hubName),
			zap.Strings("audience", claims.Audience),
		)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Pre-generate the connection ID so it can be included in the sys.connect event.
	connID := uuid.New().String()
	host := r.Host

	// Fire sys.connect before WebSocket upgrade (still in HTTP request/response).
	if s.dispatcher.HasUpstream(hubName) {
		connInfo := webhook.ConnectionInfo{ID: connID, UserID: claims.UserID}
		connectReq := webhook.ConnectRequest{
			Claims:             buildClaimsMap(claims),
			Query:              r.URL.Query(),
			Headers:            map[string][]string(r.Header),
			ClientCertificates: nil,
		}
		resp, err := s.dispatcher.SendConnect(r.Context(), host, hubName, connInfo, connectReq)
		if err != nil {
			s.logger.Warn("upstream rejected connection",
				zap.String("hub", hubName),
				zap.String("connectionId", connID),
				zap.Error(err),
			)
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		// Apply upstream overrides from the connect response.
		if resp != nil {
			if resp.UserID != "" {
				claims.UserID = resp.UserID
			}
			claims.Groups = append(claims.Groups, resp.Groups...)
			claims.Roles = append(claims.Roles, resp.Roles...)
		}
	}

	wsConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("WebSocket upgrade failed", zap.Error(err))
		return
	}

	conn := s.manager.NewConnectionWithID(hubName, connID, claims.UserID)

	// Pre-join groups declared in the JWT (and any added by the upstream).
	h := s.manager.GetOrCreate(hubName)
	for _, group := range claims.Groups {
		if err := h.AddConnectionToGroup(conn.ID, group); err != nil {
			s.logger.Warn("pre-join group failed",
				zap.String("group", group),
				zap.Error(err),
			)
		}
	}

	s.logger.Info("client connected",
		zap.String("hub", hubName),
		zap.String("connectionId", conn.ID),
		zap.String("userId", conn.UserID),
	)

	// Notify upstream that the connection is established (fire-and-forget).
	s.dispatcher.SendConnected(host, hubName, webhook.ConnectionInfo{ID: conn.ID, UserID: conn.UserID})

	// Queue system.connected — write pump will deliver it as the first message.
	connectedData, _ := json.Marshal(systemMsg{
		Type:         "system",
		Event:        "connected",
		UserID:       conn.UserID,
		ConnectionID: conn.ID,
	})
	conn.Send(hub.Message{Type: hub.MessageTypeText, Data: connectedData})

	go s.wsWritePump(wsConn, conn)
	s.wsReadPump(wsConn, conn, hubName, host)
}

// wsWritePump runs in a dedicated goroutine and is the sole writer to wsConn.
func (s *Server) wsWritePump(wsConn *websocket.Conn, conn *hub.Connection) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		wsConn.Close()
	}()

	for {
		select {
		case msg, ok := <-conn.Messages():
			wsConn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Channel was closed (server-initiated close)
				wsConn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			if err := wsConn.WriteMessage(msg.Type, msg.Data); err != nil {
				return
			}
		case <-ticker.C:
			wsConn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := wsConn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// wsReadPump runs in the handler goroutine and blocks until the connection closes.
func (s *Server) wsReadPump(wsConn *websocket.Conn, conn *hub.Connection, hubName, host string) {
	defer func() {
		s.manager.GetOrCreate(hubName).RemoveConnection(conn.ID)
		conn.Close()
		s.dispatcher.SendDisconnected(host, hubName,
			webhook.ConnectionInfo{ID: conn.ID, UserID: conn.UserID}, "")
		s.logger.Info("client disconnected",
			zap.String("hub", hubName),
			zap.String("connectionId", conn.ID),
		)
	}()

	wsConn.SetReadLimit(maxMessageSize)
	wsConn.SetReadDeadline(time.Now().Add(pongWait))
	wsConn.SetPongHandler(func(string) error {
		wsConn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		msgType, data, err := wsConn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseNormalClosure,
				websocket.CloseNoStatusReceived,
			) {
				s.logger.Warn("WebSocket read error",
					zap.String("hub", hubName),
					zap.String("connectionId", conn.ID),
					zap.Error(err),
				)
			}
			return
		}

		if s.dispatcher.HasUpstream(hubName) {
			resp, err := s.dispatcher.SendMessage(
				context.Background(),
				host, hubName,
				webhook.ConnectionInfo{ID: conn.ID, UserID: conn.UserID},
				msgType, data,
			)
			if err != nil {
				s.logger.Warn("upstream message handler error",
					zap.String("hub", hubName),
					zap.String("connectionId", conn.ID),
					zap.Error(err),
				)
				continue
			}
			// If upstream responded with data, forward it back to the client.
			if resp != nil && len(resp.Data) > 0 {
				conn.Send(hub.Message{Type: hub.MessageTypeText, Data: resp.Data})
			}
		}
	}
}

// buildClaimsMap converts TokenClaims to a map suitable for the ConnectRequest payload.
func buildClaimsMap(claims *auth.TokenClaims) map[string]interface{} {
	m := make(map[string]interface{})
	if claims.UserID != "" {
		m["sub"] = []interface{}{claims.UserID}
	}
	if len(claims.Audience) > 0 {
		auds := make([]interface{}, len(claims.Audience))
		for i, a := range claims.Audience {
			auds[i] = a
		}
		m["aud"] = auds
	}
	if len(claims.Roles) > 0 {
		roles := make([]interface{}, len(claims.Roles))
		for i, r := range claims.Roles {
			roles[i] = r
		}
		m["role"] = roles
	}
	if len(claims.Groups) > 0 {
		groups := make([]interface{}, len(claims.Groups))
		for i, g := range claims.Groups {
			groups[i] = g
		}
		m["webpubsub.group"] = groups
	}
	return m
}
