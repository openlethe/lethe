package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
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
		// Skip if already applied or already satisfied by an older migration name.
		applied, err := db.migrationAppliedOrSatisfied(name)
		if err != nil {
			return fmt.Errorf("check schema_versions for %s: %w", name, err)
		}
		if applied {
			continue // already applied
		}
		if err := db.runMigration(name); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
		log.Printf("migrations: applied %s", name)
	}
	return nil
}

func (db *DB) migrationAppliedOrSatisfied(name string) (bool, error) {
	var exists int
	if err := db.QueryRow("SELECT 1 FROM schema_versions WHERE name=?", name).Scan(&exists); err == nil {
		return true, nil
	} else if err != sql.ErrNoRows {
		return false, err
	}

	// Early development builds of migration 006 recorded this version name
	// manually from inside the SQL file, before Migrate appended the .sql suffix.
	// Treat that legacy marker as applied and backfill the canonical name so
	// upgraded installations do not try to rebuild an already-migrated events table.
	if name == "006_project_scoped_events.sql" {
		if err := db.QueryRow("SELECT 1 FROM schema_versions WHERE name=?", "006_project_scoped_events").Scan(&exists); err == nil {
			if _, err := db.Exec("INSERT OR IGNORE INTO schema_versions (name) VALUES (?)", name); err != nil {
				return false, err
			}
			return true, nil
		} else if err != sql.ErrNoRows {
			return false, err
		}

		projectIDExists, sessionIDNullable, err := db.projectScopedEventsSchemaPresent()
		if err != nil {
			return false, err
		}
		if projectIDExists && sessionIDNullable {
			if _, err := db.Exec("INSERT OR IGNORE INTO schema_versions (name) VALUES (?)", name); err != nil {
				return false, err
			}
			return true, nil
		}
	}

	return false, nil
}

func (db *DB) projectScopedEventsSchemaPresent() (bool, bool, error) {
	rows, err := db.Query("PRAGMA table_info(events)")
	if err != nil {
		return false, false, err
	}
	defer rows.Close()

	projectIDExists := false
	sessionIDNullable := false
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var defaultValue interface{}
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return false, false, err
		}
		if name == "project_id" {
			projectIDExists = true
		}
		if name == "session_id" {
			sessionIDNullable = notNull == 0
		}
	}
	if err := rows.Err(); err != nil {
		return false, false, err
	}
	return projectIDExists, sessionIDNullable, nil
}

func (db *DB) runMigration(name string) error {
	sqlBytes, err := migrationsFS.ReadFile("migrations/" + name)
	if err != nil {
		return err
	}
	statements, err := splitSQLStatements(string(sqlBytes))
	if err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("INSERT INTO schema_versions (name) VALUES (?)", name); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return tx.Commit()
}

func splitSQLStatements(sqlText string) ([]string, error) {
	var statements []string
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	inLineComment := false
	inBlockComment := false

	for i := 0; i < len(sqlText); i++ {
		ch := sqlText[i]
		next := byte(0)
		if i+1 < len(sqlText) {
			next = sqlText[i+1]
		}

		if inLineComment {
			current.WriteByte(ch)
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			current.WriteByte(ch)
			if ch == '*' && next == '/' {
				current.WriteByte(next)
				i++
				inBlockComment = false
			}
			continue
		}

		if !inSingleQuote && !inDoubleQuote && ch == '-' && next == '-' {
			current.WriteByte(ch)
			current.WriteByte(next)
			i++
			inLineComment = true
			continue
		}
		if !inSingleQuote && !inDoubleQuote && ch == '/' && next == '*' {
			current.WriteByte(ch)
			current.WriteByte(next)
			i++
			inBlockComment = true
			continue
		}

		if ch == '\'' && !inDoubleQuote {
			current.WriteByte(ch)
			if inSingleQuote && next == '\'' {
				current.WriteByte(next)
				i++
				continue
			}
			inSingleQuote = !inSingleQuote
			continue
		}
		if ch == '"' && !inSingleQuote {
			current.WriteByte(ch)
			if inDoubleQuote && next == '"' {
				current.WriteByte(next)
				i++
				continue
			}
			inDoubleQuote = !inDoubleQuote
			continue
		}

		if ch == ';' && !inSingleQuote && !inDoubleQuote {
			stmt := strings.TrimSpace(current.String())
			if stmt != "" {
				statements = append(statements, stmt)
			}
			current.Reset()
			continue
		}

		current.WriteByte(ch)
	}

	if inSingleQuote || inDoubleQuote || inBlockComment {
		return nil, fmt.Errorf("unterminated SQL statement")
	}
	stmt := strings.TrimSpace(current.String())
	if stmt != "" {
		statements = append(statements, stmt)
	}
	return statements, nil
}

var errTestMigrationFailed = errors.New("test migration failed")

func (db *DB) runMigrationForTest(name string, statements []string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("%w: %v", errTestMigrationFailed, err)
		}
	}
	if _, err := tx.Exec("INSERT INTO schema_versions (name) VALUES (?)", name); err != nil {
		return fmt.Errorf("%w: record migration: %v", errTestMigrationFailed, err)
	}
	return tx.Commit()
}
