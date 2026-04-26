// Package ca implements the Praetor certificate authority for mTLS agent identity.
package ca

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	rootKeyFile  = "root.key"
	rootCertFile = "root.crt"
	srvKeyFile   = "server.key"
	srvCertFile  = "server.crt"
)

// CA holds the root keypair and signed server certificate.
type CA struct {
	rootKey   ed25519.PrivateKey
	rootCert  *x509.Certificate
	rootPEM   []byte
	serverTLS tls.Certificate
	logger    *slog.Logger
}

// New loads existing CA material from dataDir/ca/ or generates fresh material.
func New(dataDir string, logger *slog.Logger, dnsNames []string) (*CA, error) {
	dir := filepath.Join(dataDir, "ca")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("ca: mkdir %s: %w", dir, err)
	}

	rootKeyPath := filepath.Join(dir, rootKeyFile)
	if _, err := os.Stat(rootKeyPath); os.IsNotExist(err) {
		return generate(dir, dnsNames, logger)
	}
	return load(dir, logger)
}

func generate(dir string, dnsNames []string, logger *slog.Logger) (*CA, error) {
	// Root keypair
	rootPub, rootPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ca: generate root key: %w", err)
	}

	rootSerial, err := randomSerial()
	if err != nil {
		return nil, err
	}

	rootTmpl := &x509.Certificate{
		SerialNumber: rootSerial,
		Subject: pkix.Name{
			CommonName:   "Praetor Root CA",
			Organization: []string{"Praetor"},
		},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}

	rootDER, err := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, rootPub, rootPriv)
	if err != nil {
		return nil, fmt.Errorf("ca: create root cert: %w", err)
	}
	rootCert, err := x509.ParseCertificate(rootDER)
	if err != nil {
		return nil, fmt.Errorf("ca: parse root cert: %w", err)
	}

	// Server keypair
	srvPub, srvPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ca: generate server key: %w", err)
	}

	srvSerial, err := randomSerial()
	if err != nil {
		return nil, err
	}

	var certDNSNames []string
	var certIPAddrs []net.IP
	certIPAddrs = append(certIPAddrs, net.ParseIP("127.0.0.1")) // always include loopback

	for _, name := range dnsNames {
		if ip := net.ParseIP(name); ip != nil {
			certIPAddrs = append(certIPAddrs, ip)
		} else {
			certDNSNames = append(certDNSNames, name)
		}
	}

	srvTmpl := &x509.Certificate{
		SerialNumber: srvSerial,
		Subject: pkix.Name{
			CommonName:   "praetor-server",
			Organization: []string{"Praetor"},
		},
		DNSNames:    certDNSNames,
		IPAddresses: certIPAddrs,
		NotBefore:   time.Now().Add(-time.Minute),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	srvDER, err := x509.CreateCertificate(rand.Reader, srvTmpl, rootCert, srvPub, rootPriv)
	if err != nil {
		return nil, fmt.Errorf("ca: create server cert: %w", err)
	}

	rootPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})
	rootKeyPEM := marshalEd25519PrivKey(rootPriv)
	srvCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srvDER})
	srvKeyPEM := marshalEd25519PrivKey(srvPriv)

	if err := writeFile(filepath.Join(dir, rootKeyFile), rootKeyPEM, 0400); err != nil {
		return nil, err
	}
	if err := writeFile(filepath.Join(dir, rootCertFile), rootPEM, 0444); err != nil {
		return nil, err
	}
	if err := writeFile(filepath.Join(dir, srvKeyFile), srvKeyPEM, 0400); err != nil {
		return nil, err
	}
	if err := writeFile(filepath.Join(dir, srvCertFile), srvCertPEM, 0444); err != nil {
		return nil, err
	}

	serverTLS, err := tls.X509KeyPair(srvCertPEM, srvKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("ca: build server TLS cert: %w", err)
	}

	fp := sha256.Sum256(rootDER)
	logger.Info("CA initialized (generated)", "root_fingerprint", fmt.Sprintf("%x", fp))

	return &CA{
		rootKey:   rootPriv,
		rootCert:  rootCert,
		rootPEM:   rootPEM,
		serverTLS: serverTLS,
		logger:    logger,
	}, nil
}

