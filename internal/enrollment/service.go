// Package enrollment implements the Enroll RPC handler.
package enrollment

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	praetorv1 "github.com/ondrejsindelka/praetor-proto/gen/go/praetor/v1"
	"github.com/ondrejsindelka/praetor-server/internal/ca"
	"github.com/ondrejsindelka/praetor-server/internal/db/store"
	"github.com/ondrejsindelka/praetor-server/internal/token"
)

// Service implements the Enroll RPC.
type Service struct {
	praetorv1.UnimplementedAgentServiceServer
	pool       *pgxpool.Pool
	ca         *ca.CA
	hosts      *store.HostStore
	identities *store.IdentityStore
	tokens     *store.TokenStore
	logger     *slog.Logger
}

// New creates a new enrollment Service.
func New(pool *pgxpool.Pool, caInstance *ca.CA, logger *slog.Logger) *Service {
	return &Service{
		pool:       pool,
		ca:         caInstance,
		hosts:      store.NewHostStore(pool),
		identities: store.NewIdentityStore(pool),
		tokens:     store.NewTokenStore(pool),
		logger:     logger,
	}
}

const clientCertTTL = 24 * time.Hour

// Enroll authenticates the enrollment token, issues a client certificate, and upserts the host.
func (s *Service) Enroll(ctx context.Context, req *praetorv1.EnrollRequest) (*praetorv1.EnrollResponse, error) {
	// 1. Validate request shape
	if req.GetEnrollmentToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "enrollment_token is required")
	}
	if req.GetCsrPem() == "" {
		return nil, status.Error(codes.InvalidArgument, "csr_pem is required")
	}
	if req.GetHostInfo() == nil || req.GetHostInfo().GetHostname() == "" {
		return nil, status.Error(codes.InvalidArgument, "host_info.hostname is required")
	}

	if err := token.ParseAndValidate(req.GetEnrollmentToken()); err != nil {
		return nil, status.Error(codes.InvalidArgument, "malformed enrollment token")
	}

	// 2. Atomically claim the token
	tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(req.GetEnrollmentToken())))

	var tokenID, tokenOrgID string
	row := s.pool.QueryRow(ctx, `
		UPDATE enrollment_tokens
		SET used_at = NOW()
		WHERE token_hash = $1
		  AND used_at IS NULL
		  AND revoked_at IS NULL
		  AND expires_at > NOW()
		RETURNING id, org_id
	`, tokenHash)
	if err := row.Scan(&tokenID, &tokenOrgID); err != nil {
		s.logger.Info("enrollment rejected: token not found or expired", "token_id_prefix", safePrefix(req.GetEnrollmentToken()))
		return nil, status.Error(codes.Unauthenticated, "invalid, expired, or already-used enrollment token")
	}

	// 3. Determine host ID (stable for same machine_id + org)
	hostID, err := s.resolveHostID(ctx, req.GetHostInfo().GetMachineId(), tokenOrgID)
	if err != nil {
		s.logger.Error("enrollment: resolve host ID", "err", err)
		return nil, status.Error(codes.Internal, "internal error")
	}

	// 4. Issue client certificate (server overrides CN with hostID regardless of CSR CN)
	certPEM, err := s.ca.IssueClient([]byte(req.GetCsrPem()), hostID, clientCertTTL)
	if err != nil {
		s.logger.Error("enrollment: issue client cert", "host_id", hostID, "err", err)
		return nil, status.Errorf(codes.InvalidArgument, "CSR rejected: %v", err)
	}

	// 5. Upsert host row
	hi := req.GetHostInfo()
	cpuCores := int32(hi.GetCpuCores())
	memBytes := hi.GetMemoryBytes()
	var machineID *string
	if mid := hi.GetMachineId(); mid != "" {
		machineID = &mid
	}
	var agentVersion *string
	if av := req.GetAgentVersion(); av != "" {
		agentVersion = &av
	}
	var osVersion *string
	if ov := hi.GetOsVersion(); ov != "" {
		osVersion = &ov
	}
	var kernel *string
	if k := hi.GetKernel(); k != "" {
		kernel = &k
	}

	now := time.Now().UTC()
	h := &store.Host{
		ID:              hostID,
		Hostname:        hi.GetHostname(),
		OS:              hi.GetOs(),
		OSVersion:       osVersion,
		Kernel:          kernel,
		Arch:            hi.GetArch(),
		CPUCores:        &cpuCores,
		MemoryBytes:     &memBytes,
		MachineID:       machineID,
		IPAddresses:     hi.GetIpAddresses(),
		Labels:          hi.GetLabels(),
		FirstSeenAt:     now,
		LastHeartbeatAt: &now,
		Status:          "enrolled",
		AgentVersion:    agentVersion,
		OrgID:           tokenOrgID,
	}
	if err := s.hosts.Upsert(ctx, h); err != nil {
		s.logger.Error("enrollment: upsert host", "host_id", hostID, "err", err)
		return nil, status.Error(codes.Internal, "internal error")
	}

	// 6. Update token used_by_host_id now that we have the real host ID
	if _, err := s.pool.Exec(ctx,
		`UPDATE enrollment_tokens SET used_by_host_id = $1 WHERE id = $2`,
		hostID, tokenID,
	); err != nil {
		s.logger.Error("enrollment: update token host ref", "token_id", tokenID, "err", err)
		// Non-fatal: host is enrolled, cert is issued
	}

	// 7. Record the issued identity
	certFP := certFingerprint(certPEM)
	ident := &store.AgentIdentity{
		HostID:          hostID,
		CertPEM:         string(certPEM),
		CertFingerprint: certFP,
		IssuedAt:        now,
		ExpiresAt:       now.Add(clientCertTTL),
	}
	if err := s.identities.Insert(ctx, ident); err != nil {
		s.logger.Error("enrollment: insert identity", "host_id", hostID, "err", err)
		return nil, status.Error(codes.Internal, "internal error")
	}

	s.logger.Info("enrollment complete",
		"host_id", hostID,
		"hostname", hi.GetHostname(),
		"org_id", tokenOrgID,
		"cert_fingerprint", certFP,
	)

	return &praetorv1.EnrollResponse{
		CertPem:                    string(certPEM),
		CaBundlePem:                string(s.ca.RootBundlePEM()),
		AgentId:                    hostID,
		CertRenewalIntervalSeconds: int64((20 * time.Hour).Seconds()),
	}, nil
}

// resolveHostID returns the existing host ID if a host with machine_id+orgID exists,
// or generates a new ULID.
func (s *Service) resolveHostID(ctx context.Context, machineID, orgID string) (string, error) {
	if machineID == "" {
		return ulid.MustNew(ulid.Timestamp(time.Now()), ulid.DefaultEntropy()).String(), nil
	}
	var existing string
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM hosts WHERE machine_id = $1 AND org_id = $2`,
		machineID, orgID,
	).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	// No existing host — generate new ID
	return ulid.MustNew(ulid.Timestamp(time.Now()), ulid.DefaultEntropy()).String(), nil
}

// certFingerprint returns the SHA-256 hex fingerprint of the first cert in a PEM block.
func certFingerprint(certPEM []byte) string {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "invalid-pem"
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "invalid-cert"
	}
	h := sha256.Sum256(cert.Raw)
	return fmt.Sprintf("%x", h)
}

// safePrefix returns the first 12 chars of a token for logging (never the full token).
func safePrefix(tok string) string {
	if len(tok) < 12 {
		return "***"
	}
	return tok[:12] + "..."
}
