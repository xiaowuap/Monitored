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

INSERT INTO assets (id, stable_id, kind, name, vendor, model, health, connection, source, last_seen_at, metadata)
VALUES
  ('10000000-0000-0000-0000-000000000001', 'dmi:edge-01', 'host', 'edge-01', 'Supermicro', 'AS-1115CS-TNR', 'warning', 'online', 'agent', now() - interval '8 seconds', '{"os":"TrueNAS SCALE 25.10","agentVersion":"0.1.0","rack":"A-03","ip":"10.20.0.11"}'),
  ('10000000-0000-0000-0000-000000000002', 'zfs:tank', 'pool', 'tank', 'OpenZFS', 'RAIDZ2', 'healthy', 'online', 'truenas-api', now() - interval '12 seconds', '{"capacityBytes":87960930222080,"usedPercent":68.2,"scrubState":"idle"}'),
  ('10000000-0000-0000-0000-000000000003', 'gpu:GPU-a938', 'gpu', 'NVIDIA A40', 'NVIDIA', 'A40', 'healthy', 'online', 'nvml', now() - interval '9 seconds', '{"driver":"570.86.15","vramBytes":48318382080,"pciAddress":"0000:41:00.0"}'),
  ('10000000-0000-0000-0000-000000000004', 'nic:guid-b83f', 'nic', 'ConnectX-6 Dx', 'NVIDIA', 'MCX623106AN', 'healthy', 'online', 'agent', now() - interval '9 seconds', '{"firmware":"22.41.1000","link":"100 GbE","rdma":"RoCE v2","pciAddress":"0000:81:00.0"}'),
  ('10000000-0000-0000-0000-000000000005', 'bmc:dmi-edge-01', 'bmc', 'edge-01 BMC', 'Supermicro', 'Redfish 1.16', 'healthy', 'online', 'redfish', now() - interval '19 seconds', '{"endpoint":"10.20.1.11","protocol":"Redfish"}'),
  ('10000000-0000-0000-0000-000000000006', 'ups:ups-a', 'ups', 'UPS · Rack A', 'APC', 'Smart-UPS SRT', 'warning', 'degraded', 'nut', now() - interval '31 seconds', '{"runtimeMinutes":24,"loadPercent":73,"status":"OL LB"}'),
  ('10000000-0000-0000-0000-000000000007', 'dmi:compute-02', 'host', 'compute-02', 'Dell', 'PowerEdge R760xa', 'healthy', 'online', 'agent', now() - interval '7 seconds', '{"os":"Ubuntu 24.04","agentVersion":"0.1.0","rack":"A-04","ip":"10.20.0.12"}'),
  ('10000000-0000-0000-0000-000000000008', 'disk:wwn-0x5000c500cafe', 'disk', 'sda · Exos X20', 'Seagate', 'ST20000NM007D', 'critical', 'online', 'smart', now() - interval '52 seconds', '{"serial":"ZR5ABC12","wwn":"0x5000c500cafe","pool":"tank","bay":"12"}')
ON CONFLICT (id) DO NOTHING;

UPDATE assets SET parent_id = '10000000-0000-0000-0000-000000000001'
WHERE id IN (
  '10000000-0000-0000-0000-000000000002',
  '10000000-0000-0000-0000-000000000003',
  '10000000-0000-0000-0000-000000000004',
  '10000000-0000-0000-0000-000000000005',
  '10000000-0000-0000-0000-000000000008'
);

