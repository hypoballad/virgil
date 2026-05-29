package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hypoballad/virgil/internal/db"
	"github.com/hypoballad/virgil/internal/repository"
	"github.com/hypoballad/virgil/internal/symbols"
)

func TestGetFileImportsToolReturnsIndexedImports(t *testing.T) {
	workspace := t.TempDir()
	filePath := filepath.Join(workspace, "pkg", "module.py")
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filePath, []byte("import os\nfrom .helper import foo\n"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	repo := newTestToolRepository(t)
	if err := repo.Imports.UpsertFile(filePath, &symbols.FileImports{
		FilePath: filePath,
		Language: "python",
		Imports: []symbols.Import{
			{LineNumber: 1, Kind: "import", Module: "os", Scope: "module"},
			{LineNumber: 2, Kind: "from_import", Module: ".helper", ImportedName: "foo", IsRelative: true, RelativeLevel: 1, Scope: "module"},
		},
	}); err != nil {
		t.Fatalf("UpsertFile() error = %v", err)
	}

	tool := NewGetFileImportsTool(workspace, repo.Imports, repo.Symbols)
	result, err := tool.Execute(context.Background(), mustJSON(t, map[string]string{"path": "pkg/module.py"}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute() returned error result: %s", result.Content)
	}
	for _, want := range []string{
		"File: pkg/module.py",
		"Module-level imports:",
		"L1: import os",
		"L2: from .helper import foo (relative, level=1)",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("result missing %q:\n%s", want, result.Content)
		}
	}
}

func TestGetFileImportsToolErrors(t *testing.T) {
	workspace := t.TempDir()
	repo := newTestToolRepository(t)
	tool := NewGetFileImportsTool(workspace, repo.Imports, repo.Symbols)

	result, err := tool.Execute(context.Background(), mustJSON(t, map[string]string{"path": "missing.py"}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "file not found") {
		t.Fatalf("missing file result = %+v", result)
	}

	txt := filepath.Join(workspace, "note.txt")
	if err := os.WriteFile(txt, []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	result, err = tool.Execute(context.Background(), mustJSON(t, map[string]string{"path": "note.txt"}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "not a Python file") {
		t.Fatalf("non-python result = %+v", result)
	}

	py := filepath.Join(workspace, "unindexed.py")
	if err := os.WriteFile(py, []byte("import os\n"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	result, err = tool.Execute(context.Background(), mustJSON(t, map[string]string{"path": "unindexed.py"}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "not indexed yet") {
		t.Fatalf("unindexed result = %+v", result)
	}
}

func newTestToolRepository(t *testing.T) *repository.Repository {
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
	return repository.New(database)
}

func mustJSON(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return b
}
