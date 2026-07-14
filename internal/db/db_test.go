package db

import (
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestOpenCreatesDB(t *testing.T) {
	tmp := t.TempDir() + "/test.db"
	database, err := Open(tmp)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	// Verify WAL mode and schema.
	var journalMode string
	row := database.QueryRow("PRAGMA journal_mode")
	if err := row.Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want %q", journalMode, "wal")
	}

	// Verify schema tables exist.
	tables := []string{"agents", "projects", "sessions", "checkpoints", "events", "session_links"}
	for _, table := range tables {
		var count int
		q := "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?"
		if err := database.QueryRow(q, table).Scan(&count); err != nil {
			t.Errorf("check table %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %s: count=%d, want 1", table, count)
		}
	}
}

func TestOpenCreatesDir(t *testing.T) {
	tmp := t.TempDir()
	dbPath := tmp + "/nested/path/test.db"
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open with nested path: %v", err)
	}
	database.Close()

	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("db file not created: %v", err)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	tmp := t.TempDir() + "/idempotent.db"
	db1, err := Open(tmp)
	if err != nil {
		t.Fatalf("Open first time: %v", err)
	}
	db1.Close()

	db2, err := Open(tmp) // second open — no new migrations
	if err != nil {
		t.Fatalf("Open second time: %v", err)
	}
	db2.Close()
}