INSERT INTO sensor_definitions (
  id, stable_id, asset_id, source, source_path, raw_name, display_alias, category, unit,
  scale, monotonic, hardware_warning, hardware_critical, interval_seconds,
  discovery_status, quality, last_seen_at, metadata
)
VALUES
  ('20000000-0000-0000-0000-000000000001', 'hwmon:nct6798:temp2', '10000000-0000-0000-0000-000000000001', 'agent', '/sys/class/hwmon/hwmon2/temp2_input', 'nct6798/temp2', '主板 VRM 温度', 'temperature', 'Cel', 0.001, false, 90, 105, 10, 'present', 'good', now() - interval '8 seconds', '{"chip":"nct6798","channel":"temp2","labelSource":"accepted-llm"}'),
  ('20000000-0000-0000-0000-000000000002', 'nvml:a938:temperature.gpu', '10000000-0000-0000-0000-000000000003', 'nvml', 'GPU-a938/temperature.gpu', 'temperature.gpu', 'GPU 核心温度', 'temperature', 'Cel', 1, false, 83, 92, 10, 'present', 'good', now() - interval '9 seconds', '{"field":"NVML_FI_DEV_GPU_TEMP"}'),
  ('20000000-0000-0000-0000-000000000003', 'nvml:a938:power.draw', '10000000-0000-0000-0000-000000000003', 'nvml', 'GPU-a938/power.draw', 'power.draw', 'GPU 功耗', 'power', 'W', 1, false, 280, 300, 10, 'present', 'good', now() - interval '9 seconds', '{}'),
  ('20000000-0000-0000-0000-000000000004', 'smart:wwn-cafe:temperature', '10000000-0000-0000-0000-000000000008', 'smart', '/dev/sda/temperature/current', 'Temperature_Celsius', '磁盘温度', 'temperature', 'Cel', 1, false, 50, 60, 30, 'present', 'good', now() - interval '52 seconds', '{"sleepPolicy":"never-wake"}'),
  ('20000000-0000-0000-0000-000000000005', 'nut:ups-a:battery.charge', '10000000-0000-0000-0000-000000000006', 'nut', 'ups-a/battery.charge', 'battery.charge', '电池电量', 'capacity', '%', 1, false, 30, 15, 30, 'present', 'degraded', now() - interval '31 seconds', '{}'),
  ('20000000-0000-0000-0000-000000000006', 'mlx5:b83f:rx_discards_phy', '10000000-0000-0000-0000-000000000004', 'ethtool', 'enp129s0f0/statistics/rx_discards_phy', 'rx_discards_phy', '物理层接收丢弃', 'error_count', '1', 1, true, null, null, 30, 'present', 'good', now() - interval '9 seconds', '{"semanticGroup":"physical-errors"}'),
  ('20000000-0000-0000-0000-000000000007', 'hwmon:nct6798:temp7', '10000000-0000-0000-0000-000000000001', 'agent', '/sys/class/hwmon/hwmon2/temp7_input', 'nct6798/temp7', null, 'unknown', 'Cel', 0.001, false, 90, 105, 10, 'new', 'good', now() - interval '8 seconds', '{"chip":"nct6798","channel":"temp7","board":"Supermicro H13SSL-NT"}'),
  ('20000000-0000-0000-0000-000000000008', 'redfish:bmc:fan4', '10000000-0000-0000-0000-000000000005', 'redfish', '/redfish/v1/Chassis/1/Thermal#/Fans/4', 'FAN4', null, 'fan', 'rpm', 1, false, null, null, 30, 'new', 'good', now() - interval '19 seconds', '{"physicalContext":"SystemBoard","memberId":"Fan4"}')
ON CONFLICT (id) DO NOTHING;

INSERT INTO metric_series (id, sensor_id, normalized_key, labels_hash)
SELECT
  ('30000000-0000-0000-0000-00000000000' || row_number() OVER (ORDER BY id))::uuid,
  id,
  category,
  ''
FROM sensor_definitions
ON CONFLICT (id) DO NOTHING;

INSERT INTO metric_samples (series_id, observed_at, value, source, quality)
SELECT
  ms.id,
  point,
  CASE sd.stable_id
    WHEN 'hwmon:nct6798:temp2' THEN 63 + 5 * sin(extract(epoch FROM point) / 1800)
    WHEN 'nvml:a938:temperature.gpu' THEN 69 + 8 * sin(extract(epoch FROM point) / 1400)
    WHEN 'nvml:a938:power.draw' THEN 178 + 42 * sin(extract(epoch FROM point) / 1200)
    WHEN 'smart:wwn-cafe:temperature' THEN 52 + 2 * sin(extract(epoch FROM point) / 3600)
    WHEN 'nut:ups-a:battery.charge' THEN 42
    WHEN 'mlx5:b83f:rx_discards_phy' THEN 22184 + extract(epoch FROM (point - now() + interval '24 hours')) / 300
    WHEN 'hwmon:nct6798:temp7' THEN 47 + 3 * sin(extract(epoch FROM point) / 2100)
    WHEN 'redfish:bmc:fan4' THEN 6840 + 120 * sin(extract(epoch FROM point) / 900)
  END,
  sd.source,
  sd.quality
FROM metric_series ms
JOIN sensor_definitions sd ON sd.id = ms.sensor_id
CROSS JOIN generate_series(date_trunc('minute', now() - interval '24 hours'), now(), interval '5 minutes') point
WHERE NOT EXISTS (SELECT 1 FROM metric_samples LIMIT 1);

INSERT INTO alert_rules (id, name, sensor_id, rule_type, severity, expression, duration_seconds, recovery_expression)
VALUES
  ('40000000-0000-0000-0000-000000000001', '磁盘温度过高', '20000000-0000-0000-0000-000000000004', 'threshold', 'critical', '{"operator":">=","value":50}', 300, '{"operator":"<","value":47}'),
  ('40000000-0000-0000-0000-000000000002', 'UPS 电量偏低', '20000000-0000-0000-0000-000000000005', 'threshold', 'warning', '{"operator":"<","value":50}', 120, '{"operator":">=","value":55}'),
  ('40000000-0000-0000-0000-000000000003', 'RDMA 丢弃增长', '20000000-0000-0000-0000-000000000006', 'counter_delta', 'warning', '{"window":"5m","delta":">100"}', 60, '{"window":"10m","delta":"=0"}')
ON CONFLICT (id) DO NOTHING;

