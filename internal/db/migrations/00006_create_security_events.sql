-- +goose Up
CREATE TABLE security_events (
    id          BIGSERIAL PRIMARY KEY,
    host_id     TEXT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    occurred_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    type        TEXT NOT NULL,
    source      TEXT NOT NULL DEFAULT '',
    data        JSONB NOT NULL DEFAULT '{}',
    raw         TEXT NOT NULL DEFAULT ''
);

CREATE INDEX security_events_host_id_occurred ON security_events (host_id, occurred_at DESC);
CREATE INDEX security_events_type ON security_events (type);
CREATE INDEX security_events_occurred ON security_events (occurred_at DESC);

-- +goose Down
DROP TABLE security_events;
