package db

import (
	"compress/gzip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openlethe/lethe/internal/models"
)

// fixturePath is the path to the v0.3.0 golden fixture.
const fixturePath = "testdata/lethe-v0.3.0.db.gz"

// generateV030Fixture creates a v0.3.0 database by running migrations 001-007
// and inserting representative data, then gzips it to testdata/lethe-v0.3.0.db.gz.
// Run manually when the v0.3.0 schema changes:
//
//	go test -run TestGenerateV030Fixture -v ./internal/db
func TestGenerateV030Fixture(t *testing.T) {
	if os.Getenv("GENERATE_V030_FIXTURE") != "1" {
		t.Skip("Set GENERATE_V030_FIXTURE=1 to regenerate fixture")
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "lethe-v0.3.0.db")

	// Open raw database without running migrations so we can manually apply
	// only 001-007 and simulate a v0.3.0 database.
	raw, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	db := &DB{DB: raw}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping db: %v", err)
	}
	defer db.Close()

	// Create schema_versions tracking table first (idempotent).
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_versions (
			name    TEXT PRIMARY KEY,
			applied DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		t.Fatalf("create schema_versions: %v", err)
	}

	// Run only migrations 001-007 manually to simulate a v0.3.0 database.
	migrationNames := []string{
		"001_init.sql",
		"002_add_session_key.sql",
		"003_add_token_budget.sql",
		"004_add_lifetime_tokens.sql",
		"005_add_threads.sql",
		"006_project_scoped_events.sql",
		"007_unique_session_key.sql",
	}
	for _, name := range migrationNames {
		sqlBytes, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		stmts, err := splitSQLStatements(string(sqlBytes))
		if err != nil {
			t.Fatalf("split migration %s: %v", name, err)
		}
		if err := db.runMigrationForTest(name, stmts); err != nil {
			t.Fatalf("run migration %s: %v", name, err)
		}
	}

	ctx := context.Background()
	now := time.Now().UTC()

	// Insert agent.
	_, err = db.ExecContext(ctx, `INSERT INTO agents (agent_id, name, created_at, last_seen_at) VALUES (?, ?, ?, ?)`,
		"agent-1", "Test Agent", now, now)
	if err != nil {
		t.Fatalf("insert agent: %v", err)
	}

	// Insert project.
	_, err = db.ExecContext(ctx, `INSERT INTO projects (project_id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		"project-1", "Test Project", now, now)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}

	// Insert sessions: active, completed, interrupted.
	sessions := []struct {
		id      string
		key     string
		state   string
		started time.Time
		ended   *time.Time
		summary string
	}{
		{"sess-active", "key-active", string(models.SessionActive), now, nil, ""},
		{"sess-completed", "key-completed", string(models.SessionCompleted), now.Add(-24 * time.Hour), &now, "completed session summary"},
		{"sess-interrupted", "key-interrupted", string(models.SessionInterrupted), now.Add(-2 * time.Hour), nil, "interrupted session summary"},
	}
	for _, s := range sessions {
		var ended sql.NullTime
		if s.ended != nil {
			ended = sql.NullTime{Time: *s.ended, Valid: true}
		}
		_, err := db.ExecContext(ctx,
			`INSERT INTO sessions (session_id, session_key, agent_id, project_id, state, started_at, ended_at, summary) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			s.id, s.key, "agent-1", "project-1", s.state, s.started, ended, s.summary)
		if err != nil {
			t.Fatalf("insert session %s: %v", s.id, err)
		}
	}

	// Insert events: record, log, flag, task.
	// Also test project-level event with null session_id.
	events := []struct {
		id         string
		sessionID  sql.NullString
		typ        string
		content    string
		confidence sql.NullFloat64
		taskTitle  sql.NullString
		taskStatus sql.NullString
	}{
		{"evt-1", sql.NullString{String: "sess-active", Valid: true}, "record", "Decision A", sql.NullFloat64{Float64: 0.9, Valid: true}, sql.NullString{}, sql.NullString{}},
		{"evt-2", sql.NullString{String: "sess-active", Valid: true}, "log", "Observation B", sql.NullFloat64{}, sql.NullString{}, sql.NullString{}},
		{"evt-3", sql.NullString{String: "sess-active", Valid: true}, "flag", "Flagged uncertainty C", sql.NullFloat64{}, sql.NullString{}, sql.NullString{}},
		{"evt-4", sql.NullString{String: "sess-active", Valid: true}, "task", "Task D", sql.NullFloat64{}, sql.NullString{String: "Task Title", Valid: true}, sql.NullString{String: "todo", Valid: true}},
		{"evt-5", sql.NullString{}, "log", "Project-level event with null session", sql.NullFloat64{}, sql.NullString{}, sql.NullString{}},
	}
	for _, e := range events {
		_, err := db.ExecContext(ctx,
			`INSERT INTO events (event_id, session_id, project_id, event_type, content, created_at, confidence, task_title, task_status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.id, e.sessionID, "project-1", e.typ, e.content, now, e.confidence, e.taskTitle, e.taskStatus)
		if err != nil {
			t.Fatalf("insert event %s: %v", e.id, err)
		}
	}

	// Insert threads.
	_, err = db.ExecContext(ctx,
		`INSERT INTO threads (thread_id, session_id, name, title, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"thread-1", "sess-active", "Open Question", "Open Question", "open", now, now)
	if err != nil {
		t.Fatalf("insert thread: %v", err)
	}

	// Insert checkpoints.
	_, err = db.ExecContext(ctx,
		`INSERT INTO checkpoints (checkpoint_id, session_id, seq, snapshot, created_at) VALUES (?, ?, ?, ?, ?)`,
		"cp-1", "sess-active", 1, `{"open_threads":[],"recent_event_ids":[],"current_task":""}`, now)
	if err != nil {
		t.Fatalf("insert checkpoint: %v", err)
	}

	// Insert session links.
	_, err = db.ExecContext(ctx,
		`INSERT INTO session_links (session_id, prior_session_id, link_type) VALUES (?, ?, ?)`,
		"sess-active", "sess-completed", "resume")
	if err != nil {
		t.Fatalf("insert session_link: %v", err)
	}

	// Close database before copying/gzipping to ensure WAL is checkpointed.
	db.Close()

	if err := os.MkdirAll("testdata", 0755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}

	f, err := os.Create(fixturePath)
	if err != nil {
		t.Fatalf("create fixture: %v", err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	src, err := os.Open(dbPath)
	if err != nil {
		t.Fatalf("open db for gzip: %v", err)
	}
	defer src.Close()

	if _, err := io.Copy(gw, src); err != nil {
		t.Fatalf("gzip db: %v", err)
	}
	gw.Close()
	f.Close()

	t.Logf("Generated fixture: %s (%d bytes)", fixturePath, mustFileSize(fixturePath))
}

func mustFileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		panic(err)
	}
	return fi.Size()
}

// TestMigrateV030ToV040 validates that a v0.3.0 database migrates cleanly to
// v0.4.0, preserving all original data and adding migration 008 tables.
func TestMigrateV030ToV040(t *testing.T) {
	if _, err := os.Stat(fixturePath); os.IsNotExist(err) {
		t.Fatalf("Fixture not found: %s. Run GENERATE_V030_FIXTURE=1 to create it.", fixturePath)
	}

	ctx := context.Background()

	// Copy fixture to temp directory and decompress.
	dir := t.TempDir()
	fixtureDB := filepath.Join(dir, "lethe.db")

	gf, err := os.Open(fixturePath)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer gf.Close()

	gr, err := gzip.NewReader(gf)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gr.Close()

	dst, err := os.Create(fixtureDB)
	if err != nil {
		t.Fatalf("create db: %v", err)
	}
	if _, err := io.Copy(dst, gr); err != nil {
		t.Fatalf("decompress: %v", err)
	}
	dst.Close()

	// Open with v0.4 store — this triggers all migrations including 008.
	store, err := NewStore(fixtureDB)
	if err != nil {
		t.Fatalf("open v0.4 store: %v", err)
	}
	defer store.Close()

	// Verify migration 008 was recorded.
	var migration008 string
	if err := store.QueryRowContext(ctx, `SELECT name FROM schema_versions WHERE name = ?`, "008_context_assembly_ledger.sql").Scan(&migration008); err != nil {
		t.Fatalf("migration 008 not recorded: %v", err)
	}
	if migration008 != "008_context_assembly_ledger.sql" {
		t.Fatalf("unexpected migration name: %s", migration008)
	}

	// Verify original counts.
	assertCount(t, store, "agents", 1)
	assertCount(t, store, "projects", 1)
	assertCount(t, store, "sessions", 3)
	assertCount(t, store, "events", 5)
	assertCount(t, store, "threads", 1)
	assertCount(t, store, "checkpoints", 1)
	assertCount(t, store, "session_links", 1)

	// Verify new tables exist (migration 008).
	assertCount(t, store, "context_assemblies", 0) // empty after migration
	assertCount(t, store, "context_assembly_items", 0)
	assertCount(t, store, "context_assembly_feedback", 0)

	// Verify session data integrity.
	var activeSummary, interruptedSummary string
	if err := store.QueryRowContext(ctx, `SELECT summary FROM sessions WHERE session_id = ?`, "sess-active").Scan(&activeSummary); err != nil {
		t.Fatalf("read active session: %v", err)
	}
	if activeSummary != "" {
		t.Fatalf("active session should have empty summary, got %q", activeSummary)
	}
	if err := store.QueryRowContext(ctx, `SELECT summary FROM sessions WHERE session_id = ?`, "sess-interrupted").Scan(&interruptedSummary); err != nil {
		t.Fatalf("read interrupted session: %v", err)
	}
	if interruptedSummary != "interrupted session summary" {
		t.Fatalf("interrupted summary mismatch: got %q", interruptedSummary)
	}

	// Verify project-level event with null session.
	var nullSessionCount int
	if err := store.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE session_id IS NULL`).Scan(&nullSessionCount); err != nil {
		t.Fatalf("count null session events: %v", err)
	}
	if nullSessionCount != 1 {
		t.Fatalf("expected 1 null-session event, got %d", nullSessionCount)
	}

	// Verify event types.
	assertEventTypeCount(t, store, "record", 1)
	assertEventTypeCount(t, store, "log", 2)
	assertEventTypeCount(t, store, "flag", 1)
	assertEventTypeCount(t, store, "task", 1)

	// Verify foreign keys are still active.
	var fkEnabled int
	if err := store.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fkEnabled); err != nil {
		t.Fatalf("read foreign_keys pragma: %v", err)
	}
	if fkEnabled != 1 {
		t.Fatalf("foreign keys should be enabled, got %d", fkEnabled)
	}

	// Verify we can create an assembly on the migrated database.
	assembly := &models.ContextAssembly{
		AssemblyID:       "asm-test-1",
		SessionID:        "sess-active",
		ProjectID:        "project-1",
		Source:           "test",
		AssemblerVersion: "test-v1",
		MessageCount:     5,
		PackedBytes:      123,
		Items: []models.ContextAssemblyItem{
			{
				Ordinal:         0,
				ItemKind:        "summary",
				Bucket:          "summary",
				ContentSnapshot: "summary snapshot",
				ContentSHA256:   "abc123",
				PackedBytes:     100,
			},
		},
	}
	if err := store.CreateContextAssembly(ctx, assembly); err != nil {
		t.Fatalf("create assembly on migrated db: %v", err)
	}

	readBack, err := store.GetContextAssembly(ctx, "asm-test-1")
	if err != nil {
		t.Fatalf("get assembly: %v", err)
	}
	if readBack.AssemblyID != "asm-test-1" {
		t.Fatalf("assembly ID mismatch: %s", readBack.AssemblyID)
	}
	if len(readBack.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(readBack.Items))
	}
}

func assertCount(t *testing.T, store *Store, table string, want int) {
	var got int
	if err := store.QueryRowContext(context.Background(), fmt.Sprintf(`SELECT COUNT(*) FROM %s`, table)).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s count: want %d, got %d", table, want, got)
	}
}

func assertEventTypeCount(t *testing.T, store *Store, typ string, want int) {
	var got int
	if err := store.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM events WHERE event_type = ?`, typ).Scan(&got); err != nil {
		t.Fatalf("count events %s: %v", typ, err)
	}
	if got != want {
		t.Fatalf("events %s count: want %d, got %d", typ, want, got)
	}
}
