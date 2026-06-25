package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFileBasic(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "edit-test-*")
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "test.txt")
	initialContent := "line 1\nline 2\nline 3\nline 4\nline 5\n"
	os.WriteFile(testFile, []byte(initialContent), 0644)

	tool := NewEditFileTool(tmpDir)

	// mapを使用してMarshalすることで、NewLinesの型変更の影響を避ける
	args, _ := json.Marshal(map[string]interface{}{
		"path":       "test.txt",
		"start_line": 2,
		"end_line":   3,
		"new_lines":  []string{"new line 2", "new line 3"},
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("edit failed: %s", result.Content)
	}

	content, _ := os.ReadFile(testFile)
	expected := "line 1\nnew line 2\nnew line 3\nline 4\nline 5\n"
	if string(content) != expected {
		t.Errorf("expected %q, got %q", expected, string(content))
	}
}

func TestEditFileInsert(t *testing.T) {
	// 同じ行を別の行に置換 = 行数増加
	tmpDir, _ := os.MkdirTemp("", "edit-test-*")
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("a\nb\nc\n"), 0644)

	tool := NewEditFileTool(tmpDir)

	args, _ := json.Marshal(map[string]interface{}{
		"path":       "test.txt",
		"start_line": 2,
		"end_line":   2,
		"new_lines":  []string{"b1", "b2", "b3"}, // 1行を3行に
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil || result.IsError {
		t.Fatalf("edit failed: %v / %s", err, result.Content)
	}

	content, _ := os.ReadFile(testFile)
	expected := "a\nb1\nb2\nb3\nc\n"
	if string(content) != expected {
		t.Errorf("expected %q, got %q", expected, string(content))
	}
}

func TestEditFileDelete(t *testing.T) {
	// 行を削除（new_lines が空配列）
	tmpDir, _ := os.MkdirTemp("", "edit-test-*")
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("a\nb\nc\nd\n"), 0644)

	tool := NewEditFileTool(tmpDir)

	args, _ := json.Marshal(map[string]interface{}{
		"path":       "test.txt",
		"start_line": 2,
		"end_line":   3,
		"new_lines":  []string{},
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil || result.IsError {
		t.Fatalf("edit failed: %v / %s", err, result.Content)
	}

	content, _ := os.ReadFile(testFile)
	expected := "a\nd\n"
	if string(content) != expected {
		t.Errorf("expected %q, got %q", expected, string(content))
	}
}

func TestEditFileAcceptsExpectedLineHashes(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "edit-test-*")
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("line 1\nline 2\nline 3\n"), 0644)

	tool := NewEditFileTool(tmpDir)
	args, _ := json.Marshal(map[string]interface{}{
		"path":                "test.txt",
		"start_line":          2,
		"end_line":            2,
		"expected_start_hash": "h:" + lineHash("line 2"),
		"expected_end_hash":   "[h:" + lineHash("line 2") + "]",
		"new_lines":           []string{"updated line 2"},
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}

	content, _ := os.ReadFile(testFile)
	expected := "line 1\nupdated line 2\nline 3\n"
	if string(content) != expected {
		t.Fatalf("expected %q, got %q", expected, string(content))
	}
}

func TestEditFileRejectsMismatchedLineHash(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "edit-test-*")
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "test.txt")
	initial := "line 1\nline 2\nline 3\n"
	os.WriteFile(testFile, []byte(initial), 0644)

	tool := NewEditFileTool(tmpDir)
	args, _ := json.Marshal(map[string]interface{}{
		"path":                "test.txt",
		"start_line":          2,
		"end_line":            2,
		"expected_start_hash": "h:00000000",
		"expected_end_hash":   "h:" + lineHash("line 2"),
		"new_lines":           []string{"updated line 2"},
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("expected hash mismatch error, got success: %s", result.Content)
	}
	if !strings.Contains(result.Content, "line hash mismatch") || !strings.Contains(result.Content, "re-read a narrow range") {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	content, _ := os.ReadFile(testFile)
	if string(content) != initial {
		t.Fatalf("file changed despite hash mismatch:\n%s", string(content))
	}
}

func TestEditFileOutOfRange(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "edit-test-*")
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("only one line\n"), 0644)

	tool := NewEditFileTool(tmpDir)

	// start_lineが範囲外
	args, _ := json.Marshal(map[string]interface{}{
		"path":       "test.txt",
		"start_line": 10,
		"end_line":   10,
		"new_lines":  []string{"new"},
	})

	result, _ := tool.Execute(context.Background(), args)
	if !result.IsError {
		t.Error("expected error for out-of-range, got success")
	}
	if !strings.Contains(result.Content, "exceeds file length") {
		t.Errorf("expected range error message, got: %s", result.Content)
	}
}

func TestEditFileNonExistent(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "edit-test-*")
	defer os.RemoveAll(tmpDir)

	tool := NewEditFileTool(tmpDir)

	args, _ := json.Marshal(map[string]interface{}{
		"path":       "nonexistent.txt",
		"start_line": 1,
		"end_line":   1,
		"new_lines":  []string{"hello"},
	})

	result, _ := tool.Execute(context.Background(), args)
	if !result.IsError {
		t.Error("expected error for nonexistent file")
	}
	if !strings.Contains(result.Content, "use write_file") {
		t.Errorf("expected suggestion to use write_file, got: %s", result.Content)
	}
}

