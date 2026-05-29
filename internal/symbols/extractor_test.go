package symbols

import (
	"path/filepath"
	"testing"
)

func TestExtractFromFile_Go(t *testing.T) {
	extractor := NewExtractor()
	path := filepath.Join("testdata", "sample.go")

	outline, err := extractor.ExtractFromFile(path)
	if err != nil {
		t.Fatalf("ExtractFromFile failed: %v", err)
	}

	if outline.Language != "go" {
		t.Errorf("Language = %q, want %q", outline.Language, "go")
	}

	if len(outline.Symbols) == 0 {
		t.Fatal("no symbols extracted")
	}

	// 期待されるシンボルの存在を確認
	expectedNames := map[string]SymbolType{
		"Add":            SymbolFunction,
		"Sub":            SymbolFunction,
		"NewCalculator":  SymbolFunction,
		"helperFunction": SymbolFunction,
		"Increment":      SymbolMethod,
		"Value":          SymbolMethod,
		"Calculator":     SymbolStruct,
		"Greeter":        SymbolInterface,
		"MaxValue":       SymbolConst,
		"CurrentVersion": SymbolVar,
	}

	found := make(map[string]Symbol)
	for _, sym := range outline.Symbols {
		found[sym.Name] = sym
	}

	for name, expectedType := range expectedNames {
		sym, ok := found[name]
		if !ok {
			t.Errorf("symbol %q not found", name)
			continue
		}
		if sym.Type != expectedType {
			t.Errorf("symbol %q: type = %q, want %q", name, sym.Type, expectedType)
		}
		if sym.StartLine <= 0 {
			t.Errorf("symbol %q: StartLine = %d, want > 0", name, sym.StartLine)
		}
	}

	if got := found["Add"].Signature; got != "func Add(a, b int) int" {
		t.Errorf("Add signature = %q, want %q", got, "func Add(a, b int) int")
	}
}

func TestExtractFromFile_UnsupportedExtension(t *testing.T) {
	extractor := NewExtractor()
	_, err := extractor.ExtractFromFile("test.py")
	if err == nil {
		t.Error("expected error for unsupported extension, got nil")
	}
}

func TestExtractFromSource_Go(t *testing.T) {
	src := []byte(`package main

func Hello() string {
	return "hi"
}
`)

	extractor := NewExtractor()
	symbols, err := extractor.ExtractFromSource(src, "go")
	if err != nil {
		t.Fatalf("ExtractFromSource failed: %v", err)
	}

	if len(symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(symbols))
	}

	if symbols[0].Name != "Hello" {
		t.Errorf("Name = %q, want Hello", symbols[0].Name)
	}

	if symbols[0].Type != SymbolFunction {
		t.Errorf("Type = %q, want function", symbols[0].Type)
	}
}

func TestExtractFromFile_EndLineGo(t *testing.T) {
	extractor := NewExtractor()
	outline, err := extractor.ExtractFromFile(filepath.Join("testdata", "endline.go"))
	if err != nil {
		t.Fatalf("ExtractFromFile failed: %v", err)
	}

	found := symbolsByName(outline.Symbols)
	assertSymbolRange(t, found, "singleLine", SymbolFunction, "", 3, 3)
	assertSymbolRange(t, found, "multiLine", SymbolFunction, "", 5, 10)
	assertSymbolRange(t, found, "EndLineReader", SymbolInterface, "", 12, 15)
}

func TestExtractFromFile_EndLinePython(t *testing.T) {
	extractor := NewExtractor()
	outline, err := extractor.ExtractFromFile(filepath.Join("testdata", "endline.py"))
	if err != nil {
		t.Fatalf("ExtractFromFile failed: %v", err)
	}

	found := symbolsByName(outline.Symbols)
	assertSymbolRange(t, found, "top_level", SymbolFunction, "", 1, 2)
	assertSymbolRange(t, found, "EndLineExample", SymbolClass, "", 5, 9)
	assertSymbolRange(t, found, "method", SymbolMethod, "EndLineExample", 6, 9)
	assertSymbolRange(t, found, "outer", SymbolFunction, "", 12, 16)
	assertSymbolRange(t, found, "inner", SymbolFunction, "", 13, 14)
}

