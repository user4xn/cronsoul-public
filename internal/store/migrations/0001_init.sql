-- CronSoul — skema SQLite (fase 1)
-- Aturan: waktu disimpan TEXT ISO-8601 UTC, ID UUIDv7 TEXT, JSON disimpan TEXT + CHECK(json_valid).

PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

-- ============================================================
-- Pengguna & sesi
-- ============================================================
CREATE TABLE users (
  id                   TEXT PRIMARY KEY,
  username             TEXT NOT NULL UNIQUE,
  email                TEXT,
  password_hash        TEXT NOT NULL,                -- argon2id encoded string
  role                 TEXT NOT NULL CHECK (role IN ('viewer','qa','admin','superadmin')),
  must_change_password INTEGER NOT NULL DEFAULT 0,
  disabled             INTEGER NOT NULL DEFAULT 0,
  created_by           TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  last_login_at        TEXT
);

CREATE TABLE sessions (
  id          TEXT PRIMARY KEY,
  user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash  TEXT NOT NULL UNIQUE,                  -- sha256(token)
  ip          TEXT,
  user_agent  TEXT,
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  expires_at  TEXT NOT NULL,
  revoked_at  TEXT
);
CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);

-- ============================================================
-- Endpoint & konfigurasi eksekusi
-- ============================================================
CREATE TABLE jobs (
  id                          TEXT PRIMARY KEY,
  key                         TEXT NOT NULL UNIQUE,
  name                        TEXT NOT NULL,
  service                     TEXT NOT NULL,
  method                      TEXT NOT NULL CHECK (method IN ('GET','HEAD','POST','PUT','PATCH','DELETE')),
  url                         TEXT NOT NULL,
  scheme                      TEXT NOT NULL DEFAULT 'https' CHECK (scheme IN ('http','https')),
  auth_type                   TEXT NOT NULL DEFAULT 'none'
                                CHECK (auth_type IN ('none','bearer','api_key','basic','hmac_sha256','custom_header')),
  auth_cipher                 TEXT,                  -- AES-GCM(base64)
  headers_json                TEXT CHECK (headers_json IS NULL OR json_valid(headers_json)),
  query_json                  TEXT CHECK (query_json IS NULL OR json_valid(query_json)),
  body_json                   TEXT CHECK (body_json IS NULL OR json_valid(body_json)),
  cron                        TEXT NOT NULL,
  timezone                    TEXT NOT NULL DEFAULT 'Asia/Jakarta',
  next_run_at                 TEXT,
  timeout_s                   INTEGER NOT NULL DEFAULT 30 CHECK (timeout_s BETWEEN 1 AND 600),
  singleton                   INTEGER NOT NULL DEFAULT 1,
  concurrency_key             TEXT,
  risk_tier                   TEXT NOT NULL DEFAULT 'B' CHECK (risk_tier IN ('A','B','C')),
  idempotent                  INTEGER NOT NULL DEFAULT 0,
  dry_run_param               TEXT,
  expected_status             INTEGER,
  tags_json                   TEXT CHECK (tags_json IS NULL OR json_valid(tags_json)),
  owner                       TEXT,
  enabled                     INTEGER NOT NULL DEFAULT 1,
  retry_max                   INTEGER NOT NULL DEFAULT 0 CHECK (retry_max BETWEEN 0 AND 5),
  retry_backoff               TEXT NOT NULL DEFAULT 'exponential' CHECK (retry_backoff IN ('exponential','fixed')),
  retry_on_json               TEXT CHECK (retry_on_json IS NULL OR json_valid(retry_on_json)),
  notify_on_json              TEXT CHECK (notify_on_json IS NULL OR json_valid(notify_on_json)),
  notify_email_json           TEXT CHECK (notify_email_json IS NULL OR json_valid(notify_email_json)),
  notify_telegram_json        TEXT CHECK (notify_telegram_json IS NULL OR json_valid(notify_telegram_json)),
  assertions_json             TEXT CHECK (assertions_json IS NULL OR json_valid(assertions_json)),
  assertions_enabled          INTEGER NOT NULL DEFAULT 0,
  simulated_time_header       TEXT NOT NULL DEFAULT 'X-Simulated-Time',
  clock_aware                 INTEGER NOT NULL DEFAULT 0,
  allow_simulated_time_prod   INTEGER NOT NULL DEFAULT 0,
  created_by                  TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at                  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at                  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX idx_jobs_enabled_next ON jobs(enabled, next_run_at);
CREATE INDEX idx_jobs_service ON jobs(service);
CREATE INDEX idx_jobs_tier ON jobs(risk_tier);

-- ============================================================
-- Eksekusi
-- ============================================================
CREATE TABLE runs (
  id                 TEXT PRIMARY KEY,
  job_id             TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  trigger            TEXT NOT NULL CHECK (trigger IN ('manual','schedule','retry','replay')),
  triggered_by       TEXT REFERENCES users(id) ON DELETE SET NULL,
  scheduled_for      TEXT,
  started_at         TEXT,
  finished_at        TEXT,
  status             TEXT NOT NULL CHECK (status IN
                       ('queued','running','success','failed','timeout','failed_after_retries','cancelled')),
  http_status        INTEGER,
  duration_ms        INTEGER,
  attempts           INTEGER NOT NULL DEFAULT 0,
  simulated          INTEGER NOT NULL DEFAULT 0,
  simulated_time     TEXT,
  dry_run            INTEGER NOT NULL DEFAULT 0,
  response_truncated INTEGER NOT NULL DEFAULT 0,
  assertion_passed   INTEGER,
  error              TEXT,
  request_excerpt    TEXT,
  response_excerpt   TEXT,
  created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX idx_runs_job_time ON runs(job_id, created_at DESC);
CREATE INDEX idx_runs_status ON runs(status);
CREATE INDEX idx_runs_open ON runs(job_id) WHERE status IN ('queued','running');
CREATE UNIQUE INDEX uq_runs_scheduled_window ON runs(job_id, scheduled_for) WHERE trigger='schedule' AND scheduled_for IS NOT NULL;

-- Satu run aktif per job (singleton) — ditegakkan di level DB
CREATE UNIQUE INDEX uq_runs_singleton ON runs(job_id)
  WHERE status IN ('queued','running');

CREATE TABLE run_attempts (
  id               TEXT PRIMARY KEY,
  run_id           TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  attempt_no       INTEGER NOT NULL,
  started_at       TEXT,
  finished_at      TEXT,
  status           TEXT NOT NULL CHECK (status IN ('success','failed','timeout','blocked')),
  http_status      INTEGER,
  duration_ms      INTEGER,
  error            TEXT,
  response_excerpt TEXT,
  next_attempt_at  TEXT,
  UNIQUE (run_id, attempt_no)
);
CREATE INDEX idx_attempts_run ON run_attempts(run_id);
CREATE INDEX idx_attempts_pending ON run_attempts(next_attempt_at) WHERE next_attempt_at IS NOT NULL;

CREATE TABLE assertion_results (
  id       TEXT PRIMARY KEY,
  run_id   TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  kind     TEXT NOT NULL,
  target   TEXT,
  expected TEXT,
  actual   TEXT,
  passed   INTEGER NOT NULL
);
CREATE INDEX idx_assert_run ON assertion_results(run_id);

-- ============================================================
-- Notifikasi
-- ============================================================
CREATE TABLE outbox (
  id              TEXT PRIMARY KEY,
  kind            TEXT NOT NULL,                 -- failure | missed_run | recovered | assertion_failed | success | slow_run
  job_id          TEXT REFERENCES jobs(id) ON DELETE CASCADE,
  run_id          TEXT REFERENCES runs(id) ON DELETE CASCADE,
  channel         TEXT NOT NULL,                 -- smtp | telegram | webhook
  target          TEXT,                          -- email / chat_id
  payload_json    TEXT NOT NULL CHECK (json_valid(payload_json)),
  attempts        INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TEXT NOT NULL,
  sent_at         TEXT,
  status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','sent','failed')),
  last_error      TEXT
);
CREATE INDEX idx_outbox_pending ON outbox(status, next_attempt_at);

CREATE TABLE alerts (
  id           TEXT PRIMARY KEY,
  job_id       TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  window_start TEXT NOT NULL,
  kind         TEXT NOT NULL,                    -- missed_run | failure | assertion_failed
  channel      TEXT,
  sent_at      TEXT,
  resolved_at  TEXT,
  UNIQUE (job_id, window_start, kind)            -- dedupe per jendela jadwal
);

-- ============================================================
-- Secret, kanal, allowlist, audit
-- ============================================================
CREATE TABLE secrets (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL UNIQUE,
  value_cipher TEXT NOT NULL,
  created_by  TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  rotated_at  TEXT
);

CREATE TABLE channels (
  id            TEXT PRIMARY KEY,
  kind          TEXT NOT NULL CHECK (kind IN ('smtp','telegram','webhook')),
  name          TEXT NOT NULL,
  config_cipher TEXT NOT NULL,
  enabled       INTEGER NOT NULL DEFAULT 1,
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TABLE allowlist (
  id         TEXT PRIMARY KEY,
  pattern    TEXT NOT NULL UNIQUE,               -- api.example.com | *.example.com | host:8443
  note       TEXT,
  created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TABLE audit_log (
  id          TEXT PRIMARY KEY,
  actor_id    TEXT REFERENCES users(id) ON DELETE SET NULL,
  actor_name  TEXT NOT NULL,
  action      TEXT NOT NULL,                     -- job.create | job.run | auth.login | user.create | ...
  target_type TEXT,
  target_id   TEXT,
  before_json TEXT CHECK (before_json IS NULL OR json_valid(before_json)),
  after_json  TEXT CHECK (after_json IS NULL OR json_valid(after_json)),
  ip          TEXT,
  at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX idx_audit_time ON audit_log(at DESC);
CREATE INDEX idx_audit_actor ON audit_log(actor_id);

-- Audit bersifat append-only
CREATE TRIGGER trg_audit_no_update BEFORE UPDATE ON audit_log
BEGIN SELECT RAISE(ABORT, 'audit_log tidak boleh diubah'); END;
CREATE TRIGGER trg_audit_no_delete BEFORE DELETE ON audit_log
BEGIN SELECT RAISE(ABORT, 'audit_log tidak boleh dihapus'); END;

CREATE TABLE settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_by TEXT REFERENCES users(id) ON DELETE SET NULL,
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

-- Seed pengaturan keamanan (default: ketat)
INSERT INTO settings (key, value) VALUES
  ('scheduler.enabled', 'false'),
  ('allow_insecure_targets', 'false'),          -- izinkan job memanggil http://
  ('allow_insecure_targets_until', ''),         -- batas waktu otomatis (ISO-8601 UTC)
  ('allow_private_egress', 'false'),            -- izinkan target RFC1918 (10.x/172.16/192.168) di luar allowlist
  ('max_response_bytes', '32768'),
  ('follow_redirects', 'false'),                -- selalu false di fase 1
  ('session_ttl_hours', '12');

-- ============================================================
-- Seed superadmin
-- ============================================================
-- Hash argon2id dibuat oleh aplikasi saat boot pertama (bukan ditulis manual di SQL).
-- Nilai di bawah hanya contoh bentuk: $argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>
-- INSERT INTO users (id, username, password_hash, role, must_change_password)
-- VALUES ('0192f0c4-0000-7000-8000-000000000001', 'admin', '<hash-dari-12345678>', 'superadmin', 1);
