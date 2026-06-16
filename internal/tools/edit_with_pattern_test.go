package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditWithPattern_BasicReplace(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.go")
	originalContent := `package main

func Hello() string {
	return "hello"
}
`
	if err := os.WriteFile(testFile, []byte(originalContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tool := NewEditWithPatternTool(tmpDir)

	args, _ := json.Marshal(map[string]string{
		"path":         "test.go",
		"find_text":    `return "hello"`,
		"replace_with": `return "world"`,
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("got error: %s", result.Content)
	}

	newContent, _ := os.ReadFile(testFile)
	if !strings.Contains(string(newContent), `return "world"`) {
		t.Errorf("file content not updated correctly: %s", newContent)
	}
}

func TestEditWithPattern_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tool := NewEditWithPatternTool(tmpDir)

	args, _ := json.Marshal(map[string]string{
		"path":         "test.go",
		"find_text":    "nonexistent_text",
		"replace_with": "new",
	})

	result, _ := tool.Execute(context.Background(), args)
	if !result.IsError {
		t.Error("expected IsError=true for not-found pattern")
	}
	if !strings.Contains(result.Content, "not found") {
		t.Errorf("expected 'not found' in error, got: %s", result.Content)
	}
}

func TestEditWithPattern_WhitespaceTolerantFallbackIgnoresTrailingWhitespace(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	content := "alpha\nkeep trailing   \nomega\n"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tool := NewEditWithPatternTool(tmpDir)

	args, _ := json.Marshal(map[string]string{
		"path":         "test.txt",
		"find_text":    "keep trailing\n",
		"replace_with": "replacement\n",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("got error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "whitespace-tolerant") {
		t.Fatalf("result should mention whitespace-tolerant fallback: %s", result.Content)
	}

	newContent, _ := os.ReadFile(testFile)
	if got, want := string(newContent), "alpha\nreplacement\nomega\n"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestEditWithPattern_WhitespaceTolerantFallbackHandlesCRLF(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	content := "alpha\r\nbeta\r\nomega\r\n"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tool := NewEditWithPatternTool(tmpDir)

	args, _ := json.Marshal(map[string]string{
		"path":         "test.txt",
		"find_text":    "beta\n",
		"replace_with": "BETA\r\n",
	})

	result, _ := tool.Execute(context.Background(), args)
	if result.IsError {
		t.Fatalf("got error: %s", result.Content)
	}

	newContent, _ := os.ReadFile(testFile)
	if got, want := string(newContent), "alpha\r\nBETA\r\nomega\r\n"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestEditWithPattern_WhitespaceTolerantFallbackRejectsAmbiguousMatches(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	content := "target   \nother\ntarget\t\n"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tool := NewEditWithPatternTool(tmpDir)

	args, _ := json.Marshal(map[string]string{
		"path":         "test.txt",
		"find_text":    "target\n",
		"replace_with": "replacement\n",
	})

	result, _ := tool.Execute(context.Background(), args)
	if !result.IsError {
		t.Fatal("expected ambiguous whitespace-tolerant match to be rejected")
	}
	if !strings.Contains(result.Content, "matched multiple locations") {
		t.Fatalf("expected ambiguous fallback error, got: %s", result.Content)
	}
	newContent, _ := os.ReadFile(testFile)
	if string(newContent) != content {
		t.Fatalf("ambiguous fallback should not modify file: %q", string(newContent))
	}
}

func TestEditWithPattern_MultipleMatches(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.go")
	content := `package main

func A() {}
func B() {}
`
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tool := NewEditWithPatternTool(tmpDir)

	// "func" は2回出現
	args, _ := json.Marshal(map[string]string{
		"path":         "test.go",
		"find_text":    "func",
		"replace_with": "FUNC",
	})

	result, _ := tool.Execute(context.Background(), args)
	if !result.IsError {
		t.Error("expected IsError=true for multiple matches")
	}
	if !strings.Contains(result.Content, "appears") || !strings.Contains(result.Content, "UNIQUE") {
		t.Errorf("expected multiplicity message, got: %s", result.Content)
	}
}

func TestEditWithPattern_SyntaxValidation(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.go")
	content := `package main

func Hello() string {
	return "hello"
}
`
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tool := NewEditWithPatternTool(tmpDir)

	// 構文を壊す編集（閉じ括弧を削除）
	args, _ := json.Marshal(map[string]string{
		"path":         "test.go",
		"find_text":    "return \"hello\"\n}",
		"replace_with": `return "hello"`, // 閉じ括弧を削除
	})

	result, _ := tool.Execute(context.Background(), args)
	// 編集自体は成功するが、警告が含まれる
	if !strings.Contains(result.Content, "Warning") && !strings.Contains(result.Content, "syntax") {
		t.Errorf("expected syntax warning, got: %s", result.Content)
	}
}

func TestEditWithPattern_EmptyReplaceWith(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.go")
	content := `package main

// TODO: remove this comment
func Hello() {}
`
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tool := NewEditWithPatternTool(tmpDir)

	args, _ := json.Marshal(map[string]string{
		"path":         "test.go",
		"find_text":    "// TODO: remove this comment\n",
		"replace_with": "",
	})

	result, _ := tool.Execute(context.Background(), args)
	if result.IsError {
		t.Errorf("expected success, got error: %s", result.Content)
	}

	newContent, _ := os.ReadFile(testFile)
	if strings.Contains(string(newContent), "TODO") {
		t.Error("comment should have been removed")
	}
}
