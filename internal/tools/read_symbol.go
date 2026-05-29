package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hypoballad/virgil/internal/symbols"
)

// ReadSymbolTool は AST 境界を使って指定シンボルのソースを正確に読むツール
type ReadSymbolTool struct {
	workspaceRoot string
	extractor     *symbols.Extractor
}

const (
	readSymbolSummaryThresholdLines = 50
	readSymbolFullWarningLines      = 100
	readSymbolFullSoftLimitLines    = 300
	readSymbolFullHardLimitLines    = 1000
)

type readSymbolArgs struct {
	Path       string `json:"path"`
	SymbolName string `json:"symbol_name"`
	Receiver   string `json:"receiver,omitempty"`
	Full       bool   `json:"full,omitempty"`
}

func NewReadSymbolTool(workspaceRoot string) *ReadSymbolTool {
	return &ReadSymbolTool{
		workspaceRoot: workspaceRoot,
		extractor:     symbols.NewExtractor(),
	}
}

func (t *ReadSymbolTool) Name() string {
	return "read_symbol"
}

func (t *ReadSymbolTool) Description() string {
	return "Read a specific symbol (function, method, class, struct, constant, etc.) using AST boundaries. By default, small symbols are returned in full, but large symbols over 50 lines return a compact SUMMARY with signature, docstring/comment metadata, methods, and structure. Use receiver to disambiguate methods/classes. Use full=true only for small symbols; symbols over 300 lines are not returned in full, and symbols over 1000 lines are forbidden in full mode. Prefer this over read_file when you know the symbol name."
}

func (t *ReadSymbolTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the file, relative to workspace root",
					},
					"symbol_name": map[string]interface{}{
						"type":        "string",
						"description": "Exact symbol name to read, for example Execute or NewRegistry",
					},
					"receiver": map[string]interface{}{
						"type":        "string",
						"description": "Optional exact receiver/class filter for methods, e.g. Calculator or *Server. Use this when several methods share the same name.",
					},
					"full": map[string]interface{}{
						"type":        "boolean",
						"description": "If true, return the complete symbol body for small symbols. Default false: large symbols over 50 lines return a summary to protect context. Full mode is blocked for symbols over 300 lines and forbidden for symbols over 1000 lines.",
						"default":     false,
					},
				},
				"required": []string{"path", "symbol_name"},
			},
		},
	}
}

func (t *ReadSymbolTool) IsMutating() bool {
	return false
}

func (t *ReadSymbolTool) Execute(ctx context.Context, argsJSON json.RawMessage) (*Result, error) {
	var args readSymbolArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	args.Path = strings.TrimSpace(args.Path)
	args.SymbolName = strings.TrimSpace(args.SymbolName)
	args.Receiver = strings.TrimSpace(args.Receiver)
	if args.Path == "" {
		return ErrorResult("path is required"), nil
	}
	if args.SymbolName == "" {
		return ErrorResult("symbol_name is required"), nil
	}

	resolvedPath, err := t.resolvePath(args.Path)
	if err != nil {
		return ErrorResult(err.Error()), nil
	}

	info, err := os.Stat(resolvedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrorResult(formatPathError(t.workspaceRoot, args.Path, resolvedPath)), nil
		}
		return ErrorResult(fmt.Sprintf("failed to stat file: %v", err)), nil
	}
	if info.IsDir() {
		return ErrorResult(fmt.Sprintf("path is a directory, not a file: %s", args.Path)), nil
	}

	if !symbols.IsSupportedFile(resolvedPath) {
		ext := strings.ToLower(filepath.Ext(resolvedPath))
		return ErrorResult(fmt.Sprintf(
			"unsupported file type: %s. read_symbol currently supports: %s. For other file types, use read_file instead.",
			ext, strings.Join(symbols.SupportedExtensions(), ", "),
		)), nil
	}

	outline, err := t.extractor.ExtractFromFile(resolvedPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to extract symbols: %v", err)), nil
	}

	matches := make([]symbols.Symbol, 0)
	for _, sym := range outline.Symbols {
		if sym.Name == args.SymbolName {
			if args.Receiver != "" && sym.Receiver != args.Receiver {
				continue
			}
			matches = append(matches, sym)
		}
	}
	if len(matches) == 0 {
		if args.Receiver != "" {
			return ErrorResult(fmt.Sprintf("Symbol %q with receiver %q not found in %s", args.SymbolName, args.Receiver, args.Path)), nil
		}
		return ErrorResult(fmt.Sprintf("Symbol %q not found in %s", args.SymbolName, args.Path)), nil
	}

	lines, err := readSymbolLines(resolvedPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("read error: %v", err)), nil
	}

	if args.Full {
		if result := guardReadSymbolFull(args.Path, args.SymbolName, matches); result != nil {
			result.Metadata = map[string]interface{}{
				"path":        args.Path,
				"symbol_name": args.SymbolName,
				"receiver":    args.Receiver,
				"matches":     len(matches),
				"language":    outline.Language,
				"full":        args.Full,
			}
			return result, nil
		}
	}

	output := formatReadSymbolResult(args.Path, outline.Language, args.SymbolName, matches, outline.Symbols, lines, args.Full)
	result := SuccessResult(output)
	result.Metadata = map[string]interface{}{
		"path":        args.Path,
		"symbol_name": args.SymbolName,
		"receiver":    args.Receiver,
		"matches":     len(matches),
		"language":    outline.Language,
		"full":        args.Full,
	}
	return result, nil
}

