package repository

import (
	"path/filepath"
	"testing"

	"github.com/hypoballad/virgil/internal/db"
	"github.com/hypoballad/virgil/internal/symbols"
)

func TestSymbolRepositoryFindSymbolsFilters(t *testing.T) {
	repo := newTestSymbolRepository(t)

	files := map[string][]symbols.Symbol{
		"/workspace/train/src/AE.py": {
			{Name: "__init__", Type: symbols.SymbolMethod, Receiver: "myAE", Signature: "def __init__(self):", StartLine: 10, EndLine: 20, IsFallback: true, Doc: "myAE constructor"},
			{Name: "train", Type: symbols.SymbolMethod, Receiver: "myAE", Signature: "def train(self):", StartLine: 30, EndLine: 40, IsFallback: true},
		},
		"/workspace/train/src/Other.py": {
			{Name: "__init__", Type: symbols.SymbolMethod, Receiver: "Other", Signature: "def __init__(self):", StartLine: 5, EndLine: 8},
			{Name: "__init__", Type: symbols.SymbolFunction, Signature: "def __init__():", StartLine: 50, EndLine: 55},
		},
	}

	for filePath, symbolList := range files {
		if err := repo.UpsertFile(filePath, &symbols.FileOutline{FilePath: filePath, Language: "python", Symbols: symbolList}, 1); err != nil {
			t.Fatalf("UpsertFile(%s) error = %v", filePath, err)
		}
	}

	records, err := repo.FindSymbols(SymbolSearchOptions{
		Pattern:      "%__init__%",
		SymbolType:   "method",
		Receiver:     "myAE",
		FilePath:     "AE.py",
		FallbackOnly: true,
		Limit:        20,
	})
	if err != nil {
		t.Fatalf("FindSymbols() error = %v", err)
	}
	if got, want := len(records), 1; got != want {
		t.Fatalf("len(records) = %d, want %d: %+v", got, want, records)
	}
	if records[0].FilePath != "/workspace/train/src/AE.py" || records[0].Receiver != "myAE" || !records[0].IsFallback {
		t.Fatalf("unexpected filtered record: %+v", records[0])
	}
	if records[0].Doc != "myAE constructor" {
		t.Fatalf("Doc = %q", records[0].Doc)
	}
}

func newTestSymbolRepository(t *testing.T) *SymbolRepository {
	t.Helper()
	database, err := db.New(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatalf("db.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Fatalf("database.Close() error = %v", err)
		}
	})
	return NewSymbolRepository(database)
}
