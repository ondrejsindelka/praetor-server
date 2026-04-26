// Package store provides typed query functions for each Postgres table.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// HostConfig mirrors the host_configs table.
type HostConfig struct {
	HostID                          string
	ConfigVersion                   int64
	HeartbeatIntervalSeconds        int
	MetricCollectionIntervalSeconds int
	LogSources                      []string
	UpdatedAt                       time.Time
}

// ConfigStore provides CRUD for host_configs.
type ConfigStore struct {
	pool *pgxpool.Pool
}

func NewConfigStore(pool *pgxpool.Pool) *ConfigStore {
	return &ConfigStore{pool: pool}
}

// Get returns config for hostID. Returns default config if no row exists.
func (s *ConfigStore) Get(ctx context.Context, hostID string) (*HostConfig, error) {
	const q = `
		SELECT host_id, config_version,
		       heartbeat_interval_seconds, metric_collection_interval_seconds,
		       log_sources, updated_at
		FROM host_configs WHERE host_id = $1
	`
	c := &HostConfig{}
	err := s.pool.QueryRow(ctx, q, hostID).Scan(
		&c.HostID, &c.ConfigVersion,
		&c.HeartbeatIntervalSeconds, &c.MetricCollectionIntervalSeconds,
		&c.LogSources, &c.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &HostConfig{
				HostID:                          hostID,
				ConfigVersion:                   0,
				HeartbeatIntervalSeconds:        30,
				MetricCollectionIntervalSeconds: 15,
				LogSources:                      []string{},
			}, nil
		}
		return nil, fmt.Errorf("configs: get %s: %w", hostID, err)
	}
	return c, nil
}

// SetField updates one integer config field and increments config_version.
// Allowed fields: heartbeat_interval_seconds, metric_collection_interval_seconds.
func (s *ConfigStore) SetField(ctx context.Context, hostID, field string, value int) error {
	allowed := map[string]bool{
		"heartbeat_interval_seconds":         true,
		"metric_collection_interval_seconds": true,
	}
	if !allowed[field] {
		return fmt.Errorf("configs: field %q is not updatable", field)
	}
	// Use format string only for the column name (allowlisted above)
	q := fmt.Sprintf(`
		INSERT INTO host_configs (host_id, config_version, %s)
		VALUES ($1, 1, $2)
		ON CONFLICT (host_id) DO UPDATE SET
			%s = EXCLUDED.%s,
			config_version = host_configs.config_version + 1,
			updated_at = now()
	`, field, field, field)
	if _, err := s.pool.Exec(ctx, q, hostID, value); err != nil {
		return fmt.Errorf("configs: set %s for %s: %w", field, hostID, err)
	}
	return nil
}