func guardReadSymbolFull(displayPath, symbolName string, matches []symbols.Symbol) *Result {
	maxLines := 0
	for _, sym := range matches {
		if lineCount := symbolLineCount(sym); lineCount > maxLines {
			maxLines = lineCount
		}
	}

	switch {
	case maxLines > readSymbolFullHardLimitLines:
		return ErrorResult(formatReadSymbolFullGuard(displayPath, symbolName, maxLines, true))
	case maxLines > readSymbolFullSoftLimitLines:
		return SuccessResult(formatReadSymbolFullGuard(displayPath, symbolName, maxLines, false))
	default:
		return nil
	}
}

func formatReadSymbolFullGuard(displayPath, symbolName string, lineCount int, hard bool) string {
	var sb strings.Builder
	if hard {
		sb.WriteString("Full symbol read refused.\n")
	} else {
		sb.WriteString("Full symbol read skipped to protect context.\n")
	}
	sb.WriteString(fmt.Sprintf("Symbol: %s in %s\n", symbolName, displayPath))
	sb.WriteString(fmt.Sprintf("Requested lines: %d | full=true limit: %d soft / %d hard\n",
		lineCount, readSymbolFullSoftLimitLines, readSymbolFullHardLimitLines))
	sb.WriteString("\nUse one of these focused alternatives:\n")
	sb.WriteString(fmt.Sprintf("- read_symbol(path=%q, symbol_name=%q) for SUMMARY mode\n", displayPath, symbolName))
	sb.WriteString(fmt.Sprintf("- Prefer get_symbol_outline(path=%q, symbol_name=%q) before any read_file ranges, to list child methods without reading the body\n", displayPath, symbolName))
	sb.WriteString(fmt.Sprintf("- get_file_outline(path=%q, receiver=\"CLASS_OR_RECEIVER\") to list child methods\n", displayPath))
	sb.WriteString(fmt.Sprintf("- read_symbol(path=%q, symbol_name=\"METHOD_NAME\", receiver=\"CLASS_OR_RECEIVER\", full=true) for a small child method\n", displayPath))
	sb.WriteString(fmt.Sprintf("- read_file(path=%q, start_line=START_LINE, end_line=END_LINE) only for a justified narrow range\n", displayPath))
	sb.WriteString("- For class/type symbols, omit receiver on read_symbol; receiver is for method disambiguation.\n")
	sb.WriteString("\nDo not reconstruct this large symbol by reading adjacent read_file ranges. Inspect child symbols first, then read only the methods or narrow ranges relevant to the task.\n")
	if !hard {
		sb.WriteString("\nNo source body was returned. Narrow the request before using full=true.\n")
	}
	return sb.String()
}

