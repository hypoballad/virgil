package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectSerializedLineListForCodeDetectsJSONCodeLines(t *testing.T) {
	content := `[
  "class myCA:",
  "    def __init__(self):",
  "        self.x = 1"
]`
	if err := RejectSerializedLineListForCode("model.py", content); err == nil {
		t.Fatal("expected serialized code line list to be rejected")
	}
}

func TestRejectSerializedLineListForCodeAllowsRealSource(t *testing.T) {
	content := "class myCA:\n    def __init__(self):\n        self.x = 1\n"
	if err := RejectSerializedLineListForCode("model.py", content); err != nil {
		t.Fatalf("real source should be allowed: %v", err)
	}
}

func TestRejectSerializedLineListForCodeAllowsJSONFile(t *testing.T) {
	content := `[
  "class myCA:",
  "    def __init__(self):",
  "        self.x = 1"
]`
	if err := RejectSerializedLineListForCode("payload.json", content); err != nil {
		t.Fatalf("json file should be allowed: %v", err)
	}
}

func TestEditWithPatternRejectsSerializedLineListReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "model.py")
	if err := os.WriteFile(path, []byte("class Old:\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	replacement := `[
  "class myCA:",
  "    def __init__(self):",
  "        self.x = 1"
]`
	tool := NewEditWithPatternTool(dir)
	args, _ := json.Marshal(map[string]interface{}{
		"path":         "model.py",
		"find_text":    "class Old:\n    pass",
		"replace_with": replacement,
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("expected error result, got %#v", result)
	}
	content, _ := os.ReadFile(path)
	if strings.Contains(string(content), "[") || strings.Contains(string(content), "myCA") {
		t.Fatalf("file was modified: %q", string(content))
	}
}

func TestWriteFileRejectsSerializedLineListContentForCode(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFileTool(dir)
	args, _ := json.Marshal(map[string]interface{}{
		"path": "model.py",
		"content": `[
  "class myCA:",
  "    def __init__(self):",
  "        self.x = 1"
]`,
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("expected error result, got %#v", result)
	}
	if _, err := os.Stat(filepath.Join(dir, "model.py")); !os.IsNotExist(err) {
		t.Fatalf("file should not be created, stat err=%v", err)
	}
}

func TestEditFileRejectsSerializedLineListStringForCode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "model.py")
	if err := os.WriteFile(path, []byte("class Old:\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditFileTool(dir)
	rawArgs := json.RawMessage(`{
  "path": "model.py",
  "start_line": 1,
  "end_line": 2,
  "new_lines": "[\n  \"class myCA:\",\n  \"    def __init__(self):\",\n  \"        self.x = 1\"\n]"
}`)
	result, err := tool.Execute(context.Background(), rawArgs)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("expected error result, got %#v", result)
	}
	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), "class Old") {
		t.Fatalf("file was modified: %q", string(content))
	}
}
