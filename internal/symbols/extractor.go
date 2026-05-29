package symbols

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// Extractor はファイルからシンボルを抽出する
type Extractor struct{}

// NewExtractor は新しい Extractor を返す
func NewExtractor() *Extractor {
	return &Extractor{}
}

// LanguageConfig は言語ごとの設定を保持する
type LanguageConfig struct {
	Name     string
	Query    string
	Detector func(string) *grammars.LangEntry
}

func detectLanguage(name string) func(string) *grammars.LangEntry {
	return func(path string) *grammars.LangEntry {
		if entry := grammars.DetectLanguageByName(name); entry != nil {
			return entry
		}
		return grammars.DetectLanguage(path)
	}
}

var langConfigs = map[string]LanguageConfig{
	".go": {
		Name:     "go",
		Query:    goTagsQuery,
		Detector: detectLanguage("go"),
	},
	".py": {
		Name:     "python",
		Query:    pythonTagsQuery,
		Detector: detectLanguage("python"),
	},
	".js":  javascriptLanguageConfig(),
	".mjs": javascriptLanguageConfig(),
	".cjs": javascriptLanguageConfig(),
	".ts":  typescriptLanguageConfig("typescript"),
	".mts": typescriptLanguageConfig("typescript"),
	".cts": typescriptLanguageConfig("typescript"),
	".tsx": typescriptLanguageConfig("tsx"),
	".rs": {
		Name:     "rust",
		Query:    rustTagsQuery,
		Detector: detectLanguage("rust"),
	},
}

func javascriptLanguageConfig() LanguageConfig {
	return LanguageConfig{
		Name:     "javascript",
		Query:    javascriptTagsQuery,
		Detector: detectLanguage("javascript"),
	}
}

func typescriptLanguageConfig(grammar string) LanguageConfig {
	return LanguageConfig{
		Name:     "typescript",
		Query:    typescriptTagsQuery,
		Detector: detectLanguage(grammar),
	}
}

func IsSupportedFile(path string) bool {
	_, ok := langConfigs[strings.ToLower(filepath.Ext(path))]
	return ok
}

func SupportedExtensions() []string {
	exts := make([]string, 0, len(langConfigs))
	for ext := range langConfigs {
		exts = append(exts, ext)
	}
	sort.Strings(exts)
	return exts
}

// ExtractFromFile はファイルパスを指定してシンボルを抽出する
func (e *Extractor) ExtractFromFile(path string) (*FileOutline, error) {
	ext := strings.ToLower(filepath.Ext(path))
	config, ok := langConfigs[ext]
	if !ok {
		return nil, fmt.Errorf("unsupported file type: %s", ext)
	}

	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	symbols, err := e.extractSymbols(src, config, path)
	if err != nil {
		return nil, err
	}
	if config.Name == "python" {
		symbols = MergeWithFallback(symbols, ExtractPythonFallbackSymbols(src))
	}
	AttachSymbolDocs(symbols, src, config.Name)

	// FilePath を埋める
	for i := range symbols {
		symbols[i].FilePath = path
	}

	return &FileOutline{
		FilePath: path,
		Language: config.Name,
		Symbols:  symbols,
	}, nil
}

// ExtractFromSource はソースコードを直接渡してシンボル抽出する
func (e *Extractor) ExtractFromSource(src []byte, language string) ([]Symbol, error) {
	var ext string
	switch language {
	case "go":
		ext = ".go"
	case "python":
		ext = ".py"
	case "javascript", "js":
		ext = ".js"
	case "typescript", "ts":
		ext = ".ts"
	case "tsx":
		ext = ".tsx"
	case "rust", "rs":
		ext = ".rs"
	default:
		return nil, fmt.Errorf("unsupported language: %s", language)
	}

	config := langConfigs[ext]
	symbols, err := e.extractSymbols(src, config, "dummy"+ext)
	if err != nil {
		return nil, err
	}
	if config.Name == "python" {
		symbols = MergeWithFallback(symbols, ExtractPythonFallbackSymbols(src))
	}
	AttachSymbolDocs(symbols, src, config.Name)
	return symbols, nil
}

