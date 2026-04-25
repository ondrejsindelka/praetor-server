-- +goose Up
-- +goose StatementBegin
CREATE TABLE enrollment_tokens (
    id               TEXT PRIMARY KEY,
    token_hash       TEXT NOT NULL,
    label            TEXT,
    org_id           TEXT NOT NULL DEFAULT 'default',
    created_by       TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at       TIMESTAMPTZ NOT NULL,
    used_at          TIMESTAMPTZ,
    used_by_host_id  TEXT REFERENCES hosts(id),
    revoked_at       TIMESTAMPTZ
);

CREATE INDEX enrollment_tokens_active_idx
    ON enrollment_tokens (org_id, expires_at)
    WHERE used_at IS NULL AND revoked_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS enrollment_tokens;
-- +goose StatementEnd
