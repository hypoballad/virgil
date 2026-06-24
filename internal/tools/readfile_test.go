package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReadFileTool(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "virgil-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	tool := NewReadFileTool(tmpDir)

	t.Run("read small file", func(t *testing.T) {
		content := "hello world\nline 2"
		path := filepath.Join(tmpDir, "small.txt")
		os.WriteFile(path, []byte(content), 0644)

		args := readFileArgs{Path: "small.txt"}
		rawArgs, _ := json.Marshal(args)
		result, err := tool.Execute(context.Background(), rawArgs)

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Errorf("unexpected tool error: %s", result.Content)
		}
		if !strings.Contains(result.Content, "1 | [h:") || !strings.Contains(result.Content, "] hello world") {
			t.Errorf("expected content not found: %s", result.Content)
		}
		if result.Metadata["mode"] != "full" {
			t.Errorf("expected mode full, got %v", result.Metadata["mode"])
		}
	})

	t.Run("read range", func(t *testing.T) {
		content := "line 1\nline 2\nline 3\nline 4\nline 5"
		path := filepath.Join(tmpDir, "range.txt")
		os.WriteFile(path, []byte(content), 0644)

		args := readFileArgs{Path: "range.txt", StartLine: 2, EndLine: 4}
		rawArgs, _ := json.Marshal(args)
		result, err := tool.Execute(context.Background(), rawArgs)

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Errorf("unexpected tool error: %s", result.Content)
		}
		lines := strings.Split(strings.TrimSpace(result.Content), "\n")
		// Header (File: ...), separator (---...), and 3 lines of content = 5 lines total
		if len(lines) != 5 {
			t.Errorf("expected 5 lines (including header), got %d: %v", len(lines), lines)
		}
		if !strings.Contains(lines[2], "2 | [h:") || !strings.Contains(lines[2], "] line 2") ||
			!strings.Contains(lines[4], "4 | [h:") || !strings.Contains(lines[4], "] line 4") {
			t.Errorf("unexpected content: %v", lines)
		}
		if result.Metadata["mode"] != "range" {
			t.Errorf("expected mode range, got %v", result.Metadata["mode"])
		}
	})

	t.Run("cap open ended range", func(t *testing.T) {
		var sb strings.Builder
		for i := 1; i <= MaxReadRangeLines+50; i++ {
			sb.WriteString(fmt.Sprintf("line %d\n", i))
		}
		path := filepath.Join(tmpDir, "open_range.txt")
		os.WriteFile(path, []byte(sb.String()), 0644)

		args := readFileArgs{Path: "open_range.txt", StartLine: 10}
		rawArgs, _ := json.Marshal(args)
		result, err := tool.Execute(context.Background(), rawArgs)

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected tool error: %s", result.Content)
		}
		if !strings.Contains(result.Content, fmt.Sprintf("lines 10-%d", 10+MaxReadRangeLines-1)) {
			t.Fatalf("expected capped line range header, got:\n%s", result.Content)
		}
		if !strings.Contains(result.Content, "read_file range capped") {
			t.Fatalf("expected capped range guidance, got:\n%s", result.Content)
		}
		if result.Metadata["range_capped"] != true {
			t.Fatalf("expected range_capped metadata, got %#v", result.Metadata)
		}
	})

	t.Run("refuse full markdown read", func(t *testing.T) {
		content := "# Report\n\n## Section\n\nbody\n"
		path := filepath.Join(tmpDir, "report.md")
		os.WriteFile(path, []byte(content), 0644)

		args := readFileArgs{Path: "report.md"}
		rawArgs, _ := json.Marshal(args)
		result, err := tool.Execute(context.Background(), rawArgs)

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatalf("expected full Markdown read to be refused, got: %s", result.Content)
		}
		for _, want := range []string{
			"Refusing full Markdown read",
			"get_markdown_outline",
			"read_markdown_section",
			"edit_with_pattern directly",
		} {
			if !strings.Contains(result.Content, want) {
				t.Fatalf("expected refusal to contain %q, got:\n%s", want, result.Content)
			}
		}
	})

	t.Run("allow markdown range read", func(t *testing.T) {
		content := "# Report\n\n## Section\n\nbody\n"
		path := filepath.Join(tmpDir, "range.md")
		os.WriteFile(path, []byte(content), 0644)

		args := readFileArgs{Path: "range.md", StartLine: 3, EndLine: 5}
		rawArgs, _ := json.Marshal(args)
		result, err := tool.Execute(context.Background(), rawArgs)

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected tool error: %s", result.Content)
		}
		if !strings.Contains(result.Content, "3 | [h:") || !strings.Contains(result.Content, "] ## Section") ||
			!strings.Contains(result.Content, "5 | [h:") || !strings.Contains(result.Content, "] body") {
			t.Fatalf("expected range content, got:\n%s", result.Content)
		}
	})

	t.Run("summarize code file over line limit without range", func(t *testing.T) {
		var sb strings.Builder
		sb.WriteString("package main\n\n")
		for i := 0; i < FullCodeReadLineLimit+5; i++ {
			sb.WriteString(fmt.Sprintf("func helper%d() {}\n", i))
		}
		path := filepath.Join(tmpDir, "large_code.go")
		os.WriteFile(path, []byte(sb.String()), 0644)

		args := readFileArgs{Path: "large_code.go"}
		rawArgs, _ := json.Marshal(args)
		result, err := tool.Execute(context.Background(), rawArgs)

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected tool error: %s", result.Content)
		}
		for _, want := range []string{
			"Refusing full read_file without a range",
			"get_file_outline",
			"get_symbol_outline",
			"read_symbol",
		} {
			if !strings.Contains(result.Content, want) {
				t.Fatalf("expected code summary to contain %q, got:\n%s", want, result.Content)
			}
		}
		if strings.Contains(result.Content, "helper") && strings.Contains(result.Content, "104") {
			t.Fatalf("code summary should not include full body, got:\n%s", result.Content)
		}
	})

	t.Run("allow small code file full read", func(t *testing.T) {
		content := "package main\n\nfunc main() {}\n"
		path := filepath.Join(tmpDir, "small.go")
		os.WriteFile(path, []byte(content), 0644)

		args := readFileArgs{Path: "small.go"}
		rawArgs, _ := json.Marshal(args)
		result, err := tool.Execute(context.Background(), rawArgs)

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected tool error: %s", result.Content)
		}
		if !strings.Contains(result.Content, "func main()") {
			t.Fatalf("expected small code body, got:\n%s", result.Content)
		}
	})

	t.Run("read large file summary", func(t *testing.T) {
		path := filepath.Join(tmpDir, "large.txt")
		f, _ := os.Create(path)
		// Write more than 50KB
		data := strings.Repeat("a", 1024) // 1KB
		for i := 0; i < 60; i++ {
			f.WriteString(data)
			f.WriteString("\n")
		}
		f.Close()

		args := readFileArgs{Path: "large.txt"}
		rawArgs, _ := json.Marshal(args)
		result, err := tool.Execute(context.Background(), rawArgs)

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Errorf("unexpected tool error: %s", result.Content)
		}
		if !strings.Contains(result.Content, "File is large") {
			t.Errorf("expected summary message not found: %s", result.Content)
		}
		if result.Metadata["mode"] != "summary" {
			t.Errorf("expected mode summary, got %v", result.Metadata["mode"])
		}
	})

	t.Run("file not found", func(t *testing.T) {
		args := readFileArgs{Path: "non-existent.txt"}
		rawArgs, _ := json.Marshal(args)
		result, _ := tool.Execute(context.Background(), rawArgs)

		if !result.IsError {
			t.Error("expected error for non-existent file")
		}
		if !strings.Contains(result.Content, "file not found") {
			t.Errorf("expected 'file not found' error, got: %s", result.Content)
		}
	})

	t.Run("security: outside allowed root", func(t *testing.T) {
		args := readFileArgs{Path: "../outside.txt"}
		rawArgs, _ := json.Marshal(args)
		result, _ := tool.Execute(context.Background(), rawArgs)

		if !result.IsError {
			t.Error("expected error for path outside allowed root")
		}
		if !strings.Contains(result.Content, "path outside allowed root") {
			t.Errorf("expected security error, got: %s", result.Content)
		}
	})

	t.Run("binary file", func(t *testing.T) {
		path := filepath.Join(tmpDir, "binary.bin")
		// Write some NUL bytes
		os.WriteFile(path, []byte{0, 1, 2, 3, 4, 0}, 0644)

		args := readFileArgs{Path: "binary.bin"}
		rawArgs, _ := json.Marshal(args)
		result, _ := tool.Execute(context.Background(), rawArgs)

		if !result.IsError {
			t.Error("expected error for binary file")
		}
		if !strings.Contains(result.Content, "binary file not supported") {
			t.Errorf("expected binary file error, got: %s", result.Content)
		}
	})
}

func TestReadFileGuidanceAvoidsXMLStylePlaceholders(t *testing.T) {
	xmlLikePlaceholder := regexp.MustCompile(`<([A-Za-z][A-Za-z0-9_-]*)>`)
	outputs := []string{
		formatCodeFullReadSummary("large.py", 12345, 200),
		formatMarkdownFullReadRefusal("report.md"),
	}
	for _, output := range outputs {
		if xmlLikePlaceholder.MatchString(output) {
			t.Fatalf("guidance should avoid XML-style placeholders:\n%s", output)
		}
	}
}
