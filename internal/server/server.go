package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/hiro110/azure-web-pubsub-emulator/internal/config"
	"github.com/hiro110/azure-web-pubsub-emulator/internal/hub"
	"github.com/hiro110/azure-web-pubsub-emulator/internal/webhook"
)

// Server is the emulator HTTP server.
type Server struct {
	cfg        *config.Config
	logger     *zap.Logger
	http       *http.Server
	accessKey  []byte              // decoded access key bytes
	manager    *hub.Manager        // in-memory hub state
	dispatcher *webhook.Dispatcher // upstream CloudEvents sender
}

// New creates a new Server with the given configuration and logger.
func New(cfg *config.Config, logger *zap.Logger) (*Server, error) {
	// The Azure SDK signs both JWTs and HMAC-SHA256 REST requests using the
	// raw AccessKey string bytes (not the base64-decoded bytes).
	accessKey := []byte(cfg.Auth.AccessKey)

	s := &Server{
		cfg:        cfg,
		logger:     logger,
		accessKey:  accessKey,
		manager:    hub.NewManager(),
		dispatcher: webhook.New(cfg.Hubs, logger),
	}
	s.http = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:      s.routes(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 0, // disabled: WebSocket connections are long-lived
		IdleTimeout:  120 * time.Second,
	}
	return s, nil
}

// Handler returns the HTTP handler for use in tests.
func (s *Server) Handler() http.Handler {
	return s.http.Handler
}

// Manager returns the hub manager for use in tests.
func (s *Server) Manager() *hub.Manager {
	return s.manager
}

// Run starts the server and blocks until a shutdown signal is received.
func (s *Server) Run() error {
	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", s.http.Addr, err)
	}

	s.logger.Info("emulator started",
		zap.String("addr", ln.Addr().String()),
		zap.Int("hubs", len(s.cfg.Hubs)),
	)
	s.logger.Info("connection string",
		zap.String("value", fmt.Sprintf(
			"Endpoint=http://localhost:%d;AccessKey=%s;Version=1.0;",
			s.cfg.Server.Port,
			s.cfg.Auth.AccessKey,
		)),
	)

	errCh := make(chan error, 1)
	go func() {
		if err := s.http.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	case sig := <-quit:
		s.logger.Info("shutting down", zap.String("signal", sig.String()))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.http.Shutdown(ctx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	s.logger.Info("server stopped")
	return nil
}
