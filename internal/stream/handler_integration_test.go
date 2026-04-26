//go:build integration

package stream_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	praetorv1 "github.com/ondrejsindelka/praetor-proto/gen/go/praetor/v1"
	internalca "github.com/ondrejsindelka/praetor-server/internal/ca"
	"github.com/ondrejsindelka/praetor-server/internal/db"
	"github.com/ondrejsindelka/praetor-server/internal/db/store"
	"github.com/ondrejsindelka/praetor-server/internal/enrollment"
	"github.com/ondrejsindelka/praetor-server/internal/stream"
	"github.com/ondrejsindelka/praetor-server/internal/token"
)

func integTestDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("TEST_POSTGRES_DSN"); dsn != "" {
		return dsn
	}
	if dsn := os.Getenv("PRAETOR_TEST_DSN"); dsn != "" {
		return dsn
	}
	return "postgres://praetor:praetor@localhost:5432/praetor?sslmode=disable"
}

// localAgentService composes enrollment + stream into one AgentServiceServer for tests.
type localAgentService struct {
	praetorv1.UnimplementedAgentServiceServer
	enroll  *enrollment.Service
	connect *stream.Handler
}

func (s *localAgentService) Enroll(ctx context.Context, req *praetorv1.EnrollRequest) (*praetorv1.EnrollResponse, error) {
	return s.enroll.Enroll(ctx, req)
}

func (s *localAgentService) Connect(st praetorv1.AgentService_ConnectServer) error {
	return s.connect.Connect(st)
}

// startTestServer starts an in-process gRPC server backed by a real Postgres DB.
// Returns the listening address, the CA instance (for building client TLS), and a cleanup func.
func startTestServer(t *testing.T) (addr string, testCA *internalca.CA, cleanup func()) {
	t.Helper()
	ctx := context.Background()

	pool, err := db.Connect(ctx, integTestDSN(t))
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	if err := db.Migrate(ctx, pool, db.Migrations); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}

	testCA, err = internalca.New(t.TempDir(), slog.Default(), []string{"localhost"})
	if err != nil {
		t.Fatalf("ca.New: %v", err)
	}

	reg := stream.NewRegistry()
	hosts := store.NewHostStore(pool)
	connectHandler := stream.NewHandler(reg, hosts, slog.Default())
	enrollSvc := enrollment.New(pool, testCA, slog.Default())

	svc := &localAgentService{enroll: enrollSvc, connect: connectHandler}

	// ServerTLSConfig uses VerifyClientCertIfGiven so Enroll works without a cert.
	grpcSrv := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(testCA.ServerTLSConfig())),
	)
	praetorv1.RegisterAgentServiceServer(grpcSrv, svc)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	go func() { _ = grpcSrv.Serve(lis) }()

	return lis.Addr().String(), testCA, func() {
		grpcSrv.Stop()
		pool.Exec(context.Background(), "DELETE FROM agent_identities")
		pool.Exec(context.Background(), "DELETE FROM enrollment_tokens")
		pool.Exec(context.Background(), "DELETE FROM hosts")
		pool.Close()
	}
}

// makeAgentCSR generates an Ed25519 key and CSR. Returns the CSR PEM and a builder
// that creates a *tls.Config from the issued cert PEM and CA bundle PEM.
func makeAgentCSR(t *testing.T) (csrPEM string, buildClientTLS func(certPEM, caBundlePEM string) *tls.Config) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.CertificateRequest{Subject: pkix.Name{}}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, priv)
	if err != nil {
		t.Fatalf("create CSR: %v", err)
	}
	csr := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))

	build := func(certPEM, caBundlePEM string) *tls.Config {
		privDER, _ := x509.MarshalPKCS8PrivateKey(priv)
		keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})
		pair, err := tls.X509KeyPair([]byte(certPEM), keyPEM)
		if err != nil {
			t.Fatalf("X509KeyPair: %v", err)
		}
		caPool := x509.NewCertPool()
		caPool.AppendCertsFromPEM([]byte(caBundlePEM))
		return &tls.Config{
			Certificates: []tls.Certificate{pair},
			RootCAs:      caPool,
			ServerName:   "localhost",
		}
	}
	return csr, build
}

