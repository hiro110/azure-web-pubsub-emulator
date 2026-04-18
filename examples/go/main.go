// Azure Web PubSub Emulator – Go compatibility check
//
// Tests the full client lifecycle against the running emulator.
// Run after starting the emulator on port 7290.
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
)

const (
	endpoint  = "http://localhost:7290"
	accessKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	hub       = "hub1"
)

var failed bool

func pass(msg string) { fmt.Printf("✓ %s\n", msg) }
func fail(msg string) { failed = true; fmt.Fprintf(os.Stderr, "✗ %s\n", msg) }

// generateToken creates a signed JWT for the given WebSocket audience and optional userId.
// The emulator validates tokens signed with the raw AccessKey bytes (not base64-decoded).
func generateToken(audience, userID string) (string, error) {
	claims := jwt.MapClaims{
		"aud": audience,
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(60 * time.Minute).Unix(),
	}
	if userID != "" {
		claims["sub"] = userID
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(accessKey))
}

// signRequest adds HMAC-SHA256 Authorization headers to r.
func signRequest(r *http.Request) {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	hash := sha256.Sum256(body)
	contentHash := base64.StdEncoding.EncodeToString(hash[:])
	date := time.Now().UTC().Format(http.TimeFormat)

	r.Header.Set("x-ms-date", date)
	r.Header.Set("x-ms-content-sha256", contentHash)

	stringToSign := r.Method + "\n" + r.URL.RequestURI() + "\n" +
		date + ";" + r.URL.Host + ";" + contentHash
	mac := hmac.New(sha256.New, []byte(accessKey))
	mac.Write([]byte(stringToSign))
	r.Header.Set("Authorization", fmt.Sprintf(
		"HMAC-SHA256 SignedHeaders=x-ms-date;host;x-ms-content-sha256&Signature=%s",
		base64.StdEncoding.EncodeToString(mac.Sum(nil)),
	))
}

// apiCall executes a signed REST request and returns the response.
func apiCall(method, path, contentType, body string) *http.Response {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = bytes.NewBufferString(body)
	}
	req, err := http.NewRequest(method, endpoint+path, bodyReader)
	if err != nil {
		panic(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	signRequest(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(fmt.Sprintf("%s %s: %v", method, path, err))
	}
	return resp
}

type systemMsg struct {
	Type         string `json:"type"`
	Event        string `json:"event"`
	UserID       string `json:"userId"`
	ConnectionID string `json:"connectionId"`
}

// connectWS connects and blocks until system.connected is received.
// Returns the connection and connectionId.
func connectWS(userID string) (*websocket.Conn, string) {
	audience := fmt.Sprintf("%s/client/hubs/%s", endpoint, hub)
	token, err := generateToken(audience, userID)
	if err != nil {
		panic(err)
	}
	wsURL := fmt.Sprintf("ws://localhost:7290/client/hubs/%s?access_token=%s", hub, token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		panic(fmt.Sprintf("WebSocket dial: %v", err))
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	conn.SetReadDeadline(time.Time{})
	if err != nil {
		panic(fmt.Sprintf("reading system.connected: %v", err))
	}
	var msg systemMsg
	json.Unmarshal(data, &msg) //nolint:errcheck
	return conn, msg.ConnectionID
}

// waitForMessage waits up to timeout for a raw text message satisfying pred.
func waitForMessage(conn *websocket.Conn, pred func(string) bool, timeout time.Duration) bool {
	conn.SetReadDeadline(time.Now().Add(timeout))
	defer conn.SetReadDeadline(time.Time{})
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return false
		}
		if pred(string(data)) {
			return true
		}
	}
}

// assertNoMessage asserts that no message matching pred arrives within timeout.
func assertNoMessage(conn *websocket.Conn, pred func(string) bool, timeout time.Duration) bool {
	conn.SetReadDeadline(time.Now().Add(timeout))
	defer conn.SetReadDeadline(time.Time{})
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return true // deadline exceeded — no matching message
		}
		if pred(string(data)) {
			return false
		}
	}
}