// ExtractCallsFromFile はファイルから呼び出し関係を抽出する。
// 各呼び出しが「どの関数の中で行われているか」を文脈付きで返す。
func (e *Extractor) ExtractCallsFromFile(path string) (*FileCallGraph, error) {
	ext := strings.ToLower(filepath.Ext(path))

	var language string
	switch ext {
	case ".go":
		language = "go"
	case ".py":
		language = "python"
	default:
		return nil, fmt.Errorf("unsupported file type for call graph: %s", ext)
	}

	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	symbols, err := e.ExtractFromSource(src, language)
	if err != nil {
		return nil, fmt.Errorf("extract symbols: %w", err)
	}

	calls, err := e.extractCalls(src, language, symbols)
	if err != nil {
		return nil, err
	}

	for i := range calls {
		calls[i].CallerFile = path
		calls[i].Language = language
	}

	return &FileCallGraph{
		FilePath: path,
		Language: language,
		Calls:    calls,
	}, nil
}

const goTagsQuery = `
(function_declaration
  name: (identifier) @name) @definition.function

(method_declaration
  name: (field_identifier) @name) @definition.method

(type_spec
  name: (type_identifier) @name
  type: (struct_type)) @definition.struct

(type_spec
  name: (type_identifier) @name
  type: (interface_type)) @definition.interface

(type_spec
  name: (type_identifier) @name) @definition.type

; トップレベル const のみ（関数内のローカル const は除外）
(const_spec
  name: (identifier) @name
  (#not-has-ancestor? @name function_declaration)
  (#not-has-ancestor? @name method_declaration)) @definition.constant

; トップレベル var のみ（関数内のローカル var は除外）
(var_spec
  name: (identifier) @name
  (#not-has-ancestor? @name function_declaration)
  (#not-has-ancestor? @name method_declaration)) @definition.variable
`

const goCallsQuery = `
; 関数呼び出し: foo()
(call_expression
  function: (identifier) @name) @reference.call

; メソッド呼び出し: obj.foo()
(call_expression
  function: (selector_expression
    operand: (_)
    field: (field_identifier) @name)) @reference.call
`

const pythonTagsQuery = `
(class_definition
  name: (identifier) @name) @definition.class

(function_definition
  name: (identifier) @name) @definition.function

; 変数/定数の代入（関数内は除外）
(assignment
  left: (identifier) @name
  (#not-has-ancestor? @name function_definition)) @definition.variable
`

const pythonCallsQuery = `
; 関数呼び出し: foo()
(call
  function: (identifier) @name) @reference.call

; メソッド呼び出し: obj.foo()
(call
  function: (attribute
    object: (_)
    attribute: (identifier) @name)) @reference.call
`

const javascriptTagsQuery = `
(function_declaration
  name: (identifier) @name) @definition.function

(method_definition
  name: (property_identifier) @name) @definition.method

(class_declaration
  name: (identifier) @name) @definition.class

(lexical_declaration
  "const"
  (variable_declarator
    name: (identifier) @name)
  (#not-has-ancestor? @name function_declaration)
  (#not-has-ancestor? @name method_definition)
  (#not-has-ancestor? @name arrow_function)) @definition.constant

(variable_declaration
  (variable_declarator
    name: (identifier) @name)
  (#not-has-ancestor? @name function_declaration)
  (#not-has-ancestor? @name method_definition)
  (#not-has-ancestor? @name arrow_function)) @definition.variable

(lexical_declaration
  (variable_declarator
    name: (identifier) @name)
  (#not-has-ancestor? @name function_declaration)
  (#not-has-ancestor? @name method_definition)
  (#not-has-ancestor? @name arrow_function)) @definition.variable
`

