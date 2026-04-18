// Azure Web PubSub Emulator – webhook event handler example
//
// Demonstrates end-to-end CloudEvents delivery between the emulator and an
// upstream event handler. The program starts both:
//   - A mock upstream HTTP server that records incoming CloudEvents
//   - The emulator (in-process) configured to deliver events to that server
//
// Then it connects WebSocket clients and verifies all four system events:
//
//	sys.connect     → fired synchronously before WebSocket upgrade
//	sys.connected   → fired asynchronously after upgrade
//	user.message    → fired when the client sends a message
//	sys.disconnected → fired when the client disconnects
//
// Usage:
//
//	go run ./examples/webhook
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/hiro110/azure-web-pubsub-emulator/internal/auth"
	"github.com/hiro110/azure-web-pubsub-emulator/internal/config"
	"github.com/hiro110/azure-web-pubsub-emulator/internal/server"
)

const (
	accessKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	hub       = "hub1"
)

var failed atomic.Bool

func pass(msg string) { fmt.Printf("✓ %s\n", msg) }
func fail(msg string) {
	failed.Store(true)
	fmt.Fprintf(os.Stderr, "✗ %s\n", msg)
}

func main() {
	// 1. Start mock upstream server
	upstream := newUpstreamServer()
	defer upstream.srv.Close()

	// 2. Start emulator in-process, pointing at the upstream
	emulatorAddr := startEmulator(upstream.URL())

	// 3. Run verification scenarios
	runChecks(emulatorAddr, upstream)

	if failed.Load() {
		os.Exit(1)
	}
}

// ── Verification scenarios ────────────────────────────────────────────────────

func runChecks(emulatorAddr string, upstream *upstreamServer) {
	accessKeyBytes := []byte(accessKey)
	audience := fmt.Sprintf("http://%s/client/hubs/%s", emulatorAddr, hub)

	// Connect as user1
	token1, err := auth.GenerateClientToken(accessKeyBytes, audience, "user1", nil, nil, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "GenerateClientToken: %v\n", err)
		os.Exit(1)
	}

	// sys.connect must be fired before the WebSocket upgrade completes
	ws1, connID1 := connectWS(emulatorAddr, token1)
	defer ws1.Close()
	pass(fmt.Sprintf("sys.connect fired (connectionId: %s)", connID1))

	// sys.connected is async — wait for it
	if upstream.waitFor("azure.webpubsub.sys.connected", 3*time.Second) {
		pass("sys.connected fired after WebSocket upgrade")
	} else {
		fail("sys.connected not received within 3s")
	}

	// user.message — client sends a text message
	if err := ws1.WriteMessage(websocket.TextMessage, []byte("hello upstream")); err != nil {
		fail(fmt.Sprintf("WriteMessage: %v", err))
	} else if upstream.waitFor("azure.webpubsub.user.message", 3*time.Second) {
		pass("user.message fired when client sends a message")
	} else {
		fail("user.message not received within 3s")
	}

	// user.message reply — upstream responds with data that is forwarded back
	upstream.setMessageReply([]byte("pong"), "text/plain")
	if err := ws1.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		fail(fmt.Sprintf("WriteMessage: %v", err))
	} else {
		ws1.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, data, err := ws1.ReadMessage()
		if err != nil {
			fail(fmt.Sprintf("reading upstream reply: %v", err))
		} else if string(data) == "pong" {
			pass("user.message reply forwarded to WebSocket client")
		} else {
			fail(fmt.Sprintf("unexpected reply: %q", data))
		}
	}
	upstream.setMessageReply(nil, "")

	// sys.connect rejection — a second upstream handler rejects connections
	upstream.setRejectNextConnect(true)
	token2, _ := auth.GenerateClientToken(accessKeyBytes, audience, "rejected-user", nil, nil, 0)
	wsURL := fmt.Sprintf("ws://%s/client/hubs/%s?access_token=%s", emulatorAddr, hub, token2)
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	upstream.setRejectNextConnect(false)
	if err != nil && resp != nil && resp.StatusCode == http.StatusForbidden {
		pass("sys.connect rejection prevents WebSocket upgrade (403)")
	} else if err == nil {
		fail("expected connection to be rejected by upstream")
	} else {
		fail(fmt.Sprintf("unexpected error or status: %v", err))
	}

	// sys.disconnected — close ws1 and wait
	ws1.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	ws1.Close()
	if upstream.waitFor("azure.webpubsub.sys.disconnected", 3*time.Second) {
		pass("sys.disconnected fired after WebSocket close")
	} else {
		fail("sys.disconnected not received within 3s")
	}
}

