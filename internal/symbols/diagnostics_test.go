package symbols

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnosePythonSymbols(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.py")
	src := []byte(`class First:
    def method(self):
        return 1

class second_class:
    pass
`)
	if err := os.WriteFile(path, src, 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	opts := PythonDiagnosticsOptions{MaxList: 20}
	diag, err := DiagnosePythonSymbols(path, opts)
	if err != nil {
		t.Fatalf("DiagnosePythonSymbols() error = %v", err)
	}
	if diag.LineCount != 6 {
		t.Fatalf("LineCount = %d, want 6", diag.LineCount)
	}
	if len(diag.ASTClasses) != 2 {
		t.Fatalf("ASTClasses = %+v, want 2 classes", diag.ASTClasses)
	}
	if len(diag.TaggerClasses) != 2 {
		t.Fatalf("TaggerClasses = %+v, want 2 classes", diag.TaggerClasses)
	}
	if len(diag.LineClasses) != 2 {
		t.Fatalf("LineClasses = %+v, want 2 classes", diag.LineClasses)
	}
	if len(diag.ExtractorSymbols) < 3 {
		t.Fatalf("ExtractorSymbols = %+v, want class/function symbols", diag.ExtractorSymbols)
	}

	output := FormatPythonSymbolDiagnostics(diag, opts)
	for _, want := range []string{
		"Tree-sitter parse:",
		"AST class_definition",
		"Tagger definition.class",
		"Virgil Extractor symbols",
		"Line regex class scan",
		"second_class",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestDiagnosePythonSymbolsRedactsNames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.py")
	if err := os.WriteFile(path, []byte("class SecretName:\n    pass\n"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	opts := PythonDiagnosticsOptions{MaxList: 20, RedactNames: true}
	diag, err := DiagnosePythonSymbols(path, opts)
	if err != nil {
		t.Fatalf("DiagnosePythonSymbols() error = %v", err)
	}
	output := FormatPythonSymbolDiagnostics(diag, opts)
	if strings.Contains(output, "SecretName") {
		t.Fatalf("redacted output leaked symbol name:\n%s", output)
	}
	if !strings.Contains(output, "name_") {
		t.Fatalf("redacted output missing hash placeholder:\n%s", output)
	}
}
