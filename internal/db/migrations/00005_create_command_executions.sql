-- +goose Up
CREATE TABLE command_executions (
    id              TEXT PRIMARY KEY,           -- ULID, matches CommandRequest.id
    host_id         TEXT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    tier            INTEGER NOT NULL,           -- 0=safe, 1=validated
    command_json    JSONB NOT NULL,             -- full CommandRequest as JSON
    reason          TEXT NOT NULL,
    issued_by       TEXT NOT NULL DEFAULT '',
    issued_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ,
    exit_code       INTEGER,
    stdout          TEXT,
    stderr          TEXT,
    stdout_truncated BOOLEAN NOT NULL DEFAULT false,
    stderr_truncated BOOLEAN NOT NULL DEFAULT false,
    duration_ms     BIGINT,
    error           TEXT,
    status          TEXT NOT NULL DEFAULT 'pending'  -- pending|running|completed|failed|timeout
);

CREATE INDEX command_executions_host_id ON command_executions (host_id, issued_at DESC);
CREATE INDEX command_executions_status ON command_executions (status) WHERE status IN ('pending','running');

-- +goose Down
DROP TABLE command_executions;
