package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB wraps a SQLite database.
type DB struct {
	*sql.DB
}

// Open opens or creates a SQLite database.
func Open(dbPath string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_foreign_keys=1")
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	ret := &DB{DB: db}
	if err := db.PingContext(context.Background()); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}
	if err := ret.Migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return ret, nil
}

// Migrate runs all embedded SQL migrations.
// A schema_versions table tracks which migrations have been applied so each
// migration runs exactly once.
func (db *DB) Migrate() error {
	// Ensure the tracking table exists (idempotent).
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_versions (
			name    TEXT PRIMARY KEY,
			applied DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		return fmt.Errorf("create schema_versions: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	// Migrations run in lexical order, so "001_init.sql" always runs before
	// "002_add_session_key.sql" regardless of filesystem ordering. This is
	// intentional — later migrations may depend on earlier ones adding columns
	// or tables that they modify. If a migration fails, subsequent migrations
	// will not run (the schema_versions table is updated only on success).

	for _, name := range names {
		// Skip if already applied.
		var exists int
		if err := db.QueryRow("SELECT 1 FROM schema_versions WHERE name=?", name).Scan(&exists); err == nil {
			continue // already applied
		}
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("check schema_versions for %s: %w", name, err)
		}
		if err := db.runMigration(name); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := db.Exec("INSERT INTO schema_versions (name) VALUES (?)", name); err != nil {
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		log.Printf("migrations: applied %s", name)
	}
	return nil
}

func (db *DB) runMigration(name string) error {
	sqlBytes, err := migrationsFS.ReadFile("migrations/" + name)
	if err != nil {
		return err
	}
	_, err = db.Exec(string(sqlBytes))
	return err
}
