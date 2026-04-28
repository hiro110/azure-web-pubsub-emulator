package tlsutil_test

import (
	"crypto/tls"
	"crypto/x509"
	"testing"
	"time"

	"github.com/hiro110/azure-web-pubsub-emulator/internal/tlsutil"
)

func TestGenerateSelfSigned_ReturnsPEM(t *testing.T) {
	caCertPEM, certPEM, keyPEM, err := tlsutil.GenerateSelfSigned([]string{"localhost", "127.0.0.1"})
	if err != nil {
		t.Fatalf("GenerateSelfSigned: %v", err)
	}
	if len(caCertPEM) == 0 {
		t.Error("caCertPEM should not be empty")
	}
	if len(certPEM) == 0 {
		t.Error("certPEM should not be empty")
	}
	if len(keyPEM) == 0 {
		t.Error("keyPEM should not be empty")
	}
}

func TestGenerateSelfSigned_ValidTLSPair(t *testing.T) {
	_, certPEM, keyPEM, err := tlsutil.GenerateSelfSigned([]string{"localhost"})
	if err != nil {
		t.Fatalf("GenerateSelfSigned: %v", err)
	}

	_, err = tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
}

func TestGenerateSelfSigned_LeafSANs(t *testing.T) {
	hosts := []string{"localhost", "127.0.0.1", "example.local"}
	_, certPEM, _, err := tlsutil.GenerateSelfSigned(hosts)
	if err != nil {
		t.Fatalf("GenerateSelfSigned: %v", err)
	}

	block, _ := tlsutil.DecodePEMBlock(certPEM)
	if block == nil {
		t.Fatal("failed to decode leaf cert PEM block")
	}

	cert, err := x509.ParseCertificate(block)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}

	sanDNS := map[string]bool{}
	for _, n := range cert.DNSNames {
		sanDNS[n] = true
	}
	sanIP := map[string]bool{}
	for _, ip := range cert.IPAddresses {
		sanIP[ip.String()] = true
	}

	if !sanDNS["localhost"] {
		t.Error("expected SAN DNS: localhost")
	}
	if !sanIP["127.0.0.1"] {
		t.Error("expected SAN IP: 127.0.0.1")
	}
	if !sanDNS["example.local"] {
		t.Error("expected SAN DNS: example.local")
	}
}

func TestGenerateSelfSigned_Validity(t *testing.T) {
	_, certPEM, _, err := tlsutil.GenerateSelfSigned([]string{"localhost"})
	if err != nil {
		t.Fatalf("GenerateSelfSigned: %v", err)
	}

	block, _ := tlsutil.DecodePEMBlock(certPEM)
	cert, err := x509.ParseCertificate(block)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}

	now := time.Now()
	if cert.NotBefore.After(now) {
		t.Error("NotBefore should be in the past")
	}
	if cert.NotAfter.Before(now.Add(29 * 24 * time.Hour)) {
		t.Error("NotAfter should be at least 29 days from now")
	}
}

func TestGenerateSelfSigned_LeafIsNotCA(t *testing.T) {
	_, certPEM, _, err := tlsutil.GenerateSelfSigned([]string{"localhost"})
	if err != nil {
		t.Fatalf("GenerateSelfSigned: %v", err)
	}

	block, _ := tlsutil.DecodePEMBlock(certPEM)
	cert, err := x509.ParseCertificate(block)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}

	if cert.IsCA {
		t.Error("leaf cert should NOT be a CA cert")
	}
}

func TestGenerateSelfSigned_CAIsCA(t *testing.T) {
	caCertPEM, _, _, err := tlsutil.GenerateSelfSigned([]string{"localhost"})
	if err != nil {
		t.Fatalf("GenerateSelfSigned: %v", err)
	}

	block, _ := tlsutil.DecodePEMBlock(caCertPEM)
	cert, err := x509.ParseCertificate(block)
	if err != nil {
		t.Fatalf("ParseCertificate (CA): %v", err)
	}

	if !cert.IsCA {
		t.Error("CA cert should have IsCA=true")
	}
}

func TestGenerateSelfSigned_CASignsLeaf(t *testing.T) {
	caCertPEM, certPEM, _, err := tlsutil.GenerateSelfSigned([]string{"localhost"})
	if err != nil {
		t.Fatalf("GenerateSelfSigned: %v", err)
	}

	caBlock, _ := tlsutil.DecodePEMBlock(caCertPEM)
	caCert, err := x509.ParseCertificate(caBlock)
	if err != nil {
		t.Fatalf("ParseCertificate (CA): %v", err)
	}

	leafBlock, _ := tlsutil.DecodePEMBlock(certPEM)
	leafCert, err := x509.ParseCertificate(leafBlock)
	if err != nil {
		t.Fatalf("ParseCertificate (leaf): %v", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	_, err = leafCert.Verify(x509.VerifyOptions{
		Roots:     pool,
		DNSName:   "localhost",
	})
	if err != nil {
		t.Errorf("leaf cert does not verify against CA: %v", err)
	}
}

func TestGenerateSelfSigned_EmptyHosts(t *testing.T) {
	_, _, _, err := tlsutil.GenerateSelfSigned([]string{})
	if err == nil {
		t.Error("expected error for empty hosts, got nil")
	}
}
