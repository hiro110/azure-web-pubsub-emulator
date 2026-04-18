# API Specification

All REST API endpoints require HMAC-SHA256 authentication identical to the Azure Web PubSub service.

## Authentication

### HMAC-SHA256 Signature

**Authorization header format:**
```
Authorization: HMAC-SHA256 SignedHeaders=date;host;x-ms-content-sha256&Signature=<base64>
```

**Required headers:**
- `Date` or `x-ms-date`: RFC 1123 timestamp
- `Host`: hostname and port
- `x-ms-content-sha256`: Base64(SHA256(request body)); empty body = Base64(SHA256(""))

**Signature computation:**
```
StringToSign = HTTPMethod + "\n"
             + PathAndQuery + "\n"
             + SignedHeaders  // "date;host;x-ms-content-sha256"
             + ":" + date + ";" + host + ";" + contentHash

Signature = Base64(HMAC-SHA256(AccessKey, StringToSign))
```

---

## Token Endpoint

### Generate Client Access Token

```
POST /api/hubs/{hub}/:generateClientAccessToken
```

**Request body (JSON, optional):**
```json
{
  "userId": "user1",
  "roles": ["webpubsub.joinLeaveGroup", "webpubsub.sendToGroup"],
  "minutesToExpire": 60,
  "groups": ["group1"]
}
```

**Response 200:**
```json
{
  "token": "<jwt>"
}
```

The returned JWT is signed with the configured AccessKey (HS256) and contains:
- `sub`: userId (if specified)
- `role`: array of roles
- `iat` / `exp`: issued-at / expiry
- `webpubsub.group`: pre-joined groups (if specified)

---

## Client WebSocket Endpoint

```
GET /client/hubs/{hub}?access_token={jwt}
Upgrade: websocket
```

Successful connection triggers:
1. (If upstream configured) `connect` CloudEvent to upstream — blocks until response
2. WebSocket upgraded
3. `system.connected` message sent to client:
   ```json
   {
     "type": "system",
     "event": "connected",
     "userId": "user1",
     "connectionId": "abcdefgh"
   }
   ```
4. (If upstream configured) `connected` CloudEvent fired async

---

## Data Plane REST API

Base path: `/api/hubs/{hub}`

### Hub-level Operations

#### Send message to all connections in hub

```
POST /api/hubs/{hub}/:send
```

**Query parameters:**
- `excluded` (optional): comma-separated connectionIds to exclude

**Request headers:**
- `Content-Type`: `text/plain` | `application/json` | `application/octet-stream`

**Request body:** message payload

**Response:** `202 Accepted`

---

### Connection Operations

#### Check if connection exists

```
HEAD /api/hubs/{hub}/connections/{connectionId}
```

**Response:** `200 OK` (exists) | `404 Not Found`

#### Send message to connection

```
POST /api/hubs/{hub}/connections/{connectionId}/:send
```

**Request headers:**
- `Content-Type`: `text/plain` | `application/json` | `application/octet-stream`

**Request body:** message payload

**Response:** `202 Accepted` | `404 Not Found`

#### Close connection

```
DELETE /api/hubs/{hub}/connections/{connectionId}
```

**Query parameters:**
- `reason` (optional): close reason string

**Response:** `200 OK` | `404 Not Found`

#### Add connection to group

```
PUT /api/hubs/{hub}/connections/{connectionId}/groups/{group}
```

**Response:** `200 OK` | `404 Not Found`

#### Remove connection from group

```
DELETE /api/hubs/{hub}/connections/{connectionId}/groups/{group}
```

**Response:** `200 OK` | `404 Not Found`

#### Grant permission to connection

```
PUT /api/hubs/{hub}/connections/{connectionId}/permissions/{permission}
```

**Path parameter `permission`:** `sendToGroup` | `joinLeaveGroup`

**Query parameters:**
- `targetName` (optional): specific group name (scoped permission)

**Response:** `200 OK` | `404 Not Found`

#### Revoke permission from connection

```
DELETE /api/hubs/{hub}/connections/{connectionId}/permissions/{permission}
```

**Response:** `200 OK` | `404 Not Found`

#### Check connection permission

```
HEAD /api/hubs/{hub}/connections/{connectionId}/permissions/{permission}
```

**Response:** `200 OK` (has permission) | `404 Not Found`

---

### User Operations

