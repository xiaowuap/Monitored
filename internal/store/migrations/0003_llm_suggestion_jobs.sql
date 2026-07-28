CREATE TABLE IF NOT EXISTS llm_suggestion_jobs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  sensor_id uuid NOT NULL REFERENCES sensor_definitions(id) ON DELETE RESTRICT,
  provider text NOT NULL CHECK (provider IN ('openai-compatible', 'ollama')),
  model text NOT NULL,
  status text NOT NULL DEFAULT 'queued'
    CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'budget_exceeded')),
  token_usage integer NOT NULL DEFAULT 0,
  requested_by text NOT NULL,
  requested_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  context jsonb NOT NULL DEFAULT '{}'::jsonb,
  error text NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS llm_suggestion_jobs_queue_idx
  ON llm_suggestion_jobs(status, requested_at);
