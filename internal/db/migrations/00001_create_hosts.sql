-- +goose Up
-- +goose StatementBegin
CREATE TABLE hosts (
    id                TEXT PRIMARY KEY,
    hostname          TEXT NOT NULL,
    os                TEXT NOT NULL,
    os_version        TEXT,
    kernel            TEXT,
    arch              TEXT NOT NULL,
    cpu_cores         INTEGER,
    memory_bytes      BIGINT,
    machine_id        TEXT,
    ip_addresses      JSONB,
    labels            JSONB,
    first_seen_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_heartbeat_at TIMESTAMPTZ,
    -- status valid values: pending (initial after enrollment), online (heartbeat active),
    -- offline (set by staleness detector, not on TCP disconnect), disabled (operator)
    status            TEXT NOT NULL DEFAULT 'pending',
    agent_version     TEXT,
    org_id            TEXT NOT NULL DEFAULT 'default'
);

CREATE UNIQUE INDEX hosts_machine_id_org_id_uidx
    ON hosts (machine_id, org_id)
    WHERE machine_id IS NOT NULL;

CREATE INDEX hosts_org_id_status_idx ON hosts (org_id, status);
CREATE INDEX hosts_last_heartbeat_at_idx ON hosts (last_heartbeat_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS hosts;
-- +goose StatementEnd
