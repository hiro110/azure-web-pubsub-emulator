package server_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/hiro110/azure-web-pubsub-emulator/internal/auth"
	"github.com/hiro110/azure-web-pubsub-emulator/internal/config"
	"github.com/hiro110/azure-web-pubsub-emulator/internal/server"
)

const testAccessKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func mustDecodeBase64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	return b
}

func newTestServer(t *testing.T) *server.Server {
	t.Helper()
	cfg := &config.Config{
		Server:  config.ServerConfig{Host: "0.0.0.0", Port: 8080},
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

func TestHealth(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type: got %q, want application/json", ct)
	}
}

func TestGenerateClientToken_NoAuth(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/hubs/hub1/:generateClientAccessToken", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without auth, got %d", resp.StatusCode)
	}
}

func TestGenerateClientToken_WithAuth(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	accessKeyBytes := mustDecodeBase64(t, testAccessKey)
	body := `{"userId":"user1","minutesToExpire":30}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/hubs/hub1/:generateClientAccessToken", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if err := auth.SignRequest(req, accessKeyBytes); err != nil {
		t.Fatalf("SignRequest: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if result.Token == "" {
		t.Error("expected non-empty token")
	}

	// Token must be a valid, decodable JWT with the correct userId
	claims, err := auth.ValidateClientToken(result.Token, accessKeyBytes)
	if err != nil {
		t.Fatalf("ValidateClientToken: %v", err)
	}
	if claims.UserID != "user1" {
		t.Errorf("userId: got %q, want user1", claims.UserID)
	}
}

func TestGenerateClientToken_EmptyBody(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	accessKeyBytes := mustDecodeBase64(t, testAccessKey)
	req, _ := http.NewRequest("POST", ts.URL+"/api/hubs/hub1/:generateClientAccessToken", nil)
	if err := auth.SignRequest(req, accessKeyBytes); err != nil {
		t.Fatalf("SignRequest: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if result.Token == "" {
		t.Error("expected non-empty token for anonymous request")
	}
}

func TestGenerateClientToken_InvalidAuth(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/hubs/hub1/:generateClientAccessToken", nil)
	req.Header.Set("Authorization", "HMAC-SHA256 SignedHeaders=x-ms-date;host;x-ms-content-sha256&Signature=invalidsig")
	req.Header.Set("x-ms-date", "Mon, 01 Jan 2024 00:00:00 GMT") // stale

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}
