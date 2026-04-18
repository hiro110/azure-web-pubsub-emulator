package hub

import (
	"sync"

	"github.com/google/uuid"
)

// Manager is a thread-safe registry of hubs.
type Manager struct {
	mu   sync.RWMutex
	hubs map[string]*Hub
}

// NewManager creates a new Manager.
func NewManager() *Manager {
	return &Manager{
		hubs: make(map[string]*Hub),
	}
}

// GetOrCreate returns the hub with the given name, creating it if necessary.
func (m *Manager) GetOrCreate(name string) *Hub {
	m.mu.RLock()
	h, ok := m.hubs[name]
	m.mu.RUnlock()
	if ok {
		return h
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Double-checked locking
	if h, ok = m.hubs[name]; ok {
		return h
	}
	h = newHub(name)
	m.hubs[name] = h
	return h
}

// Get returns the named hub or nil if it does not exist.
func (m *Manager) Get(name string) *Hub {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.hubs[name]
}

// NewConnection creates a Connection with a new UUID, registers it in the
// named hub, and returns it. The hub is auto-created if needed.
func (m *Manager) NewConnection(hubName, userID string) *Connection {
	conn := newConnection(uuid.New().String(), userID)
	m.GetOrCreate(hubName).AddConnection(conn)
	return conn
}

// NewConnectionWithID creates a Connection with a specific ID, registers it in the
// named hub, and returns it. The hub is auto-created if needed.
// Use this when the connection ID must be known before the upstream webhook fires.
func (m *Manager) NewConnectionWithID(hubName, connID, userID string) *Connection {
	conn := newConnection(connID, userID)
	m.GetOrCreate(hubName).AddConnection(conn)
	return conn
}
