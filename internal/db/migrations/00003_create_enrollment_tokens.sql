-- +goose Up
CREATE TABLE enrollment_tokens (
    id              TEXT PRIMARY KEY,
    token_hash      TEXT NOT NULL,
    label           TEXT NOT NULL DEFAULT '',
    org_id          TEXT NOT NULL DEFAULT 'default',
    created_by      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ,
    used_at         TIMESTAMPTZ,
    used_by_host_id TEXT REFERENCES hosts(id),
    revoked_at      TIMESTAMPTZ
);

CREATE INDEX enrollment_tokens_active
    ON enrollment_tokens (org_id, expires_at)
    WHERE used_at IS NULL AND revoked_at IS NULL;

-- +goose Down
DROP TABLE enrollment_tokens;
