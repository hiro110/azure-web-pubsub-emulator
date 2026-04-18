/**
 * Azure Web PubSub Emulator – Node.js SDK compatibility check
 *
 * Tests the full client lifecycle against the running emulator.
 * Run after starting the emulator on port 7290.
 */

import { WebPubSubServiceClient } from "@azure/web-pubsub";
import WebSocket from "ws";

const CONNECTION_STRING =
  "Endpoint=http://localhost:7290;AccessKey=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=;Version=1.0;";
const HUB = "hub1";

const client = new WebPubSubServiceClient(CONNECTION_STRING, HUB, {
  allowInsecureConnection: true,
});

// ── helpers ──────────────────────────────────────────────────────────────────

function pass(msg) {
  console.log(`✓ ${msg}`);
}

function fail(msg) {
  console.error(`✗ ${msg}`);
  process.exitCode = 1;
}

/**
 * Connect a WebSocket client and wait for the system.connected message.
 * Returns { ws, connectionId, userId }.
 * Uses once() so the handler is removed after the first message.
 */
async function connectWS(tokenUrl) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(tokenUrl);
    const timer = setTimeout(
      () => reject(new Error("WS connect timeout")),
      5000
    );

    ws.once("message", (data) => {
      clearTimeout(timer);
      try {
        const msg = JSON.parse(data.toString());
        if (msg.type === "system" && msg.event === "connected") {
          resolve({ ws, connectionId: msg.connectionId, userId: msg.userId });
        } else {
          reject(new Error(`unexpected first message: ${data}`));
        }
      } catch (err) {
        reject(err);
      }
    });

    ws.on("error", (err) => {
      clearTimeout(timer);
      reject(err);
    });
  });
}

/**
 * Wait up to `timeoutMs` for the WebSocket to receive a message matching
 * the predicate. Predicate receives (parsedJson | null, rawString).
 */
function waitForMessage(ws, predicate, timeoutMs = 2000) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(
      () => reject(new Error("message timeout")),
      timeoutMs
    );

    const handler = (data) => {
      const raw = data.toString();
      let msg = null;
      try { msg = JSON.parse(raw); } catch { /* plain text */ }
      if (predicate(msg, raw)) {
        clearTimeout(timer);
        ws.off("message", handler);
        resolve(msg ?? raw);
      }
    };

    ws.on("message", handler);
  });
}

/**
 * Assert that NO message matching predicate arrives within timeoutMs.
 * Predicate receives (parsedJson | null, rawString).
 */
function assertNoMessage(ws, predicate, timeoutMs = 500) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(resolve, timeoutMs);

    const handler = (data) => {
      const raw = data.toString();
      let msg = null;
      try { msg = JSON.parse(raw); } catch { /* plain text */ }
      if (predicate(msg, raw)) {
        clearTimeout(timer);
        ws.off("message", handler);
        reject(new Error("unexpected message received"));
      }
    };

    ws.on("message", handler);
  });
}

/** Wait for a WebSocket close event. */
function waitForClose(ws, timeoutMs = 2000) {
  return new Promise((resolve, reject) => {
    if (ws.readyState === WebSocket.CLOSED) {
      resolve();
      return;
    }
    const timer = setTimeout(
      () => reject(new Error("close timeout")),
      timeoutMs
    );
    ws.once("close", () => {
      clearTimeout(timer);
      resolve();
    });
  });
}

// ── main ─────────────────────────────────────────────────────────────────────

