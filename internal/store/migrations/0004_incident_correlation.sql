ALTER TABLE incidents
  ADD COLUMN IF NOT EXISTS correlation_key text NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS incidents_active_correlation_idx
  ON incidents(correlation_key)
  WHERE correlation_key <> '' AND state <> 'resolved';
