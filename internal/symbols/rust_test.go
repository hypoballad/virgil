package symbols

import "testing"

func TestExtractFromSource_Rust(t *testing.T) {
	src := []byte(`
pub const VERSION: &str = "1.0";

pub struct Calculator {
    value: i32,
}

pub enum Mode {
    Fast,
    Slow,
}

pub trait Reset {
    fn reset(&mut self);
}

impl Calculator {
    pub fn new(value: i32) -> Self {
        Self { value }
    }

    pub fn increment(&mut self, delta: i32) {
        self.value += delta;
    }
}

pub fn add(a: i32, b: i32) -> i32 {
    a + b
}
`)

	e := NewExtractor()
	symbols, err := e.ExtractFromSource(src, "rust")
	if err != nil {
		t.Fatalf("ExtractFromSource failed: %v", err)
	}

	found := symbolsByName(symbols)
	assertSymbol(t, found, "VERSION", SymbolConst, "")
	assertSymbol(t, found, "Calculator", SymbolStruct, "")
	assertSymbol(t, found, "Mode", SymbolStruct, "")
	assertSymbol(t, found, "Reset", SymbolInterface, "")
	assertSymbol(t, found, "new", SymbolMethod, "Calculator")
	assertSymbol(t, found, "increment", SymbolMethod, "Calculator")
	assertSymbol(t, found, "add", SymbolFunction, "")
}
