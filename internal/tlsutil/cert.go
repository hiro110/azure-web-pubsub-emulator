package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"
)

// GenerateSelfSigned creates an ephemeral self-signed CA certificate for the
// given hosts (DNS names or IP addresses). Returns PEM-encoded cert and key.
func GenerateSelfSigned(hosts []string) (certPEM, keyPEM []byte, err error) {
	if len(hosts) == 0 {
		return nil, nil, fmt.Errorf("at least one host is required")
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generating key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("generating serial: %w", err)
	}

	now := time.Now()
	// Dev-only pattern: single cert acts as both self-signed CA (trust anchor)
	// and server leaf. This lets NODE_EXTRA_CA_CERTS point to this file without
	// a separate CA chain. KeyUsageKeyEncipherment is included for RSA-compatible
	// clients even though we use ECDSA.
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"Azure Web PubSub Emulator"},
			CommonName:   hosts[0],
		},
		NotBefore:             now.Add(-1 * time.Minute),
		NotAfter:              now.Add(30 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("creating certificate: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshaling key: %w", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM, nil
}

// DecodePEMBlock decodes the first PEM block from data and returns the DER
// bytes along with any remaining data. Exported for use in tests.
func DecodePEMBlock(data []byte) ([]byte, []byte) {
	block, rest := pem.Decode(data)
	if block == nil {
		return nil, rest
	}
	return block.Bytes, rest
}