func (t *ReadSymbolTool) resolvePath(path string) (string, error) {
	var abs string
	var err error
	if filepath.IsAbs(path) {
		abs = filepath.Clean(path)
	} else {
		abs, err = filepath.Abs(filepath.Join(t.workspaceRoot, path))
		if err != nil {
			return "", fmt.Errorf("failed to resolve path: %v", err)
		}
	}

	if t.workspaceRoot != "" {
		root, err := filepath.Abs(t.workspaceRoot)
		if err != nil {
			return "", fmt.Errorf("failed to resolve workspace root: %v", err)
		}
		relPath, err := filepath.Rel(root, abs)
		if err != nil || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("path outside allowed root: %s", path)
		}
	}

	return abs, nil
}

func readSymbolLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	lines := make([]string, 0)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

func formatReadSymbolResult(displayPath, language, symbolName string, matches []symbols.Symbol, allSymbols []symbols.Symbol, lines []string, full bool) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Symbol: %s in %s\n", symbolName, displayPath))
	sb.WriteString(fmt.Sprintf("Language: %s | Matches: %d\n", language, len(matches)))

	for i, sym := range matches {
		if len(matches) > 1 {
			sb.WriteString(fmt.Sprintf("\nMatch %d of %d\n", i+1, len(matches)))
		}
		receiver := sym.Receiver
		if receiver == "" {
			receiver = "-"
		}
		lineCount := symbolLineCount(sym)
		if !full && lineCount > readSymbolSummaryThresholdLines {
			sb.WriteString(formatReadSymbolSummary(displayPath, language, sym, allSymbols, lines, lineCount))
			continue
		}
		sb.WriteString(formatReadSymbolFull(sym, receiver, lines, lineCount, full))
	}

	return sb.String()
}

func symbolLineCount(sym symbols.Symbol) int {
	if sym.EndLine < sym.StartLine {
		return 1
	}
	return sym.EndLine - sym.StartLine + 1
}

func formatReadSymbolFull(sym symbols.Symbol, receiver string, lines []string, lineCount int, explicitFull bool) string {
	var sb strings.Builder
	bodyCharCount := symbolBodyCharCount(sym, lines)
	sb.WriteString(fmt.Sprintf("Type: %s | Receiver: %s | Lines: %d-%d (%d lines) | Signature: %s\n",
		formatSymbolType(string(sym.Type), sym.IsFallback), receiver, sym.StartLine, sym.EndLine, lineCount, sym.Signature))
	if explicitFull || lineCount > readSymbolSummaryThresholdLines {
		sb.WriteString(fmt.Sprintf("Mode: FULL (%d chars, ~%d tokens)\n", bodyCharCount, approximateTokens(bodyCharCount)))
	} else {
		sb.WriteString("Mode: FULL\n")
	}
	if explicitFull && lineCount > readSymbolFullWarningLines {
		sb.WriteString("Warning: Large symbol. Consider using default mode (summary) for overview first.\n")
	}
	if strings.TrimSpace(sym.Doc) != "" {
		sb.WriteString(fmt.Sprintf("Doc: %s\n", sym.Doc))
	}
	sb.WriteString(strings.Repeat("-", 40))
	sb.WriteString("\n")

	startLine, endLine := clampSymbolLines(sym, len(lines))
	for lineNum := startLine; lineNum <= endLine; lineNum++ {
		sb.WriteString(fmt.Sprintf("%4d | %s\n", lineNum, lines[lineNum-1]))
	}
	return sb.String()
}

