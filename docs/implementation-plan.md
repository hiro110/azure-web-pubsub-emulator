# Implementation Plan

## Project Structure

```
web-pubsub-emulator/
├── cmd/
│   └── emulator/
│       └── main.go              # Entrypoint: CLI flag parsing, config load, server start
├── internal/
│   ├── config/
│   │   ├── config.go            # Config struct, YAML loading, env/flag merging
│   │   └── config_test.go
│   ├── auth/
│   │   ├── hmac.go              # HMAC-SHA256 request validation
│   │   ├── token.go             # JWT client access token generation and validation
│   │   └── auth_test.go
│   ├── hub/
│   │   ├── hub.go               # Hub struct: connections, groups, users
│   │   ├── manager.go           # HubManager: multi-hub registry
│   │   ├── connection.go        # Connection struct and send helpers
│   │   └── hub_test.go
│   ├── handler/
│   │   ├── websocket.go         # WebSocket upgrade, connection lifecycle
│   │   ├── restapi.go           # Data plane REST API handlers
│   │   ├── token.go             # generateClientAccessToken endpoint
│   │   └── middleware.go        # HMAC auth middleware, request logging
│   ├── webhook/
│   │   ├── dispatcher.go        # CloudEvents HTTP POST delivery
│   │   ├── validation.go        # Abuse protection (OPTIONS handshake)
│   │   └── webhook_test.go
│   └── server/
│       ├── server.go            # HTTP server setup, router wiring, graceful shutdown
│       └── routes.go            # Route registration
├── pkg/
│   └── cloudevents/
│       └── types.go             # CloudEvents header constants and payload types
├── config.yaml                  # Default config for local use
├── docker-compose.yml
├── Dockerfile
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

---

## Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/gorilla/websocket` | WebSocket server |
| `github.com/go-chi/chi/v5` | HTTP router |
| `github.com/golang-jwt/jwt/v5` | JWT generation and validation |
| `gopkg.in/yaml.v3` | YAML config parsing |
| `github.com/spf13/cobra` | CLI flag parsing |
| `go.uber.org/zap` | Structured logging |
| `github.com/google/uuid` | connectionId generation |

---

## Implementation Phases

### Phase 1: Project Scaffold & Configuration

**Goal:** Runnable binary that reads config and starts an HTTP server.

Tasks:
- [ ] `go mod init github.com/yourorg/web-pubsub-emulator`
- [ ] Define `Config` struct with YAML tags
- [ ] Implement config loading: file → env vars → CLI flags (priority order)
- [ ] Validate config (accessKey length, port range, hub names)
- [ ] Set up `cobra` CLI with `--config`, `--port`, `--access-key`, `--log-level` flags
- [ ] Set up `zap` logger
- [ ] Basic HTTP server with `/health` endpoint
- [ ] Unit tests for config loading and validation

**Deliverable:** `./web-pubsub-emulator --help` and `GET /health` returns `200 OK`

---

### Phase 2: Authentication

**Goal:** HMAC-SHA256 middleware and JWT token generation endpoint.

Tasks:
- [ ] Implement HMAC-SHA256 request validator (`internal/auth/hmac.go`)
  - Parse `Authorization: HMAC-SHA256 ...` header
  - Reconstruct string-to-sign from request method, path, headers
  - Validate signature against configured AccessKey
- [ ] Implement JWT generation (`internal/auth/token.go`)
  - Sign with HS256 using AccessKey
  - Encode userId, roles, groups, exp claims
- [ ] Implement JWT validation (for incoming WebSocket connections)
- [ ] Register HMAC middleware on REST API router
- [ ] Register `POST /api/hubs/{hub}/:generateClientAccessToken` endpoint
- [ ] Unit tests for HMAC validation (known test vectors from Azure SDK)
- [ ] Unit tests for JWT round-trip

**Deliverable:** Token generation endpoint works with Azure SDK connection string

---

### Phase 3: Hub Manager & In-Memory State

**Goal:** Thread-safe in-memory state for connections, groups, and users.

Tasks:
- [ ] Implement `Connection` struct (`connectionId`, `userId`, `groups`, `permissions`, `wsConn`)
- [ ] Implement `Hub` struct with `sync.RWMutex`-guarded maps
  - `connections map[string]*Connection`
  - `groups map[string]map[string]struct{}`  (group → set of connectionIds)
  - `users map[string]map[string]struct{}`   (userId → set of connectionIds)
- [ ] Implement `HubManager` (registry of hubs, auto-create on first access)
- [ ] Hub operations:
  - `AddConnection`, `RemoveConnection`
  - `AddToGroup`, `RemoveFromGroup`, `RemoveFromAllGroups`
  - `GetConnectionsByGroup`, `GetConnectionsByUser`
  - `SendToConnection`, `SendToGroup`, `SendToUser`, `SendToAll`
  - `GrantPermission`, `RevokePermission`, `HasPermission`
- [ ] Unit tests with concurrent access scenarios

**Deliverable:** Hub state management fully tested in isolation

---

### Phase 4: WebSocket Handler

**Goal:** Clients can connect via WebSocket, receive `system.connected`, and send/receive messages.

