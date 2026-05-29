package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchTextFallsBackWhenRgIsMissing(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "a.py"), []byte("import numpy as np\nprint(np.array([]))\n"), 0644); err != nil {
		t.Fatalf("WriteFile(a.py) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "b.go"), []byte("package main\n// numpy in comment\n"), 0644); err != nil {
		t.Fatalf("WriteFile(b.go) error = %v", err)
	}

	t.Setenv("PATH", t.TempDir())

	tool := NewSearchTextTool(workspace)
	result, err := tool.Execute(context.Background(), mustJSON(t, map[string]interface{}{
		"pattern":   "numpy",
		"file_type": "py",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute() returned error result: %s", result.Content)
	}
	for _, want := range []string{
		"using Go fallback",
		"a.py:1:import numpy as np",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("result missing %q:\n%s", want, result.Content)
		}
	}
	if strings.Contains(result.Content, "b.go") {
		t.Fatalf("file_type filter did not exclude b.go:\n%s", result.Content)
	}
	if result.Metadata["fallback"] != "go" {
		t.Fatalf("fallback metadata = %+v", result.Metadata)
	}
}

func TestSearchTextFallbackReportsInvalidPattern(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("PATH", t.TempDir())

	tool := NewSearchTextTool(workspace)
	result, err := tool.Execute(context.Background(), mustJSON(t, map[string]interface{}{
		"pattern": "[",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "invalid pattern") {
		t.Fatalf("invalid pattern result = %+v", result)
	}
}