func TestMigrationVersionsRecordedAfterAtomicMigration(t *testing.T) {
	tmp := t.TempDir() + "/versions.db"
	database, err := Open(tmp)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	rows, err := database.Query("SELECT name FROM schema_versions ORDER BY name")
	if err != nil {
		t.Fatalf("query schema_versions: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan schema_versions: %v", err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("schema_versions rows: %v", err)
	}

	want := []string{
		"001_init.sql",
		"002_add_session_key.sql",
		"003_add_token_budget.sql",
		"004_add_lifetime_tokens.sql",
		"005_add_threads.sql",
		"006_project_scoped_events.sql",
		"007_unique_session_key.sql",
		"008_context_assembly_ledger.sql",
		"009_memory_git.sql",
	}
	if len(got) != len(want) {
		t.Fatalf("schema_versions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("schema_versions = %v, want %v", got, want)
		}
	}
}

func TestSessionKeyUniqueIndex(t *testing.T) {
	tmp := t.TempDir() + "/session-key.db"
	database, err := Open(tmp)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	var indexName string
	if err := database.QueryRow("SELECT name FROM sqlite_master WHERE type='index' AND name='idx_sessions_session_key'").Scan(&indexName); err != nil {
		t.Fatalf("session key index missing: %v", err)
	}

	for _, stmt := range []string{
		"INSERT INTO agents(agent_id, name) VALUES ('agent', 'Agent')",
		"INSERT INTO projects(project_id, name) VALUES ('project', 'Project')",
		"INSERT INTO sessions(session_id, session_key, agent_id, project_id, state, started_at) VALUES ('s1', 'stable-key', 'agent', 'project', 'active', datetime('now'))",
		"INSERT INTO sessions(session_id, agent_id, project_id, state, started_at) VALUES ('null-key-1', 'agent', 'project', 'active', datetime('now'))",
		"INSERT INTO sessions(session_id, agent_id, project_id, state, started_at) VALUES ('null-key-2', 'agent', 'project', 'active', datetime('now'))",
	} {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}

	_, err = database.Exec("INSERT INTO sessions(session_id, session_key, agent_id, project_id, state, started_at) VALUES ('s2', 'stable-key', 'agent', 'project', 'active', datetime('now'))")
	if err == nil {
		t.Fatal("expected duplicate session_key insert to fail")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Fatalf("expected unique constraint error, got %v", err)
	}
}

func TestSessionKeyMigrationDetachesExistingDuplicates(t *testing.T) {
	tmp := t.TempDir() + "/duplicate-session-key.db"
	raw, err := sql.Open("sqlite", tmp+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	database := &DB{DB: raw}
	defer database.Close()

	if _, err := database.Exec(`
		CREATE TABLE IF NOT EXISTS schema_versions (
			name    TEXT PRIMARY KEY,
			applied DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		t.Fatalf("create schema_versions: %v", err)
	}
	for _, name := range []string{
		"001_init.sql",
		"002_add_session_key.sql",
		"003_add_token_budget.sql",
		"004_add_lifetime_tokens.sql",
		"005_add_threads.sql",
		"006_project_scoped_events.sql",
	} {
		if err := database.runMigration(name); err != nil {
			t.Fatalf("run %s: %v", name, err)
		}
	}

	for _, stmt := range []string{
		"INSERT INTO agents(agent_id, name) VALUES ('agent', 'Agent')",
		"INSERT INTO projects(project_id, name) VALUES ('project', 'Project')",
		"INSERT INTO sessions(session_id, session_key, agent_id, project_id, state, started_at, last_heartbeat_at) VALUES ('older', 'stable-key', 'agent', 'project', 'interrupted', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')",
		"INSERT INTO sessions(session_id, session_key, agent_id, project_id, state, started_at, last_heartbeat_at) VALUES ('newer', 'stable-key', 'agent', 'project', 'active', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')",
		"INSERT INTO sessions(session_id, session_key, agent_id, project_id, state, started_at, last_heartbeat_at) VALUES ('other', 'other-key', 'agent', 'project', 'active', '2026-01-03T00:00:00Z', '2026-01-03T00:00:00Z')",
	} {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}

	if err := database.runMigration("007_unique_session_key.sql"); err != nil {
		t.Fatalf("run 007 with duplicate session_key: %v", err)
	}

	var kept string
	if err := database.QueryRow("SELECT session_id FROM sessions WHERE session_key='stable-key'").Scan(&kept); err != nil {
		t.Fatalf("query kept stable-key: %v", err)
	}
	if kept != "newer" {
		t.Fatalf("kept session = %q, want newer", kept)
	}

	var detached int
	if err := database.QueryRow("SELECT COUNT(*) FROM sessions WHERE session_id='older' AND session_key IS NULL").Scan(&detached); err != nil {
		t.Fatalf("query detached duplicate: %v", err)
	}
	if detached != 1 {
		t.Fatalf("older duplicate was not detached")
	}

	_, err = database.Exec("INSERT INTO sessions(session_id, session_key, agent_id, project_id, state, started_at) VALUES ('dupe', 'stable-key', 'agent', 'project', 'active', datetime('now'))")
	if err == nil {
		t.Fatal("expected duplicate session_key insert to fail after migration")
	}
}

func TestSplitSQLStatementsHandlesSemicolonsInsideStrings(t *testing.T) {
	stmts, err := splitSQLStatements(`
-- comment with ;
CREATE TABLE t (v TEXT DEFAULT 'a;b');
INSERT INTO t(v) VALUES ('x; y');
`)
	if err != nil {
		t.Fatalf("splitSQLStatements: %v", err)
	}
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d: %#v", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "a;b") || !strings.Contains(stmts[1], "x; y") {
		t.Fatalf("split statements lost quoted semicolons: %#v", stmts)
	}
}

func TestRunMigrationRollsBackOnFailure(t *testing.T) {
	tmp := t.TempDir() + "/rollback.db"
	database, err := Open(tmp)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	err = database.runMigrationForTest("999_bad.sql", []string{
		"CREATE TABLE migration_should_rollback (id TEXT)",
		"INSERT INTO missing_table VALUES (1)",
	})
	if err == nil {
		t.Fatal("expected migration failure")
	}
	if !errors.Is(err, errTestMigrationFailed) {
		t.Fatalf("unexpected error: %v", err)
	}

	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='migration_should_rollback'").Scan(&count); err != nil {
		t.Fatalf("check rollback table: %v", err)
	}
	if count != 0 {
		t.Fatalf("migration table survived rollback")
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM schema_versions WHERE name='999_bad.sql'").Scan(&count); err != nil {
		t.Fatalf("check schema version: %v", err)
	}
	if count != 0 {
		t.Fatalf("failed migration was recorded")
	}
}
