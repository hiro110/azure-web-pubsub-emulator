package hub

import "sync"

const SendBufferSize = 256

// MessageType mirrors websocket message type constants to avoid a direct
// dependency on a WebSocket library in this package.
const (
	MessageTypeText   = 1
	MessageTypeBinary = 2
)

// Message is an outbound WebSocket message queued for delivery to a client.
type Message struct {
	Type int
	Data []byte
}

// Connection represents a single connected WebSocket client.
//
// Concurrency model:
//   - groups is only read or written while the parent Hub's mutex is held.
//   - mu protects closed, permissions, and the send channel close operation.
type Connection struct {
	ID     string
	UserID string

	send   chan Message
	closed bool
	mu     sync.RWMutex

	// groups this connection belongs to (maintained by Hub under Hub.mu)
	groups map[string]struct{}
	// permissions granted to this connection (maintained by Hub under Hub.mu)
	permissions map[string]struct{}
}

func newConnection(id, userID string) *Connection {
	return &Connection{
		ID:          id,
		UserID:      userID,
		send:        make(chan Message, SendBufferSize),
		groups:      make(map[string]struct{}),
		permissions: make(map[string]struct{}),
	}
}

// Send enqueues a message for delivery. Returns false if the connection is
// closed or the send buffer is full (message is dropped).
func (c *Connection) Send(msg Message) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return false
	}
	select {
	case c.send <- msg:
		return true
	default:
		return false // buffer full — drop
	}
}

// Messages returns the receive-only channel consumed by the WebSocket write loop.
func (c *Connection) Messages() <-chan Message {
	return c.send
}

// Close marks the connection closed and closes the send channel, signalling
// the WebSocket write loop to terminate.
func (c *Connection) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.send)
	}
}

// IsClosed returns true if the connection has been closed.
func (c *Connection) IsClosed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.closed
}
