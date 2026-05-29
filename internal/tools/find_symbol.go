package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hypoballad/virgil/internal/repository"
)

const findSymbolDefaultLimit = 20
const findSymbolMaxLimit = 50

// FindSymbolTool はプロジェクト全体から名前でシンボルを検索するツール
type FindSymbolTool struct {
	symbols *repository.SymbolRepository
}

func NewFindSymbolTool(symbols *repository.SymbolRepository) *FindSymbolTool {
	return &FindSymbolTool{symbols: symbols}
}

func (t *FindSymbolTool) Name() string {
	return "find_symbol"
}

func (t *FindSymbolTool) Description() string {
	return "[PRIMARY SEARCH TOOL] Locate any function, method, type, or const by name across the entire codebase. " +
		"This is your DEFAULT tool for finding code. " +
		"Returns exact file paths, line numbers, signatures, and indexed doc/comment summaries in <50ms (uses pre-built index, no full-text scan needed). " +
		"Use type, receiver, file_path, and fallback_only filters to narrow noisy results such as common method names. " +
		"Searches symbol names, not arbitrary doc/comment text; use search_text only when you need free-text search inside comments, docstrings, or strings. " +
		"Equivalent to `grep -n \"func.*Name\"` but 100x faster and only returns true definitions, not call sites. " +
		"Always try this FIRST when looking for code by name. " +
		"Currently indexed: Go, Python, JavaScript, TypeScript/TSX, and Rust files."
}

func (t *FindSymbolTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		},
	}
}

func (t *FindSymbolTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Symbol name to search for (substring match). Results include any indexed docstring/leading comment attached to the symbol.",
			},
			"type": map[string]interface{}{
				"type":        "string",
				"description": "Optional filter by symbol type: function, method, class, struct, interface, type, const, var",
			},
			"receiver": map[string]interface{}{
				"type":        "string",
				"description": "Optional exact receiver/class filter for methods, e.g. Calculator, *Agent, or myAE.",
			},
			"file_path": map[string]interface{}{
				"type":        "string",
				"description": "Optional substring filter for indexed file paths, e.g. train/src/AE.py or internal/agent.",
			},
			"fallback_only": map[string]interface{}{
				"type":        "boolean",
				"description": "If true, only return symbols recovered by fallback extraction.",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of results (default 20, max 50)",
			},
		},
		"required": []string{"name"},
	}
}

func (t *FindSymbolTool) IsMutating() bool {
	return false
}

type findSymbolArgs struct {
	Name         string `json:"name"`
	Type         string `json:"type,omitempty"`
	Receiver     string `json:"receiver,omitempty"`
	FilePath     string `json:"file_path,omitempty"`
	FallbackOnly bool   `json:"fallback_only,omitempty"`
	Limit        int    `json:"limit,omitempty"`
}

type findSymbolFilters struct {
	Type         string
	Receiver     string
	FilePath     string
	FallbackOnly bool
	HasFilters   bool
}

func (t *FindSymbolTool) Execute(ctx context.Context, argsJSON json.RawMessage) (*Result, error) {
	var args findSymbolArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	if strings.TrimSpace(args.Name) == "" {
		return ErrorResult("name is required"), nil
	}

	// limit の正規化
	limit := args.Limit
	if limit <= 0 {
		limit = findSymbolDefaultLimit
	}
	if limit > findSymbolMaxLimit {
		limit = findSymbolMaxLimit
	}

	// 部分一致パターン: %name%
	pattern := "%" + args.Name + "%"

	filters := newFindSymbolFilters(args)

	records, err := t.symbols.FindSymbols(repository.SymbolSearchOptions{
		Pattern:      pattern,
		SymbolType:   filters.Type,
		Receiver:     filters.Receiver,
		FilePath:     filters.FilePath,
		FallbackOnly: filters.FallbackOnly,
		Limit:        limit,
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("query failed: %v", err)), nil
	}

	// 結果フォーマット
	output := formatFindSymbolResults(args.Name, filters, records, limit)
	return &Result{
		IsError: false,
		Content: output,
	}, nil
}

func newFindSymbolFilters(args findSymbolArgs) findSymbolFilters {
	symbolType := strings.ToLower(strings.TrimSpace(args.Type))
	receiver := strings.TrimSpace(args.Receiver)
	filePath := strings.TrimSpace(args.FilePath)
	return findSymbolFilters{
		Type:         symbolType,
		Receiver:     receiver,
		FilePath:     filePath,
		FallbackOnly: args.FallbackOnly,
		HasFilters:   symbolType != "" || receiver != "" || filePath != "" || args.FallbackOnly,
	}
}

func formatFindSymbolResults(query string, filters findSymbolFilters, records []repository.SymbolRecord, limit int) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# Symbol search: %q", query))
	sb.WriteString("\n\n")
	if filters.HasFilters {
		sb.WriteString(fmt.Sprintf("Filters: %s\n\n", formatFindSymbolFilters(filters)))
	}

	if len(records) == 0 {
		sb.WriteString("No matching symbols found.\n\n")
		sb.WriteString("Possible reasons:\n")
		sb.WriteString("- Name typo (try a partial match)\n")
		sb.WriteString("- File hasn't been indexed yet\n")
		sb.WriteString("- Symbol exists in an unsupported file type (use search_text instead)\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Found %d match(es)", len(records)))
	if len(records) == limit {
		sb.WriteString(fmt.Sprintf(" (limited to %d)", limit))
	}
	sb.WriteString("\n\n")

	sb.WriteString("| File | Line | Type | Receiver | Name | Signature | Doc |\n")
	sb.WriteString("|------|------|------|----------|------|-----------|-----|\n")

	for _, r := range records {
		receiver := r.Receiver
		if receiver == "" {
			receiver = "-"
		}
		signature := truncateSignature(r.Signature, 60)
		doc := truncateSignature(r.Doc, 100)
		if doc == "" {
			doc = "-"
		}
		sb.WriteString(fmt.Sprintf("| `%s` | %d | %s | %s | `%s` | `%s` | %s |\n",
			r.FilePath, r.StartLine, formatSymbolType(r.Type, r.IsFallback), receiver, r.Name, signature, doc))
	}

	sb.WriteString("\n")
	sb.WriteString("**Next steps:**\n")
	sb.WriteString("- To read a symbol: `read_symbol(path=\"FILE\", symbol_name=\"SYMBOL_NAME\")`\n")
	sb.WriteString("- To see all symbols in a file: `get_file_outline(path=\"FILE\")`\n")

	return sb.String()
}

func formatFindSymbolFilters(filters findSymbolFilters) string {
	parts := make([]string, 0, 4)
	if filters.Type != "" {
		parts = append(parts, fmt.Sprintf("type=%q", filters.Type))
	}
	if filters.Receiver != "" {
		parts = append(parts, fmt.Sprintf("receiver=%q", filters.Receiver))
	}
	if filters.FilePath != "" {
		parts = append(parts, fmt.Sprintf("file_path=%q", filters.FilePath))
	}
	if filters.FallbackOnly {
		parts = append(parts, "fallback_only=true")
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}
