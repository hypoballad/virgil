package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hypoballad/virgil/internal/symbols"
)

func TestReadSymbol_GoFunction(t *testing.T) {
	wsRoot, err := filepath.Abs("../symbols")
	if err != nil {
		t.Fatalf("failed to get workspace root: %v", err)
	}

	tool := NewReadSymbolTool(wsRoot)
	args, _ := json.Marshal(map[string]string{
		"path":        "testdata/sample.go",
		"symbol_name": "Add",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("got error result: %s", result.Content)
	}

	expected := []string{
		"Symbol: Add in testdata/sample.go",
		"Language: go | Matches: 1",
		"Type: function",
		"func Add(a, b int) int",
		"   9 | func Add(a, b int) int {",
		"  10 | \treturn a + b",
		"  11 | }",
	}
	for _, s := range expected {
		if !strings.Contains(result.Content, s) {
			t.Fatalf("expected output to contain %q, got:\n%s", s, result.Content)
		}
	}
}

func TestReadSymbol_ReturnsAllMatchingSymbols(t *testing.T) {
	tmpDir := t.TempDir()
	source := `package sample

type Alpha struct{}
type Beta struct{}

func (a Alpha) Execute() string {
	return "alpha"
}

func (b Beta) Execute() string {
	return "beta"
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "dupes.go"), []byte(source), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tool := NewReadSymbolTool(tmpDir)
	args, _ := json.Marshal(map[string]string{
		"path":        "dupes.go",
		"symbol_name": "Execute",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("got error result: %s", result.Content)
	}

	expected := []string{
		"Language: go | Matches: 2",
		"Match 1 of 2",
		"Receiver: Alpha",
		"return \"alpha\"",
		"Match 2 of 2",
		"Receiver: Beta",
		"return \"beta\"",
	}
	for _, s := range expected {
		if !strings.Contains(result.Content, s) {
			t.Fatalf("expected output to contain %q, got:\n%s", s, result.Content)
		}
	}
}

func TestReadSymbol_NotFound(t *testing.T) {
	wsRoot, err := filepath.Abs("../symbols")
	if err != nil {
		t.Fatalf("failed to get workspace root: %v", err)
	}

	tool := NewReadSymbolTool(wsRoot)
	args, _ := json.Marshal(map[string]string{
		"path":        "testdata/sample.go",
		"symbol_name": "DoesNotExist",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
	if !strings.Contains(result.Content, `Symbol "DoesNotExist" not found in testdata/sample.go`) {
		t.Fatalf("unexpected error content: %s", result.Content)
	}
}

func TestReadSymbol_UnsupportedExtension(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "notes.txt"), []byte("hello\n"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tool := NewReadSymbolTool(tmpDir)
	args, _ := json.Marshal(map[string]string{
		"path":        "notes.txt",
		"symbol_name": "hello",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
	if !strings.Contains(result.Content, "unsupported file type") {
		t.Fatalf("unexpected error content: %s", result.Content)
	}
}

func TestFormatReadSymbolResultMarksFallback(t *testing.T) {
	matches := []symbols.Symbol{
		{
			Name:       "Recovered",
			Type:       symbols.SymbolClass,
			Signature:  "class Recovered:",
			StartLine:  1,
			EndLine:    2,
			IsFallback: true,
		},
	}
	lines := []string{"class Recovered:", "    pass"}

	output := formatReadSymbolResult("broken.py", "python", "Recovered", matches, matches, lines, false)
	if !strings.Contains(output, "Type: class (via fallback)") {
		t.Fatalf("expected fallback marker, got:\n%s", output)
	}
	if !strings.Contains(output, "   2 |     pass") {
		t.Fatalf("expected symbol body, got:\n%s", output)
	}
}

func TestReadSymbol_LargeClassDefaultsToSummary(t *testing.T) {
	tmpDir := t.TempDir()
	source := largePythonClassSource()
	if err := os.WriteFile(filepath.Join(tmpDir, "large.py"), []byte(source), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tool := NewReadSymbolTool(tmpDir)
	args, _ := json.Marshal(map[string]interface{}{
		"path":        "large.py",
		"symbol_name": "BigModel",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("got error result: %s", result.Content)
	}

	expected := []string{
		"Mode: SUMMARY",
		"use full=true",
		"Signature:",
		"class BigModel",
		"Docstring/comment:",
		"Large model docstring.",
		"Methods (2):",
		"def __init__(self)",
		"def forward(self, x)",
	}
	for _, s := range expected {
		if !strings.Contains(result.Content, s) {
			t.Fatalf("expected output to contain %q, got:\n%s", s, result.Content)
		}
	}
	if strings.Contains(result.Content, "filler_59") {
		t.Fatalf("summary should not include full body, got:\n%s", result.Content)
	}
}

func TestReadSymbol_LargeFunctionSummaryIncludesInternalObservations(t *testing.T) {
	tmpDir := t.TempDir()
	var sb strings.Builder
	sb.WriteString("def train_model(data):\n")
	sb.WriteString("    config = {}\n")
	sb.WriteString("    learning_rate = 0.001\n")
	sb.WriteString("    epochs = 10\n\n")
	sb.WriteString("    encoder = build_encoder(data)\n")
	sb.WriteString("    decoder = build_decoder(encoder)\n\n")
	sb.WriteString("    loss = compute_loss(data, decoder)\n")
	sb.WriteString("    optimizer = tf.train.AdamOptimizer(learning_rate).minimize(loss)\n\n")
	sb.WriteString("    for epoch in range(epochs):\n")
	sb.WriteString("        result = sess.run(optimizer)\n")
	sb.WriteString("        print(result)\n\n")
	sb.WriteString("    return decoder\n")
	for i := 0; i < 70; i++ {
		sb.WriteString("    value_")
		sb.WriteString(fmt.Sprint(i))
		sb.WriteString(" = ")
		sb.WriteString(fmt.Sprint(i))
		sb.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "train.py"), []byte(sb.String()), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tool := NewReadSymbolTool(tmpDir)
	args, _ := json.Marshal(map[string]interface{}{
		"path":        "train.py",
		"symbol_name": "train_model",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("got error result: %s", result.Content)
	}
	for _, want := range []string{
		"Mode: SUMMARY",
		"Internal observations (compressed; no source body returned):",
		"Blocks:",
		"Important calls/APIs:",
		"tf.train.AdamOptimizer",
		"Important assignments:",
		"Suggested focused ranges:",
		"Prefer get_symbol_outline",
		"get_call_graph may help",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, result.Content)
		}
	}
	if strings.Contains(result.Content, "value_69 = 69") {
		t.Fatalf("summary should not include full body, got:\n%s", result.Content)
	}
}

func TestReadSymbol_LargeClassFullModeReturnsBodyWithWarning(t *testing.T) {
	tmpDir := t.TempDir()
	source := largePythonClassSource()
	if err := os.WriteFile(filepath.Join(tmpDir, "large.py"), []byte(source), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tool := NewReadSymbolTool(tmpDir)
	args, _ := json.Marshal(map[string]interface{}{
		"path":        "large.py",
		"symbol_name": "BigModel",
		"full":        true,
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("got error result: %s", result.Content)
	}

	expected := []string{
		"Mode: FULL",
		"Warning: Large symbol",
		"filler_59 = 59",
	}
	for _, s := range expected {
		if !strings.Contains(result.Content, s) {
			t.Fatalf("expected output to contain %q, got:\n%s", s, result.Content)
		}
	}
}

func TestReadSymbol_FullModeSkipsVeryLargeSymbol(t *testing.T) {
	tmpDir := t.TempDir()
	source := largePythonClassSourceWithFillers(readSymbolFullSoftLimitLines + 10)
	if err := os.WriteFile(filepath.Join(tmpDir, "large.py"), []byte(source), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tool := NewReadSymbolTool(tmpDir)
	args, _ := json.Marshal(map[string]interface{}{
		"path":        "large.py",
		"symbol_name": "BigModel",
		"full":        true,
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("soft limit should return guidance, got error: %s", result.Content)
	}
	for _, want := range []string{
		"Full symbol read skipped",
		"read_symbol(path=\"large.py\", symbol_name=\"BigModel\")",
		"get_symbol_outline(path=\"large.py\", symbol_name=\"BigModel\")",
		"get_file_outline(path=\"large.py\"",
		"Do not reconstruct this large symbol by reading adjacent read_file ranges",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("expected guidance to contain %q, got:\n%s", want, result.Content)
		}
	}
	if strings.Contains(result.Content, "filler_") {
		t.Fatalf("guard should not return source body, got:\n%s", result.Content)
	}
}

func TestReadSymbolGuidanceAvoidsXMLStylePlaceholders(t *testing.T) {
	xmlLikePlaceholder := regexp.MustCompile(`<([A-Za-z][A-Za-z0-9_-]*)>`)
	output := formatReadSymbolFullGuard("large.py", "BigModel", readSymbolFullSoftLimitLines+1, false)
	if xmlLikePlaceholder.MatchString(output) {
		t.Fatalf("guidance should avoid XML-style placeholders:\n%s", output)
	}
	if !strings.Contains(output, "receiver is for method disambiguation") {
		t.Fatalf("expected receiver guidance, got:\n%s", output)
	}
	if !strings.Contains(output, "before any read_file ranges") {
		t.Fatalf("expected get_symbol_outline priority guidance, got:\n%s", output)
	}
}

func TestReadSymbol_FullModeRejectsHugeSymbol(t *testing.T) {
	tmpDir := t.TempDir()
	source := largePythonClassSourceWithFillers(readSymbolFullHardLimitLines + 10)
	if err := os.WriteFile(filepath.Join(tmpDir, "huge.py"), []byte(source), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tool := NewReadSymbolTool(tmpDir)
	args, _ := json.Marshal(map[string]interface{}{
		"path":        "huge.py",
		"symbol_name": "BigModel",
		"full":        true,
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !result.IsError {
		t.Fatalf("hard limit should return error, got:\n%s", result.Content)
	}
	if !strings.Contains(result.Content, "Full symbol read refused") {
		t.Fatalf("expected refusal guidance, got:\n%s", result.Content)
	}
	if !strings.Contains(result.Content, "Do not reconstruct this large symbol") {
		t.Fatalf("expected anti-reconstruction guidance, got:\n%s", result.Content)
	}
	if strings.Contains(result.Content, "filler_") {
		t.Fatalf("guard should not return source body, got:\n%s", result.Content)
	}
}

func TestReadSymbol_FilterByReceiver(t *testing.T) {
	tmpDir := t.TempDir()
	source := `package sample

type Alpha struct{}
type Beta struct{}

func (a Alpha) Execute() string {
	return "alpha"
}

func (b Beta) Execute() string {
	return "beta"
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "dupes.go"), []byte(source), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tool := NewReadSymbolTool(tmpDir)
	args, _ := json.Marshal(map[string]string{
		"path":        "dupes.go",
		"symbol_name": "Execute",
		"receiver":    "Beta",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("got error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Language: go | Matches: 1") || !strings.Contains(result.Content, "return \"beta\"") {
		t.Fatalf("expected Beta match, got:\n%s", result.Content)
	}
	if strings.Contains(result.Content, "return \"alpha\"") {
		t.Fatalf("receiver filter should exclude Alpha, got:\n%s", result.Content)
	}
}

func TestReadSymbolDefinitionMentionsFullAndReceiverParameters(t *testing.T) {
	tool := NewReadSymbolTool(t.TempDir())
	def := tool.Definition()
	properties := def.Function.Parameters["properties"].(map[string]interface{})
	if _, ok := properties["full"]; !ok {
		t.Fatal("read_symbol definition should expose full parameter")
	}
	if _, ok := properties["receiver"]; !ok {
		t.Fatal("read_symbol definition should expose receiver parameter")
	}
	if !strings.Contains(def.Function.Description, "full=true") {
		t.Fatalf("description should explain full=true, got: %s", def.Function.Description)
	}
}

func largePythonClassSource() string {
	return largePythonClassSourceWithFillers(120)
}

func largePythonClassSourceWithFillers(fillers int) string {
	var sb strings.Builder
	sb.WriteString("class BigModel:\n")
	sb.WriteString("    \"\"\"Large model docstring.\"\"\"\n")
	sb.WriteString("    def __init__(self):\n")
	sb.WriteString("        self.value = 1\n\n")
	sb.WriteString("    def forward(self, x):\n")
	sb.WriteString("        return x + self.value\n\n")
	for i := 0; i < fillers; i++ {
		sb.WriteString("    filler_")
		sb.WriteString(fmt.Sprint(i))
		sb.WriteString(" = ")
		sb.WriteString(fmt.Sprint(i))
		sb.WriteString("\n")
	}
	return sb.String()
}
