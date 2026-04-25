-- +goose Up
CREATE TABLE hosts (
    id              TEXT PRIMARY KEY,
    hostname        TEXT NOT NULL,
    os              TEXT NOT NULL,
    os_version      TEXT NOT NULL,
    kernel          TEXT NOT NULL,
    arch            TEXT NOT NULL,
    cpu_cores       INTEGER NOT NULL DEFAULT 0,
    memory_bytes    BIGINT NOT NULL DEFAULT 0,
    machine_id      TEXT,
    ip_addresses    JSONB NOT NULL DEFAULT '[]',
    labels          JSONB NOT NULL DEFAULT '{}',
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    status          TEXT NOT NULL DEFAULT 'pending',
    agent_version   TEXT NOT NULL DEFAULT '',
    org_id          TEXT NOT NULL DEFAULT 'default'
);

CREATE UNIQUE INDEX hosts_machine_id_org_id_unique
    ON hosts (machine_id, org_id)
    WHERE machine_id IS NOT NULL;

CREATE INDEX hosts_org_id_status ON hosts (org_id, status);

-- +goose Down
DROP TABLE hosts;
