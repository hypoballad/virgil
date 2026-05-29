package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/hypoballad/virgil/internal/symbols"
)

func TestFindDependentsToolReturnsIndexedDependents(t *testing.T) {
	repo := newTestToolRepository(t)
	if err := repo.Imports.UpsertFile("/workspace/a.py", &symbols.FileImports{
		FilePath: "/workspace/a.py",
		Language: "python",
		Imports: []symbols.Import{
			{LineNumber: 2, Kind: "import", Module: "numpy", Alias: "np", Scope: "module"},
		},
	}); err != nil {
		t.Fatalf("UpsertFile(a.py) error = %v", err)
	}
	if err := repo.Imports.UpsertFile("/workspace/b.py", &symbols.FileImports{
		FilePath: "/workspace/b.py",
		Language: "python",
		Imports: []symbols.Import{
			{LineNumber: 3, Kind: "from_import", Module: "numpy", ImportedName: "ndarray", Scope: "module"},
		},
	}); err != nil {
		t.Fatalf("UpsertFile(b.py) error = %v", err)
	}

	tool := NewFindDependentsTool(repo.Imports)
	result, err := tool.Execute(context.Background(), mustJSON(t, map[string]interface{}{"module": "numpy"}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute() returned error result: %s", result.Content)
	}
	for _, want := range []string{
		"Module: numpy",
		"Found 2 file(s)",
		"/workspace/a.py",
		"L2: import numpy as np (alias)",
		"/workspace/b.py",
		"L3: from numpy import ndarray",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("result missing %q:\n%s", want, result.Content)
		}
	}
}

func TestFindDependentsToolExtendedFilters(t *testing.T) {
	repo := newTestToolRepository(t)
	if err := repo.Imports.UpsertFile("/workspace/pkg/a.py", &symbols.FileImports{
		FilePath: "/workspace/pkg/a.py",
		Language: "python",
		Imports: []symbols.Import{
			{LineNumber: 2, Kind: "import", Module: "numpy", Alias: "np", Scope: "module"},
			{LineNumber: 3, Kind: "import", Module: "numpy.linalg", Alias: "linalg", Scope: "function"},
			{LineNumber: 4, Kind: "from_import", Module: "typing", IsWildcard: true, Scope: "conditional"},
		},
	}); err != nil {
		t.Fatalf("UpsertFile(a.py) error = %v", err)
	}
	if err := repo.Imports.UpsertFile("/workspace/pkg/b.py", &symbols.FileImports{
		FilePath: "/workspace/pkg/b.py",
		Language: "python",
		Imports: []symbols.Import{
			{LineNumber: 5, Kind: "from_import", Module: "numpy", ImportedName: "ndarray", Alias: "NDArray", Scope: "module"},
		},
	}); err != nil {
		t.Fatalf("UpsertFile(b.py) error = %v", err)
	}

	tool := NewFindDependentsTool(repo.Imports)
	result, err := tool.Execute(context.Background(), mustJSON(t, map[string]interface{}{
		"module":        "numpy",
		"exact_module":  true,
		"import_kind":   "from_import",
		"imported_name": "ndarray",
		"file_path":     "pkg/b.py",
	}))
	if err != nil {
		t.Fatalf("Execute(filtered) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("filtered result error: %s", result.Content)
	}
	for _, want := range []string{
		"Filters:",
		"exact_module=true",
		`import_kind="from_import"`,
		`imported_name="ndarray"`,
		`file_path="pkg/b.py"`,
		"/workspace/pkg/b.py",
		"from numpy import ndarray as NDArray",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("filtered result missing %q:\n%s", want, result.Content)
		}
	}
	if strings.Contains(result.Content, "/workspace/pkg/a.py") {
		t.Fatalf("filtered result should not contain a.py:\n%s", result.Content)
	}

	result, err = tool.Execute(context.Background(), mustJSON(t, map[string]interface{}{
		"module":        "typing",
		"wildcard_only": true,
		"scope":         "conditional",
	}))
	if err != nil {
		t.Fatalf("Execute(wildcard) error = %v", err)
	}
	if result.IsError || !strings.Contains(result.Content, "from typing import * (wildcard; scope=conditional)") {
		t.Fatalf("wildcard result = %+v", result)
	}
}

func TestFindDependentsToolOptionsAndErrors(t *testing.T) {
	repo := newTestToolRepository(t)
	if err := repo.Imports.UpsertFile("/workspace/rel.py", &symbols.FileImports{
		FilePath: "/workspace/rel.py",
		Language: "python",
		Imports: []symbols.Import{
			{LineNumber: 1, Kind: "from_import", Module: ".helper", ImportedName: "foo", IsRelative: true, RelativeLevel: 1, Scope: "module"},
		},
	}); err != nil {
		t.Fatalf("UpsertFile(rel.py) error = %v", err)
	}

	tool := NewFindDependentsTool(repo.Imports)
	result, err := tool.Execute(context.Background(), mustJSON(t, map[string]interface{}{"module": ""}))
	if err != nil {
		t.Fatalf("Execute(empty) error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "module is required") {
		t.Fatalf("empty module result = %+v", result)
	}

	result, err = tool.Execute(context.Background(), mustJSON(t, map[string]interface{}{"module": ".helper"}))
	if err != nil {
		t.Fatalf("Execute(relative excluded) error = %v", err)
	}
	if result.IsError || !strings.Contains(result.Content, "Found 0 file(s)") {
		t.Fatalf("relative excluded result = %+v", result)
	}

	result, err = tool.Execute(context.Background(), mustJSON(t, map[string]interface{}{"module": ".helper", "include_relative": true}))
	if err != nil {
		t.Fatalf("Execute(relative included) error = %v", err)
	}
	if result.IsError || !strings.Contains(result.Content, "from .helper import foo (relative level=1)") {
		t.Fatalf("relative included result = %+v", result)
	}
}

func TestFindDependentsToolEmptyIndex(t *testing.T) {
	repo := newTestToolRepository(t)
	tool := NewFindDependentsTool(repo.Imports)
	result, err := tool.Execute(context.Background(), mustJSON(t, map[string]interface{}{"module": "numpy"}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError || !strings.Contains(result.Content, "No imports indexed yet") {
		t.Fatalf("empty index result = %+v", result)
	}
}
