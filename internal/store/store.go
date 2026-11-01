package store

import (
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	_ "modernc.org/sqlite"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct{ DB *sql.DB }

func Open(path string) (*Store, error) {
	db, e := sql.Open("sqlite", path)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	for _, p := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON", "PRAGMA synchronous=NORMAL"} {
		if _, e = db.Exec(p); e != nil {
			db.Close()
			return nil, e
		}
	}
	return &Store{db}, nil
}

type migration struct {
	version int
	name    string
	body    string
}

// loadMigrations reads every embedded migration in ascending numeric order.
func loadMigrations() ([]migration, error) {
	entries, e := fs.ReadDir(migrations, "migrations")
	if e != nil {
		return nil, e
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	out := make([]migration, 0, len(names))
	for _, name := range names {
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			return nil, fmt.Errorf("migration %q must start with a numeric version", name)
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("migration %q has a non-numeric version: %w", name, err)
		}
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: version, name: name, body: string(body)})
	}
	return out, nil
}

// Migrate applies pending migrations exactly once each. Boot is idempotent, and
// an existing database is upgraded in place rather than rebuilt.
func (s *Store) Migrate() error {
	if _, e := s.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); e != nil {
		return e
	}
	all, e := loadMigrations()
	if e != nil {
		return e
	}
	for _, m := range all {
		var n int
		if e = s.DB.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=?", m.version).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			continue
		}
		tx, e := s.DB.Begin()
		if e != nil {
			return e
		}
		if _, e = tx.Exec(m.body); e != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", m.name, e)
		}
		if _, e = tx.Exec("INSERT INTO schema_migrations VALUES (?,?)", m.version, time.Now().UTC().Format(time.RFC3339)); e != nil {
			tx.Rollback()
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) Seed() error {
	var n int
	if e := s.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return nil
	}
	h, e := HashPassword("12345678")
	if e != nil {
		return e
	}
	_, e = s.DB.Exec("INSERT INTO users(id,username,password_hash,role,must_change_password) VALUES(?,?,?,?,1)", ID(), "admin", h, "superadmin")
	return e
}
func ID() string                            { b := make([]byte, 16); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func (s *Store) Close() error               { return s.DB.Close() }
func HashPassword(p string) (string, error) { return argonHash(p) }
func VerifyPassword(h, p string) bool       { return argonVerify(h, p) }