// ── WebSocket helper ──────────────────────────────────────────────────────────

func connectWS(emulatorAddr, token string) (*websocket.Conn, string) {
	wsURL := fmt.Sprintf("ws://%s/client/hubs/%s?access_token=%s", emulatorAddr, hub, token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WebSocket dial: %v\n", err)
		os.Exit(1)
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ReadMessage (system.connected): %v\n", err)
		os.Exit(1)
	}
	conn.SetReadDeadline(time.Time{})

	var msg map[string]interface{}
	json.Unmarshal(data, &msg)
	connID, _ := msg["connectionId"].(string)
	return conn, connID
}

// ── In-process emulator ───────────────────────────────────────────────────────

func startEmulator(upstreamURL string) string {
	cfg := &config.Config{
		Server:  config.ServerConfig{Host: "127.0.0.1", Port: 0},
		Auth:    config.AuthConfig{AccessKey: accessKey},
		Logging: config.LoggingConfig{Level: "error", Format: "text"},
		Hubs:    []config.HubConfig{{Name: hub, EventHandlerURL: upstreamURL}},
	}
	srv, err := server.New(cfg, zap.NewNop())
	if err != nil {
		fmt.Fprintf(os.Stderr, "server.New: %v\n", err)
		os.Exit(1)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "net.Listen: %v\n", err)
		os.Exit(1)
	}

	httpSrv := &http.Server{Handler: srv.Handler()}
	go httpSrv.Serve(ln) //nolint:errcheck

	return ln.Addr().String()
}

// ── Mock upstream server ──────────────────────────────────────────────────────

type upstreamServer struct {
	srv  *http.Server
	addr string // base URL: "http://127.0.0.1:<port>"

	mu             sync.Mutex
	events         []string
	rejectConnect  bool
	messageReply   []byte
	messageReplyCT string
}

func newUpstreamServer() *upstreamServer {
	u := &upstreamServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", u.handle)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "upstream listen: %v\n", err)
		os.Exit(1)
	}
	u.addr = "http://" + ln.Addr().String()
	u.srv = &http.Server{Handler: mux}
	go u.srv.Serve(ln) //nolint:errcheck
	return u
}

func (u *upstreamServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	ceType := r.Header.Get("ce-type")
	io.Copy(io.Discard, r.Body) //nolint:errcheck

	u.mu.Lock()
	u.events = append(u.events, ceType)
	reject := u.rejectConnect && ceType == "azure.webpubsub.sys.connect"
	reply := u.messageReply
	replyCT := u.messageReplyCT
	u.mu.Unlock()

	if reject {
		http.Error(w, "rejected by upstream", http.StatusForbidden)
		return
	}
	if ceType == "azure.webpubsub.user.message" && len(reply) > 0 {
		w.Header().Set("Content-Type", replyCT)
		w.WriteHeader(http.StatusOK)
		w.Write(reply) //nolint:errcheck
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (u *upstreamServer) waitFor(ceType string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		u.mu.Lock()
		for _, e := range u.events {
			if e == ceType {
				u.mu.Unlock()
				return true
			}
		}
		u.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func (u *upstreamServer) setRejectNextConnect(v bool) {
	u.mu.Lock()
	u.rejectConnect = v
	u.mu.Unlock()
}

func (u *upstreamServer) setMessageReply(data []byte, ct string) {
	u.mu.Lock()
	u.messageReply = data
	u.messageReplyCT = ct
	u.mu.Unlock()
}

// URL returns the base URL of the upstream server.
func (u *upstreamServer) URL() string { return u.addr }
