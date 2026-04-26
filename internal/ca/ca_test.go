package ca_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ondrejsindelka/praetor-server/internal/ca"
)

func TestGenerateAndLoad(t *testing.T) {
	dir := t.TempDir()
	logger := testLogger(t)

	c1, err := ca.New(dir, logger, []string{"localhost"})
	if err != nil {
		t.Fatalf("New (generate): %v", err)
	}
	if len(c1.RootBundlePEM()) == 0 {
		t.Fatal("RootBundlePEM is empty")
	}

	// Load from existing dir
	c2, err := ca.New(dir, logger, []string{"localhost"})
	if err != nil {
		t.Fatalf("New (load): %v", err)
	}
	if string(c1.RootBundlePEM()) != string(c2.RootBundlePEM()) {
		t.Fatal("loaded CA has different root cert than generated")
	}

	// Verify root key file has mode 0400
	info, err := os.Stat(dir + "/ca/root.key")
	if err != nil {
		t.Fatalf("stat root.key: %v", err)
	}
	if info.Mode().Perm() != 0400 {
		t.Errorf("root.key mode = %o, want 0400", info.Mode().Perm())
	}
}

func TestIssueClientHappyPath(t *testing.T) {
	c := newTestCA(t)
	hostID := "01HZ000TEST000000001"
	csrPEM := makeCSR(t, hostID)

	certPEM, err := c.IssueClient(csrPEM, hostID, 24*time.Hour)
	if err != nil {
		t.Fatalf("IssueClient: %v", err)
	}

	// Verify the issued cert is valid against the CA's root
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(c.RootBundlePEM())
	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse issued cert: %v", err)
	}
	_, err = cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	if err != nil {
		t.Errorf("cert verify: %v", err)
	}
	if cert.Subject.CommonName != hostID {
		t.Errorf("cert CN = %q, want %q", cert.Subject.CommonName, hostID)
	}
}

func TestIssueClientRejectsMismatchedCN(t *testing.T) {
	// Since IssueClient now ignores CSR CN and uses hostID, a "mismatched CN" in the CSR
	// is actually fine — the server always overrides with hostID. So this test verifies
	// that the issued cert always has CN == hostID, regardless of CSR CN.
	c := newTestCA(t)
	csrPEM := makeCSR(t, "wrong-host-id")
	certPEM, err := c.IssueClient(csrPEM, "correct-host-id", 24*time.Hour)
	if err != nil {
		t.Fatalf("IssueClient: %v", err)
	}
	// The issued cert should have CN == "correct-host-id" (hostID), not "wrong-host-id"
	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	if cert.Subject.CommonName != "correct-host-id" {
		t.Errorf("cert CN = %q, want %q", cert.Subject.CommonName, "correct-host-id")
	}
}

func TestConcurrentIssuance(t *testing.T) {
	c := newTestCA(t)
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			hostID := fmt.Sprintf("host-%02d", n)
			csrPEM := makeCSR(t, hostID)
			if _, err := c.IssueClient(csrPEM, hostID, time.Hour); err != nil {
				t.Errorf("concurrent IssueClient %d: %v", n, err)
			}
		}(i)
	}
	wg.Wait()
}

func TestServerCertSANClassification(t *testing.T) {
	dir := t.TempDir()
	logger := testLogger(t)

	// Mixed: hostnames + IPv4 + IPv6
	dnsNames := []string{"localhost", "10.0.0.5", "example.com", "::1"}
	c, err := ca.New(dir, logger, dnsNames)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Get the server TLS config and inspect the certificate
	tlsCfg := c.ServerTLSConfig()
	if len(tlsCfg.Certificates) == 0 {
		t.Fatal("no certificates in server TLS config")
	}
	leaf, err := x509.ParseCertificate(tlsCfg.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatalf("parse server cert: %v", err)
	}

	// DNS SANs: only hostnames
	wantDNS := map[string]bool{"localhost": true, "example.com": true}
	for _, name := range leaf.DNSNames {
		if !wantDNS[name] {
			t.Errorf("unexpected DNS SAN: %q", name)
		}
		delete(wantDNS, name)
	}
	if len(wantDNS) > 0 {
		t.Errorf("missing DNS SANs: %v", wantDNS)
	}

	// IP SANs: 127.0.0.1 (always added) + 10.0.0.5 + ::1
	wantIPs := map[string]bool{
		"127.0.0.1": true,
		"10.0.0.5":  true,
		"::1":       true,
	}
	for _, ip := range leaf.IPAddresses {
		s := ip.String()
		if !wantIPs[s] {
			t.Errorf("unexpected IP SAN: %s", s)
		}
		delete(wantIPs, s)
	}
	if len(wantIPs) > 0 {
		t.Errorf("missing IP SANs: %v", wantIPs)
	}

	// Verify no IP addresses leaked into DNS SANs
	for _, name := range leaf.DNSNames {
		if net.ParseIP(name) != nil {
			t.Errorf("IP address %q found in DNSNames, should be in IPAddresses", name)
		}
	}
}

// helpers

func newTestCA(t *testing.T) *ca.CA {
	t.Helper()
	c, err := ca.New(t.TempDir(), testLogger(t), []string{"localhost"})
	if err != nil {
		t.Fatalf("new CA: %v", err)
	}
	return c
}

func makeCSR(t *testing.T, cn string) []byte {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: cn},
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, priv)
	if err != nil {
		t.Fatalf("create CSR: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