func load(dir string, logger *slog.Logger) (*CA, error) {
	rootKeyPEM, err := os.ReadFile(filepath.Join(dir, rootKeyFile))
	if err != nil {
		return nil, fmt.Errorf("ca: read root key: %w", err)
	}
	rootCertPEM, err := os.ReadFile(filepath.Join(dir, rootCertFile))
	if err != nil {
		return nil, fmt.Errorf("ca: read root cert: %w", err)
	}
	srvCertPEM, err := os.ReadFile(filepath.Join(dir, srvCertFile))
	if err != nil {
		return nil, fmt.Errorf("ca: read server cert: %w", err)
	}
	srvKeyPEM, err := os.ReadFile(filepath.Join(dir, srvKeyFile))
	if err != nil {
		return nil, fmt.Errorf("ca: read server key: %w", err)
	}

	block, _ := pem.Decode(rootKeyPEM)
	if block == nil {
		return nil, fmt.Errorf("ca: decode root key PEM")
	}
	rootPriv, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ca: parse root key: %w", err)
	}
	edPriv, ok := rootPriv.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("ca: root key is not Ed25519")
	}

	block, _ = pem.Decode(rootCertPEM)
	if block == nil {
		return nil, fmt.Errorf("ca: decode root cert PEM")
	}
	rootCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ca: parse root cert: %w", err)
	}

	serverTLS, err := tls.X509KeyPair(srvCertPEM, srvKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("ca: load server TLS cert: %w", err)
	}

	fp := sha256.Sum256(rootCert.Raw)
	logger.Info("CA initialized (loaded)", "root_fingerprint", fmt.Sprintf("%x", fp))

	return &CA{
		rootKey:   edPriv,
		rootCert:  rootCert,
		rootPEM:   rootCertPEM,
		serverTLS: serverTLS,
		logger:    logger,
	}, nil
}

// IssueClient signs a client certificate using the public key from the CSR.
// The CN of the issued cert is always set to hostID (CSR Subject is ignored).
// Only Ed25519 and P-256 public keys are accepted.
func (c *CA) IssueClient(csrPEM []byte, hostID string, ttl time.Duration) ([]byte, error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, fmt.Errorf("ca: invalid CSR PEM")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ca: parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("ca: CSR signature invalid: %w", err)
	}

	// Validate key type
	switch pub := csr.PublicKey.(type) {
	case ed25519.PublicKey:
		_ = pub // ok
	case *ecdsa.PublicKey:
		if pub.Curve.Params().Name != "P-256" {
			return nil, fmt.Errorf("ca: ECDSA key must use P-256, got %s", pub.Curve.Params().Name)
		}
	default:
		return nil, fmt.Errorf("ca: unsupported key type %T (only Ed25519 and P-256 allowed)", csr.PublicKey)
	}

	// No CA basic constraints allowed
	for _, ext := range csr.Extensions {
		if ext.Id.String() == "2.5.29.19" && ext.Critical {
			return nil, fmt.Errorf("ca: CSR must not have critical BasicConstraints")
		}
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:         hostID,
			OrganizationalUnit: []string{"praetor-agent"},
		},
		NotBefore:   time.Now().Add(-time.Minute),
		NotAfter:    time.Now().Add(ttl),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, c.rootCert, csr.PublicKey, c.rootKey)
	if err != nil {
		return nil, fmt.Errorf("ca: sign client cert: %w", err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}), nil
}

// ServerTLSConfig returns a *tls.Config for the gRPC server.
// ClientAuth is set to VerifyClientCertIfGiven so Enroll works without a cert.
// All other RPCs should enforce mTLS via an interceptor.
func (c *CA) ServerTLSConfig() *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(c.rootCert)
	return &tls.Config{
		Certificates: []tls.Certificate{c.serverTLS},
		ClientAuth:   tls.VerifyClientCertIfGiven,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS13,
	}
}

// RootBundlePEM returns the root CA certificate in PEM format.
func (c *CA) RootBundlePEM() []byte {
	return c.rootPEM
}

func randomSerial() (*big.Int, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("ca: random serial: %w", err)
	}
	return new(big.Int).SetBytes(b), nil
}

func marshalEd25519PrivKey(priv ed25519.PrivateKey) []byte {
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.WriteFile(path, data, mode); err != nil {
		return fmt.Errorf("ca: write %s: %w", path, err)
	}
	// Verify the mode was applied (defense in depth)
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("ca: stat %s: %w", path, err)
	}
	if info.Mode().Perm() != mode {
		return fmt.Errorf("ca: %s has mode %o, want %o", path, info.Mode().Perm(), mode)
	}
	return nil
}
