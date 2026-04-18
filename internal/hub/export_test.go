package hub

// ExportNewConnection exposes the unexported newConnection constructor for tests.
func ExportNewConnection(id, userID string) *Connection {
	return newConnection(id, userID)
}
