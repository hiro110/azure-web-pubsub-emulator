package webhook_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/hiro110/azure-web-pubsub-emulator/internal/config"
	"github.com/hiro110/azure-web-pubsub-emulator/internal/webhook"
)

// newDispatcher creates a Dispatcher whose "hub1" upstream points to the given URL.
func newDispatcher(upstreamURL string) *webhook.Dispatcher {
	hubs := []config.HubConfig{
		{Name: "hub1", EventHandlerURL: upstreamURL},
	}
	return webhook.New(hubs, zap.NewNop())
}

// testConn returns a sample ConnectionInfo for tests.
func testConn() webhook.ConnectionInfo {
	return webhook.ConnectionInfo{ID: "conn-test-id", UserID: "user1"}
}

// ── HasUpstream ────────────────────────────────────────────────────────────────

func TestDispatcher_HasUpstream_True(t *testing.T) {
	d := newDispatcher("http://example.com/events")
	if !d.HasUpstream("hub1") {
		t.Error("expected HasUpstream=true for hub1")
	}
}

func TestDispatcher_HasUpstream_False(t *testing.T) {
	d := newDispatcher("http://example.com/events")
	if d.HasUpstream("hub2") {
		t.Error("expected HasUpstream=false for hub2")
	}
}

func TestDispatcher_HasUpstream_EmptyURL(t *testing.T) {
	d := webhook.New([]config.HubConfig{{Name: "hub1", EventHandlerURL: ""}}, zap.NewNop())
	if d.HasUpstream("hub1") {
		t.Error("expected HasUpstream=false when EventHandlerURL is empty")
	}
}

// ── ValidateUpstreams ──────────────────────────────────────────────────────────

func TestDispatcher_ValidateUpstreams_Success(t *testing.T) {
	var called atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodOptions {
			t.Errorf("expected OPTIONS, got %s", r.Method)
		}
		if r.Header.Get("WebHook-Request-Origin") == "" {
			t.Error("missing WebHook-Request-Origin header")
		}
		called.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	d := newDispatcher(ts.URL)
	d.ValidateUpstreams(context.Background())

	if !called.Load() {
		t.Error("OPTIONS request was not sent")
	}
}

func TestDispatcher_ValidateUpstreams_FailureDoesNotPanic(t *testing.T) {
	// Point to a server that returns 500 — should log warn but not panic.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	d := newDispatcher(ts.URL)
	d.ValidateUpstreams(context.Background()) // must not panic
}

// ── SendConnect ────────────────────────────────────────────────────────────────

func TestDispatcher_SendConnect_NoUpstream(t *testing.T) {
	d := webhook.New(nil, zap.NewNop())
	resp, err := d.SendConnect(context.Background(), "localhost:8080", "hub1", testConn(), webhook.ConnectRequest{})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if resp != nil {
		t.Errorf("expected nil response, got %+v", resp)
	}
}

func TestDispatcher_SendConnect_UpstreamAllows_EmptyBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertCEHeaders(t, r, "azure.webpubsub.sys.connect", "hub1", "conn-test-id")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	d := newDispatcher(ts.URL)
	resp, err := d.SendConnect(context.Background(), "localhost", "hub1", testConn(), webhook.ConnectRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil ConnectResponse")
	}
}

func TestDispatcher_SendConnect_UpstreamAllows_WithOverrides(t *testing.T) {
	override := webhook.ConnectResponse{
		UserID: "overridden-user",
		Groups: []string{"room1"},
		Roles:  []string{"webpubsub.sendToGroup.room1"},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(override)
	}))
	defer ts.Close()

	d := newDispatcher(ts.URL)
	resp, err := d.SendConnect(context.Background(), "localhost", "hub1", testConn(), webhook.ConnectRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.UserID != "overridden-user" {
		t.Errorf("UserID: got %q, want overridden-user", resp.UserID)
	}
	if len(resp.Groups) != 1 || resp.Groups[0] != "room1" {
		t.Errorf("Groups: got %v, want [room1]", resp.Groups)
	}
}

func TestDispatcher_SendConnect_UpstreamRejects(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	d := newDispatcher(ts.URL)
	resp, err := d.SendConnect(context.Background(), "localhost", "hub1", testConn(), webhook.ConnectRequest{})
	if err == nil {
		t.Error("expected error when upstream rejects connection")
	}
	if resp != nil {
		t.Errorf("expected nil response on rejection, got %+v", resp)
	}
}

func TestDispatcher_SendConnect_Payload(t *testing.T) {
	var received webhook.ConnectRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	d := newDispatcher(ts.URL)
	req := webhook.ConnectRequest{
		Claims:             map[string]interface{}{"sub": []interface{}{"user1"}},
		Query:              map[string][]string{"key": {"val"}},
		Headers:            map[string][]string{"X-Custom": {"v1"}},
		ClientCertificates: nil,
	}
	_, _ = d.SendConnect(context.Background(), "localhost", "hub1", testConn(), req)

	if received.Claims["sub"] == nil {
		t.Error("claims.sub was not sent to upstream")
	}
}