// insertEnrollmentToken inserts a fresh single-use enrollment token and returns the plain value.
func insertEnrollmentToken(t *testing.T, ctx context.Context, dsn string) string {
	t.Helper()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("db for token insert: %v", err)
	}
	defer db.Close(pool)

	tok, err := token.Generate()
	if err != nil {
		t.Fatalf("token.Generate: %v", err)
	}
	label := "integration-test"
	createdBy := "test"
	ts := store.NewTokenStore(pool)
	if err := ts.Insert(ctx, &store.EnrollmentToken{
		ID:        tok.ID,
		TokenHash: fmt.Sprintf("%x", tok.Hash),
		Label:     &label,
		OrgID:     "default",
		CreatedBy: &createdBy,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(15 * time.Minute),
	}); err != nil {
		t.Fatalf("insert enrollment token: %v", err)
	}
	return tok.Plain
}

func TestConnectHeartbeatUpdatesDB(t *testing.T) {
	addr, testCA, cleanup := startTestServer(t)
	defer cleanup()

	ctx := context.Background()
	dsn := integTestDSN(t)

	// ---- Step 1: Enroll to get a real client certificate ----
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(testCA.RootBundlePEM())
	noCert := credentials.NewTLS(&tls.Config{
		RootCAs:    caPool,
		ServerName: "localhost",
	})
	enrollConn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(noCert))
	if err != nil {
		t.Fatalf("dial for enroll: %v", err)
	}
	defer enrollConn.Close()

	plain := insertEnrollmentToken(t, ctx, dsn)
	csrPEM, buildClientTLS := makeAgentCSR(t)

	enrollResp, err := praetorv1.NewAgentServiceClient(enrollConn).Enroll(ctx, &praetorv1.EnrollRequest{
		EnrollmentToken: plain,
		CsrPem:          csrPEM,
		AgentVersion:    "0.0.1-integration",
		HostInfo: &praetorv1.HostInfo{
			Hostname:  "integration-connect-host",
			Os:        "linux",
			Arch:      "amd64",
			MachineId: fmt.Sprintf("test-machine-%d", time.Now().UnixNano()),
		},
	})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	agentID := enrollResp.GetAgentId()
	t.Logf("enrolled agent_id=%s", agentID)

	// ---- Step 2: Open the Connect stream using the issued cert ----
	clientTLS := buildClientTLS(enrollResp.GetCertPem(), enrollResp.GetCaBundlePem())
	connectConn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)))
	if err != nil {
		t.Fatalf("dial for connect: %v", err)
	}
	defer connectConn.Close()

	streamCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	connectStream, err := praetorv1.NewAgentServiceClient(connectConn).Connect(streamCtx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// ---- Step 3: Send a Heartbeat ----
	if err := connectStream.Send(&praetorv1.AgentMessage{
		Payload: &praetorv1.AgentMessage_Heartbeat{
			Heartbeat: &praetorv1.Heartbeat{
				AgentVersion:  "0.0.1-integration",
				UptimeSeconds: 99,
			},
		},
	}); err != nil {
		t.Fatalf("Send heartbeat: %v", err)
	}

	// Give the server time to process the heartbeat.
	time.Sleep(300 * time.Millisecond)

	// ---- Step 4: Verify DB was updated to status='online' ----
	dbPool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("db for verify: %v", err)
	}
	defer db.Close(dbPool)

	hostStore := store.NewHostStore(dbPool)
	h, err := hostStore.GetByID(ctx, agentID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if h.Status != "online" {
		t.Errorf("expected status='online', got %q", h.Status)
	}
	if h.LastHeartbeatAt == nil {
		t.Error("expected last_heartbeat_at to be set after heartbeat")
	} else {
		age := time.Since(*h.LastHeartbeatAt)
		if age > 10*time.Second {
			t.Errorf("last_heartbeat_at is %v old, expected recent", age)
		}
		t.Logf("last_heartbeat_at=%v status=%s", h.LastHeartbeatAt.Format(time.RFC3339), h.Status)
	}
}
