CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS assets (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  stable_id text NOT NULL UNIQUE,
  parent_id uuid REFERENCES assets(id) ON DELETE SET NULL,
  kind text NOT NULL,
  name text NOT NULL,
  vendor text NOT NULL DEFAULT '',
  model text NOT NULL DEFAULT '',
  health text NOT NULL DEFAULT 'unknown' CHECK (health IN ('healthy', 'warning', 'critical', 'unknown')),
  connection text NOT NULL DEFAULT 'offline' CHECK (connection IN ('online', 'degraded', 'offline')),
  source text NOT NULL DEFAULT 'agent',
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS assets_parent_idx ON assets(parent_id);
CREATE INDEX IF NOT EXISTS assets_kind_idx ON assets(kind);
CREATE INDEX IF NOT EXISTS assets_last_seen_idx ON assets(last_seen_at DESC);

CREATE TABLE IF NOT EXISTS sensor_definitions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  stable_id text NOT NULL UNIQUE,
  asset_id uuid NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,
  source text NOT NULL,
  source_path text NOT NULL,
  raw_name text NOT NULL,
  display_alias text,
  category text NOT NULL DEFAULT 'unknown',
  unit text NOT NULL DEFAULT '1',
  scale double precision NOT NULL DEFAULT 1,
  monotonic boolean NOT NULL DEFAULT false,
  hardware_warning double precision,
  hardware_critical double precision,
  user_warning double precision,
  user_critical double precision,
  interval_seconds integer NOT NULL DEFAULT 30 CHECK (interval_seconds > 0),
  discovery_status text NOT NULL DEFAULT 'new' CHECK (discovery_status IN ('new', 'present', 'stale', 'removed')),
  missing_discoveries integer NOT NULL DEFAULT 0,
  source_priority integer NOT NULL DEFAULT 100,
  quality text NOT NULL DEFAULT 'good' CHECK (quality IN ('good', 'degraded', 'unavailable', 'error')),
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS sensors_asset_idx ON sensor_definitions(asset_id);
CREATE INDEX IF NOT EXISTS sensors_discovery_idx ON sensor_definitions(discovery_status, last_seen_at DESC);
CREATE INDEX IF NOT EXISTS sensors_category_idx ON sensor_definitions(category);

CREATE TABLE IF NOT EXISTS metric_series (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  sensor_id uuid NOT NULL REFERENCES sensor_definitions(id) ON DELETE RESTRICT,
  normalized_key text NOT NULL,
  labels jsonb NOT NULL DEFAULT '{}'::jsonb,
  labels_hash text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(sensor_id, normalized_key, labels_hash)
);

CREATE INDEX IF NOT EXISTS metric_series_sensor_idx ON metric_series(sensor_id);

CREATE TABLE IF NOT EXISTS metric_samples (
  id bigint GENERATED ALWAYS AS IDENTITY,
  series_id uuid NOT NULL REFERENCES metric_series(id) ON DELETE RESTRICT,
  observed_at timestamptz NOT NULL,
  recorded_at timestamptz NOT NULL DEFAULT now(),
  value double precision,
  text_value text,
  source text NOT NULL,
  quality text NOT NULL DEFAULT 'good',
  collection_error text NOT NULL DEFAULT '',
  PRIMARY KEY (id, recorded_at),
  CHECK (value IS NOT NULL OR text_value IS NOT NULL OR collection_error <> '')
) PARTITION BY RANGE (recorded_at);

CREATE TABLE IF NOT EXISTS metric_samples_default
  PARTITION OF metric_samples DEFAULT;

CREATE INDEX IF NOT EXISTS metric_samples_series_time_idx
  ON metric_samples_default(series_id, observed_at DESC);

CREATE TABLE IF NOT EXISTS metric_1m (
  series_id uuid NOT NULL REFERENCES metric_series(id) ON DELETE CASCADE,
  bucket timestamptz NOT NULL,
  min_value double precision NOT NULL,
  avg_value double precision NOT NULL,
  max_value double precision NOT NULL,
  sample_count integer NOT NULL,
  first_value double precision NOT NULL,
  last_value double precision NOT NULL,
  PRIMARY KEY (series_id, bucket)
);

CREATE TABLE IF NOT EXISTS metric_1h (
  series_id uuid NOT NULL REFERENCES metric_series(id) ON DELETE CASCADE,
  bucket timestamptz NOT NULL,
  min_value double precision NOT NULL,
  avg_value double precision NOT NULL,
  max_value double precision NOT NULL,
  sample_count integer NOT NULL,
  first_value double precision NOT NULL,
  last_value double precision NOT NULL,
  PRIMARY KEY (series_id, bucket)
);

CREATE TABLE IF NOT EXISTS discovery_runs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  agent_id text,
  kind text NOT NULL DEFAULT 'full',
  state text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
  requested_by text NOT NULL,
  started_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,
  assets_found integer NOT NULL DEFAULT 0,
  sensors_found integer NOT NULL DEFAULT 0,
  unknown_found integer NOT NULL DEFAULT 0,
  progress integer NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
  details jsonb NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE IF NOT EXISTS alert_rules (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  sensor_id uuid REFERENCES sensor_definitions(id) ON DELETE SET NULL,
  rule_type text NOT NULL,
  severity text NOT NULL,
  expression jsonb NOT NULL,
  duration_seconds integer NOT NULL DEFAULT 0,
  recovery_expression jsonb NOT NULL DEFAULT '{}'::jsonb,
  enabled boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS alerts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  fingerprint text NOT NULL UNIQUE,
  rule_id uuid REFERENCES alert_rules(id) ON DELETE SET NULL,
  rule_name text NOT NULL,
  asset_id uuid NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,
  sensor_id uuid REFERENCES sensor_definitions(id) ON DELETE SET NULL,
  severity text NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
  state text NOT NULL CHECK (state IN ('OK', 'Pending', 'Firing', 'Resolved', 'Silenced')),
  summary text NOT NULL,
  value double precision,
  unit text NOT NULL DEFAULT '',
  started_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  resolved_at timestamptz
);

CREATE INDEX IF NOT EXISTS alerts_state_idx ON alerts(state, updated_at DESC);

CREATE TABLE IF NOT EXISTS incidents (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  title text NOT NULL,
  severity text NOT NULL,
  state text NOT NULL CHECK (state IN ('open', 'investigating', 'mitigated', 'resolved')),
  diagnosis text,
  opened_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  resolved_at timestamptz
);

CREATE TABLE IF NOT EXISTS incident_alerts (
  incident_id uuid NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
  alert_id uuid NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
  PRIMARY KEY (incident_id, alert_id)
);

CREATE TABLE IF NOT EXISTS llm_suggestions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  sensor_id uuid NOT NULL REFERENCES sensor_definitions(id) ON DELETE RESTRICT,
  suggested_name text NOT NULL,
  suggested_asset_kind text NOT NULL,
  suggested_category text NOT NULL,
  suggested_unit text NOT NULL,
  confidence double precision NOT NULL CHECK (confidence BETWEEN 0 AND 1),
  evidence jsonb NOT NULL DEFAULT '[]'::jsonb,
  context jsonb NOT NULL DEFAULT '{}'::jsonb,
  provider text NOT NULL,
  model text NOT NULL,
  status text NOT NULL DEFAULT 'Pending' CHECK (status IN ('Pending', 'Accepted', 'Rejected')),
  created_at timestamptz NOT NULL DEFAULT now(),
  reviewed_at timestamptz,
  reviewed_by text
);

