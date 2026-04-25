-- +goose Up
CREATE TABLE agent_identities (
    id              BIGSERIAL PRIMARY KEY,
    host_id         TEXT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    cert_pem        TEXT NOT NULL,
    cert_fingerprint TEXT NOT NULL,
    issued_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    revoked_at      TIMESTAMPTZ,
    revoked_reason  TEXT
);

CREATE UNIQUE INDEX agent_identities_fingerprint_active
    ON agent_identities (cert_fingerprint)
    WHERE revoked_at IS NULL;

CREATE INDEX agent_identities_host_id ON agent_identities (host_id);

-- +goose Down
DROP TABLE agent_identities;
