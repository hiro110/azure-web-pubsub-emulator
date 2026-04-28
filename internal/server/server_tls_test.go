package server_test

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"os"
	"testing"

	"go.uber.org/zap"

	"github.com/hiro110/azure-web-pubsub-emulator/internal/config"
	"github.com/hiro110/azure-web-pubsub-emulator/internal/server"
	"github.com/hiro110/azure-web-pubsub-emulator/internal/tlsutil"
)

func newTLSTestServer(t *testing.T, tlsCfg config.TLSConfig) *server.Server {
	t.Helper()
	cfg := &config.Config{
		Server: config.ServerConfig{
			Host: "127.0.0.1",
			Port: 0,
			TLS:  tlsCfg,
		},
		Auth:    config.AuthConfig{AccessKey: testAccessKey},
		Logging: config.LoggingConfig{Level: "info", Format: "text"},
		Hubs:    []config.HubConfig{{Name: "hub1"}},
	}
	srv, err := server.New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	return srv
}

func TestBuildTLSListener_AutoGenerate(t *testing.T) {
	srv := newTLSTestServer(t, config.TLSConfig{
		Enabled:      true,
		AutoGenerate: true,
		Hosts:        []string{"localhost", "127.0.0.1"},
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	tlsLn, caPath, err := srv.ExportBuildTLSListener(ln)
	if err != nil {
		t.Fatalf("ExportBuildTLSListener: %v", err)
	}
	defer tlsLn.Close()

	if caPath == "" {
		t.Error("expected non-empty caPath for auto-generated cert")
	}
	if _, statErr := os.Stat(caPath); statErr != nil {
		t.Errorf("CA cert file not found at %q: %v", caPath, statErr)
	}

	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("reading CA cert: %v", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("failed to parse CA cert from file")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	httpSrv := &http.Server{Handler: mux}
	go httpSrv.Serve(tlsLn) //nolint:errcheck
	defer httpSrv.Close()

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    pool,
				MinVersion: tls.VersionTLS12,
			},
		},
	}

	url := "https://" + tlsLn.Addr().String() + "/ping"
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("TLS GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestBuildTLSListener_UserCert(t *testing.T) {
	certPEM, keyPEM, err := tlsutil.GenerateSelfSigned([]string{"localhost", "127.0.0.1"})
	if err != nil {
		t.Fatalf("GenerateSelfSigned: %v", err)
	}

	certFile := writeTempPEM(t, certPEM)
	keyFile := writeTempPEM(t, keyPEM)

	srv := newTLSTestServer(t, config.TLSConfig{
		Enabled:  true,
		CertFile: certFile,
		KeyFile:  keyFile,
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	tlsLn, caPath, err := srv.ExportBuildTLSListener(ln)
	if err != nil {
		t.Fatalf("ExportBuildTLSListener: %v", err)
	}
	defer tlsLn.Close()

	if caPath != "" {
		t.Errorf("expected empty caPath for user-supplied cert, got %q", caPath)
	}

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)

	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	httpSrv := &http.Server{Handler: mux}
	go httpSrv.Serve(tlsLn) //nolint:errcheck
	defer httpSrv.Close()

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    pool,
				MinVersion: tls.VersionTLS12,
			},
		},
	}

	resp, err := client.Get("https://" + tlsLn.Addr().String() + "/ping")
	if err != nil {
		t.Fatalf("TLS GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestBuildTLSListener_BadCertFiles(t *testing.T) {
	srv := newTLSTestServer(t, config.TLSConfig{
		Enabled:  true,
		CertFile: "/nonexistent/cert.pem",
		KeyFile:  "/nonexistent/key.pem",
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	_, _, err = srv.ExportBuildTLSListener(ln)
	if err == nil {
		t.Error("expected error for missing cert files, got nil")
	}
}

func TestWriteCAFile_Deterministic(t *testing.T) {
	certPEM, _, err := tlsutil.GenerateSelfSigned([]string{"localhost"})
	if err != nil {
		t.Fatalf("GenerateSelfSigned: %v", err)
	}

	path1, err := server.ExportWriteCAFile(certPEM)
	if err != nil {
		t.Fatalf("first ExportWriteCAFile: %v", err)
	}

	path2, err := server.ExportWriteCAFile(certPEM)
	if err != nil {
		t.Fatalf("second ExportWriteCAFile: %v", err)
	}

	if path1 != path2 {
		t.Errorf("expected same path on repeated calls, got %q and %q", path1, path2)
	}

	data, err := os.ReadFile(path1)
	if err != nil {
		t.Fatalf("reading CA file: %v", err)
	}
	if string(data) != string(certPEM) {
		t.Error("CA file content does not match written PEM")
	}
}

func writeTempPEM(t *testing.T, data []byte) string {
	t.Helper()
	f, err := os.CreateTemp("", "tls-test-*.pem")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })
	if _, err := f.Write(data); err != nil {
		t.Fatalf("Write: %v", err)
	}
	f.Close()
	return f.Name()
}