// ── SendConnected ─────────────────────────────────────────────────────────────

func TestDispatcher_SendConnected_NoUpstream(t *testing.T) {
	d := webhook.New(nil, zap.NewNop())
	d.SendConnected("localhost", "hub1", testConn()) // must not panic
}

func TestDispatcher_SendConnected_FiresAsync(t *testing.T) {
	var called atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertCEHeaders(t, r, "azure.webpubsub.sys.connected", "hub1", "conn-test-id")
		called.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	d := newDispatcher(ts.URL)
	d.SendConnected("localhost", "hub1", testConn())

	// Give goroutine time to fire
	deadline := time.Now().Add(3 * time.Second)
	for !called.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !called.Load() {
		t.Error("connected event was not sent to upstream")
	}
}

// ── SendDisconnected ──────────────────────────────────────────────────────────

func TestDispatcher_SendDisconnected_NoUpstream(t *testing.T) {
	d := webhook.New(nil, zap.NewNop())
	d.SendDisconnected("localhost", "hub1", testConn(), "") // must not panic
}

func TestDispatcher_SendDisconnected_FiresAsync(t *testing.T) {
	var called atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertCEHeaders(t, r, "azure.webpubsub.sys.disconnected", "hub1", "conn-test-id")
		called.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	d := newDispatcher(ts.URL)
	d.SendDisconnected("localhost", "hub1", testConn(), "client closed")

	deadline := time.Now().Add(3 * time.Second)
	for !called.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !called.Load() {
		t.Error("disconnected event was not sent to upstream")
	}
}

func TestDispatcher_SendDisconnected_PayloadContainsReason(t *testing.T) {
	done := make(chan []byte, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		done <- b
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	d := newDispatcher(ts.URL)
	d.SendDisconnected("localhost", "hub1", testConn(), "timeout")

	select {
	case body := <-done:
		var payload map[string]string
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		if payload["reason"] != "timeout" {
			t.Errorf("reason: got %q, want timeout", payload["reason"])
		}
	case <-time.After(3 * time.Second):
		t.Error("timed out waiting for disconnected event")
	}
}

// ── SendMessage ───────────────────────────────────────────────────────────────

func TestDispatcher_SendMessage_NoUpstream(t *testing.T) {
	d := webhook.New(nil, zap.NewNop())
	resp, err := d.SendMessage(context.Background(), "localhost", "hub1", testConn(), 1, []byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != nil {
		t.Errorf("expected nil response, got %+v", resp)
	}
}

func TestDispatcher_SendMessage_UpstreamNoContent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertCEHeaders(t, r, "azure.webpubsub.user.message", "hub1", "conn-test-id")
		body, _ := io.ReadAll(r.Body)
		if string(body) != "hello" {
			t.Errorf("body: got %q, want hello", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	d := newDispatcher(ts.URL)
	resp, err := d.SendMessage(context.Background(), "localhost", "hub1", testConn(), 1, []byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != nil {
		t.Error("expected nil response for 204")
	}
}

func TestDispatcher_SendMessage_UpstreamRespondsWithData(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pong"))
	}))
	defer ts.Close()

	d := newDispatcher(ts.URL)
	resp, err := d.SendMessage(context.Background(), "localhost", "hub1", testConn(), 1, []byte("ping"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if string(resp.Data) != "pong" {
		t.Errorf("data: got %q, want pong", resp.Data)
	}
	if resp.ContentType != "text/plain" {
		t.Errorf("content-type: got %q, want text/plain", resp.ContentType)
	}
}

func TestDispatcher_SendMessage_BinaryContentType(t *testing.T) {
	var gotCT string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	d := newDispatcher(ts.URL)
	_, _ = d.SendMessage(context.Background(), "localhost", "hub1", testConn(), 2, []byte{0x01, 0x02})

	if gotCT != "application/octet-stream" {
		t.Errorf("Content-Type: got %q, want application/octet-stream", gotCT)
	}
}

func TestDispatcher_SendMessage_UpstreamError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	d := newDispatcher(ts.URL)
	resp, err := d.SendMessage(context.Background(), "localhost", "hub1", testConn(), 1, []byte("data"))
	if err == nil {
		t.Error("expected error for 500 response")
	}
	if resp != nil {
		t.Error("expected nil response on error")
	}
}

// ── CloudEvent header assertions ──────────────────────────────────────────────

func assertCEHeaders(t *testing.T, r *http.Request, ceType, hub, connID string) {
	t.Helper()
	check := func(header, want string) {
		t.Helper()
		if got := r.Header.Get(header); got != want {
			t.Errorf("header %s: got %q, want %q", header, got, want)
		}
	}
	check("ce-specversion", "1.0")
	check("ce-type", ceType)
	check("ce-hub", hub)
	check("ce-connectionid", connID)

	if r.Header.Get("ce-id") == "" {
		t.Error("ce-id must not be empty")
	}
	if r.Header.Get("ce-source") == "" {
		t.Error("ce-source must not be empty")
	}
	if r.Header.Get("ce-time") == "" {
		t.Error("ce-time must not be empty")
	}
}
