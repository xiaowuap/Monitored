CREATE TABLE IF NOT EXISTS connections (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  kind text NOT NULL CHECK (kind IN ('truenas', 'redfish', 'ipmi', 'nut', 'snmp', 'ssh', 'http', 'tcp')),
  endpoint text NOT NULL,
  agent_id text,
  enabled boolean NOT NULL DEFAULT true,
  tls_mode text NOT NULL DEFAULT 'verify' CHECK (tls_mode IN ('verify', 'custom_ca', 'insecure')),
  config jsonb NOT NULL DEFAULT '{}'::jsonb,
  encrypted_secret bytea,
  status text NOT NULL DEFAULT 'untested' CHECK (status IN ('untested', 'healthy', 'degraded', 'failed')),
  status_message text NOT NULL DEFAULT '',
  capability jsonb NOT NULL DEFAULT '{}'::jsonb,
  last_tested_at timestamptz,
  last_success_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(kind, endpoint)
);

CREATE INDEX IF NOT EXISTS connections_kind_status_idx ON connections(kind, status);

CREATE TABLE IF NOT EXISTS agents (
  id text PRIMARY KEY,
  stable_host_id text NOT NULL,
  hostname text NOT NULL,
  os text NOT NULL,
  architecture text NOT NULL,
  version text NOT NULL,
  state text NOT NULL DEFAULT 'online' CHECK (state IN ('online', 'degraded', 'offline', 'revoked')),
  certificate_id text NOT NULL DEFAULT '',
  capabilities jsonb NOT NULL DEFAULT '[]'::jsonb,
  tools jsonb NOT NULL DEFAULT '[]'::jsonb,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  wal_bytes bigint NOT NULL DEFAULT 0,
  oldest_wal_at timestamptz,
  last_gap_from timestamptz,
  last_gap_to timestamptz,
  last_gap_reason text NOT NULL DEFAULT '',
  registered_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS agents_state_seen_idx ON agents(state, last_seen_at DESC);

CREATE TABLE IF NOT EXISTS agent_tasks (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  agent_id text NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('discovery', 'diagnostic', 'action')),
  state text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'dispatched', 'acknowledged', 'succeeded', 'failed', 'expired')),
  payload jsonb NOT NULL,
  immutable_hash text NOT NULL DEFAULT '',
  attempt integer NOT NULL DEFAULT 0,
  not_before timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL DEFAULT now() + interval '15 minutes',
  dispatched_at timestamptz,
  acknowledged_at timestamptz,
  finished_at timestamptz,
  result jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS agent_tasks_dispatch_idx
  ON agent_tasks(agent_id, state, not_before, created_at);

CREATE TABLE IF NOT EXISTS discovery_snapshots (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  run_id uuid REFERENCES discovery_runs(id) ON DELETE CASCADE,
  agent_id text,
  observed_stable_ids jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS maintenance_windows (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  starts_at timestamptz NOT NULL,
  ends_at timestamptz NOT NULL,
  asset_id uuid REFERENCES assets(id) ON DELETE CASCADE,
  rule_id uuid REFERENCES alert_rules(id) ON DELETE CASCADE,
  created_by text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (ends_at > starts_at)
);

ALTER TABLE alert_rules
  ADD COLUMN IF NOT EXISTS asset_kind text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS missing_data_mode text NOT NULL DEFAULT 'ignore'
    CHECK (missing_data_mode IN ('ignore', 'alert', 'resolve')),
  ADD COLUMN IF NOT EXISTS maintenance_window_id uuid REFERENCES maintenance_windows(id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS alert_transitions (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  alert_id uuid NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
  previous_state text NOT NULL,
  next_state text NOT NULL,
  reason text NOT NULL,
  value double precision,
  occurred_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS alert_transitions_alert_idx ON alert_transitions(alert_id, occurred_at DESC);

CREATE TABLE IF NOT EXISTS notification_channels (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  kind text NOT NULL CHECK (kind IN ('telegram', 'feishu', 'webhook', 'smtp', 'bark')),
  enabled boolean NOT NULL DEFAULT true,
  config jsonb NOT NULL DEFAULT '{}'::jsonb,
  encrypted_secret bytea,
  last_tested_at timestamptz,
  last_status text NOT NULL DEFAULT 'untested',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS notification_routes (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  channel_id uuid NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
  min_severity text NOT NULL DEFAULT 'warning',
  asset_kinds jsonb NOT NULL DEFAULT '[]'::jsonb,
  group_window_seconds integer NOT NULL DEFAULT 120,
  repeat_interval_seconds integer NOT NULL DEFAULT 3600,
  send_recovery boolean NOT NULL DEFAULT true,
  locale text NOT NULL DEFAULT 'zh-CN',
  enabled boolean NOT NULL DEFAULT true
);

CREATE TABLE IF NOT EXISTS notification_deliveries (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  channel_id uuid NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
  alert_id uuid REFERENCES alerts(id) ON DELETE SET NULL,
  dedup_key text NOT NULL,
  status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'sending', 'sent', 'retrying', 'failed')),
  attempt integer NOT NULL DEFAULT 0,
  not_before timestamptz NOT NULL DEFAULT now(),
  payload jsonb NOT NULL,
  last_error text NOT NULL DEFAULT '',
  sent_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS notification_delivery_queue_idx
  ON notification_deliveries(status, not_before, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS notification_delivery_dedup_idx
  ON notification_deliveries(dedup_key);

CREATE TABLE IF NOT EXISTS diagnoses (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  incident_id uuid REFERENCES incidents(id) ON DELETE SET NULL,
  asset_id uuid REFERENCES assets(id) ON DELETE SET NULL,
  status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'budget_exceeded')),
  provider text NOT NULL,
  model text NOT NULL,
  summary text NOT NULL DEFAULT '',
  hypotheses jsonb NOT NULL DEFAULT '[]'::jsonb,
  evidence jsonb NOT NULL DEFAULT '[]'::jsonb,
  data_gaps jsonb NOT NULL DEFAULT '[]'::jsonb,
  checks jsonb NOT NULL DEFAULT '[]'::jsonb,
  actions jsonb NOT NULL DEFAULT '[]'::jsonb,
  rollback text NOT NULL DEFAULT '',
  confidence double precision NOT NULL DEFAULT 0 CHECK (confidence BETWEEN 0 AND 1),
  token_usage integer NOT NULL DEFAULT 0,
  requested_by text NOT NULL,
  requested_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  context jsonb NOT NULL DEFAULT '{}'::jsonb,
  error text NOT NULL DEFAULT ''
);

CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE IF NOT EXISTS users (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email citext NOT NULL UNIQUE,
  password_hash text NOT NULL DEFAULT '',
  role text NOT NULL CHECK (role IN ('viewer', 'operator', 'admin')),
  enabled boolean NOT NULL DEFAULT true,
  totp_secret_encrypted bytea,
  last_login_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE action_requests
  ADD COLUMN IF NOT EXISTS agent_id text,
  ADD COLUMN IF NOT EXISTS canonical_payload jsonb NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN IF NOT EXISTS execution_token_expires_at timestamptz;

CREATE TABLE IF NOT EXISTS source_preferences (
  sensor_id uuid NOT NULL REFERENCES sensor_definitions(id) ON DELETE CASCADE,
  source text NOT NULL,
  priority integer NOT NULL DEFAULT 100,
  freshness_seconds integer NOT NULL DEFAULT 120,
  enabled boolean NOT NULL DEFAULT true,
  PRIMARY KEY(sensor_id, source)
);
