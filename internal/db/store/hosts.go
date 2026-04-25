// Package store provides typed CRUD access to the Postgres database.
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Host represents a row in the hosts table.
type Host struct {
	ID              string
	Hostname        string
	OS              string
	OSVersion       string
	Kernel          string
	Arch            string
	CPUCores        int
	MemoryBytes     int64
	MachineID       *string
	IPAddresses     json.RawMessage
	Labels          json.RawMessage
	FirstSeenAt     time.Time
	LastHeartbeatAt time.Time
	Status          string
	AgentVersion    string
	OrgID           string
}

// HostStore provides CRUD operations for the hosts table.
type HostStore struct {
	pool *pgxpool.Pool
}

// NewHostStore creates a new HostStore.
func NewHostStore(pool *pgxpool.Pool) *HostStore {
	return &HostStore{pool: pool}
}

// Upsert inserts or updates a host row by id.
func (s *HostStore) Upsert(ctx context.Context, h *Host) error {
	const q = `
		INSERT INTO hosts (
			id, hostname, os, os_version, kernel, arch,
			cpu_cores, memory_bytes, machine_id,
			ip_addresses, labels, first_seen_at, last_heartbeat_at,
			status, agent_version, org_id
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9,
			$10, $11, $12, $13,
			$14, $15, $16
		)
		ON CONFLICT (id) DO UPDATE SET
			hostname         = EXCLUDED.hostname,
			os               = EXCLUDED.os,
			os_version       = EXCLUDED.os_version,
			kernel           = EXCLUDED.kernel,
			arch             = EXCLUDED.arch,
			cpu_cores        = EXCLUDED.cpu_cores,
			memory_bytes     = EXCLUDED.memory_bytes,
			machine_id       = EXCLUDED.machine_id,
			ip_addresses     = EXCLUDED.ip_addresses,
			labels           = EXCLUDED.labels,
			last_heartbeat_at = EXCLUDED.last_heartbeat_at,
			status           = EXCLUDED.status,
			agent_version    = EXCLUDED.agent_version
	`
	_, err := s.pool.Exec(ctx, q,
		h.ID, h.Hostname, h.OS, h.OSVersion, h.Kernel, h.Arch,
		h.CPUCores, h.MemoryBytes, h.MachineID,
		h.IPAddresses, h.Labels, h.FirstSeenAt, h.LastHeartbeatAt,
		h.Status, h.AgentVersion, h.OrgID,
	)
	if err != nil {
		return fmt.Errorf("hosts: upsert %s: %w", h.ID, err)
	}
	return nil
}

// GetByID returns a host by primary key.
func (s *HostStore) GetByID(ctx context.Context, id string) (*Host, error) {
	const q = `
		SELECT id, hostname, os, os_version, kernel, arch,
			cpu_cores, memory_bytes, machine_id,
			ip_addresses, labels, first_seen_at, last_heartbeat_at,
			status, agent_version, org_id
		FROM hosts WHERE id = $1
	`
	row := s.pool.QueryRow(ctx, q, id)
	h := &Host{}
	err := row.Scan(
		&h.ID, &h.Hostname, &h.OS, &h.OSVersion, &h.Kernel, &h.Arch,
		&h.CPUCores, &h.MemoryBytes, &h.MachineID,
		&h.IPAddresses, &h.Labels, &h.FirstSeenAt, &h.LastHeartbeatAt,
		&h.Status, &h.AgentVersion, &h.OrgID,
	)
	if err != nil {
		return nil, fmt.Errorf("hosts: get %s: %w", id, err)
	}
	return h, nil
}

// List returns all hosts for a given org, ordered by last_heartbeat_at desc.
func (s *HostStore) List(ctx context.Context, orgID string) ([]*Host, error) {
	const q = `
		SELECT id, hostname, os, os_version, kernel, arch,
			cpu_cores, memory_bytes, machine_id,
			ip_addresses, labels, first_seen_at, last_heartbeat_at,
			status, agent_version, org_id
		FROM hosts
		WHERE org_id = $1
		ORDER BY last_heartbeat_at DESC
	`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("hosts: list org %s: %w", orgID, err)
	}
	defer rows.Close()

	var hosts []*Host
	for rows.Next() {
		h := &Host{}
		if err := rows.Scan(
			&h.ID, &h.Hostname, &h.OS, &h.OSVersion, &h.Kernel, &h.Arch,
			&h.CPUCores, &h.MemoryBytes, &h.MachineID,
			&h.IPAddresses, &h.Labels, &h.FirstSeenAt, &h.LastHeartbeatAt,
			&h.Status, &h.AgentVersion, &h.OrgID,
		); err != nil {
			return nil, fmt.Errorf("hosts: list scan: %w", err)
		}
		hosts = append(hosts, h)
	}
	return hosts, rows.Err()
}

// UpdateHeartbeat updates last_heartbeat_at and status for a host.
func (s *HostStore) UpdateHeartbeat(ctx context.Context, id string, t time.Time, status string) error {
	const q = `UPDATE hosts SET last_heartbeat_at = $2, status = $3 WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, id, t, status)
	if err != nil {
		return fmt.Errorf("hosts: update heartbeat %s: %w", id, err)
	}
	return nil
}