func TestExtractPythonSignatureKeepsAnnotationColons(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{
			name:  "typed annotations",
			lines: []string{`def typed(name: str, values: dict[str, list[int]]) -> tuple[str, int]:`},
			want:  `def typed(name: str, values: dict[str, list[int]]) -> tuple[str, int]`,
		},
		{
			name:  "async with string default colon",
			lines: []string{`async def fetch(key: str = "a:b") -> str:`},
			want:  `async def fetch(key: str = "a:b") -> str`,
		},
		{
			name: "multiline typed signature",
			lines: []string{
				`def multiline(`,
				`    name: str,`,
				`    values: dict[str, list[int]],`,
				`) -> tuple[str, int]:`,
			},
			want: `def multiline( name: str, values: dict[str, list[int]], ) -> tuple[str, int]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractPythonSignature(tt.lines, 0); got != tt.want {
				t.Fatalf("signature = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMethodReceiver(t *testing.T) {
	src := []byte(`package main

type T struct{}

func (t *T) Pointer() {}
func (t T) Value() {}
`)

	extractor := NewExtractor()
	symbols, err := extractor.ExtractFromSource(src, "go")
	if err != nil {
		t.Fatalf("ExtractFromSource failed: %v", err)
	}

	receivers := make(map[string]string)
	for _, sym := range symbols {
		if sym.Type == SymbolMethod {
			receivers[sym.Name] = sym.Receiver
		}
	}

	if receivers["Pointer"] != "*T" {
		t.Errorf("Pointer receiver = %q, want *T", receivers["Pointer"])
	}
	if receivers["Value"] != "T" {
		t.Errorf("Value receiver = %q, want T", receivers["Value"])
	}
}

func assertSymbolRange(t *testing.T, found map[string]Symbol, name string, typ SymbolType, receiver string, startLine int, endLine int) {
	t.Helper()

	sym, ok := found[name]
	if !ok {
		t.Fatalf("symbol %q not found", name)
	}
	if sym.Type != typ {
		t.Fatalf("%s type = %s, want %s", name, sym.Type, typ)
	}
	if sym.Receiver != receiver {
		t.Fatalf("%s receiver = %q, want %q", name, sym.Receiver, receiver)
	}
	if sym.StartLine != startLine || sym.EndLine != endLine {
		t.Fatalf("%s range = %d-%d, want %d-%d", name, sym.StartLine, sym.EndLine, startLine, endLine)
	}
}

// TestExtractFromFile_NoLocalVariables はローカル変数が抽出されないことを確認する
func TestExtractFromFile_NoLocalVariables(t *testing.T) {
	extractor := NewExtractor()
	path := filepath.Join("testdata", "sample.go")

	outline, err := extractor.ExtractFromFile(path)
	if err != nil {
		t.Fatalf("ExtractFromFile failed: %v", err)
	}

	// 抽出されてはならないローカル変数の名前リスト
	excluded := []string{"localBuffer", "localPrefix"}

	for _, name := range excluded {
		for _, sym := range outline.Symbols {
			if sym.Name == name {
				t.Errorf("local symbol %q should NOT be extracted but was found (type=%s, line=%d)",
					name, sym.Type, sym.StartLine)
			}
		}
	}
}

// TestExtractFromSource_LocalVarExcluded はソースコード直接渡しでも
// ローカル変数が除外されることを確認する
func TestExtractFromSource_LocalVarExcluded(t *testing.T) {
	src := []byte(`package main

var GlobalVar = "global"
const GlobalConst = 42

func foo() {
	var localVar = "local"
	const localConst = 99
	_ = localVar
	_ = localConst
}
`)

	extractor := NewExtractor()
	symbols, err := extractor.ExtractFromSource(src, "go")
	if err != nil {
		t.Fatalf("ExtractFromSource failed: %v", err)
	}

	// 期待: GlobalVar, GlobalConst, foo の3つのみ
	// localVar, localConst は抽出されてはならない

	expectedNames := map[string]bool{
		"GlobalVar":   false, // false = まだ見つけていない
		"GlobalConst": false,
		"foo":         false,
	}

	excludedNames := []string{"localVar", "localConst"}

	for _, sym := range symbols {
		if _, ok := expectedNames[sym.Name]; ok {
			expectedNames[sym.Name] = true
		}
		for _, exc := range excludedNames {
			if sym.Name == exc {
				t.Errorf("local symbol %q should NOT be extracted but was found", exc)
			}
		}
	}

	// すべての期待されるシンボルが見つかったか
	for name, found := range expectedNames {
		if !found {
			t.Errorf("expected symbol %q not found", name)
		}
	}
}

// TestExtractFromFile_AllTopLevelExtracted はすべてのトップレベル
// var/const が抽出されることを確認する
func TestExtractFromFile_AllTopLevelExtracted(t *testing.T) {
	src := []byte(`package main

import "errors"

const FirstConst = 1
const SecondConst = 2
const ThirdConst = 3

var FirstVar = errors.New("first")
var SecondVar = errors.New("second")
var ThirdVar = errors.New("third")

func foo() {
	const localOnly = 99
	var localVar = "x"
	_ = localOnly
	_ = localVar
}
`)

	extractor := NewExtractor()
	symbols, err := extractor.ExtractFromSource(src, "go")
	if err != nil {
		t.Fatalf("ExtractFromSource failed: %v", err)
	}

	// 期待: 3 const + 3 var + 1 function = 7
	// localOnly, localVar は除外される
	expectedNames := map[string]SymbolType{
		"FirstConst":  SymbolConst,
		"SecondConst": SymbolConst,
		"ThirdConst":  SymbolConst,
		"FirstVar":    SymbolVar,
		"SecondVar":   SymbolVar,
		"ThirdVar":    SymbolVar,
		"foo":         SymbolFunction,
	}

	excludedNames := []string{"localOnly", "localVar"}

	found := make(map[string]Symbol)
	for _, sym := range symbols {
		found[sym.Name] = sym
	}

	// 期待されるシンボルがすべて抽出されているか
	for name, expectedType := range expectedNames {
		sym, ok := found[name]
		if !ok {
			t.Errorf("expected symbol %q not found", name)
			continue
		}
		if sym.Type != expectedType {
			t.Errorf("symbol %q: type = %q, want %q", name, sym.Type, expectedType)
		}
	}

	// 除外されるべきシンボルが含まれていないか
	for _, name := range excludedNames {
		if _, ok := found[name]; ok {
			t.Errorf("symbol %q should be excluded but was extracted", name)
		}
	}
}

// TestExtractFromFile_SortedByLine はシンボルが行番号順に並んでいることを確認する
func TestExtractFromFile_SortedByLine(t *testing.T) {
	src := []byte(`package main

const C1 = 1

type T struct{}

func F1() {}

const C2 = 2

func F2() {}

var V1 = 1
`)

	extractor := NewExtractor()
	symbols, err := extractor.ExtractFromSource(src, "go")
	if err != nil {
		t.Fatalf("ExtractFromSource failed: %v", err)
	}

	if len(symbols) < 2 {
		t.Fatal("not enough symbols extracted")
	}

	// 行番号が昇順になっているか
	for i := 1; i < len(symbols); i++ {
		if symbols[i-1].StartLine > symbols[i].StartLine {
			t.Errorf("symbols not sorted by line: symbols[%d].StartLine=%d > symbols[%d].StartLine=%d (%q > %q)",
				i-1, symbols[i-1].StartLine, i, symbols[i].StartLine,
				symbols[i-1].Name, symbols[i].Name)
		}
	}
}

// TestExtractCallsFromFile_Go は Go ファイルの呼び出し関係抽出をテストする
func TestExtractCallsFromFile_Go(t *testing.T) {
	extractor := NewExtractor()
	path := filepath.Join("testdata", "sample_calls.go")

	graph, err := extractor.ExtractCallsFromFile(path)
	if err != nil {
		t.Fatalf("ExtractCallsFromFile failed: %v", err)
	}

	if graph.Language != "go" {
		t.Errorf("Language = %q, want go", graph.Language)
	}

	if len(graph.Calls) == 0 {
		t.Fatal("no calls extracted")
	}

	hasMainCall := false
	for _, c := range graph.Calls {
		if c.CallerName == "main" && c.CalleeName == "greet" {
			hasMainCall = true
			break
		}
	}
	if !hasMainCall {
		t.Error("expected call from main to greet")
	}

	hasMethodCall := false
	for _, c := range graph.Calls {
		if c.CallerName == "Increment" && c.CallerReceiver == "*Counter" && c.CalleeName == "log" {
			hasMethodCall = true
			break
		}
	}
	if !hasMethodCall {
		t.Error("expected method call from (*Counter).Increment to log")
	}
}

// TestExtractCallsFromFile_Python は Python ファイルの呼び出し関係抽出をテストする
func TestExtractCallsFromFile_Python(t *testing.T) {
	extractor := NewExtractor()
	path := filepath.Join("testdata", "sample_calls.py")

	graph, err := extractor.ExtractCallsFromFile(path)
	if err != nil {
		t.Fatalf("ExtractCallsFromFile failed: %v", err)
	}

	if graph.Language != "python" {
		t.Errorf("Language = %q, want python", graph.Language)
	}

	if len(graph.Calls) == 0 {
		t.Fatal("no calls extracted")
	}

	hasMainCall := false
	for _, c := range graph.Calls {
		if c.CallerName == "main" && c.CalleeName == "greet" {
			hasMainCall = true
			break
		}
	}
	if !hasMainCall {
		t.Error("expected call from main to greet")
	}
}

// TestExtractCallsFromFile_UnsupportedExtension は非対応拡張子のエラーを確認する
func TestExtractCallsFromFile_UnsupportedExtension(t *testing.T) {
	extractor := NewExtractor()
	_, err := extractor.ExtractCallsFromFile("test.rb")
	if err == nil {
		t.Error("expected error for unsupported extension")
	}
}

// TestFindEnclosingSymbol は所属関数の判定をテストする
func TestFindEnclosingSymbol(t *testing.T) {
	symbols := []Symbol{
		{Name: "foo", Type: SymbolFunction, StartLine: 10, EndLine: 20},
		{Name: "bar", Type: SymbolFunction, StartLine: 25, EndLine: 35},
	}

	if s := findEnclosingSymbol(symbols, 15); s == nil || s.Name != "foo" {
		t.Errorf("line 15 should be in foo")
	}
	if s := findEnclosingSymbol(symbols, 30); s == nil || s.Name != "bar" {
		t.Errorf("line 30 should be in bar")
	}
	if s := findEnclosingSymbol(symbols, 22); s != nil {
		t.Errorf("line 22 should be in no function, got %s", s.Name)
	}
}
