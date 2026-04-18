package auth_test

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hiro110/azure-web-pubsub-emulator/internal/auth"
)

var testAccessKey = mustDecodeBase64("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")

func mustDecodeBase64(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func signedRequest(t *testing.T, method, url, body string) *http.Request {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, url, bodyReader)
	r.Host = "localhost:8080"
	if err := auth.SignRequest(r, testAccessKey); err != nil {
		t.Fatalf("SignRequest: %v", err)
	}
	return r
}

func TestValidateRequest_Valid(t *testing.T) {
	tests := []struct {
		name   string
		method string
		url    string
		body   string
	}{
		{"GET no body", "GET", "http://localhost:8080/api/hubs/hub1", ""},
		{"POST with body", "POST", "http://localhost:8080/api/hubs/hub1/:send", `{"data":"hello"}`},
		{"POST empty body", "POST", "http://localhost:8080/api/hubs/hub1/:generateClientAccessToken", ""},
		{"URL with query", "GET", "http://localhost:8080/api/hubs/hub1?foo=bar", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := signedRequest(t, tt.method, tt.url, tt.body)
			if err := auth.ValidateRequest(r, testAccessKey); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateRequest_MissingAuthHeader(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/hubs/hub1", nil)
	err := auth.ValidateRequest(r, testAccessKey)
	if err == nil {
		t.Error("expected error for missing Authorization header")
	}
	if !strings.Contains(err.Error(), "missing Authorization header") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateRequest_WrongScheme(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/hubs/hub1", nil)
	r.Header.Set("Authorization", "Bearer sometoken")
	err := auth.ValidateRequest(r, testAccessKey)
	if err == nil || !strings.Contains(err.Error(), "unsupported authorization scheme") {
		t.Errorf("expected scheme error, got: %v", err)
	}
}

func TestValidateRequest_WrongKey(t *testing.T) {
	r := signedRequest(t, "GET", "http://localhost:8080/api/hubs/hub1", "")
	wrongKey := mustDecodeBase64("BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=")
	err := auth.ValidateRequest(r, wrongKey)
	if err == nil || !strings.Contains(err.Error(), "signature mismatch") {
		t.Errorf("expected signature mismatch, got: %v", err)
	}
}

func TestValidateRequest_TamperedBody(t *testing.T) {
	r := signedRequest(t, "POST", "http://localhost:8080/api/hubs/hub1/:send", `{"data":"hello"}`)
	// Replace body after signing
	r.Body = io.NopCloser(strings.NewReader(`{"data":"tampered"}`))
	err := auth.ValidateRequest(r, testAccessKey)
	if err == nil || !strings.Contains(err.Error(), "content hash mismatch") {
		t.Errorf("expected content hash mismatch, got: %v", err)
	}
}

func TestValidateRequest_OldTimestamp(t *testing.T) {
	r := signedRequest(t, "GET", "http://localhost:8080/api/hubs/hub1", "")
	// Overwrite x-ms-date with a stale timestamp
	oldTime := time.Now().UTC().Add(-10 * time.Minute).Format(http.TimeFormat)
	r.Header.Set("x-ms-date", oldTime)
	err := auth.ValidateRequest(r, testAccessKey)
	if err == nil || !strings.Contains(err.Error(), "outside allowed window") {
		t.Errorf("expected timestamp error, got: %v", err)
	}
}

func TestValidateRequest_FutureTimestamp(t *testing.T) {
	r := signedRequest(t, "GET", "http://localhost:8080/api/hubs/hub1", "")
	futureTime := time.Now().UTC().Add(10 * time.Minute).Format(http.TimeFormat)
	r.Header.Set("x-ms-date", futureTime)
	err := auth.ValidateRequest(r, testAccessKey)
	if err == nil || !strings.Contains(err.Error(), "outside allowed window") {
		t.Errorf("expected timestamp error, got: %v", err)
	}
}

func TestValidateRequest_MissingDateHeader(t *testing.T) {
	r := signedRequest(t, "GET", "http://localhost:8080/api/hubs/hub1", "")
	r.Header.Del("x-ms-date")
	err := auth.ValidateRequest(r, testAccessKey)
	if err == nil || !strings.Contains(err.Error(), "missing date header") {
		t.Errorf("expected date header error, got: %v", err)
	}
}

func TestValidateRequest_BodyRestoredAfterValidation(t *testing.T) {
	body := `{"data":"hello world"}`
	r := signedRequest(t, "POST", "http://localhost:8080/api/hubs/hub1/:send", body)

	if err := auth.ValidateRequest(r, testAccessKey); err != nil {
		t.Fatalf("validation failed: %v", err)
	}

	// Body must still be readable after validation
	remaining, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if string(remaining) != body {
		t.Errorf("body not restored: got %q, want %q", remaining, body)
	}
}

func TestSignRequest_SetsRequiredHeaders(t *testing.T) {
	r := httptest.NewRequest("POST", "http://localhost:8080/api/hubs/hub1/:send", bytes.NewBufferString("hello"))
	r.Host = "localhost:8080"

	if err := auth.SignRequest(r, testAccessKey); err != nil {
		t.Fatalf("SignRequest: %v", err)
	}

	if r.Header.Get("x-ms-date") == "" {
		t.Error("x-ms-date header not set")
	}
	if r.Header.Get("x-ms-content-sha256") == "" {
		t.Error("x-ms-content-sha256 header not set")
	}
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "HMAC-SHA256 ") {
		t.Errorf("Authorization header invalid: %q", authHeader)
	}
	if !strings.Contains(authHeader, "SignedHeaders=x-ms-date;host;x-ms-content-sha256") {
		t.Errorf("SignedHeaders missing in Authorization: %q", authHeader)
	}
}