func formatReadSymbolSummary(displayPath, language string, sym symbols.Symbol, allSymbols []symbols.Symbol, lines []string, lineCount int) string {
	var sb strings.Builder
	receiver := sym.Receiver
	if receiver == "" {
		receiver = "-"
	}
	sb.WriteString(fmt.Sprintf("Symbol: %s in %s\n", sym.Name, displayPath))
	sb.WriteString(fmt.Sprintf("Type: %s | Receiver: %s | Lines: %d-%d (%d lines, large symbol)\n",
		formatSymbolType(string(sym.Type), sym.IsFallback), receiver, sym.StartLine, sym.EndLine, lineCount))
	sb.WriteString("Mode: SUMMARY (use full=true to retrieve complete body)\n\n")
	sb.WriteString("Signature:\n")
	if strings.TrimSpace(sym.Signature) != "" {
		sb.WriteString(sym.Signature)
	} else {
		sb.WriteString("(no signature indexed)")
	}
	sb.WriteString("\n\n")

	if strings.TrimSpace(sym.Doc) != "" {
		sb.WriteString("Docstring/comment:\n")
		sb.WriteString(sym.Doc)
		sb.WriteString("\n\n")
	}

	methods := methodsForSymbol(sym, allSymbols)
	if len(methods) > 0 {
		sb.WriteString(fmt.Sprintf("Methods (%d):\n", len(methods)))
		for _, method := range methods {
			sb.WriteString(fmt.Sprintf("- %s [L%d-L%d]\n", methodSignature(method), method.StartLine, method.EndLine))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Structure:\n")
	switch {
	case len(methods) > 0:
		sb.WriteString(fmt.Sprintf("- %s contains %d indexed methods.\n", sym.Name, len(methods)))
	case sym.Type == symbols.SymbolClass || sym.Type == symbols.SymbolStruct || sym.Type == symbols.SymbolInterface:
		sb.WriteString(fmt.Sprintf("- %s is a large type with no indexed child methods in this file.\n", sym.Name))
	default:
		sb.WriteString(fmt.Sprintf("- %s is a large %s spanning %d lines.\n", sym.Name, language, lineCount))
	}

	if len(methods) == 0 && (sym.Type == symbols.SymbolMethod || sym.Type == symbols.SymbolFunction) {
		sb.WriteString("\n")
		sb.WriteString(formatSymbolBodyObservation(displayPath, sym, lines))
	}

	sb.WriteString("\nNext steps:\n")
	sb.WriteString(fmt.Sprintf("- Prefer get_symbol_outline(path=%q, symbol_name=%q) before any read_file ranges, for child symbols without the body.\n", displayPath, sym.Name))
	sb.WriteString(fmt.Sprintf("- Use get_file_outline(path=%q, receiver=\"CLASS_OR_RECEIVER\") when receiver-based method grouping is enough.\n", displayPath))
	sb.WriteString("- For class/type symbols, omit receiver on read_symbol; receiver is for method disambiguation.\n")
	sb.WriteString("- Read only relevant child methods or narrow ranges. Do not reconstruct this large symbol by adjacent read_file ranges.\n")
	sb.WriteString("- For processing-flow reports, get_call_graph may help verify key entrypoints; do not treat it as mandatory.\n")
	sb.WriteString("- Use full=true only after narrowing to a small symbol.\n")
	return sb.String()
}

type symbolBlockObservation struct {
	StartLine int
	EndLine   int
	Label     string
}

func formatSymbolBodyObservation(displayPath string, sym symbols.Symbol, lines []string) string {
	var sb strings.Builder
	blocks := summarizeSymbolBlocks(sym, lines)
	calls := importantSymbolCalls(sym, lines, 12)
	assignments := importantSymbolAssignments(sym, lines, 8)

	if len(blocks) == 0 && len(calls) == 0 && len(assignments) == 0 {
		return ""
	}

	sb.WriteString("Internal observations (compressed; no source body returned):\n")
	if len(blocks) > 0 {
		sb.WriteString("Blocks:\n")
		for _, block := range blocks {
			sb.WriteString(fmt.Sprintf("- L%d-L%d: %s\n", block.StartLine, block.EndLine, block.Label))
		}
	}
	if len(calls) > 0 {
		sb.WriteString("Important calls/APIs:\n")
		for _, call := range calls {
			sb.WriteString(fmt.Sprintf("- %s\n", call))
		}
	}
	if len(assignments) > 0 {
		sb.WriteString("Important assignments:\n")
		for _, assignment := range assignments {
			sb.WriteString(fmt.Sprintf("- %s\n", assignment))
		}
	}
	if len(blocks) > 0 {
		sb.WriteString("Suggested focused ranges:\n")
		for i, block := range blocks {
			if i >= 4 {
				break
			}
			sb.WriteString(fmt.Sprintf("- read_file(path=%q, start_line=%d, end_line=%d)\n", displayPath, block.StartLine, block.EndLine))
		}
	}
	return sb.String()
}

func summarizeSymbolBlocks(sym symbols.Symbol, lines []string) []symbolBlockObservation {
	startLine, endLine := clampSymbolLines(sym, len(lines))
	if endLine < startLine {
		return nil
	}

	const maxBlocks = 8
	const minBlockLines = 8
	const maxBlockLines = 70
	blocks := make([]symbolBlockObservation, 0, maxBlocks)
	blockStart := startLine
	blankRun := 0

	flush := func(end int) {
		if end < blockStart {
			return
		}
		for end >= blockStart && strings.TrimSpace(lines[end-1]) == "" {
			end--
		}
		if end < blockStart {
			blockStart = end + 1
			return
		}
		if len(blocks) >= maxBlocks {
			return
		}
		blocks = append(blocks, symbolBlockObservation{
			StartLine: blockStart,
			EndLine:   end,
			Label:     classifySymbolBlock(lines[blockStart-1 : end]),
		})
		blockStart = end + 1
	}

	for lineNum := startLine; lineNum <= endLine; lineNum++ {
		text := strings.TrimSpace(lines[lineNum-1])
		if text == "" {
			blankRun++
		} else {
			blankRun = 0
		}
		blockLen := lineNum - blockStart + 1
		if blockLen >= minBlockLines && (blankRun >= 1 || blockLen >= maxBlockLines) {
			flush(lineNum - blankRun)
			for blockStart <= endLine && strings.TrimSpace(lines[blockStart-1]) == "" {
				blockStart++
			}
			blankRun = 0
		}
	}
	flush(endLine)
	return blocks
}

func classifySymbolBlock(blockLines []string) string {
	joined := strings.ToLower(strings.Join(blockLines, "\n"))
	switch {
	case strings.Contains(joined, "placeholder") || strings.Contains(joined, "input") || strings.Contains(joined, "dataset") || strings.Contains(joined, "dataloader"):
		return "input / placeholder / data setup"
	case strings.Contains(joined, "encoder") || strings.Contains(joined, "encode"):
		return "encoder / feature construction"
	case strings.Contains(joined, "decoder") || strings.Contains(joined, "decode"):
		return "decoder / reconstruction construction"
	case strings.Contains(joined, "loss") || strings.Contains(joined, "mse") || strings.Contains(joined, "optimizer") || strings.Contains(joined, "minimize"):
		return "loss / optimizer setup"
	case strings.Contains(joined, "session") || strings.Contains(joined, "sess.") || strings.Contains(joined, "saver") || strings.Contains(joined, "checkpoint"):
		return "session / saver / checkpoint handling"
	case strings.Contains(joined, "for ") || strings.Contains(joined, "while ") || strings.Contains(joined, "range("):
		return "loop / iterative processing"
	case strings.Contains(joined, "return "):
		return "return / output construction"
	default:
		assignments := 0
		calls := 0
		for _, line := range blockLines {
			if looksLikeSymbolAssignment(line) {
				assignments++
			}
			if strings.Contains(line, "(") && strings.Contains(line, ")") {
				calls++
			}
		}
		switch {
		case assignments >= calls && assignments > 0:
			return "configuration / assignment block"
		case calls > 0:
			return "call / computation block"
		default:
			return "code block"
		}
	}
}

var symbolCallPattern = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\s*\(`)

func importantSymbolCalls(sym symbols.Symbol, lines []string, limit int) []string {
	startLine, endLine := clampSymbolLines(sym, len(lines))
	seen := map[string]bool{}
	calls := make([]string, 0, limit)
	for lineNum := startLine; lineNum <= endLine; lineNum++ {
		for _, match := range symbolCallPattern.FindAllStringSubmatch(lines[lineNum-1], -1) {
			if len(match) < 2 || ignoredSymbolCall(match[1]) || seen[match[1]] {
				continue
			}
			seen[match[1]] = true
			calls = append(calls, fmt.Sprintf("%s (L%d)", match[1], lineNum))
			if len(calls) >= limit {
				return calls
			}
		}
	}
	return calls
}

func importantSymbolAssignments(sym symbols.Symbol, lines []string, limit int) []string {
	startLine, endLine := clampSymbolLines(sym, len(lines))
	assignments := make([]string, 0, limit)
	for lineNum := startLine; lineNum <= endLine; lineNum++ {
		text := strings.TrimSpace(lines[lineNum-1])
		if !looksLikeSymbolAssignment(text) {
			continue
		}
		assignments = append(assignments, fmt.Sprintf("L%d: %s", lineNum, truncateSummaryLine(text, 120)))
		if len(assignments) >= limit {
			return assignments
		}
	}
	return assignments
}

func looksLikeSymbolAssignment(line string) bool {
	text := strings.TrimSpace(line)
	if text == "" || strings.HasPrefix(text, "#") || strings.HasPrefix(text, "//") {
		return false
	}
	if strings.Contains(text, "==") || strings.Contains(text, "!=") || strings.Contains(text, "<=") || strings.Contains(text, ">=") {
		return false
	}
	return strings.Contains(text, "=") || strings.Contains(text, ":=")
}

func ignoredSymbolCall(name string) bool {
	switch name {
	case "if", "for", "while", "switch", "return", "func", "def", "class", "len", "range", "str", "int", "float", "print", "append", "make", "new":
		return true
	default:
		return false
	}
}

func truncateSummaryLine(line string, maxRunes int) string {
	line = strings.Join(strings.Fields(line), " ")
	runes := []rune(line)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "..."
	}
	return line
}

func methodsForSymbol(parent symbols.Symbol, allSymbols []symbols.Symbol) []symbols.Symbol {
	if parent.Type != symbols.SymbolClass && parent.Type != symbols.SymbolStruct && parent.Type != symbols.SymbolInterface {
		return nil
	}
	methods := make([]symbols.Symbol, 0)
	for _, sym := range allSymbols {
		if sym.Type != symbols.SymbolMethod {
			continue
		}
		if sym.Receiver == parent.Name {
			methods = append(methods, sym)
			continue
		}
		if sym.Receiver == "" && sym.StartLine > parent.StartLine && sym.EndLine <= parent.EndLine {
			methods = append(methods, sym)
		}
	}
	return methods
}

func methodSignature(sym symbols.Symbol) string {
	if strings.TrimSpace(sym.Signature) != "" {
		return sym.Signature
	}
	if sym.Receiver != "" {
		return sym.Receiver + "." + sym.Name
	}
	return sym.Name
}

func clampSymbolLines(sym symbols.Symbol, totalLines int) (int, int) {
	startLine := sym.StartLine
	if startLine < 1 {
		startLine = 1
	}
	endLine := sym.EndLine
	if endLine < startLine {
		endLine = startLine
	}
	if endLine > totalLines {
		endLine = totalLines
	}
	return startLine, endLine
}

func symbolBodyCharCount(sym symbols.Symbol, lines []string) int {
	startLine, endLine := clampSymbolLines(sym, len(lines))
	count := 0
	for lineNum := startLine; lineNum <= endLine; lineNum++ {
		count += len(lines[lineNum-1]) + 1
	}
	return count
}

func approximateTokens(chars int) int {
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
}