const typescriptTagsQuery = `
(function_declaration
  name: (identifier) @name) @definition.function

(method_definition
  name: (property_identifier) @name) @definition.method

(class_declaration
  name: (type_identifier) @name) @definition.class

(interface_declaration
  name: (type_identifier) @name) @definition.interface

(type_alias_declaration
  name: (type_identifier) @name) @definition.type

(lexical_declaration
  "const"
  (variable_declarator
    name: (identifier) @name)
  (#not-has-ancestor? @name function_declaration)
  (#not-has-ancestor? @name method_definition)
  (#not-has-ancestor? @name arrow_function)) @definition.constant

(variable_declaration
  (variable_declarator
    name: (identifier) @name)
  (#not-has-ancestor? @name function_declaration)
  (#not-has-ancestor? @name method_definition)
  (#not-has-ancestor? @name arrow_function)) @definition.variable

(lexical_declaration
  (variable_declarator
    name: (identifier) @name)
  (#not-has-ancestor? @name function_declaration)
  (#not-has-ancestor? @name method_definition)
  (#not-has-ancestor? @name arrow_function)) @definition.variable
`

const rustTagsQuery = `
(function_item
  name: (identifier) @name) @definition.function

(impl_item
  body: (declaration_list
    (function_item
      name: (identifier) @name) @definition.method))

(struct_item
  name: (type_identifier) @name) @definition.struct

(enum_item
  name: (type_identifier) @name) @definition.struct

(trait_item
  name: (type_identifier) @name) @definition.interface

(type_item
  name: (type_identifier) @name) @definition.type

(const_item
  name: (identifier) @name) @definition.constant

(static_item
  name: (identifier) @name) @definition.constant
`

// extractSymbols は汎用的なシンボル抽出ロジック
func (e *Extractor) extractSymbols(src []byte, config LanguageConfig, path string) ([]Symbol, error) {
	entry := config.Detector(path)
	if entry == nil {
		return nil, fmt.Errorf("failed to detect language entry for %s", path)
	}

	lang := entry.Language()
	tagger, err := gotreesitter.NewTagger(lang, config.Query)
	if err != nil {
		return nil, fmt.Errorf("create tagger: %w", err)
	}

	tags := tagger.Tag(src)

	symbols := make([]Symbol, 0, len(tags))
	seen := make(map[string]SymbolType) // 重複排除用

	for _, tag := range tags {
		sym := convertTag(tag, src, config.Name)
		if sym == nil {
			continue
		}

		key := fmt.Sprintf("%d:%d:%s", sym.StartLine, sym.EndLine, sym.Name)
		if existing, ok := seen[key]; ok {
			if shouldPreferSymbolType(existing, sym.Type) {
				for i := range symbols {
					if symbols[i].Name == sym.Name && symbols[i].StartLine == sym.StartLine {
						symbols[i].Type = sym.Type
						symbols[i].Receiver = sym.Receiver
						symbols[i].Signature = sym.Signature
						break
					}
				}
				seen[key] = sym.Type
			}
			continue
		}

		symbols = append(symbols, *sym)
		seen[key] = sym.Type
	}

	// 行番号順にソート
	sort.Slice(symbols, func(i, j int) bool {
		if symbols[i].StartLine != symbols[j].StartLine {
			return symbols[i].StartLine < symbols[j].StartLine
		}
		return symbols[i].Name < symbols[j].Name
	})

	return symbols, nil
}

