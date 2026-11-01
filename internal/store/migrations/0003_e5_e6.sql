-- E5/E6: run history pagination support and the per-run assertion override.
-- NULL means "follow the job's stored assertions_enabled"; 0/1 is a one-shot
-- decision captured at Play time so the worker evaluates what the caller asked for.
ALTER TABLE runs ADD COLUMN assertions_override INTEGER;

CREATE INDEX IF NOT EXISTS idx_runs_created ON runs(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_runs_trigger ON runs(trigger);
