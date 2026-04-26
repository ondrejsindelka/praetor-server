-- +goose Up
CREATE TABLE host_configs (
    host_id         TEXT PRIMARY KEY REFERENCES hosts(id) ON DELETE CASCADE,
    config_version  BIGINT NOT NULL DEFAULT 1,
    heartbeat_interval_seconds              INTEGER NOT NULL DEFAULT 30,
    metric_collection_interval_seconds      INTEGER NOT NULL DEFAULT 15,
    log_sources     TEXT[] NOT NULL DEFAULT '{}',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE host_configs;
