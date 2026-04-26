// Package staleness runs a background job that marks online hosts as offline
// when no heartbeat has been received within the threshold window.
package staleness

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// CheckInterval is how often the staleness job runs.
	CheckInterval = 30 * time.Second
	// HeartbeatTimeout is how long since last heartbeat before a host is marked offline.
	HeartbeatTimeout = 90 * time.Second
)

// Run marks online hosts as offline when last_heartbeat_at is older than
// HeartbeatTimeout. Runs until ctx is cancelled.
func Run(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	ticker := time.NewTicker(CheckInterval)
	defer ticker.Stop()

	logger.Info("staleness job started",
		"check_interval", CheckInterval,
		"heartbeat_timeout", HeartbeatTimeout,
	)

	for {
		select {
		case <-ctx.Done():
			logger.Info("staleness job stopped")
			return
		case <-ticker.C:
			if err := RunOnce(ctx, pool, logger); err != nil {
				logger.Warn("staleness check failed", "err", err)
			}
		}
	}
}

// RunOnce performs a single staleness sweep: updates all online hosts whose
// last_heartbeat_at is older than HeartbeatTimeout to status='offline'.
// Exported so tests can drive it directly without waiting for the ticker.
func RunOnce(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	rows, err := pool.Query(ctx, `
		UPDATE hosts
		SET status = 'offline'
		WHERE status = 'online'
		  AND last_heartbeat_at < NOW() - ($1 * INTERVAL '1 second')
		RETURNING id, hostname
	`, int(HeartbeatTimeout.Seconds()))
	if err != nil {
		return fmt.Errorf("staleness: update: %w", err)
	}
	defer rows.Close()

	type affected struct{ id, hostname string }
	var hosts []affected
	for rows.Next() {
		var h affected
		if err := rows.Scan(&h.id, &h.hostname); err != nil {
			return fmt.Errorf("staleness: scan: %w", err)
		}
		hosts = append(hosts, h)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("staleness: rows: %w", err)
	}

	if len(hosts) > 0 {
		ids := make([]string, len(hosts))
		names := make([]string, len(hosts))
		for i, h := range hosts {
			ids[i] = h.id
			names[i] = h.hostname
		}
		logger.Info("marked hosts offline (missed heartbeat)",
			"count", len(hosts),
			"host_ids", strings.Join(ids, ", "),
			"hostnames", strings.Join(names, ", "),
		)
	}

	return nil
}
