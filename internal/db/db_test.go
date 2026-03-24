package db

import (
	"os"
	"testing"
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