// extractCalls はソースコードと既知のシンボル一覧から呼び出しエッジを抽出する。
func (e *Extractor) extractCalls(src []byte, language string, symbols []Symbol) ([]CallEdge, error) {
	var query string
	var entry *grammars.LangEntry

	switch language {
	case "go":
		entry = detectLanguage("go")("dummy.go")
		query = goCallsQuery
	case "python":
		entry = detectLanguage("python")("dummy.py")
		query = pythonCallsQuery
	default:
		return nil, fmt.Errorf("unsupported language: %s", language)
	}

	if entry == nil {
		return nil, fmt.Errorf("failed to detect language entry")
	}

	tagger, err := gotreesitter.NewTagger(entry.Language(), query)
	if err != nil {
		return nil, fmt.Errorf("create tagger: %w", err)
	}

	tags := tagger.Tag(src)
	edges := make([]CallEdge, 0, len(tags))

	for _, tag := range tags {
		if tag.Kind != "reference.call" {
			continue
		}

		callLine := int(tag.Range.StartPoint.Row) + 1
		caller := findEnclosingSymbol(symbols, callLine)
		if caller == nil {
			edges = append(edges, CallEdge{
				CallerName: "<global>",
				CalleeName: tag.Name,
				CallLine:   callLine,
			})
			continue
		}

		edges = append(edges, CallEdge{
			CallerName:     caller.Name,
			CallerReceiver: caller.Receiver,
			CalleeName:     tag.Name,
			CalleeReceiver: "",
			CallLine:       callLine,
		})
	}

	return edges, nil
}

// findEnclosingSymbol は指定された行番号を含む関数/メソッドのうち、最も小さいスコープを返す。
func findEnclosingSymbol(symbols []Symbol, line int) *Symbol {
	var best *Symbol
	bestSize := -1

	for i := range symbols {
		s := &symbols[i]
		if s.Type != SymbolFunction && s.Type != SymbolMethod {
			continue
		}
		if line < s.StartLine || line > s.EndLine {
			continue
		}
		size := s.EndLine - s.StartLine
		if best == nil || size < bestSize {
			best = s
			bestSize = size
		}
	}

	return best
}

func shouldPreferSymbolType(existing SymbolType, candidate SymbolType) bool {
	if existing == SymbolType_ && candidate != SymbolType_ {
		return true
	}
	if existing == SymbolFunction && candidate == SymbolMethod {
		return true
	}
	if existing == SymbolVar && candidate == SymbolConst {
		return true
	}
	return false
}

// convertTag は gotreesitter の Tag を Virgil の Symbol に変換する
func convertTag(tag gotreesitter.Tag, src []byte, lang string) *Symbol {
	symType := mapTagKind(tag.Kind)
	if symType == "" {
		return nil
	}

	startLine := int(tag.NameRange.StartPoint.Row) + 1
	endLine := int(tag.Range.EndPoint.Row) + 1

	signature := extractSignature(src, tag, lang)

	receiver := ""
	if lang == "go" {
		if symType == SymbolMethod {
			receiver = extractReceiver(src, tag, lang, signature)
		}
	} else if lang == "python" {
		if symType == SymbolStruct {
			symType = SymbolClass
		}
		if symType == SymbolFunction || symType == SymbolVar {
			receiver = extractReceiver(src, tag, lang, signature)
			if receiver != "" && symType == SymbolFunction {
				symType = SymbolMethod
			}
		}
	} else if lang == "javascript" || lang == "typescript" {
		if symType == SymbolMethod {
			receiver = extractReceiver(src, tag, lang, signature)
		}
	} else if lang == "rust" {
		if symType == SymbolMethod || symType == SymbolFunction {
			receiver = extractReceiver(src, tag, lang, signature)
			if receiver != "" {
				symType = SymbolMethod
			}
		}
	}

	return &Symbol{
		Name:      tag.Name,
		Type:      symType,
		Receiver:  receiver,
		Signature: signature,
		StartLine: startLine,
		EndLine:   endLine,
	}
}

// mapTagKind は gotreesitter のタグ種別を Virgil の SymbolType にマッピングする
func mapTagKind(kind string) SymbolType {
	switch kind {
	case "definition.function":
		return SymbolFunction
	case "definition.method":
		return SymbolMethod
	case "definition.struct", "definition.class":
		return SymbolStruct
	case "definition.interface":
		return SymbolInterface
	case "definition.type":
		return SymbolType_
	case "definition.constant":
		return SymbolConst
	case "definition.variable":
		return SymbolVar
	default:
		return ""
	}
}

