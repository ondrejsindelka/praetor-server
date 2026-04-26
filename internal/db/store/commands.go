package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CommandExecution mirrors the command_executions table.
type CommandExecution struct {
	ID              string
	HostID          string
	Tier            int
	CommandJSON     json.RawMessage
	Reason          string
	IssuedBy        string
	IssuedAt        time.Time
	CompletedAt     *time.Time
	ExitCode        *int
	Stdout          *string
	Stderr          *string
	StdoutTruncated bool
	StderrTruncated bool
	DurationMs      *int64
	Error           *string
	Status          string
}

// CommandStore provides access to command_executions.
type CommandStore struct{ pool *pgxpool.Pool }

func NewCommandStore(pool *pgxpool.Pool) *CommandStore { return &CommandStore{pool: pool} }

// Insert stores a new pending command execution.
func (s *CommandStore) Insert(ctx context.Context, c *CommandExecution) error {
	const q = `INSERT INTO command_executions
        (id, host_id, tier, command_json, reason, issued_by, status)
        VALUES ($1, $2, $3, $4, $5, $6, 'pending')`
	_, err := s.pool.Exec(ctx, q, c.ID, c.HostID, c.Tier, c.CommandJSON, c.Reason, c.IssuedBy)
	if err != nil {
		return fmt.Errorf("commands: insert %s: %w", c.ID, err)
	}
	return nil
}

// Complete updates a command execution with its result.
func (s *CommandStore) Complete(ctx context.Context, id string, exitCode int, stdout, stderr string, stdoutTrunc, stderrTrunc bool, durationMs int64, agentErr string) error {
	status := "completed"
	if agentErr != "" {
		status = "failed"
	}
	const q = `UPDATE command_executions SET
        completed_at = now(), exit_code = $2, stdout = $3, stderr = $4,
        stdout_truncated = $5, stderr_truncated = $6, duration_ms = $7,
        error = NULLIF($8, ''), status = $9
        WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, id, exitCode, stdout, stderr, stdoutTrunc, stderrTrunc, durationMs, agentErr, status)
	if err != nil {
		return fmt.Errorf("commands: complete %s: %w", id, err)
	}
	return nil
}

// Get returns a command execution by ID.
func (s *CommandStore) Get(ctx context.Context, id string) (*CommandExecution, error) {
	const q = `SELECT id, host_id, tier, command_json, reason, issued_by,
        issued_at, completed_at, exit_code, stdout, stderr,
        stdout_truncated, stderr_truncated, duration_ms, error, status
        FROM command_executions WHERE id = $1`
	c := &CommandExecution{}
	err := s.pool.QueryRow(ctx, q, id).Scan(
		&c.ID, &c.HostID, &c.Tier, &c.CommandJSON, &c.Reason, &c.IssuedBy,
		&c.IssuedAt, &c.CompletedAt, &c.ExitCode, &c.Stdout, &c.Stderr,
		&c.StdoutTruncated, &c.StderrTruncated, &c.DurationMs, &c.Error, &c.Status,
	)
	if err != nil {
		return nil, fmt.Errorf("commands: get %s: %w", id, err)
	}
	return c, nil
}
