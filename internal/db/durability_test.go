package db

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestDurabilityPragmasActive(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "lethe.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var journal string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(journal, "wal") {
		t.Fatalf("journal_mode = %s, want wal", journal)
	}
	var synchronous int
	if err := db.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatal(err)
	}
	if synchronous != 2 {
		t.Fatalf("synchronous = %d, want 2 (FULL)", synchronous)
	}
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Fatal("foreign_keys not enforced")
	}
}

func TestDurabilityVerificationFailsClosed(t *testing.T) {
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "lethe.db")+"?_pragma=journal_mode(DELETE)&_pragma=synchronous(OFF)&_pragma=foreign_keys(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if err := verifyDurabilityPragmas(raw, "FULL"); err == nil {
		t.Fatal("verification accepted unsupported storage configuration")
	}
}

// TestCommittedChangesetSurvivesAbruptKill proves a committed changeset is
// durable when the process dies without a clean close.
func TestCommittedChangesetSurvivesAbruptKill(t *testing.T) {
	if os.Getenv("LETHE_KILL_TEST_HELPER") == "1" {
		dir := os.Args[len(os.Args)-1]
		s, err := NewStore(filepath.Join(dir, "lethe.db"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if _, _, err := s.EnsureLegacyRoot(t.Context(), "kill-project", "system"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Println("committed")
		os.Exit(0) // abrupt: no Close, no checkpoint
	}

	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=TestCommittedChangesetSurvivesAbruptKill", "--", dir)
	cmd.Env = append(os.Environ(), "LETHE_KILL_TEST_HELPER=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper process: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "committed") {
		t.Fatalf("helper did not commit: %s", out)
	}

	s, err := NewStore(filepath.Join(dir, "lethe.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ref, err := s.GetMemoryRef(t.Context(), "kill-project", "refs/shared/main")
	if err != nil {
		t.Fatalf("committed ref lost after abrupt kill: %v", err)
	}
	if ref == nil || !ref.Protected {
		t.Fatalf("ref = %#v", ref)
	}
}