INSERT INTO alerts (id, fingerprint, rule_id, rule_name, asset_id, sensor_id, severity, state, summary, value, unit, started_at, updated_at)
VALUES
  ('50000000-0000-0000-0000-000000000001', 'disk-temp:wwn-cafe', '40000000-0000-0000-0000-000000000001', '磁盘温度过高', '10000000-0000-0000-0000-000000000008', '20000000-0000-0000-0000-000000000004', 'critical', 'Firing', 'sda 温度持续高于硬件警告阈值', 52, 'Cel', now() - interval '19 minutes', now() - interval '52 seconds'),
  ('50000000-0000-0000-0000-000000000002', 'ups-charge:ups-a', '40000000-0000-0000-0000-000000000002', 'UPS 电量偏低', '10000000-0000-0000-0000-000000000006', '20000000-0000-0000-0000-000000000005', 'warning', 'Firing', 'UPS 电池电量低且连接质量降级', 42, '%', now() - interval '11 minutes', now() - interval '31 seconds'),
  ('50000000-0000-0000-0000-000000000003', 'rdma-discard:guid-b83f', '40000000-0000-0000-0000-000000000003', 'RDMA 丢弃增长', '10000000-0000-0000-0000-000000000004', '20000000-0000-0000-0000-000000000006', 'warning', 'Pending', 'ConnectX-6 Dx 物理层丢弃计数正在增长', 22472, '1', now() - interval '3 minutes', now() - interval '9 seconds')
ON CONFLICT (id) DO NOTHING;

INSERT INTO incidents (id, title, severity, state, diagnosis, opened_at, updated_at)
VALUES
  ('60000000-0000-0000-0000-000000000001', '存储节点 edge-01 热风险', 'critical', 'investigating', '磁盘 sda 温度异常，邻近盘位及机箱风扇未见同步升高，优先检查盘位 12 风道与硬盘健康。', now() - interval '18 minutes', now() - interval '2 minutes')
ON CONFLICT (id) DO NOTHING;

INSERT INTO incident_alerts (incident_id, alert_id)
VALUES ('60000000-0000-0000-0000-000000000001', '50000000-0000-0000-0000-000000000001')
ON CONFLICT DO NOTHING;

INSERT INTO llm_suggestions (
  id, sensor_id, suggested_name, suggested_asset_kind, suggested_category, suggested_unit,
  confidence, evidence, context, provider, model, status
)
VALUES
  ('70000000-0000-0000-0000-000000000001', '20000000-0000-0000-0000-000000000007', '10GbE PHY 温度', 'nic', 'temperature', 'Cel', 0.86,
   '["Supermicro H13SSL-NT 的 nct6798 temp7 与板载 X710 区域相邻","通道值随网络负载而非 CPU 负载变化","硬件阈值与 NCT6798 DTS 通道一致"]',
   '{"dmi":"Supermicro AS-1115CS-TNR","board":"H13SSL-NT","chip":"nct6798","channel":"temp7","nearby":["temp6: 44 Cel","temp8: 39 Cel"],"pci":["Intel X710"]}',
   'openai-compatible', 'operator-configured', 'Pending'),
  ('70000000-0000-0000-0000-000000000002', '20000000-0000-0000-0000-000000000008', '后置系统风扇', 'fan', 'fan', 'rpm', 0.78,
   '["Redfish PhysicalContext 为 SystemBoard","Fan4 在相同机型 inventory 中对应 rear system fan","当前转速与 FAN3/FAN5 同组"]',
   '{"redfishPath":"/redfish/v1/Chassis/1/Thermal#/Fans/4","memberId":"Fan4","chassisModel":"AS-1115CS-TNR"}',
   'ollama', 'operator-configured', 'Pending')
ON CONFLICT (id) DO NOTHING;

INSERT INTO action_requests (
  id, asset_id, kind, title, command, working_directory, run_identity,
  timeout_seconds, risk, state, immutable_hash, requested_by, requested_at
)
VALUES
  ('80000000-0000-0000-0000-000000000001', '10000000-0000-0000-0000-000000000008', 'smart-test', '运行 SMART 短测', 'smartctl -t short /dev/sda', '/', 'monitored-action', 60, 'medium', 'pending', 'sha256:bf9c307f84d5a2f0d53bc81fdc0c92f5a63c7b4ef4309d88ca15b56933d3d888', 'ops@monitored.local', now() - interval '4 minutes'),
  ('80000000-0000-0000-0000-000000000002', '10000000-0000-0000-0000-000000000001', 'diagnostic', '读取盘位 12 SMART 详情', 'smartctl -x -n standby /dev/sda', '/', 'monitored-readonly', 30, 'low', 'approved', 'sha256:0ddb482dd66e4425f227c4724b98f2cbd8c1883c87a60ea34c35f1c83149670e', 'ops@monitored.local', now() - interval '16 minutes')
ON CONFLICT (id) DO NOTHING;

INSERT INTO audit_log (actor, action, resource_type, resource_id, occurred_at, details, previous_hash, entry_hash)
SELECT 'system', 'seed.created', 'deployment', 'demo', now() - interval '1 hour', '{"version":"0.1.0-dev"}', '', encode(digest('seed.created:demo', 'sha256'), 'hex')
WHERE NOT EXISTS (SELECT 1 FROM audit_log);