Tasks:
- [ ] Register route `GET /client/hubs/{hub}` with WebSocket upgrade
- [ ] Validate JWT from `?access_token` query parameter
- [ ] Generate unique `connectionId` (UUID)
- [ ] Register connection in Hub Manager
- [ ] Send `system.connected` message to client after successful handshake
- [ ] Read loop: receive frames from client, dispatch to Webhook Dispatcher
- [ ] Write loop: send outbound messages from a buffered channel
- [ ] On disconnect: remove from Hub Manager, notify Webhook Dispatcher
- [ ] Handle WebSocket ping/pong keepalive
- [ ] Integration tests: connect, receive connected event, disconnect

**Deliverable:** WebSocket clients can connect and receive the `connected` system message

---

### Phase 5: Upstream Webhook (CloudEvents)

**Goal:** Events are forwarded to the upstream URL per hub.

Tasks:
- [ ] Implement CloudEvents types (`pkg/cloudevents/types.go`)
- [ ] Implement `WebhookDispatcher`
  - HTTP client with configurable timeout
  - `SendConnect(ctx, hub, conn) (*ConnectResponse, error)` — blocking
  - `SendConnected(hub, conn)` — async (goroutine)
  - `SendDisconnected(hub, conn, reason)` — async (goroutine)
  - `SendMessage(ctx, hub, conn, payload) (*MessageResponse, error)` — blocking
- [ ] Implement abuse protection validation on startup
- [ ] Wire dispatcher into WebSocket handler lifecycle
- [ ] Handle `connect` response: userId override, group pre-join, deny with 4xx
- [ ] Handle `message` response: send response body back to originating client
- [ ] Integration tests with mock upstream HTTP server

**Deliverable:** Full event flow — connect → upstream → connected → message → disconnected

---

### Phase 6: Data Plane REST API

**Goal:** All management endpoints work, enabling SDKs to send and manage connections.

Tasks:
- [ ] Implement all REST API handlers in `internal/handler/restapi.go`
  - `POST /:send` (to all)
  - `POST /connections/{id}/:send`, `HEAD`, `DELETE`
  - `PUT/DELETE /connections/{id}/groups/{group}`
  - `PUT/DELETE /connections/{id}/permissions/{perm}`
  - `HEAD /connections/{id}/permissions/{perm}`
  - `HEAD/DELETE /users/{userId}`, `POST /users/{userId}/:send`
  - `PUT/DELETE /groups/{group}/connections/{id}`
  - `PUT/DELETE /groups/{group}/users/{userId}`
  - `HEAD /groups/{group}`, `POST /groups/{group}/:send`
  - `DELETE /users/{userId}/groups`
- [ ] Content-Type handling: `text/plain`, `application/json`, `application/octet-stream`
- [ ] `excluded` query parameter for broadcast operations
- [ ] Integration tests using Azure Web PubSub SDK as the client

**Deliverable:** Azure SDK operations (sendToAll, sendToGroup, addToGroup, etc.) work against emulator

---

### Phase 7: Distribution & Polish

**Goal:** Production-ready artifact that can be distributed and used by others.

Tasks:
- [ ] `Dockerfile` using multi-stage build → distroless final image
- [ ] `docker-compose.yml` example
- [ ] `Makefile` with `build`, `test`, `docker-build`, `lint` targets
- [ ] `README.md` with quickstart, configuration reference, SDK usage examples
- [ ] GitHub Actions CI: lint + test + build on push
- [ ] GitHub Actions release: cross-compile binaries (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64) + Docker image push on tag
- [ ] Add `--version` flag with build-time version injection (`ldflags`)
- [ ] Graceful shutdown: drain in-flight requests, close WebSocket connections cleanly

**Deliverable:** Tagged release with binaries and Docker image; README sufficient for new users

---

## Testing Strategy

| Layer | Tool | Coverage Target |
|-------|------|----------------|
| Unit | `go test` | 80%+ |
| Integration | `go test` + in-process server | Key flows |
| SDK compatibility | Azure SDK test client | Connect, send, group ops |

### Key Integration Test Scenarios
1. Client connects → receives `system.connected` → disconnects
2. Client sends message → upstream receives CloudEvent → response sent back
3. REST API: `sendToAll` → all connected clients receive message
4. REST API: `addToGroup` + `sendToGroup` → group members receive, non-members do not
5. REST API: `closeConnection` → client WebSocket closes
6. HMAC validation: invalid signature returns 401
7. JWT validation: expired token returns 401 on WS connect

---

## Security Considerations

- AccessKey must be minimum 32 bytes (enforced at config load)
- JWT tokens use HS256; short expiry enforced (default 1 hour, max 24 hours)
- HMAC timestamps validated within ±5 minutes to prevent replay attacks
- Upstream webhook requests include `ce-signature` header for upstream validation (optional)
- No TLS in emulator (local use); document that production traffic should use the real Azure service

---

## Out of Scope (Initial Release)

- `json.webpubsub.azure.v1` subprotocol (Phase 2)
- `protobuf.webpubsub.azure.v1` subprotocol (Phase 3)
- MQTT protocol
- Persistent state (restart clears all connections)
- Multi-instance / clustering
- Management plane (resource provisioning) API
- Azure AD / Managed Identity authentication
