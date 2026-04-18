# SDK Compatibility Examples

These scripts verify end-to-end compatibility between the emulator and the official Azure Web PubSub SDKs.

Each script exercises the full client lifecycle:

1. Generate a client access token
2. Connect via WebSocket (receives `system.connected`)
3. Connect anonymously (no userId)
4. `sendToAll` — both connected clients receive the message
5. `sendToConnection` — only the target connection receives the message
6. `sendToUser` — only connections of that user receive the message
7. Group management — `addConnectionToGroup` + `sendToGroup`, non-member is excluded
8. `connectionExists` — 200 when alive, 404 after close
9. `groupExists`
10. `closeConnection` — WebSocket is closed by the server

## Prerequisites

### Runtime setup (mise)

All required runtimes (Node.js 22, Python 3.12, Go 1.24) are declared in `.mise.toml` at the repository root.

```bash
# Install mise: https://mise.jdx.dev/getting-started.html
mise install
```

### Start the emulator

Start the emulator before running any script:

```bash
# Via Docker
docker run --rm -p 7290:7290 \
  -e WEBPUBSUB_ACCESS_KEY="AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" \
  ghcr.io/hiro110/azure-web-pubsub-emulator:latest

# Or from source
go run ./cmd/web-pubsub-emulator --config config.yaml
```

---

## Node.js

### Setup

```bash
cd examples/node
npm install
```

### Run

```bash
node main.mjs
```

Expected output (all lines prefixed with `✓`):

```
✓ token generated for user1
✓ user1 connected (connectionId: ...)
✓ anonymous connected (connectionId: ...)
✓ sendToAll: user1 received message
✓ sendToAll: anon received message
✓ sendToConnection: target received message
✓ sendToConnection: other did NOT receive message
✓ sendToUser: user1 received message
✓ group send: member received message
✓ group send: non-member did NOT receive message
✓ connectionExists: 200 for live connection
✓ connectionExists: 404 after close
✓ groupExists: 200 for non-empty group
✓ closeConnection: WebSocket closed by server
```

---

## Python

### Setup

```bash
cd examples/python
pip install -r requirements.txt
```

### Run

```bash
python main.py
```

Expected output matches the Node.js output above.

---

## Go

No external Azure SDK is required. The example uses `github.com/golang-jwt/jwt/v5` for token generation and `github.com/gorilla/websocket` for WebSocket connections, implementing HMAC-SHA256 signing directly against the emulator's REST API.

### Setup

```bash
cd examples/go
go mod download
```

### Run

```bash
go run main.go
```

Expected output matches the Node.js output above.
