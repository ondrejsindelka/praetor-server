// Package store provides typed query functions for each Postgres table.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Host mirrors the hosts table schema.
type Host struct {
	ID              string
	Hostname        string
	OS              string
	OSVersion       *string
	Kernel          *string
	Arch            string
	CPUCores        *int32
	MemoryBytes     *int64
	MachineID       *string
	IPAddresses     []string
	Labels          map[string]string
	FirstSeenAt     time.Time
	LastHeartbeatAt *time.Time
	Status          string
	AgentVersion    *string
	OrgID           string
}

// HostStore executes typed queries against the hosts table.
type HostStore struct {
	pool *pgxpool.Pool
}

// NewHostStore creates a HostStore backed by pool.
func NewHostStore(pool *pgxpool.Pool) *HostStore {
	return &HostStore{pool: pool}
}

const upsertHostSQL = `
INSERT INTO hosts (
    id, hostname, os, os_version, kernel, arch, cpu_cores, memory_bytes,
    machine_id, ip_addresses, labels, last_heartbeat_at, status, agent_version, org_id
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
ON CONFLICT (id) DO UPDATE SET
    hostname          = EXCLUDED.hostname,
    os                = EXCLUDED.os,
    os_version        = EXCLUDED.os_version,
    kernel            = EXCLUDED.kernel,
    arch              = EXCLUDED.arch,
    cpu_cores         = EXCLUDED.cpu_cores,
    memory_bytes      = EXCLUDED.memory_bytes,
    machine_id        = EXCLUDED.machine_id,
    ip_addresses      = EXCLUDED.ip_addresses,
    labels            = EXCLUDED.labels,
    last_heartbeat_at = EXCLUDED.last_heartbeat_at,
    status            = EXCLUDED.status,
    agent_version     = EXCLUDED.agent_version,
    org_id            = EXCLUDED.org_id`

// Upsert inserts a new host or updates all mutable fields on id conflict.
// first_seen_at is set by DB DEFAULT on first insert and never overwritten.
func (s *HostStore) Upsert(ctx context.Context, h *Host) error {
	ipJSON, err := json.Marshal(h.IPAddresses)
	if err != nil {
		return fmt.Errorf("hosts: marshal ip_addresses: %w", err)
	}
	labelsJSON, err := json.Marshal(h.Labels)
	if err != nil {
		return fmt.Errorf("hosts: marshal labels: %w", err)
	}
	_, err = s.pool.Exec(ctx, upsertHostSQL,
		h.ID, h.Hostname, h.OS, h.OSVersion, h.Kernel, h.Arch,
		h.CPUCores, h.MemoryBytes, h.MachineID, json.RawMessage(ipJSON), json.RawMessage(labelsJSON),
		h.LastHeartbeatAt, h.Status, h.AgentVersion, h.OrgID,
	)
	if err != nil {
		return fmt.Errorf("hosts: upsert: %w", err)
	}
	return nil
}

const selectHostCols = `
    id, hostname, os, os_version, kernel, arch, cpu_cores, memory_bytes,
    machine_id, ip_addresses, labels, first_seen_at, last_heartbeat_at,
    status, agent_version, org_id`

// GetByID returns a host by primary key.
func (s *HostStore) GetByID(ctx context.Context, id string) (*Host, error) {
	row := s.pool.QueryRow(ctx,
		"SELECT "+selectHostCols+" FROM hosts WHERE id = $1", id)
	return scanHost(row)
}

// GetByMachineID returns the host with the given machine_id in the given org.
func (s *HostStore) GetByMachineID(ctx context.Context, machineID, orgID string) (*Host, error) {
	row := s.pool.QueryRow(ctx,
		"SELECT "+selectHostCols+" FROM hosts WHERE machine_id = $1 AND org_id = $2",
		machineID, orgID)
	return scanHost(row)
}

// UpdateHeartbeat updates last_heartbeat_at and status for the given host.
// t is the heartbeat timestamp (use time.Now().UTC() if not provided by the agent).
func (s *HostStore) UpdateHeartbeat(ctx context.Context, id string, t time.Time, status string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE hosts
		SET last_heartbeat_at = $2, status = $3
		WHERE id = $1`, id, t, status)
	if err != nil {
		return fmt.Errorf("hosts: update heartbeat: %w", err)
	}
	return nil
}

// List returns all hosts in orgID ordered by hostname.
func (s *HostStore) List(ctx context.Context, orgID string) ([]*Host, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT "+selectHostCols+" FROM hosts WHERE org_id = $1 ORDER BY hostname",
		orgID)
	if err != nil {
		return nil, fmt.Errorf("hosts: list: %w", err)
	}
	defer rows.Close()

	var hosts []*Host
	for rows.Next() {
		h, err := scanHost(rows)
		if err != nil {
			return nil, err
		}
		hosts = append(hosts, h)
	}
	return hosts, rows.Err()
}

func scanHost(row pgx.Row) (*Host, error) {
	var h Host
	var ipJSON, labelsJSON []byte
	err := row.Scan(
		&h.ID, &h.Hostname, &h.OS, &h.OSVersion, &h.Kernel, &h.Arch,
		&h.CPUCores, &h.MemoryBytes, &h.MachineID, &ipJSON, &labelsJSON,
		&h.FirstSeenAt, &h.LastHeartbeatAt, &h.Status, &h.AgentVersion, &h.OrgID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("hosts: %w", pgx.ErrNoRows)
	}
	if err != nil {
		return nil, fmt.Errorf("hosts: scan: %w", err)
	}
	if len(ipJSON) > 0 {
		if err := json.Unmarshal(ipJSON, &h.IPAddresses); err != nil {
			return nil, fmt.Errorf("hosts: unmarshal ip_addresses: %w", err)
		}
	}
	if len(labelsJSON) > 0 {
		if err := json.Unmarshal(labelsJSON, &h.Labels); err != nil {
			return nil, fmt.Errorf("hosts: unmarshal labels: %w", err)
		}
	}
	return &h, nil
}
