package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	praetorv1 "github.com/ondrejsindelka/praetor-proto/gen/go/praetor/v1"
)

// SecurityEvent mirrors the security_events table.
type SecurityEvent struct {
	ID         int64
	HostID     string
	OccurredAt time.Time
	ReceivedAt time.Time
	Type       string
	Source     string
	Data       json.RawMessage
	Raw        string
}

// SecurityEventStore provides access to security_events.
type SecurityEventStore struct{ pool *pgxpool.Pool }

// NewSecurityEventStore creates a SecurityEventStore backed by pool.
func NewSecurityEventStore(pool *pgxpool.Pool) *SecurityEventStore {
	return &SecurityEventStore{pool: pool}
}

// Insert stores a SecurityEvent received from the agent.
func (s *SecurityEventStore) Insert(ctx context.Context, ev *praetorv1.SecurityEvent) error {
	dataJSON, err := json.Marshal(ev.GetData())
	if err != nil {
		return fmt.Errorf("security_events: marshal data: %w", err)
	}
	occurredAt := time.Now()
	if ts := ev.GetTimestamp(); ts != nil {
		occurredAt = ts.AsTime()
	}
	const q = `INSERT INTO security_events (host_id, occurred_at, type, source, data, raw)
               VALUES ($1, $2, $3, $4, $5, $6)`
	_, err = s.pool.Exec(ctx, q,
		ev.GetHostId(),
		occurredAt,
		ev.GetType().String(),
		ev.GetSource(),
		dataJSON,
		ev.GetRaw(),
	)
	if err != nil {
		return fmt.Errorf("security_events: insert: %w", err)
	}
	return nil
}

// List returns security events for a host, most recent first.
func (s *SecurityEventStore) List(ctx context.Context, hostID string, since time.Time, limit int) ([]*SecurityEvent, error) {
	const q = `SELECT id, host_id, occurred_at, received_at, type, source, data, raw
               FROM security_events
               WHERE host_id = $1 AND occurred_at >= $2
               ORDER BY occurred_at DESC
               LIMIT $3`
	rows, err := s.pool.Query(ctx, q, hostID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("security_events: list: %w", err)
	}
	defer rows.Close()

	var events []*SecurityEvent
	for rows.Next() {
		ev := &SecurityEvent{}
		if err := rows.Scan(&ev.ID, &ev.HostID, &ev.OccurredAt, &ev.ReceivedAt,
			&ev.Type, &ev.Source, &ev.Data, &ev.Raw); err != nil {
			return nil, fmt.Errorf("security_events: scan: %w", err)
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

// ListAll returns recent security events across all hosts, most recent first.
func (s *SecurityEventStore) ListAll(ctx context.Context, since time.Time, limit int) ([]*SecurityEvent, error) {
	const q = `SELECT id, host_id, occurred_at, received_at, type, source, data, raw
               FROM security_events
               WHERE occurred_at >= $1
               ORDER BY occurred_at DESC
               LIMIT $2`
	rows, err := s.pool.Query(ctx, q, since, limit)
	if err != nil {
		return nil, fmt.Errorf("security_events: list all: %w", err)
	}
	defer rows.Close()

	var events []*SecurityEvent
	for rows.Next() {
		ev := &SecurityEvent{}
		if err := rows.Scan(&ev.ID, &ev.HostID, &ev.OccurredAt, &ev.ReceivedAt,
			&ev.Type, &ev.Source, &ev.Data, &ev.Raw); err != nil {
			return nil, fmt.Errorf("security_events: scan all: %w", err)
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}
