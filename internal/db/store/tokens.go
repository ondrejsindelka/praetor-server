package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// EnrollmentToken represents a row in the enrollment_tokens table.
type EnrollmentToken struct {
	ID           string
	TokenHash    string
	Label        string
	OrgID        string
	CreatedBy    string
	CreatedAt    time.Time
	ExpiresAt    *time.Time
	UsedAt       *time.Time
	UsedByHostID *string
	RevokedAt    *time.Time
}

// TokenStore provides CRUD operations for the enrollment_tokens table.
type TokenStore struct {
	pool *pgxpool.Pool
}

// NewTokenStore creates a new TokenStore.
func NewTokenStore(pool *pgxpool.Pool) *TokenStore {
	return &TokenStore{pool: pool}
}

// Insert stores a new enrollment token.
func (s *TokenStore) Insert(ctx context.Context, t *EnrollmentToken) error {
	const q = `
		INSERT INTO enrollment_tokens (id, token_hash, label, org_id, created_by, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := s.pool.Exec(ctx, q,
		t.ID, t.TokenHash, t.Label, t.OrgID, t.CreatedBy, t.CreatedAt, t.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("tokens: insert %s: %w", t.ID, err)
	}
	return nil
}

// GetActiveByID returns an unused, unrevoked token by its ID.
func (s *TokenStore) GetActiveByID(ctx context.Context, id string) (*EnrollmentToken, error) {
	const q = `
		SELECT id, token_hash, label, org_id, created_by, created_at, expires_at,
			used_at, used_by_host_id, revoked_at
		FROM enrollment_tokens
		WHERE id = $1 AND used_at IS NULL AND revoked_at IS NULL
	`
	row := s.pool.QueryRow(ctx, q, id)
	t := &EnrollmentToken{}
	err := row.Scan(
		&t.ID, &t.TokenHash, &t.Label, &t.OrgID, &t.CreatedBy, &t.CreatedAt, &t.ExpiresAt,
		&t.UsedAt, &t.UsedByHostID, &t.RevokedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("tokens: get active %s: %w", id, err)
	}
	return t, nil
}

// MarkUsed records that a token was consumed by a host during enrollment.
func (s *TokenStore) MarkUsed(ctx context.Context, id string, hostID string) error {
	const q = `
		UPDATE enrollment_tokens
		SET used_at = now(), used_by_host_id = $2
		WHERE id = $1 AND used_at IS NULL AND revoked_at IS NULL
	`
	tag, err := s.pool.Exec(ctx, q, id, hostID)
	if err != nil {
		return fmt.Errorf("tokens: mark used %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("tokens: %s not found or already used", id)
	}
	return nil
}

// Revoke marks a token as revoked.
func (s *TokenStore) Revoke(ctx context.Context, id string) error {
	const q = `UPDATE enrollment_tokens SET revoked_at = now() WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("tokens: revoke %s: %w", id, err)
	}
	return nil
}
