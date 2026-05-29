package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/hypoballad/virgil/internal/debugctx"
)

func TestPromptWithDebugContextAttachesContext(t *testing.T) {
	m := Model{
		debugContext: &debugctx.Context{
			Source:   "vscode-debugpy",
			Language: "python",
			Stopped:  debugctx.Stopped{Reason: "breakpoint"},
			CurrentFrame: debugctx.Frame{
				File:     "train.py",
				Line:     42,
				Function: "train",
			},
		},
	}

	got := m.promptWithDebugContext("explain this")
	for _, want := range []string{
		"<debug_context>",
		"stopped_reason: breakpoint",
		"file: train.py",
		"User request:\nexplain this",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q:\n%s", want, got)
		}
	}
}

func TestPromptWithDebugContextWithoutContext(t *testing.T) {
	m := Model{}
	if got := m.promptWithDebugContext("plain"); got != "plain" {
		t.Fatalf("promptWithDebugContext() = %q", got)
	}
}

func TestDebugContextSlashCommandAcceptsQuestion(t *testing.T) {
	input := textarea.New()
	input.SetValue("/debug-context この停止位置を見て")
	m := Model{input: input}

	dispatchInput, isSlash := slashCommandInput(m.input.Value())
	if !isSlash {
		t.Fatal("expected slash command")
	}
	question := strings.TrimSpace(strings.TrimPrefix(dispatchInput, "/debug-context"))
	if question != "この停止位置を見て" {
		t.Fatalf("question = %q", question)
	}
}

func TestDebugContextPathPrefersVSCodeLocation(t *testing.T) {
	root := t.TempDir()
	vscodeDir := filepath.Join(root, ".vscode")
	if err := os.MkdirAll(vscodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	vscodePath := filepath.Join(vscodeDir, "debug-context.json")
	if err := os.WriteFile(vscodePath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	virgilDir := filepath.Join(root, ".virgil")
	if err := os.MkdirAll(virgilDir, 0o755); err != nil {
		t.Fatal(err)
	}
	virgilPath := filepath.Join(virgilDir, "debug-context.json")
	if err := os.WriteFile(virgilPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := Model{workspaceRoot: root}
	if got := m.debugContextPath(); got != vscodePath {
		t.Fatalf("debugContextPath() = %q, want %q", got, vscodePath)
	}
}

func TestDebugContextPathFallsBackToVirgilLocation(t *testing.T) {
	root := t.TempDir()
	virgilDir := filepath.Join(root, ".virgil")
	if err := os.MkdirAll(virgilDir, 0o755); err != nil {
		t.Fatal(err)
	}
	virgilPath := filepath.Join(virgilDir, "debug-context.json")
	if err := os.WriteFile(virgilPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := Model{workspaceRoot: root}
	if got := m.debugContextPath(); got != virgilPath {
		t.Fatalf("debugContextPath() = %q, want %q", got, virgilPath)
	}
}

func TestDebugContextPathFindsParentVSCodeLocation(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "train")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	vscodeDir := filepath.Join(parent, ".vscode")
	if err := os.MkdirAll(vscodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	vscodePath := filepath.Join(vscodeDir, "debug-context.json")
	if err := os.WriteFile(vscodePath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := Model{workspaceRoot: root}
	if got := m.debugContextPath(); got != vscodePath {
		t.Fatalf("debugContextPath() = %q, want %q", got, vscodePath)
	}
}

func TestDebugContextPathUsesEnvOverride(t *testing.T) {
	root := t.TempDir()
	t.Setenv("VIRGIL_DEBUG_CONTEXT_PATH", "custom/debug.json")

	m := Model{workspaceRoot: root}
	want := filepath.Join(root, "custom", "debug.json")
	if got := m.debugContextPath(); got != want {
		t.Fatalf("debugContextPath() = %q, want %q", got, want)
	}
}
