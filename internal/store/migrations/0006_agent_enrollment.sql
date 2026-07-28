CREATE TABLE IF NOT EXISTS agent_enrollment_tokens (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  token_hash bytea NOT NULL UNIQUE,
  agent_id text NOT NULL,
  requested_by text NOT NULL,
  expires_at timestamptz NOT NULL,
  used_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (expires_at > created_at)
);

CREATE INDEX IF NOT EXISTS agent_enrollment_tokens_active_idx
  ON agent_enrollment_tokens(agent_id, expires_at)
  WHERE used_at IS NULL;
