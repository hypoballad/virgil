package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileBasic(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "write-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tool := NewWriteFileTool(tmpDir)
	ctx := context.Background()

	// 新規作成
	args, _ := json.Marshal(writeFileArgs{
		Path:    "test.txt",
		Content: "hello world",
	})

	result, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("write failed: %s", result.Content)
	}
	if result.Content != "Created test.txt (11 bytes, 1 line)" {
		t.Fatalf("unexpected success message: %s", result.Content)
	}

	// ファイル確認
	content, _ := os.ReadFile(filepath.Join(tmpDir, "test.txt"))
	if string(content) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(content))
	}
}

func TestWriteFileOverwrite(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "write-test-*")
	defer os.RemoveAll(tmpDir)

	// 既存ファイル作成
	existingPath := filepath.Join(tmpDir, "existing.txt")
	os.WriteFile(existingPath, []byte("original"), 0644)

	tool := NewWriteFileTool(tmpDir)
	args, _ := json.Marshal(writeFileArgs{
		Path:    "existing.txt",
		Content: "modified",
		Mode:    "overwrite",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("overwrite failed: %s", result.Content)
	}

	content, _ := os.ReadFile(existingPath)
	if string(content) != "modified" {
		t.Errorf("expected 'modified', got %q", string(content))
	}
}

func TestWriteFileRefusesImplicitOverwrite(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "write-test-*")
	defer os.RemoveAll(tmpDir)

	existingPath := filepath.Join(tmpDir, "existing.txt")
	os.WriteFile(existingPath, []byte("original"), 0644)

	tool := NewWriteFileTool(tmpDir)
	args, _ := json.Marshal(writeFileArgs{
		Path:    "existing.txt",
		Content: "modified",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected implicit overwrite to be refused")
	}

	content, _ := os.ReadFile(existingPath)
	if string(content) != "original" {
		t.Errorf("expected original content to remain, got %q", string(content))
	}
}

func TestWriteFileAppend(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "write-test-*")
	defer os.RemoveAll(tmpDir)

	existingPath := filepath.Join(tmpDir, "log.txt")
	os.WriteFile(existingPath, []byte("line 1\n"), 0644)

	tool := NewWriteFileTool(tmpDir)
	args, _ := json.Marshal(writeFileArgs{
		Path:    "log.txt",
		Content: "line 2\n",
		Mode:    "append",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("append failed: %s", result.Content)
	}

	content, _ := os.ReadFile(existingPath)
	if string(content) != "line 1\nline 2\n" {
		t.Errorf("expected concatenated content, got %q", string(content))
	}
}

func TestWriteFileProtectedPath(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "write-test-*")
	defer os.RemoveAll(tmpDir)

	tool := NewWriteFileTool(tmpDir)

	protectedTests := []string{
		".git/config",
		".agent/something",
		".env",
		".gitignore",
	}

	for _, path := range protectedTests {
		args, _ := json.Marshal(writeFileArgs{
			Path:    path,
			Content: "should fail",
		})

		result, err := tool.Execute(context.Background(), args)
		if err != nil {
			t.Errorf("unexpected error for %s: %v", path, err)
			continue
		}
		if !result.IsError {
			t.Errorf("expected error for protected path %s, got success", path)
		}
	}
}

func TestWriteFilePathTraversal(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "write-test-*")
	defer os.RemoveAll(tmpDir)

	tool := NewWriteFileTool(tmpDir)

	args, _ := json.Marshal(writeFileArgs{
		Path:    "../../../etc/passwd",
		Content: "malicious",
	})

	result, _ := tool.Execute(context.Background(), args)
	if !result.IsError {
		t.Error("expected error for path traversal, got success")
	}
}

func TestWriteFileTooLarge(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "write-test-*")
	defer os.RemoveAll(tmpDir)

	tool := NewWriteFileTool(tmpDir)

	// 11MB の内容
	largeContent := string(make([]byte, 11*1024*1024))
	args, _ := json.Marshal(writeFileArgs{
		Path:    "huge.txt",
		Content: largeContent,
	})

	result, _ := tool.Execute(context.Background(), args)
	if !result.IsError {
		t.Error("expected error for oversized content, got success")
	}
}
