package db

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNewConfiguresSQLitePragmas(t *testing.T) {
	database, err := New(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer database.Close()

	var busyTimeout int
	if err := database.SqlDB.QueryRow("PRAGMA busy_timeout;").Scan(&busyTimeout); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", busyTimeout)
	}

	var journalMode string
	if err := database.SqlDB.QueryRow("PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if strings.ToLower(journalMode) != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}
}
