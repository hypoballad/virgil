package symbols

import "testing"

func TestExtractPythonFallbackSymbols(t *testing.T) {
	src := []byte(`# class FakeComment:
"""module docs
class FakeDoc:
"""
class MyClass(Base):
    def method_one(self):
        def nested():
            pass
        return nested()

    async def method_two(self):
        return None

    class Inner:
        pass

def top_level(a, b) -> int:
    return a

async def fetch():
    return None

class MultiLine(
    Base1,
    Base2,
):
    def configured(self):
        pass
`)

	got := ExtractPythonFallbackSymbols(src)
	want := []struct {
		name      string
		kind      string
		startLine int
		endLine   int
	}{
		{"MyClass", "class", 5, 16},
		{"method_one", "method", 6, 10},
		{"method_two", "method", 11, 16},
		{"top_level", "function", 17, 19},
		{"fetch", "async_function", 20, 22},
		{"MultiLine", "class", 23, 28},
		{"configured", "method", 27, 28},
	}
	if len(got) != len(want) {
		t.Fatalf("len(symbols) = %d, want %d: %+v", len(got), len(want), got)
	}
	for i, exp := range want {
		if got[i].Name != exp.name || got[i].Kind != exp.kind || got[i].StartLine != exp.startLine || got[i].EndLine != exp.endLine {
			t.Fatalf("symbol[%d] = %+v, want %+v", i, got[i], exp)
		}
	}
	if got[1].Receiver != "MyClass" || got[2].Receiver != "MyClass" || got[6].Receiver != "MultiLine" {
		t.Fatalf("method receivers not set: %+v", got)
	}
}

func TestMergeWithFallback(t *testing.T) {
	ast := []Symbol{
		{Name: "Existing", Type: SymbolClass, StartLine: 1, EndLine: 5},
		{Name: "helper", Type: SymbolFunction, StartLine: 7, EndLine: 9},
		{Name: "__init__", Type: SymbolMethod, Receiver: "Parsed", StartLine: 40, EndLine: 45},
	}
	fallback := []FallbackPythonSymbol{
		{Name: "Existing", Kind: "class", StartLine: 1, EndLine: 5, Signature: "class Existing:"},
		{Name: "helper", Kind: "method", Receiver: "Recovered", StartLine: 21, EndLine: 22, Signature: "def helper(self):"},
		{Name: "Recovered", Kind: "class", StartLine: 20, EndLine: 30, Signature: "class Recovered:"},
		{Name: "recovered_func", Kind: "function", StartLine: 32, EndLine: 34, Signature: "def recovered_func():"},
		{Name: "__init__", Kind: "method", Receiver: "Parsed", StartLine: 40, EndLine: 45, Signature: "def __init__(self):"},
	}

	merged := MergeWithFallback(ast, fallback)
	if len(merged) != 6 {
		t.Fatalf("len(merged) = %d, want 6: %+v", len(merged), merged)
	}
	if merged[2].Name != "Recovered" || merged[2].Type != SymbolClass || !merged[2].IsFallback {
		t.Fatalf("merged fallback class = %+v", merged[2])
	}
	if merged[4].Name != "recovered_func" || merged[4].Type != SymbolFunction || !merged[4].IsFallback {
		t.Fatalf("merged fallback function = %+v", merged[4])
	}
	if merged[3].Name != "helper" || merged[3].Type != SymbolMethod || merged[3].Receiver != "Recovered" || !merged[3].IsFallback {
		t.Fatalf("merged fallback method = %+v", merged[3])
	}
	countInitLine40 := 0
	for _, sym := range merged {
		if sym.Name == "__init__" && sym.StartLine == 40 {
			countInitLine40++
		}
	}
	if countInitLine40 != 1 {
		t.Fatalf("duplicate __init__ line 40 count = %d, symbols=%+v", countInitLine40, merged)
	}
}

func TestExtractFromSourceMergesPythonFallbackSymbols(t *testing.T) {
	src := []byte(`class Parsed:
    pass

class Recovered:
    pass
`)

	symbols, err := NewExtractor().ExtractFromSource(src, "python")
	if err != nil {
		t.Fatalf("ExtractFromSource() error = %v", err)
	}
	found := symbolsByName(symbols)
	if found["Parsed"].IsFallback {
		t.Fatalf("AST symbol should not be fallback: %+v", found["Parsed"])
	}
	if _, ok := found["Recovered"]; !ok {
		t.Fatalf("Recovered not found: %+v", symbols)
	}
}
