# Azure Web PubSub Emulator

A local emulator for [Azure Web PubSub Service](https://learn.microsoft.com/azure/azure-web-pubsub/) that accepts the same connection string and REST API as the real service. Use it for local development and CI without an Azure subscription.

## Features

- Full Azure SDK compatibility via standard connection string format
- WebSocket client endpoint with JWT access token validation
- Complete Data Plane REST API (send, close, group management, permissions)
- Upstream webhook delivery using CloudEvents 1.0 HTTP binding
- HMAC-SHA256 request authentication (identical to Azure service)
- Multiple hub support with per-hub upstream webhook URLs

## Quickstart

### Docker

**Minimal setup — only an AccessKey is required:**

```bash
docker run --rm -p 7290:7290 \
  -e WEBPUBSUB_ACCESS_KEY="AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" \
  ghcr.io/hiro110/azure-web-pubsub-emulator:latest
```

On startup the emulator prints the connection string to use with any Azure SDK:

```
connection string   Endpoint=http://localhost:7290;AccessKey=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=;Version=1.0;
```

**Override port or log level via environment variables:**

```bash
docker run --rm -p 8000:8000 \
  -e WEBPUBSUB_ACCESS_KEY="AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" \
  -e WEBPUBSUB_PORT=8000 \
  -e WEBPUBSUB_LOG_LEVEL=debug \
  ghcr.io/hiro110/azure-web-pubsub-emulator:latest
```

**Mount a config file for full control:**

```bash
docker run --rm -p 7290:7290 \
  -v $(pwd)/config.yaml:/etc/web-pubsub-emulator/config.yaml:ro \
  ghcr.io/hiro110/azure-web-pubsub-emulator:latest
```

### docker-compose

```bash
docker-compose up
```

The bundled `docker-compose.yml` provides a ready-to-use local development setup. Override individual values with environment variables:

```bash
WEBPUBSUB_ACCESS_KEY=mykey docker-compose up
```

### Binary

Download the binary for your OS and architecture from the [Releases](https://github.com/hiro110/azure-web-pubsub-emulator/releases) page, then run:

```bash
./web-pubsub-emulator --config config.yaml
# Or supply only the access key (no config file needed)
./web-pubsub-emulator --access-key "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
```

On startup the emulator prints the connection string:

```
connection string   Endpoint=http://localhost:7290;AccessKey=...;Version=1.0;
```

## Configuration

### config.yaml

```yaml
server:
  host: "0.0.0.0"
  port: 7290

auth:
  # Base64-encoded key, must decode to at least 32 bytes
  accessKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

logging:
  level: "info"   # debug | info | warn | error
  format: "text"  # text | json

hubs:
  - name: "hub1"
    eventHandlerUrl: "http://localhost:3000/eventhandler"  # optional upstream webhook
  - name: "hub2"
```

### CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config`, `-c` | `config.yaml` | Path to config file |
| `--port`, `-p` | *(config)* | Server port |
| `--access-key` | *(config)* | Access key (Base64) |
| `--log-level` | *(config)* | Log level |

### Environment Variables

| Variable | Overrides |
|----------|-----------|
| `WEBPUBSUB_ACCESS_KEY` | `auth.accessKey` |
| `WEBPUBSUB_PORT` | `server.port` |
| `WEBPUBSUB_LOG_LEVEL` | `logging.level` |
| `WEBPUBSUB_LOG_FORMAT` | `logging.format` |

Priority: CLI flags > environment variables > config file > defaults.

## SDK Usage

Use the connection string printed at startup with any Azure Web PubSub SDK.

### .NET (C#)

```csharp
var connectionString = "Endpoint=http://localhost:7290;AccessKey=AAAA...;Version=1.0;";
var client = new WebPubSubServiceClient(connectionString, "hub1");

// Send to all connected clients
await client.SendToAllAsync("Hello from server!");

// Generate a client access token
var token = await client.GetClientAccessUriAsync(userId: "user1");
```

### Node.js / TypeScript

```typescript
import { WebPubSubServiceClient } from "@azure/web-pubsub";

const client = new WebPubSubServiceClient(
  "Endpoint=http://localhost:7290;AccessKey=AAAA...;Version=1.0;",
  "hub1"
);

await client.sendToAll("Hello!");
const token = await client.getClientAccessToken({ userId: "user1" });
```

### Python

```python
from azure.messaging.webpubsubservice import WebPubSubServiceClient

client = WebPubSubServiceClient.from_connection_string(
    "Endpoint=http://localhost:7290;AccessKey=AAAA...;Version=1.0;",
    hub="hub1"
)

client.send_to_all("Hello!")
token = client.get_client_access_token(user_id="user1")
```

### Go

```go
import "github.com/Azure/azure-sdk-for-go/sdk/messaging/azwebpubsub"

client, _ := azwebpubsub.NewClientFromConnectionString(
    "Endpoint=http://localhost:7290;AccessKey=AAAA...;Version=1.0;",
    "hub1", nil,
)

client.SendToAll(ctx, azwebpubsub.ContentTypeTextPlain,
    streaming.NopCloser(strings.NewReader("Hello!")), nil)
```

## API Reference

### WebSocket Client Endpoint

```
GET /client/hubs/{hub}?access_token={jwt}
```

Clients connect with a JWT token generated by `generateClientAccessToken`. On successful connection the server immediately sends:

```json
{"type":"system","event":"connected","userId":"user1","connectionId":"uuid"}
```

### Data Plane REST API

All endpoints require authentication. Two schemes are supported:

| Scheme | Header | Used by |
|--------|--------|---------|
| HMAC-SHA256 | `Authorization: HMAC-SHA256 SignedHeaders=...&Signature=...` | Older SDK versions / curl |
| Bearer JWT | `Authorization: Bearer <jwt>` | `@azure/web-pubsub` v1.2+ / Python SDK |

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/hubs/{hub}/:generateClientAccessToken` | Generate client JWT |
| `POST` | `/api/hubs/{hub}/:send` | Broadcast to all connections |
| `POST` | `/api/hubs/{hub}/:closeConnections` | Close all connections |
| `POST` | `/api/hubs/{hub}/:addToGroups` | Batch-add connections to groups (filter) |
| `POST` | `/api/hubs/{hub}/:removeFromGroups` | Batch-remove connections from groups (filter) |
| `HEAD` | `/api/hubs/{hub}/connections/{id}` | Check connection exists |
| `DELETE` | `/api/hubs/{hub}/connections/{id}` | Close connection |
| `POST` | `/api/hubs/{hub}/connections/{id}/:send` | Send to connection |
| `PUT` | `/api/hubs/{hub}/connections/{id}/groups/{group}` | Add connection to group |
| `DELETE` | `/api/hubs/{hub}/connections/{id}/groups/{group}` | Remove connection from group |
| `DELETE` | `/api/hubs/{hub}/connections/{id}/groups` | Remove connection from all groups |
| `HEAD` | `/api/hubs/{hub}/groups/{group}` | Check group exists |
| `GET` | `/api/hubs/{hub}/groups/{group}/connections` | List connections in group |
| `POST` | `/api/hubs/{hub}/groups/{group}/:send` | Send to group |
| `POST` | `/api/hubs/{hub}/groups/{group}/:closeConnections` | Close group connections |
| `HEAD` | `/api/hubs/{hub}/users/{userId}` | Check user exists |
| `POST` | `/api/hubs/{hub}/users/{userId}/:send` | Send to user |
| `POST` | `/api/hubs/{hub}/users/{userId}/:closeConnections` | Close user connections |
| `PUT` | `/api/hubs/{hub}/users/{userId}/groups/{group}` | Add user to group |
| `DELETE` | `/api/hubs/{hub}/users/{userId}/groups/{group}` | Remove user from group |
| `DELETE` | `/api/hubs/{hub}/users/{userId}/groups` | Remove user from all groups |
| `GET` | `/api/hubs/{hub}/permissions/{perm}/connections/{id}` | Check permission |
| `PUT` | `/api/hubs/{hub}/permissions/{perm}/connections/{id}` | Grant permission |
| `DELETE` | `/api/hubs/{hub}/permissions/{perm}/connections/{id}` | Revoke permission |

#### OData Filter (simplified)

The `filter` query parameter and request body field support two expressions:

| Expression | Description |
|---|---|
| `userId eq 'value'` | Include only connections with matching userId |
| `userId ne 'value'` | Include only connections with non-matching userId |

All other expressions fall through to no filter (match all).

## Upstream Webhook (CloudEvents)

When `eventHandlerUrl` is set for a hub, the emulator forwards lifecycle events to that URL using CloudEvents 1.0 HTTP binding.

| Event | When | Blocking |
|-------|------|---------|
| `azure.webpubsub.sys.connect` | Before WebSocket upgrade | Yes — upstream can reject or override userId/groups |
| `azure.webpubsub.sys.connected` | After upgrade | No (fire-and-forget) |
| `azure.webpubsub.sys.disconnected` | On disconnect | No (fire-and-forget) |
| `azure.webpubsub.user.message` | On client message | Yes — upstream can reply with data to send back |

CloudEvents headers included on every request:

```
ce-specversion: 1.0
ce-id:          <uuid>
ce-type:        azure.webpubsub.sys.connect
ce-source:      //localhost:7290/hubs/hub1
ce-time:        2026-03-20T10:00:00Z
ce-connectionid: <connectionId>
ce-hub:         hub1
ce-userid:      user1   (omitted for anonymous connections)
```

## Building from Source

```bash
# Run tests
make test-race

# Build binary
make build

# Cross-compile for all platforms
make release

# Build Docker image
make docker-build
```

## Compatibility

| Feature | Status |
|---------|--------|
| WebSocket client connection | Supported |
| JWT client access token | Supported |
| HMAC-SHA256 authentication | Supported |
| Data Plane REST API (all endpoints) | Supported |
| CloudEvents upstream webhook | Supported |
| OData filter (eq / ne on userId) | Supported |
| `json.webpubsub.azure.v1` subprotocol | Not supported |
| `protobuf.webpubsub.azure.v1` subprotocol | Not supported |
| MQTT | Not supported |
| Persistent state across restarts | Not supported |

## License

MIT
