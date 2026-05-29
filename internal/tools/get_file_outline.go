package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hypoballad/virgil/internal/symbols"
)

// GetFileOutlineTool はファイルのシンボル一覧（アウトライン）を返すツール
// read_file の代替として、コードの構造を低コストで把握できる
type GetFileOutlineTool struct {
	workspaceRoot string
	extractor     *symbols.Extractor
}

const getFileOutlineLargeSymbolThreshold = 120

func NewGetFileOutlineTool(workspaceRoot string) *GetFileOutlineTool {
	return &GetFileOutlineTool{
		workspaceRoot: workspaceRoot,
		extractor:     symbols.NewExtractor(),
	}
}

func (t *GetFileOutlineTool) Name() string {
	return "get_file_outline"
}

func (t *GetFileOutlineTool) Description() string {
	return "Get a structural outline of a code file (functions, methods, types, classes, etc.) WITHOUT reading the full content. " +
		"Output includes signatures plus indexed docstrings or leading comments when available. " +
		"This is the PREFERRED way to understand what's in a file before deciding whether to read specific parts. " +
		"Saves up to 95% of tokens compared to read_file on large files. " +
		"Currently supports Go, Python, JavaScript, TypeScript/TSX, and Rust files. Use read_symbol, not read_file, when you need the complete source or signature of a specific symbol after seeing the outline."
}

func (t *GetFileOutlineTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		},
	}
}

func (t *GetFileOutlineTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to the file (relative to workspace root, or absolute). Output includes a Doc column with indexed docstrings/leading comments.",
			},
			"name_filter": map[string]interface{}{
				"type":        "string",
				"description": "Optional case-insensitive substring filter for symbol names.",
			},
			"type": map[string]interface{}{
				"type":        "string",
				"description": "Optional exact symbol type filter, e.g. class, function, method, struct, interface, var, const.",
			},
			"receiver": map[string]interface{}{
				"type":        "string",
				"description": "Optional exact receiver/class filter for methods, e.g. Calculator or *Server.",
			},
			"fallback_only": map[string]interface{}{
				"type":        "boolean",
				"description": "If true, only show symbols recovered by fallback extraction.",
			},
			"include_methods": map[string]interface{}{
				"type":        "boolean",
				"description": "Whether to include method symbols. Defaults to true; set false for a high-level file outline.",
			},
		},
		"required": []string{"path"},
	}
}

// IsMutating: 読み取り専用なので false
// プランモードでも使用可能
func (t *GetFileOutlineTool) IsMutating() bool {
	return false
}

type getFileOutlineArgs struct {
	Path           string `json:"path"`
	NameFilter     string `json:"name_filter,omitempty"`
	Type           string `json:"type,omitempty"`
	Receiver       string `json:"receiver,omitempty"`
	FallbackOnly   bool   `json:"fallback_only,omitempty"`
	IncludeMethods *bool  `json:"include_methods,omitempty"`
}

type outlineFilters struct {
	NameFilter     string
	Type           string
	Receiver       string
	FallbackOnly   bool
	IncludeMethods bool
	HasFilters     bool
}

func (t *GetFileOutlineTool) Execute(ctx context.Context, argsJSON json.RawMessage) (*Result, error) {
	var args getFileOutlineArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	if strings.TrimSpace(args.Path) == "" {
		return ErrorResult("path is required"), nil
	}

	// パス解決
	resolvedPath := args.Path
	if !filepath.IsAbs(resolvedPath) {
		resolvedPath = filepath.Join(t.workspaceRoot, args.Path)
	}

	// ファイルの存在確認
	if _, err := os.Stat(resolvedPath); err != nil {
		if os.IsNotExist(err) {
			return ErrorResult(formatPathError(t.workspaceRoot, args.Path, resolvedPath)), nil
		}
		return ErrorResult(fmt.Sprintf("failed to stat file: %v", err)), nil
	}

	// 拡張子チェック
	ext := strings.ToLower(filepath.Ext(resolvedPath))
	if !symbols.IsSupportedFile(resolvedPath) {
		return ErrorResult(fmt.Sprintf(
			"unsupported file type: %s. get_file_outline currently supports: %s. "+
				"For other file types, use read_file instead.",
			ext, strings.Join(symbols.SupportedExtensions(), ", "),
		)), nil
	}

	// シンボル抽出
	outline, err := t.extractor.ExtractFromFile(resolvedPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to extract symbols: %v", err)), nil
	}

	filters := newOutlineFilters(args)
	originalCount := len(outline.Symbols)
	if filters.HasFilters {
		filtered := filterOutlineSymbols(outline.Symbols, filters)
		outline = &symbols.FileOutline{
			FilePath: outline.FilePath,
			Language: outline.Language,
			Symbols:  filtered,
		}
	}

	// Markdown 形式で整形
	output := formatOutlineAsMarkdownWithFilters(outline, args.Path, originalCount, filters)

	return &Result{
		IsError: false,
		Content: output,
	}, nil
}

