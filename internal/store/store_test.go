package store

import (
	"database/sql"
	"strings"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMigrationCreatesSchemaAndIsIdempotent(t *testing.T) {
	s := testStore(t)
	var n int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n < 14 {
		t.Fatalf("expected schema tables, got %d", n)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	var latest int
	if err := s.DB.QueryRow("SELECT COUNT(*), MAX(version) FROM schema_migrations").Scan(&n, &latest); err != nil {
		t.Fatal(err)
	}
	if n == 0 || latest != n {
		t.Fatalf("migrations applied=%d latest=%d", n, latest)
	}
	applied := n
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != applied {
		t.Fatalf("re-running migrations duplicated rows: %d -> %d", applied, n)
	}
}

func TestMigrationAddsRunAssertionOverrideAndHistoryIndexes(t *testing.T) {
	s := testStore(t)
	var ddl string
	if err := s.DB.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='runs'").Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ddl, "assertions_override") {
		t.Fatalf("runs table missing assertions_override: %s", ddl)
	}
	for _, idx := range []string{"idx_runs_created", "idx_runs_trigger"} {
		var name string
		if err := s.DB.QueryRow("SELECT name FROM sqlite_master WHERE type='index' AND name=?", idx).Scan(&name); err != nil {
			t.Fatalf("missing index %s: %v", idx, err)
		}
	}
}

func TestMigrationUpgradesExistingDatabaseInPlace(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	// Simulate a database created before E5/E6: only 0001 is recorded.
	if _, err = s.DB.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	init, err := migrations.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(string(init)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("INSERT INTO schema_migrations VALUES (1,'2026-10-01T00:00:00Z')"); err != nil {
		t.Fatal(err)
	}
	// existing data must survive the upgrade
	if _, err = s.DB.Exec("INSERT INTO users(id,username,password_hash,role) VALUES('u1','legacy','x','admin')"); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(); err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}
	var applied int
	if err = s.DB.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied < 3 {
		t.Fatalf("pending migrations not applied: %d", applied)
	}
	var ddl string
	if err = s.DB.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='runs'").Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ddl, "assertions_override") {
		t.Fatalf("runs not upgraded: %s", ddl)
	}
	var users int
	if err = s.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 1 {
		t.Fatalf("existing rows lost: %d", users)
	}
	if _, err = s.DB.Exec("INSERT INTO runs(id,job_id,trigger,status,assertions_override) VALUES('r','missing','manual','queued',1)"); err == nil {
		t.Fatal("foreign keys must stay enforced after upgrade")
	}
}

func TestSeedDefaultSuperadminOnlyWhenEmpty(t *testing.T) {
	s := testStore(t)
	if err := s.Seed(); err != nil {
		t.Fatal(err)
	}
	var username, role string
	var must int
	var hash string
	if err := s.DB.QueryRow("SELECT username,role,must_change_password,password_hash FROM users").Scan(&username, &role, &must, &hash); err != nil {
		t.Fatal(err)
	}
	if username != "admin" || role != "superadmin" || must != 1 || !VerifyPassword(hash, "12345678") {
		t.Fatalf("bad seed: %s %s %d", username, role, must)
	}
	if err := s.Seed(); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&must); err != nil {
		t.Fatal(err)
	}
	if must != 1 {
		t.Fatalf("seed duplicated users: %d", must)
	}
}

func TestMigrationEnforcesForeignKeys(t *testing.T) {
	s := testStore(t)
	_, err := s.DB.Exec("INSERT INTO sessions(id,user_id,token_hash,expires_at) VALUES('x','missing','hash','2099-01-01T00:00:00Z')")
	if err == nil {
		t.Fatal("expected foreign key violation")
	}
	_ = sql.ErrNoRows
}
