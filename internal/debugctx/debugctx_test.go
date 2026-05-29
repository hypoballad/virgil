package debugctx

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLoadDebugContextFormatsPrompt(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "train.py")
	if err := os.WriteFile(source, []byte("print('x')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mtime := time.Now().Add(-1 * time.Minute)
	if err := os.Chtimes(source, mtime, mtime); err != nil {
		t.Fatal(err)
	}

	jsonPath := filepath.Join(dir, ".virgil-debug-context.json")
	data := `{
  "schema_version": 1,
  "source": "vscode-debugpy",
  "exported_at": "2026-05-23T00:00:00Z",
  "workspace_root": "` + slashEscape(dir) + `",
  "language": "python",
  "event": "stopped",
  "stopped": {"reason": "breakpoint"},
  "current_frame": {
    "file": "train.py",
    "absolute_file": "` + slashEscape(source) + `",
    "line": 1,
    "function": "train",
    "file_mtime_unix": ` + itoa(mtime.Unix()) + `,
    "code_context": {
      "start_line": 1,
      "current_line": 1,
      "lines": [{"line": 1, "text": "print('x')"}]
    },
    "locals": [{"name": "count", "type": "int", "value": "1"}]
  },
  "stack": [{"file": "train.py", "line": 1, "function": "train"}]
}`
	if err := os.WriteFile(jsonPath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, err := Load(jsonPath, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := ctx.ActiveLabel(); got != "train.py:1" {
		t.Fatalf("ActiveLabel() = %q", got)
	}
	prompt := ctx.FormatForPrompt()
	for _, want := range []string{
		"<debug_context>",
		"stopped_reason: breakpoint",
		"file: train.py",
		"> 1 | print('x')",
		"- count (int) = 1",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestLoadDebugContextWarnsOnStaleMtime(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "train.py")
	if err := os.WriteFile(source, []byte("print('new')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	jsonPath := filepath.Join(dir, "debug-context.json")
	data := `{
  "schema_version": 1,
  "current_frame": {
    "file": "train.py",
    "line": 1,
    "file_mtime_unix": 1
  }
}`
	if err := os.WriteFile(jsonPath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, err := Load(jsonPath, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctx.Warnings) == 0 {
		t.Fatal("expected stale mtime warning")
	}
}

func TestLoadDebugContextTrimsDuplicatedWorkspaceBasename(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "train")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join(parent, "debug-context.json")
	data := `{
  "schema_version": 1,
  "current_frame": {
    "file": "train/src/AE_pytorch.py",
    "line": 3491,
    "function": "load_train_result"
  },
  "stack": [{"file": "train/src/AE_pytorch.py", "line": 3491, "function": "load_train_result"}],
  "user_focus": {"active_file": "train/src/AE_pytorch.py"}
}`
	if err := os.WriteFile(jsonPath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, err := Load(jsonPath, root)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.CurrentFrame.File != "src/AE_pytorch.py" {
		t.Fatalf("CurrentFrame.File = %q", ctx.CurrentFrame.File)
	}
	if ctx.Stack[0].File != "src/AE_pytorch.py" {
		t.Fatalf("Stack[0].File = %q", ctx.Stack[0].File)
	}
	if ctx.UserFocus.ActiveFile != "src/AE_pytorch.py" {
		t.Fatalf("UserFocus.ActiveFile = %q", ctx.UserFocus.ActiveFile)
	}
}

func TestLoadDebugContextDerivesExceptionCandidatesFromLocals(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "debug-context.json")
	data := `{
  "schema_version": 1,
  "stopped": {"reason": "unknown"},
  "exception": {"type": "", "message": "", "traceback_source": "none"},
  "current_frame": {
    "file": "train.py",
    "line": 10,
    "function": "train",
    "locals": [
      {"name": "a", "type": "ValueError", "value": "ValueError('shape mismatch')"},
      {"name": "count", "type": "int", "value": "1"}
    ]
  }
}`
	if err := os.WriteFile(jsonPath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, err := Load(jsonPath, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctx.ExceptionCandidates) != 1 {
		t.Fatalf("ExceptionCandidates len = %d", len(ctx.ExceptionCandidates))
	}
	if got := ctx.ExceptionCandidates[0].Name; got != "a" {
		t.Fatalf("candidate name = %q", got)
	}
	if ctx.Exception.Type != "ValueError" {
		t.Fatalf("Exception.Type = %q", ctx.Exception.Type)
	}
	if ctx.Exception.TracebackSource != "locals:a" {
		t.Fatalf("TracebackSource = %q", ctx.Exception.TracebackSource)
	}
	prompt := ctx.FormatForPrompt()
	for _, want := range []string{
		"exception_candidates:",
		"name: a",
		"value: ValueError('shape mismatch')",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestWithPromptNoDebugContext(t *testing.T) {
	if got := WithPrompt(nil, "hello"); got != "hello" {
		t.Fatalf("WithPrompt(nil) = %q", got)
	}
}

func slashEscape(path string) string {
	return strings.ReplaceAll(filepath.ToSlash(path), `\`, `\\`)
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
