-- +goose Up

-- watchdog_playbooks must be created before watchdog_rules (FK dependency)
CREATE TABLE watchdog_playbooks (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  fleet_id    UUID NOT NULL,
  name        TEXT NOT NULL,
  description TEXT,
  steps       JSONB NOT NULL DEFAULT '[]',
  llm_prompt  TEXT,
  llm_config  JSONB,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (fleet_id, name)
);

CREATE TABLE watchdog_rules (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  fleet_id      UUID NOT NULL,
  name          TEXT NOT NULL,
  description   TEXT,
  enabled       BOOLEAN NOT NULL DEFAULT TRUE,
  host_selector JSONB NOT NULL,
  condition     JSONB NOT NULL,
  playbook_id   UUID NOT NULL REFERENCES watchdog_playbooks(id),
  cooldown_s    INT NOT NULL DEFAULT 300,
  priority      TEXT NOT NULL DEFAULT 'normal',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (fleet_id, name)
);

CREATE INDEX idx_watchdog_rules_enabled ON watchdog_rules (fleet_id) WHERE enabled;

CREATE TABLE watchdog_rule_state (
  rule_id       UUID NOT NULL,
  host_id       UUID NOT NULL,
  phase         TEXT NOT NULL DEFAULT 'idle',  -- idle | pending | fired | cooldown
  pending_since TIMESTAMPTZ,
  last_fired_at TIMESTAMPTZ,
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (rule_id, host_id)
);

CREATE TABLE watchdog_investigations (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  fleet_id      UUID NOT NULL,
  rule_id       UUID REFERENCES watchdog_rules(id) ON DELETE SET NULL,
  playbook_id   UUID REFERENCES watchdog_playbooks(id) ON DELETE SET NULL,
  trigger_type  TEXT NOT NULL,
  triggered_at  TIMESTAMPTZ NOT NULL,
  host_ids      UUID[] NOT NULL DEFAULT '{}',
  trigger_data  JSONB,
  snapshot      JSONB,
  llm_analysis  TEXT,
  llm_metadata  JSONB,
  status        TEXT NOT NULL DEFAULT 'pending',
  error         TEXT,
  completed_at  TIMESTAMPTZ,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_watchdog_investigations_fleet ON watchdog_investigations (fleet_id, triggered_at DESC);
CREATE INDEX idx_watchdog_investigations_status ON watchdog_investigations (status) WHERE status IN ('pending', 'collecting', 'analyzing');

CREATE TABLE watchdog_schedules (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  fleet_id    UUID NOT NULL,
  name        TEXT NOT NULL,
  cron_expr   TEXT NOT NULL,
  playbook_id UUID REFERENCES watchdog_playbooks(id) ON DELETE SET NULL,
  host_ids    UUID[] NOT NULL DEFAULT '{}',
  enabled     BOOLEAN NOT NULL DEFAULT TRUE,
  last_run_at TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (fleet_id, name)
);

CREATE TABLE watchdog_llm_providers (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  fleet_id      UUID NOT NULL,
  name          TEXT NOT NULL,
  provider      TEXT NOT NULL,
  endpoint      TEXT NOT NULL,
  api_key_enc   BYTEA,
  default_model TEXT NOT NULL,
  is_default    BOOLEAN NOT NULL DEFAULT FALSE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (fleet_id, name)
);

CREATE UNIQUE INDEX idx_watchdog_llm_default
  ON watchdog_llm_providers (fleet_id)
  WHERE is_default;

CREATE TABLE watchdog_webhooks (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  fleet_id    UUID NOT NULL,
  name        TEXT NOT NULL,
  url         TEXT NOT NULL,
  events      TEXT[] NOT NULL DEFAULT '{}',
  secret_enc  BYTEA,
  enabled     BOOLEAN NOT NULL DEFAULT TRUE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (fleet_id, name)
);

-- +goose Down
DROP TABLE watchdog_webhooks;
DROP INDEX idx_watchdog_llm_default;
DROP TABLE watchdog_llm_providers;
DROP TABLE watchdog_schedules;
DROP INDEX idx_watchdog_investigations_status;
DROP INDEX idx_watchdog_investigations_fleet;
DROP TABLE watchdog_investigations;
DROP TABLE watchdog_rule_state;
DROP INDEX idx_watchdog_rules_enabled;
DROP TABLE watchdog_rules;
DROP TABLE watchdog_playbooks;
