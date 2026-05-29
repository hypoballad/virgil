package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetJSONOutlineTool(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.json")
	writeTestFile(t, path, `{
		"users": [{"id": 1, "name": "Alice", "profile": {"email": "a@example.com"}}],
		"metadata": {"version": "1", "tags": ["prod", "test"]},
		"enabled": true
	}`)

	tool := NewGetJSONOutlineTool(root)
	result, err := tool.Execute(context.Background(), mustJSONArgs(t, map[string]interface{}{
		"path":      "sample.json",
		"max_depth": 2,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	for _, want := range []string{"File: sample.json", "$.users: array", "$.users[*].id", "$.metadata.version"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("outline missing %q:\n%s", want, result.Content)
		}
	}
}

func TestReadJSONPathTool(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.json")
	writeTestFile(t, path, `{"users":[{"name":"Alice"},{"name":"Bob"}],"metadata":{"version":"1"}}`)

	tool := NewReadJSONPathTool(root)
	result, err := tool.Execute(context.Background(), mustJSONArgs(t, map[string]interface{}{
		"path":     "sample.json",
		"jsonpath": "$.users[1]",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, `"name": "Bob"`) {
		t.Fatalf("result missing Bob:\n%s", result.Content)
	}
}

func TestJSONToolsRejectNonJSONAndInvalidJSON(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "sample.txt"), `{}`)
	writeTestFile(t, filepath.Join(root, "bad.json"), `{"a":`)

	outline := NewGetJSONOutlineTool(root)
	result, err := outline.Execute(context.Background(), mustJSONArgs(t, map[string]interface{}{
		"path": "sample.txt",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content, "not a JSON file") {
		t.Fatalf("expected non-json error, got %#v", result)
	}

	result, err = outline.Execute(context.Background(), mustJSONArgs(t, map[string]interface{}{
		"path": "bad.json",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content, "invalid JSON") || !strings.Contains(result.Content, "line") {
		t.Fatalf("expected invalid JSON line/column error, got %#v", result)
	}
}

func TestReadJSONPathToolTruncatesLargeResult(t *testing.T) {
	root := t.TempDir()
	large := strings.Repeat("x", readJSONPathMaxBytes+1024)
	writeTestFile(t, filepath.Join(root, "large.json"), `{"blob":"`+large+`"}`)

	tool := NewReadJSONPathTool(root)
	result, err := tool.Execute(context.Background(), mustJSONArgs(t, map[string]interface{}{
		"path":     "large.json",
		"jsonpath": "$.blob",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "truncated") {
		t.Fatalf("expected truncation note, got:\n%s", result.Content)
	}
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func mustJSONArgs(t *testing.T, value interface{}) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
