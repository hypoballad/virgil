package symbols

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// PythonSymbolDiagnostics contains source-safe structural diagnostics for Python symbol extraction.
type PythonSymbolDiagnostics struct {
	FilePath         string
	SizeBytes        int
	LineCount        int
	RootType         string
	RootStartLine    int
	RootEndLine      int
	RootHasError     bool
	ErrorNodeCount   int
	MissingNodeCount int
	FirstError       NodeDiagnostic
	FirstMissing     NodeDiagnostic
	ASTClasses       []NodeDiagnostic
	ASTFunctions     []NodeDiagnostic
	TaggerClasses    []NodeDiagnostic
	TaggerFunctions  []NodeDiagnostic
	ExtractorSymbols []Symbol
	LineClasses      []NodeDiagnostic
}

// NodeDiagnostic identifies a structural node without including source text.
type NodeDiagnostic struct {
	Line int
	Col  int
	Name string
	Type string
}

// PythonDiagnosticsOptions controls redaction and output volume.
type PythonDiagnosticsOptions struct {
	RedactNames bool
	MaxList     int
}

// DiagnosePythonSymbols inspects how the parser, tagger, and Virgil extractor see a Python file.
func DiagnosePythonSymbols(path string, opts PythonDiagnosticsOptions) (*PythonSymbolDiagnostics, error) {
	if opts.MaxList <= 0 {
		opts.MaxList = 80
	}

	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	lang := grammars.PythonLanguage()
	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("parse python: %w", err)
	}
	if tree == nil || tree.RootNode() == nil {
		return nil, fmt.Errorf("parse python: empty tree")
	}

	root := tree.RootNode()
	diag := &PythonSymbolDiagnostics{
		FilePath:      path,
		SizeBytes:     len(src),
		LineCount:     countLines(src),
		RootType:      root.Type(lang),
		RootStartLine: int(root.StartPoint().Row) + 1,
		RootEndLine:   int(root.EndPoint().Row) + 1,
		RootHasError:  root.HasError(),
	}

	gotreesitter.Walk(root, func(node *gotreesitter.Node, depth int) gotreesitter.WalkAction {
		nodeType := node.Type(lang)
		if node.IsError() {
			diag.ErrorNodeCount++
			if diag.FirstError.Line == 0 {
				diag.FirstError = nodeDiagnostic(node, lang, src, opts.RedactNames)
			}
		}
		if node.IsMissing() {
			diag.MissingNodeCount++
			if diag.FirstMissing.Line == 0 {
				diag.FirstMissing = nodeDiagnostic(node, lang, src, opts.RedactNames)
			}
		}
		switch nodeType {
		case "class_definition":
			diag.ASTClasses = appendLimited(diag.ASTClasses, nodeDiagnostic(node, lang, src, opts.RedactNames), opts.MaxList)
		case "function_definition":
			diag.ASTFunctions = appendLimited(diag.ASTFunctions, nodeDiagnostic(node, lang, src, opts.RedactNames), opts.MaxList)
		}
		return gotreesitter.WalkContinue
	})

	tagger, err := gotreesitter.NewTagger(lang, pythonTagsQuery)
	if err != nil {
		return nil, fmt.Errorf("create tagger: %w", err)
	}
	for _, tag := range tagger.Tag(src) {
		entry := NodeDiagnostic{
			Line: int(tag.NameRange.StartPoint.Row) + 1,
			Col:  int(tag.NameRange.StartPoint.Column) + 1,
			Name: maybeRedact(tag.Name, opts.RedactNames),
			Type: tag.Kind,
		}
		switch tag.Kind {
		case "definition.class":
			diag.TaggerClasses = appendLimited(diag.TaggerClasses, entry, opts.MaxList)
		case "definition.function":
			diag.TaggerFunctions = appendLimited(diag.TaggerFunctions, entry, opts.MaxList)
		}
	}

	extracted, err := NewExtractor().ExtractFromSource(src, "python")
	if err != nil {
		return nil, fmt.Errorf("extract symbols: %w", err)
	}
	for _, sym := range extracted {
		if opts.RedactNames {
			sym.Name = redactName(sym.Name)
			sym.Receiver = redactName(sym.Receiver)
			sym.Signature = ""
		}
		diag.ExtractorSymbols = appendLimited(diag.ExtractorSymbols, sym, opts.MaxList)
	}

	diag.LineClasses = scanPythonClassLines(src, opts)
	return diag, nil
}

func countLines(src []byte) int {
	if len(src) == 0 {
		return 0
	}
	n := bytes.Count(src, []byte("\n"))
	if !bytes.HasSuffix(src, []byte("\n")) {
		n++
	}
	return n
}

func nodeDiagnostic(node *gotreesitter.Node, lang *gotreesitter.Language, src []byte, redact bool) NodeDiagnostic {
	name := ""
	if nameNode := node.ChildByFieldName("name", lang); nameNode != nil {
		name = nameNode.Text(src)
	}
	return NodeDiagnostic{
		Line: int(node.StartPoint().Row) + 1,
		Col:  int(node.StartPoint().Column) + 1,
		Name: maybeRedact(name, redact),
		Type: node.Type(lang),
	}
}

func appendLimited[T any](items []T, item T, max int) []T {
	if max > 0 && len(items) >= max {
		return items
	}
	return append(items, item)
}

