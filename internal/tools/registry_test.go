package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type registryTestTool struct {
	name     string
	mutating bool
	calls    int
}

func (t *registryTestTool) Name() string {
	return t.name
}

func (t *registryTestTool) Definition() ToolDefinition {
	return ToolDefinition{Type: "function", Function: FunctionDefinition{Name: t.name}}
}

func (t *registryTestTool) Execute(ctx context.Context, args json.RawMessage) (*Result, error) {
	t.calls++
	return SuccessResult("executed"), nil
}

func (t *registryTestTool) IsMutating() bool {
	return t.mutating
}

func TestRegistryEditAllowlistBlocksMutatingToolOutsideAllowedPath(t *testing.T) {
	registry := NewRegistry()
	tool := &registryTestTool{name: "edit_with_pattern", mutating: true}
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}
	registry.SetEditAllowlist("/workspace", []string{"src/MAE_testcase/", "src/AE_pytorch.py"}, "test allowlist")

	result, err := registry.Execute(context.Background(), "edit_with_pattern", mustRegistryJSON(t, map[string]interface{}{
		"path": "src/interface/process_set_interface.py",
	}))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("expected allowlist block result, got %#v", result)
	}
	if tool.calls != 0 {
		t.Fatalf("tool executed despite allowlist block, calls=%d", tool.calls)
	}
	if !strings.Contains(result.Content, "outside the allowed edit paths") || !strings.Contains(result.Content, "src/MAE_testcase/") {
		t.Fatalf("unexpected block message: %s", result.Content)
	}
}

func TestRegistryEditAllowlistAllowsDirectoryAndExactFile(t *testing.T) {
	registry := NewRegistry()
	tool := &registryTestTool{name: "edit_file", mutating: true}
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}
	registry.SetEditAllowlist("/workspace", []string{"src/MAE_testcase/", "src/AE_pytorch.py"}, "test allowlist")

	for _, path := range []string{"src/MAE_testcase/configs/train.ini", "src/AE_pytorch.py", "/workspace/src/MAE_testcase/test.py"} {
		result, err := registry.Execute(context.Background(), "edit_file", mustRegistryJSON(t, map[string]interface{}{
			"path": path,
		}))
		if err != nil {
			t.Fatalf("Execute(%q) error = %v", path, err)
		}
		if result == nil || result.IsError {
			t.Fatalf("Execute(%q) should pass allowlist, got %#v", path, result)
		}
	}
	if tool.calls != 3 {
		t.Fatalf("tool calls = %d, want 3", tool.calls)
	}
}

func TestRegistryEditAllowlistDoesNotBlockReadOnlyTool(t *testing.T) {
	registry := NewRegistry()
	tool := &registryTestTool{name: "read_file", mutating: false}
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}
	registry.SetEditAllowlist("/workspace", []string{"src/MAE_testcase/"}, "test allowlist")

	result, err := registry.Execute(context.Background(), "read_file", mustRegistryJSON(t, map[string]interface{}{
		"path": "src/interface/process_set_interface.py",
	}))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("read-only tool should not be blocked, got %#v", result)
	}
	if tool.calls != 1 {
		t.Fatalf("tool calls = %d, want 1", tool.calls)
	}
}

func mustRegistryJSON(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
