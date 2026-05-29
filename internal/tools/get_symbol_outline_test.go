package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetSymbolOutline_ListsChildMethodsWithoutBody(t *testing.T) {
	tmpDir := t.TempDir()
	source := largePythonClassSource()
	if err := os.WriteFile(filepath.Join(tmpDir, "large.py"), []byte(source), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tool := NewGetSymbolOutlineTool(tmpDir)
	args, _ := json.Marshal(map[string]string{
		"path":        "large.py",
		"symbol_name": "BigModel",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("got error result: %s", result.Content)
	}

	for _, want := range []string{
		"# Symbol Outline: large.py",
		"Parent: BigModel",
		"Children: 2 indexed",
		"__init__",
		"forward",
		"Do not reconstruct the parent by adjacent read_file ranges",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, result.Content)
		}
	}
	if strings.Contains(result.Content, "filler_59") {
		t.Fatalf("symbol outline should not include parent body, got:\n%s", result.Content)
	}
}

func TestGetSymbolOutline_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "sample.py"), []byte("def helper():\n    pass\n"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tool := NewGetSymbolOutlineTool(tmpDir)
	args, _ := json.Marshal(map[string]string{
		"path":        "sample.py",
		"symbol_name": "Missing",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result, got:\n%s", result.Content)
	}
}
