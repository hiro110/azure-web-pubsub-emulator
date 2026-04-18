# Configuration

## Configuration File

The emulator is configured via a YAML file (default: `config.yaml` in the working directory).

### Full Example

```yaml
# config.yaml

server:
  host: "0.0.0.0"      # Listen address (default: 0.0.0.0)
  port: 7290            # Listen port (default: 7290)

auth:
  accessKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
  # Base64-encoded string, minimum 32 bytes (required)

logging:
  level: "info"         # debug | info | warn | error (default: info)
  format: "text"        # text | json (default: text)

hubs:
  - name: "chat"
    eventHandlerUrl: "http://localhost:3000/eventhandler"
    # Events to forward to upstream (default: all)
    events:
      system:
        - connect
        - connected
        - disconnected
      user:
        - "*"           # "*" = all user events

  - name: "notifications"
    eventHandlerUrl: "http://localhost:3001/eventhandler"
    # No events section = no upstream, messages not forwarded

  - name: "standalone"
    # No eventHandlerUrl = hub works without upstream
    # Simple WebSocket messages are dropped (no upstream to handle them)
```

### Minimal Example

```yaml
auth:
  accessKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

hubs:
  - name: "hub1"
```

### Hub without upstream

A hub can operate without an upstream webhook URL. In this case:
- `connect` event: connection is always allowed
- `message` events: dropped (no handler)
- `connected` / `disconnected`: not fired

---

## CLI Flags

All configuration values can be overridden via CLI flags:

```
web-pubsub-emulator [flags]

Flags:
  -c, --config string       Path to config file (default: config.yaml)
  -p, --port int            Server port (overrides config)
      --access-key string   Access key (overrides config)
      --log-level string    Log level: debug|info|warn|error
  -h, --help                Show help
      --version             Show version
```

---

## Environment Variables

All CLI flags are also available as environment variables with the prefix `WEBPUBSUB_`:

| Environment Variable          | Equivalent Flag      |
|-------------------------------|----------------------|
| `WEBPUBSUB_CONFIG`            | `--config`           |
| `WEBPUBSUB_PORT`              | `--port`             |
| `WEBPUBSUB_ACCESS_KEY`        | `--access-key`       |
| `WEBPUBSUB_LOG_LEVEL`         | `--log-level`        |

Priority order (highest to lowest): CLI flags → environment variables → config file → defaults

---

## Connection String

After starting the emulator, the connection string for SDK clients is:

```
Endpoint=http://localhost:{port};AccessKey={accessKey};Version=1.0;
```

Example:
```
Endpoint=http://localhost:7290;AccessKey=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=;Version=1.0;
```

---

## Docker

### Environment Variables for Docker

```yaml
# docker-compose.yml
services:
  web-pubsub-emulator:
    image: ghcr.io/yourorg/web-pubsub-emulator:latest
    ports:
      - "7290:7290"
    environment:
      WEBPUBSUB_ACCESS_KEY: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
      WEBPUBSUB_LOG_LEVEL: "debug"
    volumes:
      - ./config.yaml:/app/config.yaml:ro
```

### Volume Mount

Mount a `config.yaml` for full hub configuration:

```bash
docker run -p 7290:7290 \
  -v $(pwd)/config.yaml:/app/config.yaml:ro \
  ghcr.io/yourorg/web-pubsub-emulator:latest
```