func main() {
	// 1. Token generation
	audience := fmt.Sprintf("%s/client/hubs/%s", endpoint, hub)
	token, err := generateToken(audience, "user1")
	if err != nil || token == "" {
		fail("token generated for user1")
		os.Exit(1)
	}
	pass("token generated for user1")

	// 2. WebSocket connection (named user)
	ws1, connID1 := connectWS("user1")
	defer ws1.Close()
	pass(fmt.Sprintf("user1 connected (connectionId: %s)", connID1))

	// 3. Anonymous connection
	ws2, connID2 := connectWS("")
	defer ws2.Close()
	pass(fmt.Sprintf("anonymous connected (connectionId: %s)", connID2))

	// 4. sendToAll — both clients receive
	const broadcast = "hello-broadcast"
	var wg sync.WaitGroup
	var r1, r2 bool
	wg.Add(2)
	go func() { r1 = waitForMessage(ws1, func(s string) bool { return s == broadcast }, 2*time.Second); wg.Done() }()
	go func() { r2 = waitForMessage(ws2, func(s string) bool { return s == broadcast }, 2*time.Second); wg.Done() }()
	resp := apiCall(http.MethodPost, fmt.Sprintf("/api/hubs/%s/:send", hub), "text/plain", broadcast)
	resp.Body.Close()
	wg.Wait()
	if r1 {
		pass("sendToAll: user1 received message")
	} else {
		fail("sendToAll: user1 received message")
	}
	if r2 {
		pass("sendToAll: anon received message")
	} else {
		fail("sendToAll: anon received message")
	}

	// 5. sendToConnection — only target receives
	const targetMsg = "hello-target"
	var rt, ro bool
	wg.Add(2)
	go func() {
		rt = waitForMessage(ws1, func(s string) bool { return s == targetMsg }, 2*time.Second)
		wg.Done()
	}()
	go func() {
		ro = assertNoMessage(ws2, func(s string) bool { return s == targetMsg }, 500*time.Millisecond)
		wg.Done()
	}()
	resp = apiCall(http.MethodPost, fmt.Sprintf("/api/hubs/%s/connections/%s/:send", hub, connID1), "text/plain", targetMsg)
	resp.Body.Close()
	wg.Wait()
	if rt {
		pass("sendToConnection: target received message")
	} else {
		fail("sendToConnection: target received message")
	}
	if ro {
		pass("sendToConnection: other did NOT receive message")
	} else {
		fail("sendToConnection: other did NOT receive message")
	}

	// 6. sendToUser — only user1's connection receives
	const userMsg = "hello-user1"
	var ru, ra bool
	wg.Add(2)
	go func() {
		ru = waitForMessage(ws1, func(s string) bool { return s == userMsg }, 2*time.Second)
		wg.Done()
	}()
	go func() {
		ra = assertNoMessage(ws2, func(s string) bool { return s == userMsg }, 500*time.Millisecond)
		wg.Done()
	}()
	resp = apiCall(http.MethodPost, fmt.Sprintf("/api/hubs/%s/users/user1/:send", hub), "text/plain", userMsg)
	resp.Body.Close()
	wg.Wait()
	if ru {
		pass("sendToUser: user1 received message")
	} else {
		fail("sendToUser: user1 received message")
	}
	if ra {
		pass("sendToUser: anon did NOT receive message")
	} else {
		fail("sendToUser: anon did NOT receive message")
	}

	// 7. Group management
	const group = "room1"
	const groupMsg = "hello-group"
	resp = apiCall(http.MethodPut, fmt.Sprintf("/api/hubs/%s/groups/%s/connections/%s", hub, group, connID1), "", "")
	resp.Body.Close()
	var rm, rn bool
	wg.Add(2)
	go func() {
		rm = waitForMessage(ws1, func(s string) bool { return s == groupMsg }, 2*time.Second)
		wg.Done()
	}()
	go func() {
		rn = assertNoMessage(ws2, func(s string) bool { return s == groupMsg }, 500*time.Millisecond)
		wg.Done()
	}()
	resp = apiCall(http.MethodPost, fmt.Sprintf("/api/hubs/%s/groups/%s/:send", hub, group), "text/plain", groupMsg)
	resp.Body.Close()
	wg.Wait()
	if rm {
		pass("group send: member received message")
	} else {
		fail("group send: member received message")
	}
	if rn {
		pass("group send: non-member did NOT receive message")
	} else {
		fail("group send: non-member did NOT receive message")
	}

	// 8. connectionExists — live connection
	resp = apiCall(http.MethodHead, fmt.Sprintf("/api/hubs/%s/connections/%s", hub, connID1), "", "")
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		pass("connectionExists: true for live connection")
	} else {
		fail("connectionExists: true for live connection")
	}

	// connectionExists — after close
	ws2.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")) //nolint:errcheck
	ws2.Close()
	time.Sleep(300 * time.Millisecond)
	resp = apiCall(http.MethodHead, fmt.Sprintf("/api/hubs/%s/connections/%s", hub, connID2), "", "")
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		pass("connectionExists: false after close")
	} else {
		fail("connectionExists: false after close")
	}

	// 9. groupExists
	resp = apiCall(http.MethodHead, fmt.Sprintf("/api/hubs/%s/groups/%s", hub, group), "", "")
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		pass("groupExists: true for non-empty group")
	} else {
		fail("groupExists: true for non-empty group")
	}

	// 10. closeConnection — server closes ws1
	closed := make(chan struct{})
	go func() {
		for {
			if _, _, err := ws1.ReadMessage(); err != nil {
				close(closed)
				return
			}
		}
	}()
	resp = apiCall(http.MethodDelete, fmt.Sprintf("/api/hubs/%s/connections/%s", hub, connID1), "", "")
	resp.Body.Close()
	select {
	case <-closed:
		pass("closeConnection: WebSocket closed by server")
	case <-time.After(2 * time.Second):
		fail("closeConnection: WebSocket closed by server")
	}

	if failed {
		os.Exit(1)
	}
}
