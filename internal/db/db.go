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
func (db *DB) Migrate() error {
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

	for _, name := range names {
		if err := db.runMigration(name); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
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
