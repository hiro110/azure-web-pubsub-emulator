// Azure Web PubSub Emulator – C# compatibility check
//
// Tests the full client lifecycle against the running emulator.
// Run after starting the emulator on port 7290.

using System.Net.WebSockets;
using System.Text;
using System.Text.Json;
using System.Threading.Channels;
using Azure;
using Azure.Core;
using Azure.Messaging.WebPubSub;

const string ConnectionString = "Endpoint=http://localhost:7290;AccessKey=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=;Version=1.0;";
const string Hub = "hub1";
const string Group = "room1";

bool failed = false;

void Pass(string msg) => Console.WriteLine($"✓ {msg}");
void Fail(string msg) { failed = true; Console.Error.WriteLine($"✗ {msg}"); }

var client = new WebPubSubServiceClient(ConnectionString, Hub);

// 1. Token generation
var tokenUri = await client.GetClientAccessUriAsync(
    expiresAt: DateTimeOffset.UtcNow.AddHours(1),
    userId: "user1");
if (tokenUri == null)
{
    Fail("token generated for user1");
    Environment.Exit(1);
}
Pass("token generated for user1");

// 2. WebSocket connection (named user)
var (ws1, ch1, connId1) = await ConnectWS("user1");
Pass($"user1 connected (connectionId: {connId1})");

// 3. Anonymous connection
var (ws2, ch2, connId2) = await ConnectWS(null);
Pass($"anonymous connected (connectionId: {connId2})");

// 4. sendToAll — both clients receive
const string Broadcast = "hello-broadcast";
var r1Task = WaitForMessage(ch1, raw => raw == Broadcast, TimeSpan.FromSeconds(2));
var r2Task = WaitForMessage(ch2, raw => raw == Broadcast, TimeSpan.FromSeconds(2));
await client.SendToAllAsync(RequestContent.Create(Broadcast), new ContentType("text/plain"));
bool r1 = await r1Task, r2 = await r2Task;
if (r1) Pass("sendToAll: user1 received message"); else Fail("sendToAll: user1 received message");
if (r2) Pass("sendToAll: anon received message"); else Fail("sendToAll: anon received message");

// 5. sendToConnection — only target receives
const string TargetMsg = "hello-target";
var rtTask = WaitForMessage(ch1, raw => raw == TargetMsg, TimeSpan.FromSeconds(2));
var roTask = AssertNoMessage(ch2, raw => raw == TargetMsg, TimeSpan.FromMilliseconds(500));
await client.SendToConnectionAsync(connId1, RequestContent.Create(TargetMsg), new ContentType("text/plain"));
bool rt = await rtTask, ro = await roTask;
if (rt) Pass("sendToConnection: target received message"); else Fail("sendToConnection: target received message");
if (ro) Pass("sendToConnection: other did NOT receive message"); else Fail("sendToConnection: other did NOT receive message");

// 6. sendToUser — only user1's connection receives
const string UserMsg = "hello-user1";
var ruTask = WaitForMessage(ch1, raw => raw == UserMsg, TimeSpan.FromSeconds(2));
var raTask = AssertNoMessage(ch2, raw => raw == UserMsg, TimeSpan.FromMilliseconds(500));
await client.SendToUserAsync("user1", RequestContent.Create(UserMsg), new ContentType("text/plain"));
bool ru = await ruTask, ra = await raTask;
if (ru) Pass("sendToUser: user1 received message"); else Fail("sendToUser: user1 received message");
if (ra) Pass("sendToUser: anon did NOT receive message"); else Fail("sendToUser: anon did NOT receive message");

// 7. Group management — add connId1 to group, send, verify only member receives
const string GroupMsg = "hello-group";
await client.AddConnectionToGroupAsync(Group, connId1);
var rmTask = WaitForMessage(ch1, raw => raw == GroupMsg, TimeSpan.FromSeconds(2));
var rnTask = AssertNoMessage(ch2, raw => raw == GroupMsg, TimeSpan.FromMilliseconds(500));
await client.SendToGroupAsync(Group, RequestContent.Create(GroupMsg), new ContentType("text/plain"));
bool rm = await rmTask, rn = await rnTask;
if (rm) Pass("group send: member received message"); else Fail("group send: member received message");
if (rn) Pass("group send: non-member did NOT receive message"); else Fail("group send: non-member did NOT receive message");