async function main() {
  // 1. Token generation
  const tokenRes = await client.getClientAccessToken({ userId: "user1" });
  if (!tokenRes.url) {
    fail("token generated for user1");
    return;
  }
  pass("token generated for user1");

  // 2. WebSocket connection (named user)
  const { ws: ws1, connectionId: connId1 } = await connectWS(tokenRes.url);
  pass(`user1 connected (connectionId: ${connId1})`);

  // 3. Anonymous connection
  const anonToken = await client.getClientAccessToken();
  const { ws: ws2, connectionId: connId2 } = await connectWS(anonToken.url);
  pass(`anonymous connected (connectionId: ${connId2})`);

  // 4. sendToAll — both clients receive
  const BROADCAST = "hello-broadcast";
  const p1 = waitForMessage(ws1, (_, raw) => raw === BROADCAST);
  const p2 = waitForMessage(ws2, (_, raw) => raw === BROADCAST);
  await client.sendToAll(BROADCAST, { contentType: "text/plain" });

  await p1.then(() => pass("sendToAll: user1 received message")).catch(() => fail("sendToAll: user1 received message"));
  await p2.then(() => pass("sendToAll: anon received message")).catch(() => fail("sendToAll: anon received message"));

  // 5. sendToConnection — only target receives
  const TARGET_MSG = "hello-target";
  const pTarget = waitForMessage(ws1, (_, raw) => raw === TARGET_MSG);
  const pOther = assertNoMessage(ws2, (_, raw) => raw === TARGET_MSG);
  await client.sendToConnection(connId1, TARGET_MSG, {
    contentType: "text/plain",
  });
  await pTarget
    .then(() => pass("sendToConnection: target received message"))
    .catch(() => fail("sendToConnection: target received message"));
  await pOther
    .then(() => pass("sendToConnection: other did NOT receive message"))
    .catch(() => fail("sendToConnection: other did NOT receive message"));

  // 6. sendToUser — only user1's connection receives
  const USER_MSG = "hello-user1";
  const pUser = waitForMessage(ws1, (_, raw) => raw === USER_MSG);
  const pAnonSkip = assertNoMessage(ws2, (_, raw) => raw === USER_MSG);
  await client.sendToUser("user1", USER_MSG, { contentType: "text/plain" });
  await pUser
    .then(() => pass("sendToUser: user1 received message"))
    .catch(() => fail("sendToUser: user1 received message"));
  await pAnonSkip
    .then(() => pass("sendToUser: anon did NOT receive message"))
    .catch(() => fail("sendToUser: anon did NOT receive message"));

  // 7. Group management
  const GROUP = "room1";
  const GROUP_MSG = "hello-group";
  const group = client.group(GROUP);
  await group.addConnection(connId1);

  const pMember = waitForMessage(ws1, (_, raw) => raw === GROUP_MSG);
  const pNonMember = assertNoMessage(ws2, (_, raw) => raw === GROUP_MSG);
  await group.sendToAll(GROUP_MSG, { contentType: "text/plain" });
  await pMember
    .then(() => pass("group send: member received message"))
    .catch(() => fail("group send: member received message"));
  await pNonMember
    .then(() => pass("group send: non-member did NOT receive message"))
    .catch(() => fail("group send: non-member did NOT receive message"));

  // 8. connectionExists — live connection
  if (await client.connectionExists(connId1)) {
    pass("connectionExists: true for live connection");
  } else {
    fail("connectionExists: true for live connection");
  }

  // connectionExists — after close (close ws2 first to keep connId1 clean for step 10)
  ws2.close();
  await waitForClose(ws2);
  // Give the emulator a moment to remove the connection
  await new Promise((r) => setTimeout(r, 300));
  if (!await client.connectionExists(connId2)) {
    pass("connectionExists: false after close");
  } else {
    fail("connectionExists: false after close");
  }

  // 9. groupExists
  if (await client.groupExists(GROUP)) {
    pass("groupExists: true for non-empty group");
  } else {
    fail("groupExists: true for non-empty group");
  }

  // 10. closeConnection — server closes ws1
  const pClose = waitForClose(ws1);
  await client.closeConnection(connId1);
  await pClose
    .then(() => pass("closeConnection: WebSocket closed by server"))
    .catch(() => fail("closeConnection: WebSocket closed by server"));
}

main().catch((err) => {
  console.error("Unhandled error:", err);
  process.exit(1);
});
