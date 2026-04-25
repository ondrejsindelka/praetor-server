package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnrollmentToken mirrors the enrollment_tokens table schema.
type EnrollmentToken struct {
	ID           string
	TokenHash    string
	Label        *string
	OrgID        string
	CreatedBy    *string
	CreatedAt    time.Time
	ExpiresAt    time.Time
	UsedAt       *time.Time
	UsedByHostID *string
	RevokedAt    *time.Time
}

// TokenStore executes typed queries against the enrollment_tokens table.
type TokenStore struct {
	pool *pgxpool.Pool
}

// NewTokenStore creates a TokenStore backed by pool.
func NewTokenStore(pool *pgxpool.Pool) *TokenStore {
	return &TokenStore{pool: pool}
}

// Insert stores a new enrollment token and sets t.CreatedAt from the DB.
func (s *TokenStore) Insert(ctx context.Context, t *EnrollmentToken) error {
	err := s.pool.QueryRow(ctx, `
		INSERT INTO enrollment_tokens (id, token_hash, label, org_id, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at`,
		t.ID, t.TokenHash, t.Label, t.OrgID, t.CreatedBy, t.ExpiresAt,
	).Scan(&t.CreatedAt)
	if err != nil {
		return fmt.Errorf("tokens: insert: %w", err)
	}
	return nil
}

// GetByID returns a token by its ULID id regardless of state.
func (s *TokenStore) GetByID(ctx context.Context, id string) (*EnrollmentToken, error) {
	t := &EnrollmentToken{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, token_hash, label, org_id, created_by, created_at,
		       expires_at, used_at, used_by_host_id, revoked_at
		FROM enrollment_tokens WHERE id = $1`, id,
	).Scan(&t.ID, &t.TokenHash, &t.Label, &t.OrgID, &t.CreatedBy, &t.CreatedAt,
		&t.ExpiresAt, &t.UsedAt, &t.UsedByHostID, &t.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("tokens: %w", pgx.ErrNoRows)
	}
	if err != nil {
		return nil, fmt.Errorf("tokens: get by id: %w", err)
	}
	return t, nil
}

// MarkUsed records that the token was consumed by a host during enrollment.
func (s *TokenStore) MarkUsed(ctx context.Context, id, hostID string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE enrollment_tokens
		SET used_at = NOW(), used_by_host_id = $2
		WHERE id = $1 AND used_at IS NULL AND revoked_at IS NULL`,
		id, hostID)
	if err != nil {
		return fmt.Errorf("tokens: mark used: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("tokens: %s not found or already used", id)
	}
	return nil
}

// Revoke marks a token as revoked.
func (s *TokenStore) Revoke(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE enrollment_tokens SET revoked_at = NOW()
		WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("tokens: revoke: %w", err)
	}
	return nil
}

// ListActive returns non-used, non-revoked, non-expired tokens for orgID.
func (s *TokenStore) ListActive(ctx context.Context, orgID string) ([]*EnrollmentToken, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, token_hash, label, org_id, created_by, created_at,
		       expires_at, used_at, used_by_host_id, revoked_at
		FROM enrollment_tokens
		WHERE org_id = $1 AND used_at IS NULL AND revoked_at IS NULL
		      AND expires_at > NOW()
		ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("tokens: list active: %w", err)
	}
	defer rows.Close()

	var tokens []*EnrollmentToken
	for rows.Next() {
		t := &EnrollmentToken{}
		if err := rows.Scan(&t.ID, &t.TokenHash, &t.Label, &t.OrgID, &t.CreatedBy, &t.CreatedAt,
			&t.ExpiresAt, &t.UsedAt, &t.UsedByHostID, &t.RevokedAt); err != nil {
			return nil, fmt.Errorf("tokens: scan: %w", err)
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}
