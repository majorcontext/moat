package proxy

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CA represents a certificate authority for TLS interception.
type CA struct {
	cert    *x509.Certificate
	key     *rsa.PrivateKey
	certPEM []byte
	keyPEM  []byte

	// Cache generated certificates
	certCache map[string]*tls.Certificate
	cacheMu   sync.RWMutex
}

// NewCA creates or loads a CA certificate.
// If the CA files exist at the given path, they are loaded.
// Otherwise, a new CA is generated and saved.
func NewCA(caDir string) (*CA, error) {
	certPath := filepath.Join(caDir, "ca.crt")
	keyPath := filepath.Join(caDir, "ca.key")

	// Try to load existing CA
	if certPEM, err := os.ReadFile(certPath); err == nil {
		if keyPEM, err := os.ReadFile(keyPath); err == nil {
			return loadCA(certPEM, keyPEM)
		}
	}

	// Generate new CA
	ca, err := generateCA()
	if err != nil {
		return nil, err
	}

	// Save CA files
	if err := os.MkdirAll(caDir, 0700); err != nil {
		return nil, fmt.Errorf("creating CA directory: %w", err)
	}
	if err := os.WriteFile(certPath, ca.certPEM, 0644); err != nil {
		return nil, fmt.Errorf("writing CA cert: %w", err)
	}
	if err := os.WriteFile(keyPath, ca.keyPEM, 0600); err != nil {
		return nil, fmt.Errorf("writing CA key: %w", err)
	}

	return ca, nil
}

// loadCA loads a CA from PEM-encoded certificate and key.
func loadCA(certPEM, keyPEM []byte) (*CA, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode CA certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing CA certificate: %w", err)
	}

	block, _ = pem.Decode(keyPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode CA key PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing CA key: %w", err)
	}

	return &CA{
		cert:      cert,
		key:       key,
		certPEM:   certPEM,
		keyPEM:    keyPEM,
		certCache: make(map[string]*tls.Certificate),
	}, nil
}

// generateCA creates a new CA certificate and key.
func generateCA() (*CA, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generating CA key: %w", err)
	}

	// Generate subject key identifier
	pubKeyBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("marshaling public key: %w", err)
	}
	subjectKeyID := sha1.Sum(pubKeyBytes)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Moat"},
			CommonName:   "Moat CA",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0), // 10 years
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		SubjectKeyId:          subjectKeyID[:],
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("creating CA certificate: %w", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, fmt.Errorf("parsing generated certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	return &CA{
		cert:      cert,
		key:       key,
		certPEM:   certPEM,
		keyPEM:    keyPEM,
		certCache: make(map[string]*tls.Certificate),
	}, nil
}

// CertPEM returns the CA certificate in PEM format.
func (ca *CA) CertPEM() []byte {
	return ca.certPEM
}

// GenerateCert creates a certificate for the given host signed by the CA.
func (ca *CA) GenerateCert(host string) (*tls.Certificate, error) {
	// Check cache first
	ca.cacheMu.RLock()
	if cert, ok := ca.certCache[host]; ok {
		ca.cacheMu.RUnlock()
		return cert, nil
	}
	ca.cacheMu.RUnlock()

	// Generate new certificate
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generating key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return nil, fmt.Errorf("generating serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"Moat"},
			CommonName:   host,
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().AddDate(1, 0, 0), // 1 year
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	// Add host as SAN
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, fmt.Errorf("creating certificate: %w", err)
	}

	// Include CA cert in chain so clients can verify the full chain.
	// Some SSL libraries (like Python's) need the issuer cert in the chain
	// even when using a custom CA bundle.
	cert := &tls.Certificate{
		Certificate: [][]byte{certDER, ca.cert.Raw},
		PrivateKey:  key,
	}

	// Cache the certificate
	ca.cacheMu.Lock()
	ca.certCache[host] = cert
	ca.cacheMu.Unlock()

	return cert, nil
}
