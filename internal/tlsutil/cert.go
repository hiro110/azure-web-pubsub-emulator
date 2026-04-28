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

// GenerateSelfSigned generates an ephemeral two-entity PKI:
//   - a self-signed CA certificate (caCertPEM) — write to disk and add to
//     NODE_EXTRA_CA_CERTS / SSL_CERT_FILE so clients trust the emulator
//   - a leaf server certificate (certPEM) + private key (keyPEM) — use these
//     for the TLS listener; IsCA=false, not capable of signing other certs
//
// hosts contains DNS names or IP literals for the leaf cert's SANs.
func GenerateSelfSigned(hosts []string) (caCertPEM, certPEM, keyPEM []byte, err error) {
	if len(hosts) == 0 {
		return nil, nil, nil, fmt.Errorf("at least one host is required")
	}

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generating CA key: %w", err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generating leaf key: %w", err)
	}

	caSerial, err := randSerial()
	if err != nil {
		return nil, nil, nil, err
	}
	leafSerial, err := randSerial()
	if err != nil {
		return nil, nil, nil, err
	}

	now := time.Now()
	validity := 30 * 24 * time.Hour

	caTmpl := &x509.Certificate{
		SerialNumber: caSerial,
		Subject: pkix.Name{
			Organization: []string{"Azure Web PubSub Emulator CA"},
			CommonName:   "Azure Web PubSub Emulator CA",
		},
		NotBefore:             now.Add(-1 * time.Minute),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("creating CA certificate: %w", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parsing CA certificate: %w", err)
	}

	leafTmpl := &x509.Certificate{
		SerialNumber: leafSerial,
		Subject: pkix.Name{
			Organization: []string{"Azure Web PubSub Emulator"},
			CommonName:   hosts[0],
		},
		NotBefore:             now.Add(-1 * time.Minute),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
	}

	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			leafTmpl.IPAddresses = append(leafTmpl.IPAddresses, ip)
		} else {
			leafTmpl.DNSNames = append(leafTmpl.DNSNames, h)
		}
	}

	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("creating leaf certificate: %w", err)
	}

	caCertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})

	leafKeyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("marshaling leaf key: %w", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: leafKeyDER})

	return caCertPEM, certPEM, keyPEM, nil
}

func randSerial() (*big.Int, error) {
	s, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generating serial: %w", err)
	}
	return s, nil
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