CREATE TABLE IF NOT EXISTS action_requests (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  asset_id uuid NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,
  kind text NOT NULL,
  title text NOT NULL,
  command text NOT NULL,
  working_directory text NOT NULL DEFAULT '/',
  environment jsonb NOT NULL DEFAULT '{}'::jsonb,
  run_identity text NOT NULL DEFAULT 'monitored-action',
  timeout_seconds integer NOT NULL DEFAULT 60 CHECK (timeout_seconds BETWEEN 1 AND 3600),
  risk text NOT NULL CHECK (risk IN ('low', 'medium', 'high', 'root')),
  state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'approved', 'running', 'succeeded', 'failed', 'rejected', 'expired')),
  immutable_hash text NOT NULL,
  one_time_token_hash text,
  requested_by text NOT NULL,
  requested_at timestamptz NOT NULL DEFAULT now(),
  approved_by text,
  approved_at timestamptz,
  result jsonb NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE IF NOT EXISTS audit_log (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  actor text NOT NULL,
  action text NOT NULL,
  resource_type text NOT NULL,
  resource_id text NOT NULL,
  occurred_at timestamptz NOT NULL DEFAULT now(),
  remote_addr text NOT NULL DEFAULT '',
  details jsonb NOT NULL DEFAULT '{}'::jsonb,
  previous_hash text NOT NULL DEFAULT '',
  entry_hash text NOT NULL
);

CREATE INDEX IF NOT EXISTS audit_occurred_idx ON audit_log(occurred_at DESC);