func TestEditFileProtectedPath(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "edit-test-*")
	defer os.RemoveAll(tmpDir)

	// .gitignoreを作成
	os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("*.tmp\n"), 0644)

	tool := NewEditFileTool(tmpDir)

	args, _ := json.Marshal(map[string]interface{}{
		"path":       ".gitignore",
		"start_line": 1,
		"end_line":   1,
		"new_lines":  []string{"*.log"},
	})

	result, _ := tool.Execute(context.Background(), args)
	if !result.IsError {
		t.Error("expected error for protected path")
	}
}

func TestEditFileNewLinesAsString(t *testing.T) {
	// new_linesが文字列で来た場合（Qwen3.5:4bの挙動をシミュレート）
	tmpDir, _ := os.MkdirTemp("", "edit-test-*")
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("line 1\nline 2\nline 3\n"), 0644)

	tool := NewEditFileTool(tmpDir)

	// new_lines を文字列として渡す
	rawArgs := json.RawMessage(`{
        "path": "test.txt",
        "start_line": 2,
        "end_line": 2,
        "new_lines": "new line 2a\nnew line 2b"
    }`)

	result, err := tool.Execute(context.Background(), rawArgs)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}

	content, _ := os.ReadFile(testFile)
	expected := "line 1\nnew line 2a\nnew line 2b\nline 3\n"
	if string(content) != expected {
		t.Errorf("expected %q, got %q", expected, string(content))
	}
}

func TestEditFileNewLinesEmptyString(t *testing.T) {
	// new_linesが空文字列の場合（行削除と同等）
	tmpDir, _ := os.MkdirTemp("", "edit-test-*")
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("a\nb\nc\n"), 0644)

	tool := NewEditFileTool(tmpDir)

	rawArgs := json.RawMessage(`{
        "path": "test.txt",
        "start_line": 2,
        "end_line": 2,
        "new_lines": ""
    }`)

	result, err := tool.Execute(context.Background(), rawArgs)
	if err != nil || result.IsError {
		t.Fatalf("empty string failed: err=%v, result=%v", err, result)
	}

	content, _ := os.ReadFile(testFile)
	expected := "a\nc\n"
	if string(content) != expected {
		t.Errorf("expected %q, got %q", expected, string(content))
	}
}

func TestEditFileRejectsSerializedCodeLineListString(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "edit-test-*")
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "test.py")
	initial := "class Existing:\n    pass\n"
	os.WriteFile(testFile, []byte(initial), 0644)

	tool := NewEditFileTool(tmpDir)
	rawArgs := json.RawMessage(`{
		"path": "test.py",
		"start_line": 1,
		"end_line": 1,
		"new_lines": "[\"class Broken:\", \"    def __init__(self):\", \"        pass\"]"
	}`)

	result, err := tool.Execute(context.Background(), rawArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("expected serialized line list rejection, got success: %s", result.Content)
	}
	if !strings.Contains(result.Content, "serialized list of code lines") {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	content, _ := os.ReadFile(testFile)
	if string(content) != initial {
		t.Fatalf("file changed despite rejection:\n%s", string(content))
	}
}

func TestEditFileRejectsOmittedPlaceholderStringBeforeWrite(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "edit-test-*")
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "test.py")
	initial := "print('ok')\n"
	os.WriteFile(testFile, []byte(initial), 0644)

	tool := NewEditFileTool(tmpDir)
	rawArgs := json.RawMessage(`{
		"path": "test.py",
		"start_line": 1,
		"end_line": 1,
		"new_lines": "[large tool argument omitted before LLM resend]\nThis is an internal context-compaction placeholder"
	}`)

	result, err := tool.Execute(context.Background(), rawArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("expected omitted placeholder rejection, got success: %s", result.Content)
	}
	if !strings.Contains(result.Content, "omitted-content placeholder") {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	content, _ := os.ReadFile(testFile)
	if string(content) != initial {
		t.Fatalf("file changed despite rejection:\n%s", string(content))
	}
}

func TestEditFileAllowsLargeLineReplacement(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "edit-test-*")
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "large.py")
	initial := "print('replace me')\n"
	os.WriteFile(testFile, []byte(initial), 0644)

	newLines := make([]string, 0, 1500)
	for i := 0; i < 1500; i++ {
		newLines = append(newLines, fmt.Sprintf("print(%d)", i))
	}

	tool := NewEditFileTool(tmpDir)
	args, _ := json.Marshal(map[string]interface{}{
		"path":       "large.py",
		"start_line": 1,
		"end_line":   1,
		"new_lines":  newLines,
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("expected large edit_file replacement to succeed, got: %s", result.Content)
	}

	content, _ := os.ReadFile(testFile)
	if got := strings.Count(string(content), "\n"); got != len(newLines) {
		t.Fatalf("line count = %d, want %d", got, len(newLines))
	}
}