func newOutlineFilters(args getFileOutlineArgs) outlineFilters {
	includeMethods := true
	if args.IncludeMethods != nil {
		includeMethods = *args.IncludeMethods
	}

	nameFilter := strings.TrimSpace(args.NameFilter)
	symbolType := strings.TrimSpace(args.Type)
	receiver := strings.TrimSpace(args.Receiver)

	return outlineFilters{
		NameFilter:     nameFilter,
		Type:           symbolType,
		Receiver:       receiver,
		FallbackOnly:   args.FallbackOnly,
		IncludeMethods: includeMethods,
		HasFilters: nameFilter != "" ||
			symbolType != "" ||
			receiver != "" ||
			args.FallbackOnly ||
			!includeMethods,
	}
}

func filterOutlineSymbols(symbolList []symbols.Symbol, filters outlineFilters) []symbols.Symbol {
	if !filters.HasFilters {
		return symbolList
	}

	filtered := make([]symbols.Symbol, 0, len(symbolList))
	nameFilter := strings.ToLower(filters.NameFilter)
	symbolType := strings.ToLower(filters.Type)
	for _, sym := range symbolList {
		if !filters.IncludeMethods && sym.Type == symbols.SymbolMethod {
			continue
		}
		if filters.FallbackOnly && !sym.IsFallback {
			continue
		}
		if nameFilter != "" && !strings.Contains(strings.ToLower(sym.Name), nameFilter) {
			continue
		}
		if symbolType != "" && strings.ToLower(string(sym.Type)) != symbolType {
			continue
		}
		if filters.Receiver != "" && sym.Receiver != filters.Receiver {
			continue
		}
		filtered = append(filtered, sym)
	}
	return filtered
}

// formatOutlineAsMarkdown は FileOutline を LLM 用の Markdown テーブルに変換する
func formatOutlineAsMarkdown(outline *symbols.FileOutline, displayPath string) string {
	return formatOutlineAsMarkdownWithFilters(outline, displayPath, len(outline.Symbols), outlineFilters{
		IncludeMethods: true,
	})
}

func formatOutlineAsMarkdownWithFilters(outline *symbols.FileOutline, displayPath string, originalCount int, filters outlineFilters) string {
	if !filters.HasFilters && len(outline.Symbols) > getFileOutlineLargeSymbolThreshold {
		return formatLargeOutlineSummary(outline, displayPath)
	}

	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# Outline: %s\n\n", displayPath))
	if filters.HasFilters {
		sb.WriteString(fmt.Sprintf("Language: %s | Symbols: %d (filtered from %d)\n",
			outline.Language, len(outline.Symbols), originalCount))
		sb.WriteString(fmt.Sprintf("Filters: %s\n\n", formatOutlineFilters(filters)))
	} else {
		sb.WriteString(fmt.Sprintf("Language: %s | Symbols: %d\n\n",
			outline.Language, len(outline.Symbols)))
	}

	if len(outline.Symbols) == 0 {
		if filters.HasFilters {
			sb.WriteString("(no symbols matched filters)\n")
		} else {
			sb.WriteString("(no symbols found)\n")
		}
		return sb.String()
	}

	// Markdown テーブルヘッダ
	sb.WriteString("| Line | Type | Receiver | Name | Signature | Doc |\n")
	sb.WriteString("|------|------|----------|------|-----------|-----|\n")

	// 各シンボルを行として出力
	for _, sym := range outline.Symbols {
		receiver := sym.Receiver
		if receiver == "" {
			receiver = "-"
		}
		signature := truncateSignature(sym.Signature, 160)
		doc := truncateSignature(sym.Doc, 120)
		if doc == "" {
			doc = "-"
		}
		sb.WriteString(fmt.Sprintf("| %d | %s | %s | `%s` | `%s` | %s |\n",
			sym.StartLine, formatSymbolType(string(sym.Type), sym.IsFallback), receiver, sym.Name, signature, doc))
	}

	sb.WriteString("\n")
	sb.WriteString("**Next steps:** Use `read_symbol` to read a specific symbol without guessing end lines.\n")
	sb.WriteString(fmt.Sprintf("Example: `read_symbol(path=\"%s\", symbol_name=\"SYMBOL_NAME\")`\n", displayPath))

	return sb.String()
}

