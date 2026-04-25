package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AgentIdentity mirrors the agent_identities table schema.
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

// IdentityStore executes typed queries against the agent_identities table.
type IdentityStore struct {
	pool *pgxpool.Pool
}

// NewIdentityStore creates an IdentityStore backed by pool.
func NewIdentityStore(pool *pgxpool.Pool) *IdentityStore {
	return &IdentityStore{pool: pool}
}

// Insert stores a new agent identity and sets ai.ID and ai.IssuedAt from the DB.
func (s *IdentityStore) Insert(ctx context.Context, ai *AgentIdentity) error {
	err := s.pool.QueryRow(ctx, `
		INSERT INTO agent_identities (host_id, cert_pem, cert_fingerprint, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, issued_at`,
		ai.HostID, ai.CertPEM, ai.CertFingerprint, ai.ExpiresAt,
	).Scan(&ai.ID, &ai.IssuedAt)
	if err != nil {
		return fmt.Errorf("identities: insert: %w", err)
	}
	return nil
}

// GetByCertFingerprint returns the identity with the given fingerprint (any state).
func (s *IdentityStore) GetByCertFingerprint(ctx context.Context, fingerprint string) (*AgentIdentity, error) {
	ai := &AgentIdentity{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, host_id, cert_pem, cert_fingerprint, issued_at, expires_at, revoked_at, revoked_reason
		FROM agent_identities
		WHERE cert_fingerprint = $1`,
		fingerprint,
	).Scan(&ai.ID, &ai.HostID, &ai.CertPEM, &ai.CertFingerprint,
		&ai.IssuedAt, &ai.ExpiresAt, &ai.RevokedAt, &ai.RevokedReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("identities: %w", pgx.ErrNoRows)
	}
	if err != nil {
		return nil, fmt.Errorf("identities: get by fingerprint: %w", err)
	}
	return ai, nil
}

// GetCurrentByHostID returns the most recent non-revoked cert for hostID.
func (s *IdentityStore) GetCurrentByHostID(ctx context.Context, hostID string) (*AgentIdentity, error) {
	ai := &AgentIdentity{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, host_id, cert_pem, cert_fingerprint, issued_at, expires_at, revoked_at, revoked_reason
		FROM agent_identities
		WHERE host_id = $1 AND revoked_at IS NULL
		ORDER BY issued_at DESC
		LIMIT 1`,
		hostID,
	).Scan(&ai.ID, &ai.HostID, &ai.CertPEM, &ai.CertFingerprint,
		&ai.IssuedAt, &ai.ExpiresAt, &ai.RevokedAt, &ai.RevokedReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("identities: %w", pgx.ErrNoRows)
	}
	if err != nil {
		return nil, fmt.Errorf("identities: get current: %w", err)
	}
	return ai, nil
}

// Revoke marks an identity as revoked with the given reason.
func (s *IdentityStore) Revoke(ctx context.Context, id int64, reason string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_identities
		SET revoked_at = NOW(), revoked_reason = $2
		WHERE id = $1 AND revoked_at IS NULL`,
		id, reason)
	if err != nil {
		return fmt.Errorf("identities: revoke: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("identities: %d not found or already revoked", id)
	}
	return nil
}
