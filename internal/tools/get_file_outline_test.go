package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hypoballad/virgil/internal/symbols"
)

func TestGetFileOutline_GoFile(t *testing.T) {
	// テスト用の Go ファイルパス（symbols パッケージの testdata を流用）
	// ワークスペース root を symbols/testdata の親ディレクトリに設定
	wsRoot, err := filepath.Abs("../symbols")
	if err != nil {
		t.Fatalf("failed to get workspace root: %v", err)
	}

	tool := NewGetFileOutlineTool(wsRoot)

	args, _ := json.Marshal(map[string]string{
		"path": "testdata/sample.go",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if result.IsError {
		t.Fatalf("got error result: %s", result.Content)
	}

	// 出力に主要な要素が含まれているか
	expectedSubstrings := []string{
		"# Outline:",
		"Language: go",
		"| Line | Type | Receiver | Name | Signature | Doc |",
		"Add",         // 関数
		"Calculator",  // 構造体
		"Increment",   // メソッド
		"MaxValue",    // 定数
		"*Calculator", // レシーバ
		"Next steps:", // フッターのガイド
		"read_symbol",
	}

	for _, expected := range expectedSubstrings {
		if !strings.Contains(result.Content, expected) {
			t.Errorf("expected output to contain %q, got:\n%s", expected, result.Content)
		}
	}
}

func TestGetFileOutline_TruncatedSignatureSuggestsReadSymbol(t *testing.T) {
	outline := &symbols.FileOutline{
		FilePath: "long.py",
		Language: "python",
		Symbols: []symbols.Symbol{
			{
				Name:      "very_long_function",
				Type:      symbols.SymbolFunction,
				StartLine: 1,
				Signature: "def very_long_function(first_argument: list[str], second_argument: dict[str, int], third_argument: tuple[str, ...], fourth_argument: Callable[[str], None], fifth_argument: object) -> None",
			},
			{
				Name:       "Recovered",
				Type:       symbols.SymbolClass,
				StartLine:  10,
				Signature:  "class Recovered:",
				IsFallback: true,
			},
		},
	}

	output := formatOutlineAsMarkdown(outline, "long.py")
	if !strings.Contains(output, "... [truncated; use read_symbol]") {
		t.Fatalf("expected truncation hint in output, got:\n%s", output)
	}
	if !strings.Contains(output, "read_symbol(path=\"long.py\", symbol_name=\"SYMBOL_NAME\")") {
		t.Fatalf("expected read_symbol next step in output, got:\n%s", output)
	}
	if !strings.Contains(output, "class (via fallback)") {
		t.Fatalf("expected fallback marker in output, got:\n%s", output)
	}
}

func TestGetFileOutline_PythonFile(t *testing.T) {
	wsRoot, err := filepath.Abs("../symbols")
	if err != nil {
		t.Fatalf("failed to get workspace root: %v", err)
	}

	tool := NewGetFileOutlineTool(wsRoot)

	args, _ := json.Marshal(map[string]string{
		"path": "testdata/sample.py",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if result.IsError {
		t.Fatalf("got error result: %s", result.Content)
	}

	// 出力に主要な要素が含まれているか
	expectedSubstrings := []string{
		"# Outline:",
		"Language: python",
		"| Line | Type | Receiver | Name | Signature | Doc |",
		"add",        // 関数
		"Calculator", // クラス
		"A simple calculator class.",
		"increment",  // メソッド
		"GLOBAL_VAR", // 変数
		"Calculator", // レシーバ
	}

	for _, expected := range expectedSubstrings {
		if !strings.Contains(result.Content, expected) {
			t.Errorf("expected output to contain %q, got:\n%s", expected, result.Content)
		}
	}
}

func TestGetFileOutline_PythonTypedSignatures(t *testing.T) {
	wsRoot, err := filepath.Abs("../symbols")
	if err != nil {
		t.Fatalf("failed to get workspace root: %v", err)
	}

	tool := NewGetFileOutlineTool(wsRoot)

	args, _ := json.Marshal(map[string]string{
		"path": "testdata/python_audit.py",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("got error result: %s", result.Content)
	}

	expectedSubstrings := []string{
		"async def fetch(self, key: str) -> dict[str, Any]",
		"def typed_function(name: str, count: int = 1) -> list[str]",
	}
	for _, expected := range expectedSubstrings {
		if !strings.Contains(result.Content, expected) {
			t.Errorf("expected output to contain %q, got:\n%s", expected, result.Content)
		}
	}
}

func TestGetFileOutline_FiltersByReceiverAndType(t *testing.T) {
	wsRoot, err := filepath.Abs("../symbols")
	if err != nil {
		t.Fatalf("failed to get workspace root: %v", err)
	}

	tool := NewGetFileOutlineTool(wsRoot)

	args, _ := json.Marshal(map[string]interface{}{
		"path":     "testdata/sample.py",
		"type":     "method",
		"receiver": "Calculator",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("got error result: %s", result.Content)
	}

	expectedSubstrings := []string{
		"filtered from",
		`type="method"`,
		`receiver="Calculator"`,
		"increment",
		"Calculator",
	}
	for _, expected := range expectedSubstrings {
		if !strings.Contains(result.Content, expected) {
			t.Errorf("expected output to contain %q, got:\n%s", expected, result.Content)
		}
	}

	unexpectedSubstrings := []string{
		"| `add` |",
		"| `GLOBAL_VAR` |",
	}
	for _, unexpected := range unexpectedSubstrings {
		if strings.Contains(result.Content, unexpected) {
			t.Errorf("expected output not to contain %q, got:\n%s", unexpected, result.Content)
		}
	}
}

func TestGetFileOutline_FilterHelpers(t *testing.T) {
	outline := &symbols.FileOutline{
		FilePath: "sample.py",
		Language: "python",
		Symbols: []symbols.Symbol{
			{Name: "Recovered", Type: symbols.SymbolClass, StartLine: 1, Signature: "class Recovered:", IsFallback: true},
			{Name: "run", Type: symbols.SymbolMethod, Receiver: "Recovered", StartLine: 2, Signature: "def run(self):", IsFallback: true},
			{Name: "helper", Type: symbols.SymbolFunction, StartLine: 10, Signature: "def helper():"},
		},
	}

	filters := outlineFilters{
		Receiver:       "Recovered",
		Type:           "method",
		FallbackOnly:   true,
		IncludeMethods: true,
		HasFilters:     true,
	}
	filtered := filterOutlineSymbols(outline.Symbols, filters)
	if len(filtered) != 1 || filtered[0].Name != "run" {
		t.Fatalf("expected only Recovered.run, got %#v", filtered)
	}

	output := formatOutlineAsMarkdownWithFilters(&symbols.FileOutline{
		FilePath: outline.FilePath,
		Language: outline.Language,
		Symbols:  filtered,
	}, "sample.py", len(outline.Symbols), filters)
	if !strings.Contains(output, "method (via fallback)") {
		t.Fatalf("expected fallback marker in filtered output, got:\n%s", output)
	}
	if !strings.Contains(output, "Symbols: 1 (filtered from 3)") {
		t.Fatalf("expected filtered count in output, got:\n%s", output)
	}

	includeMethods := false
	filters = newOutlineFilters(getFileOutlineArgs{IncludeMethods: &includeMethods})
	filtered = filterOutlineSymbols(outline.Symbols, filters)
	if len(filtered) != 2 {
		t.Fatalf("expected include_methods=false to remove one method, got %#v", filtered)
	}
	for _, sym := range filtered {
		if sym.Type == symbols.SymbolMethod {
			t.Fatalf("expected no methods with include_methods=false, got %#v", filtered)
		}
	}
}

func TestGetFileOutline_LargeUnfilteredOutlineReturnsSummary(t *testing.T) {
	symbolList := make([]symbols.Symbol, 0, getFileOutlineLargeSymbolThreshold+1)
	symbolList = append(symbolList, symbols.Symbol{
		Name:      "BigModel",
		Type:      symbols.SymbolClass,
		StartLine: 1,
		EndLine:   500,
		Signature: "class BigModel:",
	})
	for i := 0; i < getFileOutlineLargeSymbolThreshold; i++ {
		symbolList = append(symbolList, symbols.Symbol{
			Name:      "method",
			Type:      symbols.SymbolMethod,
			Receiver:  "BigModel",
			StartLine: i + 2,
			EndLine:   i + 2,
			Signature: "def method(self):",
		})
	}

	output := formatOutlineAsMarkdownWithFilters(&symbols.FileOutline{
		FilePath: "large.py",
		Language: "python",
		Symbols:  symbolList,
	}, "large.py", len(symbolList), outlineFilters{IncludeMethods: true})

	for _, want := range []string{
		"# Outline Summary: large.py",
		"large outline summarized",
		"method: 120",
		"BigModel: 120 method(s)",
		"get_symbol_outline(path=\"large.py\", symbol_name=\"BigModel\")",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestGetFileOutline_NonExistentFile(t *testing.T) {
	tool := NewGetFileOutlineTool("/tmp")

	args, _ := json.Marshal(map[string]string{
		"path": "does_not_exist.go",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if !result.IsError {
		t.Error("expected IsError=true for non-existent file")
	}
}

func TestGetFileOutline_UnsupportedExtension(t *testing.T) {
	// 一時ファイルを作成
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte("hello world\n"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	tool := NewGetFileOutlineTool(tmpDir)
	args, _ := json.Marshal(map[string]string{
		"path": "test.txt",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if !result.IsError {
		t.Error("expected IsError=true for unsupported extension")
	}

	if !strings.Contains(result.Content, "unsupported file type") {
		t.Errorf("expected error message to mention unsupported file type, got: %s", result.Content)
	}
}

func TestGetFileOutline_EmptyPath(t *testing.T) {
	tool := NewGetFileOutlineTool("/tmp")

	args, _ := json.Marshal(map[string]string{
		"path": "",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if !result.IsError {
		t.Error("expected IsError=true for empty path")
	}
}