// extractSignature はソースコードからシグネチャ行を抽出する
func extractSignature(src []byte, tag gotreesitter.Tag, lang string) string {
	row := int(tag.NameRange.StartPoint.Row)
	lines := strings.Split(string(src), "\n")
	if row >= len(lines) {
		return ""
	}

	line := lines[row]
	switch lang {
	case "go":
		if idx := strings.Index(line, "{"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		} else {
			line = strings.TrimSpace(line)
		}
	case "python":
		line = extractPythonSignature(lines, row)
	case "javascript", "typescript":
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, "{"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}
		if strings.HasSuffix(line, "=>") && row+1 < len(lines) {
			line = strings.TrimSpace(line + " " + strings.TrimSpace(lines[row+1]))
		}
	case "rust":
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, "{"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}
	default:
		line = strings.TrimSpace(line)
	}

	return line
}

func extractPythonSignature(lines []string, row int) string {
	var sb strings.Builder
	depth := 0
	inString := false
	var quote rune
	tripleQuote := false
	escaped := false

	for i := row; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}

		runes := []rune(line)
		for j := 0; j < len(runes); j++ {
			r := runes[j]

			if inString {
				sb.WriteRune(r)
				if escaped {
					escaped = false
					continue
				}
				if r == '\\' {
					escaped = true
					continue
				}
				if r == quote {
					if tripleQuote {
						if j+2 < len(runes) && runes[j+1] == quote && runes[j+2] == quote {
							sb.WriteRune(runes[j+1])
							sb.WriteRune(runes[j+2])
							j += 2
							inString = false
							tripleQuote = false
						}
					} else {
						inString = false
					}
				}
				continue
			}

			switch r {
			case '\'', '"':
				inString = true
				quote = r
				tripleQuote = j+2 < len(runes) && runes[j+1] == r && runes[j+2] == r
				sb.WriteRune(r)
				if tripleQuote {
					sb.WriteRune(runes[j+1])
					sb.WriteRune(runes[j+2])
					j += 2
				}
			case '#':
				j = len(runes)
			case '(', '[', '{':
				depth++
				sb.WriteRune(r)
			case ')', ']', '}':
				if depth > 0 {
					depth--
				}
				sb.WriteRune(r)
			case ':':
				if depth == 0 {
					return strings.TrimSpace(sb.String())
				}
				sb.WriteRune(r)
			default:
				sb.WriteRune(r)
			}
		}

		if !inString && depth == 0 && i > row {
			break
		}
	}

	return strings.TrimSpace(sb.String())
}

// extractReceiver はメソッドのレシーバを抽出する
func extractReceiver(src []byte, tag gotreesitter.Tag, lang string, signature string) string {
	switch lang {
	case "go":
		idx := strings.Index(signature, "func (")
		if idx < 0 {
			return ""
		}
		start := idx + 6
		end := strings.Index(signature[start:], ")")
		if end < 0 {
			return ""
		}
		receiverPart := signature[start : start+end]
		parts := strings.Fields(receiverPart)
		if len(parts) == 0 {
			return ""
		}
		if len(parts) == 1 {
			return parts[0]
		}
		return parts[len(parts)-1]

	case "python":
		// Python の場合、Node から親クラスを探す
		// gotreesitter.Tag には Node が含まれていないため、
		// 簡易的に「上の行」を探すか、再度パースして AST を辿る必要がある
		// 現状の Tagger 経由では親のコンテキストが不明。
		// 代案として、クエリで親クラス名をキャプチャできるようにするか、
		// 簡易的な行ベースの推測を行う。

		// 1. AST を使った正確な抽出（再パースが必要）
		// 2. クエリの工夫（現在不可能）
		// 3. 簡易推測: 定義行より上の、インデントが浅い class 定義を探す
		return inferPythonClassName(src, int(tag.NameRange.StartPoint.Row))

	case "javascript", "typescript":
		return inferClassName(src, int(tag.NameRange.StartPoint.Row), "class ")

	case "rust":
		return inferRustImplReceiver(src, int(tag.NameRange.StartPoint.Row))

	default:
		return ""
	}
}

