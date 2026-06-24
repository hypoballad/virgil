package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckPythonSyntaxUnavailableIncludesMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.py"), []byte("print('ok')\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	t.Setenv("VIRGIL_PYTHON_BIN", "")

	tool := NewCheckPythonSyntaxTool(root)
	args, _ := json.Marshal(map[string]string{"path": "ok.py"})
	res, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.IsError {
		t.Fatal("Execute succeeded, want checker unavailable")
	}
	if got, _ := res.Metadata["checker_unavailable"].(bool); !got {
		t.Fatalf("missing checker_unavailable metadata: %#v", res.Metadata)
	}
	if !strings.Contains(res.Content, "python checker not found") {
		t.Fatalf("unexpected content:\n%s", res.Content)
	}
}

func TestCheckPythonSyntaxUsesEnvBinary(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.py"), []byte("print('ok')\n"), 0644); err != nil {
		t.Fatal(err)
	}
	bin := fakeExecutable(t, root, "python3")
	t.Setenv("PATH", "")
	t.Setenv("VIRGIL_PYTHON_BIN", bin)

	tool := NewCheckPythonSyntaxTool(root).(*checkerTool)
	spec, unavailable, err := tool.command(checkSyntaxArgs{Path: "ok.py"})
	if err != nil {
		t.Fatalf("command returned error: %v", err)
	}
	if unavailable != "" {
		t.Fatalf("command unavailable: %s", unavailable)
	}
	if spec.name != bin {
		t.Fatalf("binary = %q, want %q", spec.name, bin)
	}
	if strings.Join(spec.args, " ") != "-m py_compile ok.py" {
		t.Fatalf("args = %#v", spec.args)
	}
}

func TestCheckTypeScriptPrefersLocalTSC(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"devDependencies":{"typescript":"latest"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(`{"compilerOptions":{"strict":true}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules", ".bin"), 0755); err != nil {
		t.Fatal(err)
	}
	localTSC := fakeExecutable(t, filepath.Join(root, "node_modules", ".bin"), "tsc")
	t.Setenv("PATH", "")
	t.Setenv("VIRGIL_TSC_BIN", "")

	tool := NewCheckTypeScriptTool(root).(*checkerTool)
	spec, unavailable, err := tool.command(checkSyntaxArgs{Path: "."})
	if err != nil {
		t.Fatalf("command returned error: %v", err)
	}
	if unavailable != "" {
		t.Fatalf("command unavailable: %s", unavailable)
	}
	if spec.name != localTSC {
		t.Fatalf("binary = %q, want %q", spec.name, localTSC)
	}
	if !containsArg(spec.args, "--project") || !containsArg(spec.args, filepath.Join(root, "tsconfig.json")) {
		t.Fatalf("args missing project tsconfig: %#v", spec.args)
	}
}

func fakeExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
