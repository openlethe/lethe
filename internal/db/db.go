package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/openlethe/lethe/internal/metrics"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB wraps a SQLite database.
type DB struct {
	*sql.DB
}

// ErrDatabaseBusy is returned when SQLite lock contention persists after the
// bounded busy retry policy is exhausted. API handlers map it to HTTP 503.
var ErrDatabaseBusy = errors.New("database busy: lock contention exhausted retries")

// Open opens or creates a SQLite database.
func Open(dbPath string) (*DB, error) {
	if dbPath != ":memory:" && !strings.HasPrefix(dbPath, "file::memory:") {
		// Memory data is private: the database directory must not be traversable
		// and the database file must not be readable by other OS users. SQLite
		// derives WAL/SHM file modes from the main database file, and the
		// container entrypoint sets umask 077 as defense in depth.
		dir := filepath.Dir(dbPath)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("mkdir: %w", err)
		}
		if dir != "." {
			// #nosec G302 -- 0700 is owner-only; execute bit is required to traverse.
			if err := chmodOwnerOnly(dir, 0700); err != nil {
				return nil, fmt.Errorf("chmod dir: %w", err)
			}
		}
		// #nosec G304 -- dbPath is operator-supplied configuration, not untrusted request input.
		dbFile, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				return nil, fmt.Errorf("precreate db: %w (directory %s is not writable by this process; in containers, chown the host data directory to the service UID, e.g. `sudo chown -R 1000:1000 <dir>`)", err, dir)
			}
			return nil, fmt.Errorf("precreate db: %w", err)
		}
		if err := dbFile.Close(); err != nil {
			return nil, fmt.Errorf("close precreated db: %w", err)
		}
		if err := chmodOwnerOnly(dbPath, 0600); err != nil {
			return nil, fmt.Errorf("chmod db: %w", err)
		}
	}
	// Durability policy (WP8): committed transactions are durable across
	// process and container kills — WAL with synchronous=FULL and foreign_keys
	// enforced. RPO is the transaction boundary; storage must honor fsync.
	synchronous := envOr("LETHE_SQLITE_SYNCHRONOUS", "FULL")
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(" +
		envOr("LETHE_SQLITE_BUSY_TIMEOUT_MS", "5000") + ")&_pragma=synchronous(" + synchronous + ")&_pragma=wal_autocheckpoint(" +
		envOr("LETHE_SQLITE_AUTOCHECKPOINT", "1000") + ")"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	// SQLite permits a single writer at a time; WAL lets readers proceed during
	// writes. Bound the pool so bursts cannot open unbounded connections and
	// thrash the write lock; busy_timeout plus withBusyRetry absorb ordinary
	// contention instead of surfacing SQLITE_BUSY to callers.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	ret := &DB{DB: db}
	if err := db.PingContext(context.Background()); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}
	if err := verifyDurabilityPragmas(db, synchronous); err != nil {
		db.Close()
		return nil, fmt.Errorf("durability verification: %w", err)
	}
	if err := ret.Migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if dbPath != ":memory:" && !strings.HasPrefix(dbPath, "file::memory:") {
		// Migrations performed the first writes, so WAL/SHM now exist. Enforce
		// owner-only modes explicitly instead of relying on driver defaults.
		for _, suffix := range []string{"-wal", "-shm"} {
			sidecar := dbPath + suffix
			if _, err := os.Stat(sidecar); err == nil {
				if err := chmodOwnerOnly(sidecar, 0600); err != nil {
					return nil, fmt.Errorf("chmod %s: %w", suffix, err)
				}
			}
		}
	}
	return ret, nil
}

// chmodOwnerOnly tightens path to mode and tolerates EPERM on targets the
// process does not own — common for host bind mounts in containers — when the
// target is already owner-only. A broader target fails closed with remediation
// guidance rather than silently running with group/other-accessible memory
// data.
func chmodOwnerOnly(path string, mode os.FileMode) error {
	err := os.Chmod(path, mode)
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrPermission) {
		return err
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		return err
	}
	if info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("%w (mode is %#o and not owned by this process; chown it to the service user or tighten it to %#o manually)", err, info.Mode().Perm(), mode)
	}
	return nil
}

// busyRetryAttempts bounds how often a short, idempotent write transaction is
// retried when SQLite reports lock contention (SQLITE_BUSY).
const busyRetryAttempts = 5

// withBusyRetry runs op with bounded exponential backoff and jitter while op
// fails with a SQLite lock-contention error. Non-busy errors return
// immediately. When attempts are exhausted the error wraps ErrDatabaseBusy so
// handlers can return 503 instead of misclassifying contention as a client
// error.
func withBusyRetry(ctx context.Context, op func() error) error {
	for attempt := 0; ; attempt++ {
		err := op()
		if !IsBusyError(err) {
			return err
		}
		metrics.Inc(metrics.BusyRetries)
		if attempt == busyRetryAttempts-1 {
			metrics.Inc(metrics.BusyExhausted)
			return fmt.Errorf("%w after %d attempts: %v", ErrDatabaseBusy, busyRetryAttempts, err)
		}
		backoff := time.Duration(25*(1<<attempt)) * time.Millisecond
		// #nosec G404 -- backoff jitter does not require cryptographic randomness.
		jitter := time.Duration(rand.Int64N(int64(backoff)/2 + 1))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff + jitter):
		}
	}
}

// IsBusyError reports whether err is a SQLite lock-contention error, including
// errors that wrap ErrDatabaseBusy after the retry policy was exhausted.
func IsBusyError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrDatabaseBusy) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked") ||
		strings.Contains(msg, "sqlite_busy")
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
	// Go-side data migration: upgrade changeset integrity digests to the
	// order-preserving v2 algorithm (see store_memory_git.go).
	if err := migrateChangesetDigestsV2(db); err != nil {
		return fmt.Errorf("migration 011_changeset_digests_v2: %w", err)
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

// verifyDurabilityPragmas fails closed when the storage engine did not apply
// the required durability configuration, so an unsupported volume is detected
// at startup rather than after data loss.
func verifyDurabilityPragmas(db *sql.DB, wantSynchronous string) error {
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return fmt.Errorf("read journal_mode: %w", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		return fmt.Errorf("journal_mode = %s, want WAL", journalMode)
	}
	var synchronous int
	if err := db.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil {
		return fmt.Errorf("read synchronous: %w", err)
	}
	wantSync := map[string]int{"OFF": 0, "NORMAL": 1, "FULL": 2, "EXTRA": 3}[strings.ToUpper(wantSynchronous)]
	if synchronous != wantSync {
		return fmt.Errorf("synchronous = %d, want %d (%s)", synchronous, wantSync, wantSynchronous)
	}
	var foreignKeys int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("read foreign_keys: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("foreign_keys not enforced")
	}
	return nil
}

// Close checkpoints the WAL (TRUNCATE) before closing so a clean shutdown
// leaves a single database file and the next open starts without recovery.
func (db *DB) Close() error {
	_, _ = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return db.DB.Close()
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
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