#### Check if user has active connections

```
HEAD /api/hubs/{hub}/users/{userId}
```

**Response:** `200 OK` (has connections) | `404 Not Found`

#### Send message to user

```
POST /api/hubs/{hub}/users/{userId}/:send
```

**Request headers:**
- `Content-Type`: `text/plain` | `application/json` | `application/octet-stream`

**Request body:** message payload

**Response:** `202 Accepted` | `404 Not Found`

#### Close all connections for user

```
DELETE /api/hubs/{hub}/users/{userId}
```

**Query parameters:**
- `reason` (optional): close reason string

**Response:** `200 OK`

#### Add user to group

```
PUT /api/hubs/{hub}/groups/{group}/users/{userId}
```

**Response:** `200 OK`

#### Remove user from group

```
DELETE /api/hubs/{hub}/groups/{group}/users/{userId}
```

**Response:** `200 OK`

#### Remove user from all groups

```
DELETE /api/hubs/{hub}/users/{userId}/groups
```

**Response:** `200 OK`

---

### Group Operations

#### Check if group has active connections

```
HEAD /api/hubs/{hub}/groups/{group}
```

**Response:** `200 OK` (has connections) | `404 Not Found`

#### Send message to group

```
POST /api/hubs/{hub}/groups/{group}/:send
```

**Query parameters:**
- `excluded` (optional): comma-separated connectionIds to exclude

**Request headers:**
- `Content-Type`: `text/plain` | `application/json` | `application/octet-stream`

**Request body:** message payload

**Response:** `202 Accepted`

#### Add connection to group

```
PUT /api/hubs/{hub}/groups/{group}/connections/{connectionId}
```

**Response:** `200 OK` | `404 Not Found`

#### Remove connection from group

```
DELETE /api/hubs/{hub}/groups/{group}/connections/{connectionId}
```

**Response:** `200 OK` | `404 Not Found`

---

## CloudEvents Webhook (Upstream)

The emulator delivers events to the upstream URL configured per hub using the CloudEvents HTTP Protocol Binding (binary content mode).

### Abuse Protection (Validation)

When the emulator starts (or when upstream URL is configured), it validates the endpoint:

```
OPTIONS {upstreamUrl}
WebHook-Request-Origin: localhost
WebHook-Request-Rate: 1
```

Expected response:
```
200 OK
WebHook-Allowed-Origin: * | localhost
```

### Common CloudEvents Headers

All event requests include:
```
ce-specversion: 1.0
ce-id: <uuid>
ce-source: //{emulator-host}/hubs/{hub}
ce-time: <ISO8601>
ce-connectionid: <connectionId>
ce-hub: <hubName>
ce-userId: <userId>        (omitted if anonymous)
Content-Type: application/json
```

### System `connect` Event (Blocking)

```
POST {upstreamUrl}
ce-type: azure.webpubsub.sys.connect
ce-subprotocol: (omitted for simple WebSocket)

Body (JSON):
{
  "claims": {},
  "query": { "access_token": ["..."] },
  "headers": { "User-Agent": ["..."] },
  "clientCertificates": []
}
```

**Response (allow):**
```
200 OK
Content-Type: application/json

{
  "userId": "user1",      // optional override
  "groups": ["group1"],   // optional pre-join groups
  "roles": [],            // optional roles
  "subprotocol": ""
}
```

**Response (deny):**
```
401 Unauthorized | 403 Forbidden
```

### System `connected` Event (Async)

```
POST {upstreamUrl}
ce-type: azure.webpubsub.sys.connected

Body: {}
```

Response is ignored (only HTTP errors are logged).

### System `disconnected` Event (Async)

```
POST {upstreamUrl}
ce-type: azure.webpubsub.sys.disconnected

Body (JSON):
{
  "reason": "normal closure"
}
```

Response is ignored.

### User `message` Event (Blocking, Simple WebSocket only)

```
POST {upstreamUrl}
ce-type: azure.webpubsub.user.message
Content-Type: text/plain | application/json | application/octet-stream

Body: <raw message payload>
```

**Response (send data back to client):**
```
200 OK
Content-Type: text/plain | application/json | application/octet-stream

Body: <payload to send to client>
```

**Response (no reply):**
```
200 OK  (empty body)
```

**Response (error):**
```
4xx — connection is closed
500 — connection is closed
```
