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
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
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
// Re-enrollment: if the caller presents a valid mTLS client cert in the peer context, the token
// field is optional and skipped — the cert's CN (host ID) is used to look up the org instead.
func (s *Service) Enroll(ctx context.Context, req *praetorv1.EnrollRequest) (*praetorv1.EnrollResponse, error) {
	// 1. Detect re-enrollment: caller already holds a valid mTLS client cert.
	isReEnroll := false
	existingHostID := ""
	if p, ok := peer.FromContext(ctx); ok {
		if tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo); ok {
			if len(tlsInfo.State.VerifiedChains) > 0 && len(tlsInfo.State.VerifiedChains[0]) > 0 {
				existingCert := tlsInfo.State.VerifiedChains[0][0]
				// Only treat as re-enrollment if the cert is still within its validity window.
				if existingCert.NotAfter.After(time.Now()) {
					isReEnroll = true
					existingHostID = existingCert.Subject.CommonName
				}
			}
		}
	}

	// 2. Validate request shape.
	// enrollment_token is optional during re-enrollment; required for first enrollment.
	if !isReEnroll && req.GetEnrollmentToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "enrollment_token is required")
	}
	if req.GetCsrPem() == "" {
		return nil, status.Error(codes.InvalidArgument, "csr_pem is required")
	}
	if req.GetHostInfo() == nil || req.GetHostInfo().GetHostname() == "" {
		return nil, status.Error(codes.InvalidArgument, "host_info.hostname is required")
	}

	// 3. Authenticate: token claim (first enrollment) or cert lookup (re-enrollment).
	var tokenID, tokenOrgID string

	if isReEnroll {
		// Re-enrollment path: derive org_id from the existing host record; no token consumed.
		err := s.pool.QueryRow(ctx,
			`SELECT org_id FROM hosts WHERE id = $1`, existingHostID,
		).Scan(&tokenOrgID)
		if err != nil {
			s.logger.Warn("re-enrollment: host not found", "host_id", existingHostID)
			return nil, status.Error(codes.Unauthenticated, "host not found for re-enrollment")
		}
		s.logger.Info("re-enrollment: skipping token check", "host_id", existingHostID)
	} else {
		// First-enrollment path: validate and atomically claim the token.
		if err := token.ParseAndValidate(req.GetEnrollmentToken()); err != nil {
			return nil, status.Error(codes.InvalidArgument, "malformed enrollment token")
		}

		tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(req.GetEnrollmentToken())))
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
	}

	// 4. Determine host ID (stable for same machine_id + org; re-enrollment reuses existing ID).
	hostID, err := s.resolveHostID(ctx, req.GetHostInfo().GetMachineId(), tokenOrgID, existingHostID)
	if err != nil {
		s.logger.Error("enrollment: resolve host ID", "err", err)
		return nil, status.Error(codes.Internal, "internal error")
	}

	// 5. Issue client certificate (server overrides CN with hostID regardless of CSR CN)
	certPEM, err := s.ca.IssueClient([]byte(req.GetCsrPem()), hostID, clientCertTTL)
	if err != nil {
		s.logger.Error("enrollment: issue client cert", "host_id", hostID, "err", err)
		return nil, status.Errorf(codes.InvalidArgument, "CSR rejected: %v", err)
	}

	// 6. Upsert host row
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

	// 7. Update token used_by_host_id now that we have the real host ID (first enrollment only).
	if !isReEnroll && tokenID != "" {
		if _, err := s.pool.Exec(ctx,
			`UPDATE enrollment_tokens SET used_by_host_id = $1 WHERE id = $2`,
			hostID, tokenID,
		); err != nil {
			s.logger.Error("enrollment: update token host ref", "token_id", tokenID, "err", err)
			// Non-fatal: host is enrolled, cert is issued
		}
	}

	// 8. Record the issued identity
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

// resolveHostID returns the host ID to use for this enrollment.
//   - If reEnrollHostID is non-empty (re-enrollment), it is returned directly.
//   - Otherwise, if a host matching machine_id+orgID already exists, that ID is reused.
//   - Otherwise a new ULID is generated.
func (s *Service) resolveHostID(ctx context.Context, machineID, orgID, reEnrollHostID string) (string, error) {
	if reEnrollHostID != "" {
		return reEnrollHostID, nil
	}
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
