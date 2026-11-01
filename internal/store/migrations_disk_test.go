package store

import (
	"path/filepath"
	"testing"
)

// Operators run one binary against one SQLite file, so migrations must work on
// a real file and survive a restart without re-applying or losing rows.
func TestMigrationOnDiskSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cronsoul.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = first.Migrate(); err != nil {
		t.Fatalf("initial migration: %v", err)
	}
	if err = first.Seed(); err != nil {
		t.Fatal(err)
	}
	if _, err = first.DB.Exec(`INSERT INTO jobs(id,key,name,service,method,url,cron) VALUES('j1','disk-job','Disk','svc','GET','https://example.test/x','0 1 * * *')`); err != nil {
		t.Fatal(err)
	}
	var applications int
	if err = first.DB.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&applications); err != nil {
		t.Fatal(err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if err = second.Migrate(); err != nil {
		t.Fatalf("migration after reopen: %v", err)
	}
	var after int
	if err = second.DB.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != applications {
		t.Fatalf("reopen re-applied migrations: %d -> %d", applications, after)
	}
	var jobs, users int
	if err = second.DB.QueryRow("SELECT COUNT(*) FROM jobs").Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err = second.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&users); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 || users != 1 {
		t.Fatalf("data lost across reopen: jobs=%d users=%d", jobs, users)
	}
	var fk int
	if err = second.DB.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Fatalf("foreign keys must be enabled on a reopened database, got %d", fk)
	}
}