func formatLargeOutlineSummary(outline *symbols.FileOutline, displayPath string) string {
	var sb strings.Builder
	typeCounts := make(map[symbols.SymbolType]int)
	receiverCounts := make(map[string]int)
	topLevel := make([]symbols.Symbol, 0)
	for _, sym := range outline.Symbols {
		typeCounts[sym.Type]++
		if sym.Type == symbols.SymbolMethod {
			receiver := sym.Receiver
			if receiver == "" {
				receiver = "(nested methods)"
			}
			receiverCounts[receiver]++
			continue
		}
		topLevel = append(topLevel, sym)
	}

	sb.WriteString(fmt.Sprintf("# Outline Summary: %s\n\n", displayPath))
	sb.WriteString(fmt.Sprintf("Language: %s | Symbols: %d (large outline summarized)\n\n", outline.Language, len(outline.Symbols)))
	sb.WriteString("This file has many indexed symbols. The full outline was not returned by default to protect context.\n")
	sb.WriteString("Use filters such as `include_methods=false`, `receiver`, `name_filter`, or `type` to retrieve a narrower view.\n\n")

	sb.WriteString("Symbol counts:\n")
	for _, typ := range []symbols.SymbolType{
		symbols.SymbolClass,
		symbols.SymbolStruct,
		symbols.SymbolInterface,
		symbols.SymbolFunction,
		symbols.SymbolMethod,
		symbols.SymbolType_,
		symbols.SymbolConst,
		symbols.SymbolVar,
	} {
		if count := typeCounts[typ]; count > 0 {
			sb.WriteString(fmt.Sprintf("- %s: %d\n", typ, count))
		}
	}
	sb.WriteString("\n")

	if len(topLevel) > 0 {
		sb.WriteString("Top-level symbols:\n")
		sb.WriteString("| Line | Type | Name | Signature | Doc |\n")
		sb.WriteString("|------|------|------|-----------|-----|\n")
		limit := len(topLevel)
		if limit > 80 {
			limit = 80
		}
		for i := 0; i < limit; i++ {
			sym := topLevel[i]
			doc := truncateSignature(sym.Doc, 100)
			if doc == "" {
				doc = "-"
			}
			sb.WriteString(fmt.Sprintf("| %d | %s | `%s` | `%s` | %s |\n",
				sym.StartLine, formatSymbolType(string(sym.Type), sym.IsFallback), sym.Name, truncateSignature(sym.Signature, 140), doc))
		}
		if len(topLevel) > limit {
			sb.WriteString(fmt.Sprintf("| ... | ... | ... | ... | %d more top-level symbols omitted |\n", len(topLevel)-limit))
		}
		sb.WriteString("\n")
	}

	if len(receiverCounts) > 0 {
		sb.WriteString("Method groups by receiver/class:\n")
		receivers := make([]string, 0, len(receiverCounts))
		for receiver := range receiverCounts {
			receivers = append(receivers, receiver)
		}
		sort.Strings(receivers)
		shown := 0
		for _, receiver := range receivers {
			if shown >= 40 {
				sb.WriteString("- ... additional receiver groups omitted\n")
				break
			}
			count := receiverCounts[receiver]
			sb.WriteString(fmt.Sprintf("- %s: %d method(s). Use `get_file_outline(path=%q, receiver=%q)` or `get_symbol_outline(path=%q, symbol_name=%q)`.\n",
				receiver, count, displayPath, receiver, displayPath, strings.TrimPrefix(receiver, "*")))
			shown++
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Next steps:\n")
	sb.WriteString(fmt.Sprintf("- High-level view: `get_file_outline(path=%q, include_methods=false)`\n", displayPath))
	sb.WriteString(fmt.Sprintf("- Class/method view: `get_file_outline(path=%q, receiver=\"CLASS_OR_RECEIVER\")`\n", displayPath))
	sb.WriteString(fmt.Sprintf("- Child outline: `get_symbol_outline(path=%q, symbol_name=\"CLASS_OR_SYMBOL\")`\n", displayPath))
	sb.WriteString("- Read bodies only after narrowing to specific symbols or justified line ranges.\n")
	return sb.String()
}

func formatOutlineFilters(filters outlineFilters) string {
	parts := make([]string, 0, 5)
	if filters.NameFilter != "" {
		parts = append(parts, fmt.Sprintf("name_filter=%q", filters.NameFilter))
	}
	if filters.Type != "" {
		parts = append(parts, fmt.Sprintf("type=%q", filters.Type))
	}
	if filters.Receiver != "" {
		parts = append(parts, fmt.Sprintf("receiver=%q", filters.Receiver))
	}
	if filters.FallbackOnly {
		parts = append(parts, "fallback_only=true")
	}
	if !filters.IncludeMethods {
		parts = append(parts, "include_methods=false")
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

func formatSymbolType(symbolType string, isFallback bool) string {
	if isFallback {
		return symbolType + " (via fallback)"
	}
	return symbolType
}
