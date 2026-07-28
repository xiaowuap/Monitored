ALTER TABLE agent_tasks
  DROP CONSTRAINT IF EXISTS agent_tasks_kind_check;

ALTER TABLE agent_tasks
  ADD CONSTRAINT agent_tasks_kind_check
  CHECK (kind IN ('discovery', 'diagnostic', 'action', 'update'));

ALTER TABLE agents
  ADD COLUMN IF NOT EXISTS desired_version text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS last_update_status text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS last_update_at timestamptz,
  ADD COLUMN IF NOT EXISTS last_update_error text NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS agent_tasks_one_active_update_idx
  ON agent_tasks(agent_id)
  WHERE kind = 'update' AND state IN ('queued', 'dispatched', 'acknowledged');