// 8. connectionExists — live connection
var existsResp = await client.ConnectionExistsAsync(connId1, new RequestContext { ErrorOptions = ErrorOptions.NoThrow });
if (existsResp.Value) Pass("connectionExists: true for live connection"); else Fail("connectionExists: true for live connection");

// connectionExists — after close
await ws2.CloseAsync(WebSocketCloseStatus.NormalClosure, "", CancellationToken.None);
await Task.Delay(300);
var existsAfterCloseResp = await client.ConnectionExistsAsync(connId2, new RequestContext { ErrorOptions = ErrorOptions.NoThrow });
if (!existsAfterCloseResp.Value) Pass("connectionExists: false after close"); else Fail("connectionExists: false after close");

// 9. groupExists
var groupExistsResp = await client.GroupExistsAsync(Group, new RequestContext { ErrorOptions = ErrorOptions.NoThrow });
if (groupExistsResp.Value) Pass("groupExists: true for non-empty group"); else Fail("groupExists: true for non-empty group");

// 10. closeConnection — server closes ws1
var closedTask = WaitForClose(ch1, TimeSpan.FromSeconds(2));
await client.CloseConnectionAsync(connId1, reason: null);
bool closed = await closedTask;
if (closed) Pass("closeConnection: WebSocket closed by server"); else Fail("closeConnection: WebSocket closed by server");

if (failed) Environment.Exit(1);

// ── Helpers ─────────────────────────────────────────────────────────────────

// Each WebSocket has a background reader draining into a channel.
// null in the channel signals the connection was closed.
async Task<(ClientWebSocket ws, Channel<string?> ch, string connectionId)> ConnectWS(string? userId)
{
    Uri wsUri = userId != null
        ? await client.GetClientAccessUriAsync(DateTimeOffset.UtcNow.AddHours(1), userId)
        : await client.GetClientAccessUriAsync(DateTimeOffset.UtcNow.AddHours(1));

    var ws = new ClientWebSocket();
    await ws.ConnectAsync(wsUri, CancellationToken.None);

    // Read the first system.connected message to get connectionId
    var buf = new byte[4096];
    var result = await ws.ReceiveAsync(buf, CancellationToken.None);
    var json = Encoding.UTF8.GetString(buf, 0, result.Count);
    using var doc = JsonDocument.Parse(json);
    var connectionId = doc.RootElement.GetProperty("connectionId").GetString()!;

    // Start background reader
    var ch = Channel.CreateUnbounded<string?>();
    _ = Task.Run(async () =>
    {
        var readBuf = new byte[4096];
        try
        {
            while (true)
            {
                var r = await ws.ReceiveAsync(readBuf, CancellationToken.None);
                if (r.MessageType == WebSocketMessageType.Close)
                {
                    ch.Writer.TryWrite(null);
                    break;
                }
                ch.Writer.TryWrite(Encoding.UTF8.GetString(readBuf, 0, r.Count));
            }
        }
        catch (Exception)
        {
            ch.Writer.TryWrite(null);
        }
        ch.Writer.TryComplete();
    });

    return (ws, ch, connectionId);
}

async Task<bool> WaitForMessage(Channel<string?> ch, Func<string, bool> pred, TimeSpan timeout)
{
    using var cts = new CancellationTokenSource(timeout);
    try
    {
        await foreach (var msg in ch.Reader.ReadAllAsync(cts.Token))
        {
            if (msg == null) return false; // connection closed
            if (pred(msg)) return true;
        }
    }
    catch (OperationCanceledException) { }
    return false;
}

async Task<bool> AssertNoMessage(Channel<string?> ch, Func<string, bool> pred, TimeSpan timeout)
{
    using var cts = new CancellationTokenSource(timeout);
    try
    {
        await foreach (var msg in ch.Reader.ReadAllAsync(cts.Token))
        {
            if (msg == null) return true; // connection closed = no message
            if (pred(msg)) return false;
        }
    }
    catch (OperationCanceledException) { }
    return true;
}

async Task<bool> WaitForClose(Channel<string?> ch, TimeSpan timeout)
{
    using var cts = new CancellationTokenSource(timeout);
    try
    {
        await foreach (var msg in ch.Reader.ReadAllAsync(cts.Token))
        {
            if (msg == null) return true; // connection closed
        }
    }
    catch (OperationCanceledException) { }
    return false;
}
