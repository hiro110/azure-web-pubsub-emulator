package server

import "net"

// ExportBuildTLSListener exposes buildTLSListener for integration tests.
func (s *Server) ExportBuildTLSListener(ln net.Listener) (net.Listener, string, error) {
	return s.buildTLSListener(ln)
}

// ExportWriteCAFile exposes writeCAFile for tests.
func ExportWriteCAFile(certPEM []byte) (string, error) {
	return writeCAFile(certPEM)
}
