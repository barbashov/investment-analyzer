package store

import (
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	DB *sql.DB
}

// Open opens (and creates if missing) the SQLite DB at path, then runs migrations.
// Pass ":memory:" for an in-memory database (used by tests).
func Open(path string) (*Store, error) {
	if path != ":memory:" {
		if dir := filepath.Dir(path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create db dir: %w", err)
			}
		}
	}
	dsn := path
	if path != ":memory:" {
		dsn = path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite doesn't benefit from concurrent writers; keeps modernc happy.
	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.DB.Close()
}

func (s *Store) migrate() error {
	if _, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	applied := map[int]bool{}
	rows, err := s.DB.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			_ = rows.Close()
			return err
		}
		applied[v] = true
	}
	_ = rows.Close()

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	type mig struct {
		version int
		name    string
	}
	var migs []mig
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		// expect 0001_name.sql
		under := strings.IndexByte(e.Name(), '_')
		if under <= 0 {
			return fmt.Errorf("invalid migration filename: %s", e.Name())
		}
		v, err := strconv.Atoi(e.Name()[:under])
		if err != nil {
			return fmt.Errorf("invalid migration version in %s: %w", e.Name(), err)
		}
		migs = append(migs, mig{v, e.Name()})
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].version < migs[j].version })

	for _, m := range migs {
		if applied[m.version] {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + m.name)
		if err != nil {
			return err
		}
		// schema_migrations is created in 0001 too, so swallow "already exists" by using IF NOT EXISTS in SQL.
		// We strip the schema_migrations CREATE here because we made it above unconditionally.
		stmt := stripSchemaMigrationsCreate(string(body))
		tx, err := s.DB.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", m.name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`,
			m.version, time.Now().UTC().Format(time.RFC3339)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", m.name, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// stripSchemaMigrationsCreate removes the CREATE TABLE schema_migrations block so we can
// keep the canonical schema in 0001_init.sql while also creating it eagerly above.
func stripSchemaMigrationsCreate(sqlText string) string {
	lower := strings.ToLower(sqlText)
	idx := strings.Index(lower, "create table schema_migrations")
	if idx < 0 {
		return sqlText
	}
	end := strings.Index(sqlText[idx:], ");")
	if end < 0 {
		return sqlText
	}
	return sqlText[:idx] + sqlText[idx+end+2:]
}
