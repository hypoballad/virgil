package symbols

import "testing"

func TestExtractFromSource_JavaScript(t *testing.T) {
	src := []byte(`
const VERSION = "1.0";
let counter = 0;

export function add(a, b) {
  return a + b;
}

const multiply = (a, b) => a * b;

class Calculator {
  constructor(value) {
    this.value = value;
  }

  increment(delta) {
    this.value += delta;
  }
}
`)

	e := NewExtractor()
	symbols, err := e.ExtractFromSource(src, "javascript")
	if err != nil {
		t.Fatalf("ExtractFromSource failed: %v", err)
	}

	found := symbolsByName(symbols)
	assertSymbol(t, found, "VERSION", SymbolConst, "")
	assertSymbol(t, found, "counter", SymbolVar, "")
	assertSymbol(t, found, "add", SymbolFunction, "")
	assertSymbol(t, found, "multiply", SymbolConst, "")
	assertSymbol(t, found, "Calculator", SymbolStruct, "")
	assertSymbol(t, found, "increment", SymbolMethod, "Calculator")
}

func TestExtractFromSource_TypeScript(t *testing.T) {
	src := []byte(`
export interface User {
  id: string;
}

type UserID = string;

const DEFAULT_ID: UserID = "root";

async function loadUser(id: UserID): Promise<User> {
  return { id };
}

class UserService {
  async getUser(id: UserID): Promise<User> {
    return loadUser(id);
  }
}
`)

	e := NewExtractor()
	symbols, err := e.ExtractFromSource(src, "typescript")
	if err != nil {
		t.Fatalf("ExtractFromSource failed: %v", err)
	}

	found := symbolsByName(symbols)
	assertSymbol(t, found, "User", SymbolInterface, "")
	assertSymbol(t, found, "UserID", SymbolType_, "")
	assertSymbol(t, found, "DEFAULT_ID", SymbolConst, "")
	assertSymbol(t, found, "loadUser", SymbolFunction, "")
	assertSymbol(t, found, "UserService", SymbolStruct, "")
	assertSymbol(t, found, "getUser", SymbolMethod, "UserService")
}

func symbolsByName(symbols []Symbol) map[string]Symbol {
	found := make(map[string]Symbol, len(symbols))
	for _, sym := range symbols {
		found[sym.Name] = sym
	}
	return found
}

func assertSymbol(t *testing.T, found map[string]Symbol, name string, typ SymbolType, receiver string) {
	t.Helper()
	sym, ok := found[name]
	if !ok {
		t.Fatalf("symbol %q not found; found=%v", name, found)
	}
	if sym.Type != typ {
		t.Fatalf("symbol %q type = %q, want %q", name, sym.Type, typ)
	}
	if sym.Receiver != receiver {
		t.Fatalf("symbol %q receiver = %q, want %q", name, sym.Receiver, receiver)
	}
	if sym.Signature == "" {
		t.Fatalf("symbol %q signature is empty", name)
	}
}
