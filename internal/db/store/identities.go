package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AgentIdentity represents a row in the agent_identities table.
type AgentIdentity struct {
	ID              int64
	HostID          string
	CertPEM         string
	CertFingerprint string
	IssuedAt        time.Time
	ExpiresAt       time.Time
	RevokedAt       *time.Time
	RevokedReason   *string
}

// IdentityStore provides CRUD operations for the agent_identities table.
type IdentityStore struct {
	pool *pgxpool.Pool
}

// NewIdentityStore creates a new IdentityStore.
func NewIdentityStore(pool *pgxpool.Pool) *IdentityStore {
	return &IdentityStore{pool: pool}
}

// Insert stores a new agent identity certificate.
func (s *IdentityStore) Insert(ctx context.Context, ident *AgentIdentity) (int64, error) {
	const q = `
		INSERT INTO agent_identities (host_id, cert_pem, cert_fingerprint, issued_at, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`
	var id int64
	err := s.pool.QueryRow(ctx, q,
		ident.HostID, ident.CertPEM, ident.CertFingerprint, ident.IssuedAt, ident.ExpiresAt,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("identities: insert for host %s: %w", ident.HostID, err)
	}
	return id, nil
}

// GetActiveByFingerprint returns an unrevoked identity by cert fingerprint.
func (s *IdentityStore) GetActiveByFingerprint(ctx context.Context, fingerprint string) (*AgentIdentity, error) {
	const q = `
		SELECT id, host_id, cert_pem, cert_fingerprint, issued_at, expires_at, revoked_at, revoked_reason
		FROM agent_identities
		WHERE cert_fingerprint = $1 AND revoked_at IS NULL
	`
	row := s.pool.QueryRow(ctx, q, fingerprint)
	ident := &AgentIdentity{}
	err := row.Scan(
		&ident.ID, &ident.HostID, &ident.CertPEM, &ident.CertFingerprint,
		&ident.IssuedAt, &ident.ExpiresAt, &ident.RevokedAt, &ident.RevokedReason,
	)
	if err != nil {
		return nil, fmt.Errorf("identities: get by fingerprint: %w", err)
	}
	return ident, nil
}

// Revoke marks an identity as revoked.
func (s *IdentityStore) Revoke(ctx context.Context, id int64, reason string) error {
	const q = `UPDATE agent_identities SET revoked_at = now(), revoked_reason = $2 WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, id, reason)
	if err != nil {
		return fmt.Errorf("identities: revoke %d: %w", id, err)
	}
	return nil
}
