package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/hiro110/azure-web-pubsub-emulator/internal/auth"
)

func (s *Server) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(s.requestLogger())
	r.Use(middleware.Recoverer)

	// Health check (also exposed at /api/health for Azure SDK compatibility)
	r.Get("/health", s.handleHealth)
	r.Get("/api/health", s.handleHealth)

	// WebSocket client endpoint — authenticated via JWT access_token query param
	r.Get("/client/hubs/{hub}", s.handleWebSocket)

	// Data plane API — all endpoints require HMAC-SHA256 authentication
	r.Group(func(r chi.Router) {
		r.Use(s.hmacAuth())

		// ── Hub-level action endpoints (:send, :closeConnections, :addToGroups, …)
		r.Post("/api/hubs/{hub}/{action}", s.handleHubAction)

		// ── Connection endpoints
		r.Head("/api/hubs/{hub}/connections/{connectionId}", s.handleConnectionExists)
		r.Delete("/api/hubs/{hub}/connections/{connectionId}", s.handleCloseConnection)
		r.Post("/api/hubs/{hub}/connections/{connectionId}/{action}", s.handleConnectionAction)
		r.Put("/api/hubs/{hub}/connections/{connectionId}/groups/{group}", s.handleAddConnectionToGroup)
		r.Delete("/api/hubs/{hub}/connections/{connectionId}/groups/{group}", s.handleRemoveConnectionFromGroup)
		r.Delete("/api/hubs/{hub}/connections/{connectionId}/groups", s.handleRemoveConnectionFromAllGroups)

		// ── Group endpoints
		r.Head("/api/hubs/{hub}/groups/{group}", s.handleGroupExists)
		r.Get("/api/hubs/{hub}/groups/{group}/connections", s.handleListConnectionsInGroup)
		r.Post("/api/hubs/{hub}/groups/{group}/{action}", s.handleGroupAction)
		// Group-centric connection membership (used by SDK v1.2+)
		r.Put("/api/hubs/{hub}/groups/{group}/connections/{connectionId}", s.handleAddConnectionToGroup)
		r.Delete("/api/hubs/{hub}/groups/{group}/connections/{connectionId}", s.handleRemoveConnectionFromGroup)

		// ── User endpoints
		r.Head("/api/hubs/{hub}/users/{userId}", s.handleUserExists)
		r.Post("/api/hubs/{hub}/users/{userId}/{action}", s.handleUserAction)
		r.Put("/api/hubs/{hub}/users/{userId}/groups/{group}", s.handleAddUserToGroup)
		r.Delete("/api/hubs/{hub}/users/{userId}/groups/{group}", s.handleRemoveUserFromGroup)
		r.Delete("/api/hubs/{hub}/users/{userId}/groups", s.handleRemoveUserFromAllGroups)

		// ── Permission endpoints
		r.Get("/api/hubs/{hub}/permissions/{permission}/connections/{connectionId}", s.handleCheckPermission)
		r.Put("/api/hubs/{hub}/permissions/{permission}/connections/{connectionId}", s.handleGrantPermission)
		r.Delete("/api/hubs/{hub}/permissions/{permission}/connections/{connectionId}", s.handleRevokePermission)
	})

	return r
}

// hmacAuth returns middleware that validates REST API requests.
// It accepts both HMAC-SHA256 (legacy) and Bearer JWT (used by @azure/web-pubsub v1.2+).
// For Bearer tokens the JWT audience must equal the full request URL.
func (s *Server) hmacAuth() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var authErr error

			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				tokenStr := authHeader[len("Bearer "):]
				claims, err := auth.ValidateClientToken(tokenStr, s.accessKey)
				if err != nil {
					authErr = err
				} else {
					scheme := "http"
					if r.TLS != nil {
						scheme = "https"
					}
					requestURL := fmt.Sprintf("%s://%s%s", scheme, r.Host, r.RequestURI)
					matched := false
					for _, a := range claims.Audience {
						if a == requestURL {
							matched = true
							break
						}
					}
					if !matched {
						authErr = fmt.Errorf("JWT audience does not match request URL")
					}
				}
			} else {
				authErr = auth.ValidateRequest(r, s.accessKey)
			}

			if authErr != nil {
				s.logger.Warn("REST auth failed",
					zap.String("method", r.Method),
					zap.String("path", r.URL.Path),
					zap.Error(authErr),
				)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type healthResponse struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(healthResponse{
		Status:    "ok",
		Timestamp: time.Now().UTC(),
	}); err != nil {
		s.logger.Error("encoding health response", zap.Error(err))
	}
}

func (s *Server) requestLogger() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()
			next.ServeHTTP(ww, r)
			s.logger.Info("request",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", ww.Status()),
				zap.Duration("duration", time.Since(start)),
			)
		})
	}
}