func scanPythonClassLines(src []byte, opts PythonDiagnosticsOptions) []NodeDiagnostic {
	re := regexp.MustCompile(`^\s*class\s+([A-Za-z_][A-Za-z0-9_]*)`)
	scanner := bufio.NewScanner(bytes.NewReader(src))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var entries []NodeDiagnostic
	line := 0
	for scanner.Scan() {
		line++
		match := re.FindStringSubmatch(scanner.Text())
		if len(match) < 2 {
			continue
		}
		entries = appendLimited(entries, NodeDiagnostic{
			Line: line,
			Col:  1,
			Name: maybeRedact(match[1], opts.RedactNames),
			Type: "line_regex.class",
		}, opts.MaxList)
	}
	return entries
}

func maybeRedact(name string, redact bool) string {
	if !redact || name == "" {
		return name
	}
	return redactName(name)
}

func redactName(name string) string {
	if name == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(name))
	return fmt.Sprintf("name_%x", sum[:4])
}

// FormatPythonSymbolDiagnostics formats diagnostics without emitting source lines.
func FormatPythonSymbolDiagnostics(diag *PythonSymbolDiagnostics, opts PythonDiagnosticsOptions) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("File: %s\n", diag.FilePath))
	sb.WriteString(fmt.Sprintf("Size: %d bytes\n", diag.SizeBytes))
	sb.WriteString(fmt.Sprintf("Lines: %d\n\n", diag.LineCount))

	sb.WriteString("Tree-sitter parse:\n")
	sb.WriteString(fmt.Sprintf("  root: %s rows %d-%d\n", diag.RootType, diag.RootStartLine, diag.RootEndLine))
	sb.WriteString(fmt.Sprintf("  has_error: %t\n", diag.RootHasError))
	sb.WriteString(fmt.Sprintf("  error_nodes: %d\n", diag.ErrorNodeCount))
	sb.WriteString(fmt.Sprintf("  missing_nodes: %d\n", diag.MissingNodeCount))
	if diag.FirstError.Line > 0 {
		sb.WriteString(fmt.Sprintf("  first_error: %s\n", formatNodeDiagnostic(diag.FirstError)))
	}
	if diag.FirstMissing.Line > 0 {
		sb.WriteString(fmt.Sprintf("  first_missing: %s\n", formatNodeDiagnostic(diag.FirstMissing)))
	}
	sb.WriteString("\n")

	writeNodeSection(&sb, "AST class_definition", diag.ASTClasses, opts.MaxList)
	writeNodeSection(&sb, "AST function_definition", diag.ASTFunctions, opts.MaxList)
	writeNodeSection(&sb, "Tagger definition.class", diag.TaggerClasses, opts.MaxList)
	writeNodeSection(&sb, "Tagger definition.function", diag.TaggerFunctions, opts.MaxList)
	writeSymbolSection(&sb, "Virgil Extractor symbols", diag.ExtractorSymbols, opts.MaxList)
	writeNodeSection(&sb, "Line regex class scan", diag.LineClasses, opts.MaxList)

	sb.WriteString("Interpretation hints:\n")
	sb.WriteString("- If line regex sees classes that AST does not, parser recovery likely lost later structure.\n")
	sb.WriteString("- If AST sees classes that Tagger/Extractor do not, Virgil query/tag conversion is the likely issue.\n")
	sb.WriteString("- If Tagger sees classes but find_symbol does not after /reindex --force, index persistence is the likely issue.\n")
	return sb.String()
}

func writeNodeSection(sb *strings.Builder, title string, entries []NodeDiagnostic, max int) {
	sb.WriteString(fmt.Sprintf("%s: %d", title, len(entries)))
	if max > 0 && len(entries) >= max {
		sb.WriteString(fmt.Sprintf(" (limited to %d)", max))
	}
	sb.WriteString("\n")
	for _, entry := range entries {
		sb.WriteString(fmt.Sprintf("  %s\n", formatNodeDiagnostic(entry)))
	}
	sb.WriteString("\n")
}

func writeSymbolSection(sb *strings.Builder, title string, symbols []Symbol, max int) {
	sb.WriteString(fmt.Sprintf("%s: %d", title, len(symbols)))
	if max > 0 && len(symbols) >= max {
		sb.WriteString(fmt.Sprintf(" (limited to %d)", max))
	}
	sb.WriteString("\n")
	for _, sym := range symbols {
		receiver := sym.Receiver
		if receiver == "" {
			receiver = "-"
		}
		sb.WriteString(fmt.Sprintf("  L%d-%d: %s %s receiver=%s\n", sym.StartLine, sym.EndLine, sym.Type, sym.Name, receiver))
	}
	sb.WriteString("\n")
}

func formatNodeDiagnostic(entry NodeDiagnostic) string {
	parts := []string{fmt.Sprintf("L%d:C%d", entry.Line, entry.Col), entry.Type}
	if entry.Name != "" {
		parts = append(parts, entry.Name)
	}
	return strings.Join(parts, " ")
}

// SortNodeDiagnostics is useful for tests and future callers that combine entries.
func SortNodeDiagnostics(entries []NodeDiagnostic) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Line != entries[j].Line {
			return entries[i].Line < entries[j].Line
		}
		return entries[i].Name < entries[j].Name
	})
}
