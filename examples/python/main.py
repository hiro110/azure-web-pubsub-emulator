"""
Azure Web PubSub Emulator – Python SDK compatibility check

Tests the full client lifecycle against the running emulator.
Run after starting the emulator on port 7290.
"""

import asyncio
import json
import sys

import websockets
from websockets.connection import State
from azure.messaging.webpubsubservice import WebPubSubServiceClient

CONNECTION_STRING = (
    "Endpoint=http://localhost:7290;"
    "AccessKey=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=;"
    "Version=1.0;"
)
HUB = "hub1"

client = WebPubSubServiceClient.from_connection_string(CONNECTION_STRING, hub=HUB)

_failed = False


def pass_(msg: str) -> None:
    print(f"✓ {msg}")


def fail(msg: str) -> None:
    global _failed
    _failed = True
    print(f"✗ {msg}", file=sys.stderr)


# ── helpers ───────────────────────────────────────────────────────────────────


async def connect_ws(token_url: str):
    """
    Connect a WebSocket client and wait for the system.connected message.
    Returns (ws, connection_id, user_id).
    """
    ws = await websockets.connect(token_url)
    async with asyncio.timeout(5):
        raw = await ws.recv()
        msg = json.loads(raw)
        if msg.get("type") == "system" and msg.get("event") == "connected":
            return ws, msg["connectionId"], msg.get("userId")
        raise RuntimeError(f"unexpected first message: {raw}")


async def wait_for_message(ws, predicate, timeout: float = 2.0):
    """
    Wait up to `timeout` seconds for a message matching predicate.
    Predicate receives (parsed_json_or_none, raw_string).
    """
    async with asyncio.timeout(timeout):
        while True:
            raw = await ws.recv()
            raw_str = raw if isinstance(raw, str) else raw.decode()
            msg = None
            try:
                msg = json.loads(raw_str)
            except json.JSONDecodeError:
                pass
            if predicate(msg, raw_str):
                return msg if msg is not None else raw_str


async def assert_no_message(ws, predicate, timeout: float = 0.5) -> bool:
    """
    Assert that NO message matching predicate arrives within `timeout` seconds.
    Predicate receives (parsed_json_or_none, raw_string).
    Returns True if no matching message was received.
    """
    try:
        async with asyncio.timeout(timeout):
            while True:
                raw = await ws.recv()
                raw_str = raw if isinstance(raw, str) else raw.decode()
                msg = None
                try:
                    msg = json.loads(raw_str)
                except json.JSONDecodeError:
                    pass
                if predicate(msg, raw_str):
                    return False
    except TimeoutError:
        return True


async def wait_for_close(ws, timeout: float = 2.0) -> None:
    """Wait for the WebSocket connection to be closed by the server."""
    async with asyncio.timeout(timeout):
        try:
            while True:
                await ws.recv()
        except websockets.ConnectionClosed:
            return


# ── main ──────────────────────────────────────────────────────────────────────


async def main() -> None:
    # 1. Token generation
    token_res = client.get_client_access_token(user_id="user1")
    url1 = token_res.get("url")
    if not url1:
        fail("token generated for user1")
        return
    pass_("token generated for user1")

    # 2. WebSocket connection (named user)
    ws1, conn_id1, _ = await connect_ws(url1)
    pass_(f"user1 connected (connectionId: {conn_id1})")

    # 3. Anonymous connection
    anon_token = client.get_client_access_token()
    ws2, conn_id2, _ = await connect_ws(anon_token["url"])
    pass_(f"anonymous connected (connectionId: {conn_id2})")

    # 4. sendToAll — both clients receive
    broadcast = "hello-broadcast"
    p1 = asyncio.create_task(wait_for_message(ws1, lambda m, r: r == broadcast))
    p2 = asyncio.create_task(wait_for_message(ws2, lambda m, r: r == broadcast))
    client.send_to_all(broadcast, content_type="text/plain")

    try:
        await p1
        pass_("sendToAll: user1 received message")
    except Exception:
        fail("sendToAll: user1 received message")
    try:
        await p2
        pass_("sendToAll: anon received message")
    except Exception:
        fail("sendToAll: anon received message")

    # 5. sendToConnection — only target receives
    target_msg = "hello-target"
    p_target = asyncio.create_task(
        wait_for_message(ws1, lambda m, r: r == target_msg)
    )
    p_other = asyncio.create_task(
        assert_no_message(ws2, lambda m, r: r == target_msg)
    )
    client.send_to_connection(conn_id1, target_msg, content_type="text/plain")

    try:
        await p_target
        pass_("sendToConnection: target received message")
    except Exception:
        fail("sendToConnection: target received message")
    if await p_other:
        pass_("sendToConnection: other did NOT receive message")
    else:
        fail("sendToConnection: other did NOT receive message")

    # 6. sendToUser — only user1's connection receives
    user_msg = "hello-user1"
    p_user = asyncio.create_task(
        wait_for_message(ws1, lambda m, r: r == user_msg)
    )
    p_anon_skip = asyncio.create_task(
        assert_no_message(ws2, lambda m, r: r == user_msg)
    )
    client.send_to_user("user1", user_msg, content_type="text/plain")

    try:
        await p_user
        pass_("sendToUser: user1 received message")
    except Exception:
        fail("sendToUser: user1 received message")
    if await p_anon_skip:
        pass_("sendToUser: anon did NOT receive message")
    else:
        fail("sendToUser: anon did NOT receive message")

    # 7. Group management
    group = "room1"
    group_msg = "hello-group"
    client.add_connection_to_group(group, conn_id1)

    p_member = asyncio.create_task(
        wait_for_message(ws1, lambda m, r: r == group_msg)
    )
    p_non_member = asyncio.create_task(
        assert_no_message(ws2, lambda m, r: r == group_msg)
    )
    client.send_to_group(group, group_msg, content_type="text/plain")

    try:
        await p_member
        pass_("group send: member received message")
    except Exception:
        fail("group send: member received message")
    if await p_non_member:
        pass_("group send: non-member did NOT receive message")
    else:
        fail("group send: non-member did NOT receive message")

    # 8. connectionExists — live connection
    if client.connection_exists(conn_id1):
        pass_("connectionExists: true for live connection")
    else:
        fail("connectionExists: true for live connection")

    # connectionExists — after close (close ws2 first to keep conn_id1 clean)
    await ws2.close()
    await asyncio.sleep(0.3)
    if not client.connection_exists(conn_id2):
        pass_("connectionExists: false after close")
    else:
        fail("connectionExists: false after close")

    # 9. groupExists
    if client.group_exists(group):
        pass_("groupExists: true for non-empty group")
    else:
        fail("groupExists: true for non-empty group")

    # 10. closeConnection — server closes ws1
    p_close = asyncio.create_task(wait_for_close(ws1))
    client.close_connection(conn_id1)
    try:
        await p_close
        pass_("closeConnection: WebSocket closed by server")
    except Exception:
        fail("closeConnection: WebSocket closed by server")

    # Clean up any remaining open connections
    for ws in (ws1, ws2):
        if ws.state != State.CLOSED:
            await ws.close()


if __name__ == "__main__":
    asyncio.run(main())
    if _failed:
        sys.exit(1)
