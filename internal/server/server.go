package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/hiro110/azure-web-pubsub-emulator/internal/config"
	"github.com/hiro110/azure-web-pubsub-emulator/internal/hub"
	"github.com/hiro110/azure-web-pubsub-emulator/internal/tlsutil"
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

	tlsCfg := s.cfg.Server.TLS
	scheme := "http"
	endpointHost := "localhost"
	if tlsCfg.Enabled {
		tlsListener, caPath, err := s.buildTLSListener(ln)
		if err != nil {
			ln.Close() //nolint:errcheck
			return fmt.Errorf("setting up TLS: %w", err)
		}
		ln = tlsListener
		scheme = "https"
		if len(tlsCfg.Hosts) > 0 {
			endpointHost = tlsCfg.Hosts[0]
		}
		if caPath != "" {
			s.logger.Info("TLS CA cert path", zap.String("path", caPath))
			s.logger.Info("trust hint",
				zap.String("NODE_EXTRA_CA_CERTS", caPath),
				zap.String("SSL_CERT_FILE", caPath),
			)
		}
	}

	s.logger.Info("emulator started",
		zap.String("addr", ln.Addr().String()),
		zap.String("scheme", scheme),
		zap.Int("hubs", len(s.cfg.Hubs)),
	)
	s.logger.Info("connection string",
		zap.String("value", fmt.Sprintf(
			"Endpoint=%s://%s:%d;AccessKey=%s;Version=1.0;",
			scheme,
			endpointHost,
			s.cfg.Server.Port,
			maskSecret(s.cfg.Auth.AccessKey),
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

// buildTLSListener wraps the plain TCP listener with TLS. Returns the TLS
// listener, an optional path to the written CA cert (empty when user-supplied
// certs are used), and any error.
func (s *Server) buildTLSListener(ln net.Listener) (net.Listener, string, error) {
	tlsCfg := s.cfg.Server.TLS
	var tlsConfig *tls.Config
	var caPath string

	if tlsCfg.CertFile != "" && tlsCfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(tlsCfg.CertFile, tlsCfg.KeyFile)
		if err != nil {
			return nil, "", fmt.Errorf("loading TLS cert/key: %w", err)
		}
		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
	} else if tlsCfg.AutoGenerate {
		hosts := tlsCfg.Hosts
		if len(hosts) == 0 {
			hosts = []string{"localhost", "127.0.0.1"}
		}
		caCertPEM, certPEM, keyPEM, err := tlsutil.GenerateSelfSigned(hosts)
		if err != nil {
			return nil, "", fmt.Errorf("generating self-signed cert: %w", err)
		}

		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, "", fmt.Errorf("loading generated cert: %w", err)
		}
		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}

		path, err := writeCAFile(caCertPEM)
		if err != nil {
			s.logger.Warn("could not write CA cert to disk", zap.Error(err))
		} else {
			caPath = path
		}
	} else {
		return nil, "", fmt.Errorf("TLS enabled but autoGenerate is false and no certFile/keyFile provided")
	}

	return tls.NewListener(ln, tlsConfig), caPath, nil
}

// writeCAFile writes the CA PEM to a stable cache path, overwriting each run
// so the path stays constant and users can configure NODE_EXTRA_CA_CERTS once.
func writeCAFile(caCertPEM []byte) (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("finding cache dir: %w", err)
	}
	dir := filepath.Join(cacheDir, "web-pubsub-emulator")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating cache dir: %w", err)
	}
	path := filepath.Join(dir, "ca.pem")

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(caCertPEM); err != nil {
		f.Close()       //nolint:errcheck
		os.Remove(path) //nolint:errcheck
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path) //nolint:errcheck
		return "", err
	}
	return path, nil
}

// maskSecret returns the first 4 characters of s followed by "****", to avoid
// logging the full access key while still giving enough context to identify it.
func maskSecret(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return s[:4] + "****"
}
