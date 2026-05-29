package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestContainsOmittedToolArgumentNested(t *testing.T) {
	args := map[string]interface{}{
		"path": "x.py",
		"new_lines": []interface{}{
			"ok",
			OmittedToolArgumentMarker + "\nPreview: ...",
		},
	}
	if !ContainsOmittedToolArgument(args) {
		t.Fatal("expected omitted argument marker to be detected")
	}
}

func TestEditWithPatternRejectsOmittedReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.py")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditWithPatternTool(dir)
	args, _ := json.Marshal(map[string]interface{}{
		"path":         "x.py",
		"find_text":    "old",
		"replace_with": OmittedToolArgumentMarker,
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("expected error result, got %#v", result)
	}
	content, _ := os.ReadFile(path)
	if string(content) != "old\n" {
		t.Fatalf("file was modified: %q", string(content))
	}
}

func TestWriteFileRejectsOmittedContent(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFileTool(dir)
	args, _ := json.Marshal(map[string]interface{}{
		"path":    "x.py",
		"content": OmittedToolArgumentMarker,
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("expected error result, got %#v", result)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.py")); !os.IsNotExist(err) {
		t.Fatalf("file should not be created, stat err=%v", err)
	}
}

func TestEditFileRejectsOmittedNewLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.py")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditFileTool(dir)
	args, _ := json.Marshal(map[string]interface{}{
		"path":       "x.py",
		"start_line": 1,
		"end_line":   1,
		"new_lines":  []string{OmittedToolArgumentMarker},
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("expected error result, got %#v", result)
	}
	content, _ := os.ReadFile(path)
	if strings.TrimSpace(string(content)) != "old" {
		t.Fatalf("file was modified: %q", string(content))
	}
}

func TestRunCommandAutoConfirmRunsConfirmCommand(t *testing.T) {
	dir := t.TempDir()
	tool := NewRunCommandTool(RunCommandConfig{
		DefaultAction:         "confirm",
		AllowOutsideWorkspace: false,
		Timeout:               5 * time.Second,
		MaxOutputBytes:        10000,
		WorkspaceRoot:         dir,
	})
	tool.SetAutoConfirm(true)

	args, _ := json.Marshal(map[string]interface{}{
		"command": "printf vmax",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected success result, got %#v", result)
	}
	if !strings.Contains(result.Content, "vmax") {
		t.Fatalf("unexpected output: %s", result.Content)
	}
}
