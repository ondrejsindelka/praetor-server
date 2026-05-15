package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ondrejsindelka/praetor-server/internal/watchdog/storage"
)

// RuleStatePostgres implements storage.RuleStateRepo using pgxpool.
type RuleStatePostgres struct {
	pool *pgxpool.Pool
}

// NewRuleStatePostgres creates a RuleStatePostgres backed by pool.
func NewRuleStatePostgres(pool *pgxpool.Pool) *RuleStatePostgres {
	return &RuleStatePostgres{pool: pool}
}

const ruleStateCols = `rule_id, host_id, phase, pending_since, last_fired_at, updated_at`

// Upsert inserts or updates rule state for a (rule, host) pair.
func (r *RuleStatePostgres) Upsert(ctx context.Context, s *storage.RuleState) error {
	const q = `
		INSERT INTO watchdog_rule_state (rule_id, host_id, phase, pending_since, last_fired_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,NOW())
		ON CONFLICT (rule_id, host_id) DO UPDATE SET
			phase         = EXCLUDED.phase,
			pending_since = EXCLUDED.pending_since,
			last_fired_at = EXCLUDED.last_fired_at,
			updated_at    = NOW()
		RETURNING updated_at`
	row := r.pool.QueryRow(ctx, q,
		s.RuleID, s.HostID, s.Phase, s.PendingSince, s.LastFiredAt,
	)
	return row.Scan(&s.UpdatedAt)
}

// Get returns the rule state for a specific (rule, host) pair.
func (r *RuleStatePostgres) Get(ctx context.Context, ruleID, hostID string) (*storage.RuleState, error) {
	const q = `SELECT ` + ruleStateCols + ` FROM watchdog_rule_state WHERE rule_id=$1 AND host_id=$2`
	row := r.pool.QueryRow(ctx, q, ruleID, hostID)
	s, err := scanRuleState(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("rule_state: %w", pgx.ErrNoRows)
	}
	return s, err
}

// ListByRule returns all state records for a given rule across all hosts.
func (r *RuleStatePostgres) ListByRule(ctx context.Context, ruleID string) ([]*storage.RuleState, error) {
	const q = `SELECT ` + ruleStateCols + ` FROM watchdog_rule_state WHERE rule_id=$1`
	rows, err := r.pool.Query(ctx, q, ruleID)
	if err != nil {
		return nil, fmt.Errorf("rule_state: list by rule: %w", err)
	}
	defer rows.Close()

	var states []*storage.RuleState
	for rows.Next() {
		s, err := scanRuleState(rows)
		if err != nil {
			return nil, fmt.Errorf("rule_state: scan: %w", err)
		}
		states = append(states, s)
	}
	return states, rows.Err()
}

// ListAll returns all rule state records across all rules and hosts.
func (r *RuleStatePostgres) ListAll(ctx context.Context) ([]*storage.RuleState, error) {
	const q = `SELECT ` + ruleStateCols + ` FROM watchdog_rule_state`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("rule_state: list all: %w", err)
	}
	defer rows.Close()

	var states []*storage.RuleState
	for rows.Next() {
		s, err := scanRuleState(rows)
		if err != nil {
			return nil, fmt.Errorf("rule_state: scan: %w", err)
		}
		states = append(states, s)
	}
	return states, rows.Err()
}

// BulkUpsert upserts multiple rule states in a single transaction.
func (r *RuleStatePostgres) BulkUpsert(ctx context.Context, states []*storage.RuleState) error {
	if len(states) == 0 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("rule_state: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	const q = `
		INSERT INTO watchdog_rule_state (rule_id, host_id, phase, pending_since, last_fired_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,NOW())
		ON CONFLICT (rule_id, host_id) DO UPDATE SET
			phase         = EXCLUDED.phase,
			pending_since = EXCLUDED.pending_since,
			last_fired_at = EXCLUDED.last_fired_at,
			updated_at    = NOW()
		RETURNING updated_at`
	for _, s := range states {
		row := tx.QueryRow(ctx, q, s.RuleID, s.HostID, s.Phase, s.PendingSince, s.LastFiredAt)
		if err := row.Scan(&s.UpdatedAt); err != nil {
			return fmt.Errorf("rule_state: bulk upsert scan: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func scanRuleState(row pgx.Row) (*storage.RuleState, error) {
	var s storage.RuleState
	err := row.Scan(
		&s.RuleID, &s.HostID, &s.Phase, &s.PendingSince, &s.LastFiredAt, &s.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &s, nil
}
