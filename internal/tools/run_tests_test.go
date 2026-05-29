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

func TestRunTestsSelectsGo(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "pkg"), 0755); err != nil {
		t.Fatal(err)
	}

	tool := NewRunTestsTool(root)
	spec, err := tool.selectCommand(filepath.Join(root, "pkg"), "")
	if err != nil {
		t.Fatalf("selectCommand failed: %v", err)
	}

	if spec.language != "go" {
		t.Fatalf("language = %q, want go", spec.language)
	}
	if spec.name != "go" || strings.Join(spec.args, " ") != "test -v ./pkg" {
		t.Fatalf("command = %s %s, want go test -v ./pkg", spec.name, strings.Join(spec.args, " "))
	}
	if spec.workDir != root {
		t.Fatalf("workDir = %q, want %q", spec.workDir, root)
	}
}

func TestRunTestsRejectsOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	tool := NewRunTestsTool(root)
	_, err := tool.resolvePath(filepath.Dir(root))
	if err == nil {
		t.Fatal("resolvePath succeeded for outside workspace, want error")
	}
}

func TestTailOutputKeepsEnd(t *testing.T) {
	out, truncated := tailOutput([]byte("0123456789abcdef"), 6)
	if !truncated {
		t.Fatal("truncated = false, want true")
	}
	if !strings.Contains(out, "abcdef") {
		t.Fatalf("tail output = %q, want final bytes", out)
	}
	if strings.Contains(out, "012345") {
		t.Fatalf("tail output = %q, should drop leading bytes", out)
	}
}

func TestRunTestsFailureAddsInstruction(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main_test.go"), []byte(`package main

import "testing"

func TestFail(t *testing.T) {
	t.Fatal("boom")
}
`), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewRunTestsTool(root)
	tool.timeout = 10 * time.Second
	args, _ := json.Marshal(runTestsArgs{Language: "go"})
	res, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.IsError {
		t.Fatal("Execute succeeded, want test failure")
	}
	if !strings.Contains(res.Content, runTestsFailurePrompt) {
		t.Fatalf("missing failure prompt:\n%s", res.Content)
	}
}
