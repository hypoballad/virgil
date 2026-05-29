package repository

import (
	"path/filepath"
	"testing"

	"github.com/hypoballad/virgil/internal/db"
	"github.com/hypoballad/virgil/internal/symbols"
)

func TestCallRepositoryUpsertAndFind(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatalf("db.New failed: %v", err)
	}
	defer database.Close()

	repo := NewCallRepository(database)
	graph := &symbols.FileCallGraph{
		FilePath: "sample.go",
		Language: "go",
		Calls: []symbols.CallEdge{
			{
				CallerName:     "Run",
				CallerReceiver: "*Agent",
				CalleeName:     "executeTools",
				CallLine:       42,
			},
			{
				CallerName: "Run",
				CalleeName: "emitProgress",
				CallLine:   50,
			},
		},
	}

	if err := repo.UpsertFile("sample.go", graph); err != nil {
		t.Fatalf("UpsertFile failed: %v", err)
	}

	count, err := repo.CountAll()
	if err != nil {
		t.Fatalf("CountAll failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("CountAll = %d, want 2", count)
	}

	outgoing, err := repo.FindOutgoing("Run", "*Agent", 10)
	if err != nil {
		t.Fatalf("FindOutgoing failed: %v", err)
	}
	if len(outgoing) != 1 || outgoing[0].CalleeName != "executeTools" {
		t.Fatalf("FindOutgoing = %+v, want executeTools only", outgoing)
	}

	incoming, err := repo.FindIncoming("emitProgress", 10)
	if err != nil {
		t.Fatalf("FindIncoming failed: %v", err)
	}
	if len(incoming) != 1 || incoming[0].CallerName != "Run" {
		t.Fatalf("FindIncoming = %+v, want Run", incoming)
	}

	if err := repo.DeleteByFilePath("sample.go"); err != nil {
		t.Fatalf("DeleteByFilePath failed: %v", err)
	}
	count, err = repo.CountAll()
	if err != nil {
		t.Fatalf("CountAll after delete failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("CountAll after delete = %d, want 0", count)
	}
}
