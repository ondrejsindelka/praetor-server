//go:build integration

package enrollment_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	praetorv1 "github.com/ondrejsindelka/praetor-proto/gen/go/praetor/v1"
	"github.com/ondrejsindelka/praetor-server/internal/ca"
	"github.com/ondrejsindelka/praetor-server/internal/db"
	"github.com/ondrejsindelka/praetor-server/internal/db/store"
	"github.com/ondrejsindelka/praetor-server/internal/enrollment"
	"github.com/ondrejsindelka/praetor-server/internal/token"
)

func testDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("TEST_POSTGRES_DSN"); dsn != "" {
		return dsn
	}
	return "postgres://praetor:praetor@localhost:5432/praetor?sslmode=disable"
}

func setupSvc(t *testing.T) (*enrollment.Service, func()) {
	t.Helper()
	ctx := context.Background()
	pool, err := db.Connect(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	if err := db.Migrate(ctx, pool, db.Migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	testCA, err := ca.New(t.TempDir(), slog.Default(), []string{"localhost"})
	if err != nil {
		t.Fatalf("ca.New: %v", err)
	}

	svc := enrollment.New(pool, testCA, slog.Default())
	return svc, func() {
		pool.Exec(ctx, "DELETE FROM agent_identities")
		pool.Exec(ctx, "DELETE FROM enrollment_tokens")
		pool.Exec(ctx, "DELETE FROM hosts")
		pool.Close()
	}
}

func insertToken(t *testing.T, ttl time.Duration) (plain string) {
	t.Helper()
	ctx := context.Background()
	pool, err := db.Connect(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("db.Connect for token insert: %v", err)
	}
	defer db.Close(pool)

	tok, err := token.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	exp := time.Now().Add(ttl)
	label := "test"
	createdBy := "test"
	ts := store.NewTokenStore(pool)
	if err := ts.Insert(ctx, &store.EnrollmentToken{
		ID:        tok.ID,
		TokenHash: fmt.Sprintf("%x", tok.Hash),
		Label:     &label,
		OrgID:     "default",
		CreatedBy: &createdBy,
		CreatedAt: time.Now(),
		ExpiresAt: exp,
	}); err != nil {
		t.Fatalf("token insert: %v", err)
	}
	return tok.Plain
}

func makeEmptyCSR(t *testing.T) string {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	tmpl := &x509.CertificateRequest{Subject: pkix.Name{}}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, priv)
	if err != nil {
		t.Fatalf("CreateCertificateRequest: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

func TestEnrollHappyPath(t *testing.T) {
	svc, cleanup := setupSvc(t)
	defer cleanup()

	ctx := context.Background()
	plain := insertToken(t, 15*time.Minute)
	csrPEM := makeEmptyCSR(t)

	resp, err := svc.Enroll(ctx, &praetorv1.EnrollRequest{
		EnrollmentToken: plain,
		CsrPem:          csrPEM,
		AgentVersion:    "0.0.1",
		HostInfo: &praetorv1.HostInfo{
			Hostname:    "test-host",
			Os:          "linux",
			OsVersion:   "Ubuntu 24.04",
			Kernel:      "6.8.0",
			Arch:        "amd64",
			CpuCores:    4,
			MemoryBytes: 8 * 1024 * 1024 * 1024,
			MachineId:   "test-machine-for-enroll-test",
		},
	})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}

	if resp.GetAgentId() == "" {
		t.Error("agent_id is empty")
	}
	if resp.GetCertPem() == "" {
		t.Error("cert_pem is empty")
	}
	if resp.GetCaBundlePem() == "" {
		t.Error("ca_bundle_pem is empty")
	}
	if resp.GetCertRenewalIntervalSeconds() != int64((20*time.Hour).Seconds()) {
		t.Errorf("cert_renewal_interval = %d, want %d", resp.GetCertRenewalIntervalSeconds(), int64((20*time.Hour).Seconds()))
	}
	t.Logf("enrolled agent_id = %s", resp.GetAgentId())
}

func TestEnrollTokenAlreadyUsed(t *testing.T) {
	svc, cleanup := setupSvc(t)
	defer cleanup()

	ctx := context.Background()
	plain := insertToken(t, 15*time.Minute)
	csrPEM := makeEmptyCSR(t)

	req := &praetorv1.EnrollRequest{
		EnrollmentToken: plain, CsrPem: csrPEM, AgentVersion: "0.0.1",
		HostInfo: &praetorv1.HostInfo{Hostname: "host1", Os: "linux"},
	}

	if _, err := svc.Enroll(ctx, req); err != nil {
		t.Fatalf("first Enroll: %v", err)
	}
	_, err := svc.Enroll(ctx, req)
	if err == nil {
		t.Fatal("second Enroll with same token: expected error, got nil")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated, got %v", status.Code(err))
	}
}

func TestEnrollExpiredToken(t *testing.T) {
	svc, cleanup := setupSvc(t)
	defer cleanup()

	ctx := context.Background()
	plain := insertToken(t, -time.Minute) // already expired
	csrPEM := makeEmptyCSR(t)

	_, err := svc.Enroll(ctx, &praetorv1.EnrollRequest{
		EnrollmentToken: plain, CsrPem: csrPEM, AgentVersion: "0.0.1",
		HostInfo: &praetorv1.HostInfo{Hostname: "host2", Os: "linux"},
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated for expired token, got %v", err)
	}
}

func TestEnrollRevokedToken(t *testing.T) {
	svc, cleanup := setupSvc(t)
	defer cleanup()

	ctx := context.Background()
	plain := insertToken(t, 15*time.Minute)

	// Revoke the token directly
	pool, err := db.Connect(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("db.Connect for revoke: %v", err)
	}
	defer db.Close(pool)

	ts := store.NewTokenStore(pool)
	// Find the token by hash and revoke
	tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(plain)))
	var id string
	if err := pool.QueryRow(ctx, "SELECT id FROM enrollment_tokens WHERE token_hash = $1", tokenHash).Scan(&id); err != nil {
		t.Fatalf("find token by hash: %v", err)
	}
	if err := ts.Revoke(ctx, id); err != nil {
		t.Fatalf("revoke token: %v", err)
	}

	csrPEM := makeEmptyCSR(t)
	_, err = svc.Enroll(ctx, &praetorv1.EnrollRequest{
		EnrollmentToken: plain, CsrPem: csrPEM, AgentVersion: "0.0.1",
		HostInfo: &praetorv1.HostInfo{Hostname: "host3", Os: "linux"},
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated for revoked token, got %v", err)
	}
}

// TODO: integration test for re-enrollment flow (cert-based, no token).
// Requires an in-process gRPC server with mTLS configured so that the issued cert
// can be fed back as the transport credential on the second Enroll() call.
// Tracked as part of M6+ work alongside the agent-side rotation logic.

func TestEnrollReEnrollmentSameMachineID(t *testing.T) {
	svc, cleanup := setupSvc(t)
	defer cleanup()

	ctx := context.Background()
	csrPEM := makeEmptyCSR(t)

	baseReq := func(plain string) *praetorv1.EnrollRequest {
		return &praetorv1.EnrollRequest{
			EnrollmentToken: plain, CsrPem: csrPEM, AgentVersion: "0.0.1",
			HostInfo: &praetorv1.HostInfo{
				Hostname:  "same-host", Os: "linux",
				MachineId: "stable-machine-id-for-reenroll-test",
			},
		}
	}

	p1 := insertToken(t, 15*time.Minute)
	r1, err := svc.Enroll(ctx, baseReq(p1))
	if err != nil {
		t.Fatalf("first Enroll: %v", err)
	}

	p2 := insertToken(t, 15*time.Minute)
	r2, err := svc.Enroll(ctx, baseReq(p2))
	if err != nil {
		t.Fatalf("re-Enroll: %v", err)
	}

	if r1.GetAgentId() != r2.GetAgentId() {
		t.Errorf("re-enrollment got different agent_id: %s vs %s", r1.GetAgentId(), r2.GetAgentId())
	}
}
