package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/hiro110/azure-web-pubsub-emulator/internal/auth"
)

type generateTokenRequest struct {
	UserID          string   `json:"userId"`
	Roles           []string `json:"roles"`
	MinutesToExpire int      `json:"minutesToExpire"`
	Groups          []string `json:"groups"`
}

type generateTokenResponse struct {
	Token string `json:"token"`
}

func (s *Server) handleGenerateClientToken(w http.ResponseWriter, r *http.Request) {
	hub := chi.URLParam(r, "hub")

	var req generateTokenRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
	}

	if req.MinutesToExpire <= 0 {
		req.MinutesToExpire = 60
	}

	// Audience is the hub's WebSocket endpoint URL
	audience := fmt.Sprintf("http://%s/client/hubs/%s", r.Host, hub)
	expiry := time.Duration(req.MinutesToExpire) * time.Minute

	token, err := auth.GenerateClientToken(s.accessKey, audience, req.UserID, req.Roles, req.Groups, expiry)
	if err != nil {
		s.logger.Error("generating client token", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(generateTokenResponse{Token: token}); err != nil {
		s.logger.Error("encoding token response", zap.Error(err))
	}
}
