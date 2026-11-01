-- E4 compatibility migration: scheduler defaults are present for fresh and existing databases.
INSERT INTO settings(key,value) VALUES('scheduler.enabled','false') ON CONFLICT(key) DO NOTHING;
CREATE INDEX IF NOT EXISTS idx_runs_scheduled_for ON runs(scheduled_for);
