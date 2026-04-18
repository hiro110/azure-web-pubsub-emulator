# Architecture

## Overview

Azure Web PubSub Emulator is a local server that replicates the Azure Web PubSub service protocol for local development and testing. It is SDK-compatible: applications using the official Azure Web PubSub SDKs can switch to the emulator by changing only the connection string endpoint.

```mermaid
graph TB
    subgraph Emulator["Emulator Process"]
        WS["WebSocket Handler<br/>/client/hubs/{hub}"]
        REST["REST API Handler<br/>/api/hubs/{hub}/..."]
        TOKEN["Token Endpoint<br/>/api/hubs/{hub}/:generateClientAccessToken"]
        HUB["Hub Manager<br/>(in-memory)"]
        WEBHOOK["Webhook Dispatcher<br/>(CloudEvents)"]

        WS --> HUB
        REST --> HUB
        TOKEN --> HUB
        HUB --> WEBHOOK
    end

    CLIENT["WebSocket Client<br/>(Browser / App)"]
    APPSRV["App Server (upstream)<br/>/eventhandler"]
    SDK["Azure SDK<br/>(REST API caller)"]

    CLIENT -- "ws://" --> WS
    SDK -- "HTTP + HMAC" --> REST
    SDK -- "HTTP + HMAC" --> TOKEN
    WEBHOOK -- "HTTP POST<br/>CloudEvents" --> APPSRV
```

## Component Responsibilities

### HTTP Server
- Routes incoming HTTP/WebSocket requests
- Middleware: HMAC-SHA256 authentication (REST API only)
- Middleware: Request logging

### WebSocket Handler
- Upgrades HTTP connections to WebSocket at `ws://localhost:{port}/client/hubs/{hub}?access_token={jwt}`
- Validates the JWT access token (signed with the configured AccessKey)
- Assigns a unique `connectionId` per connection
- Dispatches lifecycle events to the Webhook Dispatcher
- Forwards incoming client messages to the Webhook Dispatcher

### REST API Handler
- Implements the Azure Web PubSub Data Plane REST API
- All endpoints require HMAC-SHA256 authentication (same algorithm as the Azure service)
- Delegates connection/group/user operations to the Hub Manager

### Hub Manager
- Central in-memory state store
- Manages per-hub state: active connections, group memberships, user→connection mappings
- Provides thread-safe operations (sync.RWMutex)
- Sends messages to individual connections or broadcasts to groups/users

### Webhook Dispatcher
- Sends CloudEvents HTTP POST requests to the configured upstream URL per hub
- Handles the blocking `connect` event (waits for upstream response before completing WS handshake)
- Handles async events: `connected`, `disconnected`, `message`
- Implements CloudEvents Abuse Protection (WebHook-Request-Origin validation)

### Auth
- **HMAC-SHA256 validation**: Validates `Authorization` headers on REST API requests using the configured AccessKey
- **JWT generation**: Issues client access tokens (`/api/hubs/{hub}/:generateClientAccessToken`) signed with the AccessKey

## Connection String Format

The emulator uses the same connection string format as the Azure service, enabling SDK compatibility:

```
Endpoint=http://localhost:7290;AccessKey=<key>;Version=1.0;
```

Example for local development:
```
Endpoint=http://localhost:7290;AccessKey=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=;Version=1.0;
```

The `AccessKey` value must be a Base64-encoded string of at least 32 bytes to match Azure SDK validation requirements.

## Client Connection URL

```
ws://localhost:{port}/client/hubs/{hub}?access_token={jwt}
```

The JWT is obtained from:
1. The emulator's token endpoint (`POST /api/hubs/{hub}/:generateClientAccessToken`)
2. Generated locally by the SDK using the connection string AccessKey

## In-Memory State Model

```mermaid
classDiagram
    class Emulator {
        +hubs map[string]*Hub
    }
    class Hub {
        +name string
        +connections map[string]*Connection
        +groups map[string]map[string]struct
        +users map[string]map[string]struct
        +mu sync.RWMutex
    }
    class Connection {
        +connectionId string
        +userId string
        +groups map[string]struct
        +permissions map[string]struct
        +send chan Message
        +wsConn *websocket.Conn
    }

    Emulator "1" --> "*" Hub
    Hub "1" --> "*" Connection
```

## CloudEvents Event Flow

### Blocking: `connect`

```mermaid
sequenceDiagram
    participant C as Client
    participant E as Emulator
    participant U as Upstream

    C->>E: WebSocket handshake
    E->>U: POST /eventhandler<br/>ce-type: azure.webpubsub.sys.connect
    alt allowed
        U-->>E: 200 OK (userId, groups, roles)
        E->>C: system.connected message
        E-)U: POST /eventhandler<br/>ce-type: azure.webpubsub.sys.connected (async)
    else denied
        U-->>E: 401 / 403
        E->>C: Close WebSocket (401)
    end
```

### Async: `connected` / `disconnected`

```mermaid
sequenceDiagram
    participant C as Client
    participant E as Emulator
    participant U as Upstream

    C->>E: WebSocket connected / disconnected
    E-)U: POST /eventhandler (fire-and-forget)<br/>ce-type: azure.webpubsub.sys.connected<br/>or azure.webpubsub.sys.disconnected
    Note over E,U: Errors are logged only
```

### Blocking: `message` (simple WebSocket)

```mermaid
sequenceDiagram
    participant C as Client
    participant E as Emulator
    participant U as Upstream

    C->>E: WebSocket message frame
    E->>U: POST /eventhandler<br/>ce-type: azure.webpubsub.user.message
    alt response has body
        U-->>E: 200 OK + payload
        E->>C: Send payload back to client
    else no body
        U-->>E: 200 OK (empty)
    else error
        U-->>E: 4xx / 5xx
        E->>C: Close WebSocket
    end
```

## Distribution

- **Binary**: Built with `go build`, cross-compiled for Linux/macOS/Windows
- **Docker image**: Single-binary image based on `gcr.io/distroless/static`
- **Docker Compose**: Sample `docker-compose.yml` for integration into local dev stacks
- **Configuration**: YAML file mounted at runtime; overridable via environment variables and CLI flags