// inferPythonClassName は定義行から上に遡って、最も近いインデントの浅いクラス名を探す
func inferPythonClassName(src []byte, startRow int) string {
	lines := strings.Split(string(src), "\n")
	if startRow >= len(lines) {
		return ""
	}

	targetIndent := lineIndent(lines[startRow])

	for i := startRow - 1; i >= 0; i-- {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := lineIndent(line)
		if indent >= targetIndent {
			continue
		}
		if strings.HasPrefix(trimmed, "class ") {
			namePart := strings.TrimPrefix(trimmed, "class ")
			if idx := strings.IndexAny(namePart, "(:"); idx >= 0 {
				return strings.TrimSpace(namePart[:idx])
			}
			return strings.TrimSpace(namePart)
		}
		if strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "async def ") {
			return ""
		}
	}
	return ""
}

func inferClassName(src []byte, startRow int, classKeyword string) string {
	lines := strings.Split(string(src), "\n")
	if startRow >= len(lines) {
		return ""
	}

	targetIndent := lineIndent(lines[startRow])
	for i := startRow - 1; i >= 0; i-- {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, classKeyword) {
			indent := lineIndent(line)
			if indent < targetIndent {
				namePart := strings.TrimPrefix(trimmed, classKeyword)
				if idx := strings.IndexAny(namePart, " {(<"); idx >= 0 {
					return strings.TrimSpace(namePart[:idx])
				}
				return strings.TrimSpace(namePart)
			}
		}
	}
	return ""
}

func inferRustImplReceiver(src []byte, startRow int) string {
	lines := strings.Split(string(src), "\n")
	if startRow >= len(lines) {
		return ""
	}

	for i := startRow - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trimmed, "impl") {
			continue
		}
		if !blockContainsLine(lines, i, startRow) {
			continue
		}
		header := trimmed
		for !strings.Contains(header, "{") && i+1 < len(lines) {
			i++
			header += " " + strings.TrimSpace(lines[i])
		}
		header = strings.TrimPrefix(header, "impl")
		if idx := strings.Index(header, "{"); idx >= 0 {
			header = header[:idx]
		}
		header = strings.TrimSpace(header)
		if idx := strings.LastIndex(header, " for "); idx >= 0 {
			header = header[idx+5:]
		}
		header = strings.TrimSpace(header)
		header = strings.TrimPrefix(header, "<")
		if strings.Contains(header, ">") && strings.HasPrefix(strings.TrimSpace(strings.SplitN(header, ">", 2)[0]), "") {
			parts := strings.SplitN(header, ">", 2)
			if len(parts) == 2 {
				header = strings.TrimSpace(parts[1])
			}
		}
		if idx := strings.IndexAny(header, " <{("); idx >= 0 {
			header = strings.TrimSpace(header[:idx])
		}
		return strings.Trim(header, "&*")
	}
	return ""
}

func blockContainsLine(lines []string, openRow int, targetRow int) bool {
	depth := 0
	seenOpen := false
	for i := openRow; i <= targetRow && i < len(lines); i++ {
		line := stripLineComment(lines[i])
		for _, r := range line {
			switch r {
			case '{':
				depth++
				seenOpen = true
			case '}':
				depth--
				if seenOpen && depth <= 0 && i < targetRow {
					return false
				}
			}
		}
	}
	return seenOpen && depth > 0
}

func stripLineComment(line string) string {
	if idx := strings.Index(line, "//"); idx >= 0 {
		return line[:idx]
	}
	return line
}

func lineIndent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}
