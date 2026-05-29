package symbols

import (
	"path/filepath"
	"testing"
)

func TestExtractFromFile_Python(t *testing.T) {
	e := NewExtractor()
	outline, err := e.ExtractFromFile("testdata/sample.py")
	if err != nil {
		t.Fatalf("failed to extract: %v", err)
	}

	if outline.Language != "python" {
		t.Errorf("expected language python, got %s", outline.Language)
	}

	// 期待されるシンボルリスト (行番号順)
	expected := []struct {
		name     string
		symType  SymbolType
		receiver string
	}{
		{"add", SymbolFunction, ""},
		{"Calculator", SymbolClass, ""},
		{"VERSION", SymbolVar, "Calculator"},
		{"__init__", SymbolMethod, "Calculator"},
		{"increment", SymbolMethod, "Calculator"},
		{"get_value", SymbolMethod, "Calculator"},
		{"top_level_func", SymbolFunction, ""},
		{"GLOBAL_VAR", SymbolVar, ""},
		{"PI", SymbolVar, ""},
	}

	if len(outline.Symbols) != len(expected) {
		t.Errorf("expected %d symbols, got %d", len(expected), len(outline.Symbols))
		for _, s := range outline.Symbols {
			t.Logf("Found: %s (%s) receiver=%s line=%d", s.Name, s.Type, s.Receiver, s.StartLine)
		}
	}

	for i, exp := range expected {
		if i >= len(outline.Symbols) {
			break
		}
		got := outline.Symbols[i]
		if got.Name != exp.name {
			t.Errorf("symbol %d: expected name %s, got %s", i, exp.name, got.Name)
		}
		if got.Type != exp.symType {
			t.Errorf("symbol %d (%s): expected type %s, got %s", i, got.Name, exp.symType, got.Type)
		}
		if got.Receiver != exp.receiver {
			t.Errorf("symbol %d (%s): expected receiver %s, got %s", i, got.Name, exp.receiver, got.Receiver)
		}
	}
}

func TestExtractFromSource_Python(t *testing.T) {
	e := NewExtractor()
	src := []byte(`
def hello():
    print("world")

class MyClass:
    def method(self):
        pass
`)
	symbols, err := e.ExtractFromSource(src, "python")
	if err != nil {
		t.Fatalf("failed to extract: %v", err)
	}

	if len(symbols) != 3 {
		t.Errorf("expected 3 symbols, got %d", len(symbols))
	}
}

func TestPythonSupportAuditFixture(t *testing.T) {
	e := NewExtractor()
	path := filepath.Join("testdata", "python_audit.py")

	outline, err := e.ExtractFromFile(path)
	if err != nil {
		t.Fatalf("ExtractFromFile failed: %v", err)
	}
	found := symbolsByName(outline.Symbols)

	assertSymbol(t, found, "AuditExample", SymbolClass, "")
	assertSymbol(t, found, "__init__", SymbolMethod, "AuditExample")
	assertSymbol(t, found, "doubled", SymbolMethod, "AuditExample")
	assertSymbol(t, found, "normalize", SymbolMethod, "AuditExample")
	assertSymbol(t, found, "build", SymbolMethod, "AuditExample")
	assertSymbol(t, found, "fetch", SymbolMethod, "AuditExample")
	assertSymbol(t, found, "call_everything", SymbolMethod, "AuditExample")
	assertSymbol(t, found, "load_payload", SymbolFunction, "")
	assertSymbol(t, found, "typed_function", SymbolFunction, "")
	assertSymbol(t, found, "module_level", SymbolFunction, "")

	if got := found["fetch"].Signature; got != "async def fetch(self, key: str) -> dict[str, Any]" {
		t.Fatalf("fetch signature = %q", got)
	}
	if got := found["typed_function"].Signature; got != "def typed_function(name: str, count: int = 1) -> list[str]" {
		t.Fatalf("typed_function signature = %q", got)
	}
	for _, name := range []string{"doubled", "normalize", "build", "fetch"} {
		sig := found[name].Signature
		if sig == "" || sig[0] == '@' {
			t.Fatalf("%s signature should be the def line without decorator-only text, got %q", name, sig)
		}
	}

	graph, err := e.ExtractCallsFromFile(path)
	if err != nil {
		t.Fatalf("ExtractCallsFromFile failed: %v", err)
	}
	if !hasCall(graph.Calls, "fetch", "AuditExample", "load_payload") {
		t.Fatalf("expected fetch to call load_payload; calls=%v", graph.Calls)
	}
	if !hasCall(graph.Calls, "module_level", "", "typed_function") {
		t.Fatalf("expected module_level to call typed_function; calls=%v", graph.Calls)
	}
	if !hasCall(graph.Calls, "module_level", "", "build") {
		t.Fatalf("expected module_level to call AuditExample.build; calls=%v", graph.Calls)
	}
}

func hasCall(calls []CallEdge, callerName string, callerReceiver string, calleeName string) bool {
	for _, call := range calls {
		if call.CallerName == callerName && call.CallerReceiver == callerReceiver && call.CalleeName == calleeName {
			return true
		}
	}
	return false
}
