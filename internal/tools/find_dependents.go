package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hypoballad/virgil/internal/repository"
)

const (
	findDependentsDefaultMaxResults = 50
	findDependentsMaxResults        = 200
)

// FindDependentsTool は指定モジュールを import しているファイルを返す。
type FindDependentsTool struct {
	imports *repository.ImportRepository
}

func NewFindDependentsTool(imports *repository.ImportRepository) *FindDependentsTool {
	return &FindDependentsTool{imports: imports}
}

func (t *FindDependentsTool) Name() string {
	return "find_dependents"
}

func (t *FindDependentsTool) IsMutating() bool {
	return false
}

func (t *FindDependentsTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        "find_dependents",
			Description: "Find Python files that import a module using the Tree-sitter import index. Use this before search_text for dependency reverse lookups such as 'who imports numpy?'. Matches exact module names and dotted submodules by default, with filters for imported name, alias, scope, import kind, file path, relative imports, and wildcard imports.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"module": map[string]interface{}{
						"type":        "string",
						"description": "Module name to search for. Examples: 'numpy', 'torch', 'os.path', 'typing'.",
					},
					"exact_module": map[string]interface{}{
						"type":        "boolean",
						"description": "Optional. If true, match only the exact module and not dotted submodules. Default false.",
					},
					"import_kind": map[string]interface{}{
						"type":        "string",
						"description": "Optional. Filter by import kind: import or from_import.",
					},
					"imported_name": map[string]interface{}{
						"type":        "string",
						"description": "Optional. Filter from-import names, e.g. List in 'from typing import List' or ndarray in 'from numpy import ndarray'.",
					},
					"alias": map[string]interface{}{
						"type":        "string",
						"description": "Optional. Filter aliases, e.g. np in 'import numpy as np'.",
					},
					"file_path": map[string]interface{}{
						"type":        "string",
						"description": "Optional. Substring filter for indexed file paths.",
					},
					"scope": map[string]interface{}{
						"type":        "string",
						"description": "Optional. Filter import scope: module, function, class, or conditional.",
					},
					"include_relative": map[string]interface{}{
						"type":        "boolean",
						"description": "Optional. Include relative imports such as from .helper import foo. Default false.",
					},
					"wildcard_only": map[string]interface{}{
						"type":        "boolean",
						"description": "Optional. If true, only return wildcard imports such as 'from typing import *'.",
					},
					"max_results": map[string]interface{}{
						"type":        "integer",
						"description": "Optional. Maximum import entries to return. Default 50, max 200.",
					},
				},
				"required": []string{"module"},
			},
		},
	}
}

type findDependentsArgs struct {
	Module          string `json:"module"`
	ExactModule     bool   `json:"exact_module,omitempty"`
	ImportKind      string `json:"import_kind,omitempty"`
	ImportedName    string `json:"imported_name,omitempty"`
	Alias           string `json:"alias,omitempty"`
	FilePath        string `json:"file_path,omitempty"`
	Scope           string `json:"scope,omitempty"`
	IncludeRelative bool   `json:"include_relative,omitempty"`
	WildcardOnly    bool   `json:"wildcard_only,omitempty"`
	MaxResults      int    `json:"max_results,omitempty"`
}

type findDependentsFilters struct {
	ExactModule     bool
	ImportKind      string
	ImportedName    string
	Alias           string
	FilePath        string
	Scope           string
	IncludeRelative bool
	WildcardOnly    bool
	HasFilters      bool
}

func (t *FindDependentsTool) Execute(ctx context.Context, argsJSON json.RawMessage) (*Result, error) {
	var args findDependentsArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}
	module := strings.TrimSpace(args.Module)
	if module == "" {
		return ErrorResult("module is required"), nil
	}
	if t.imports == nil {
		return ErrorResult("import repository is not available"), nil
	}

	maxResults := normalizeFindDependentsMaxResults(args.MaxResults)
	filters := newFindDependentsFilters(args)
	entries, err := t.imports.FindDependentsWithOptions(repository.DependentSearchOptions{
		Module:          module,
		IncludeRelative: filters.IncludeRelative,
		ExactModule:     filters.ExactModule,
		ImportKind:      filters.ImportKind,
		ImportedName:    filters.ImportedName,
		Alias:           filters.Alias,
		FilePath:        filters.FilePath,
		Scope:           filters.Scope,
		WildcardOnly:    filters.WildcardOnly,
		MaxResults:      maxResults + 1,
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to query dependents: %v", err)), nil
	}
	truncated := len(entries) > maxResults
	if truncated {
		entries = entries[:maxResults]
	}

	if len(entries) == 0 {
		count, err := t.imports.CountAll()
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to check import index: %v", err)), nil
		}
		if count == 0 {
			return SuccessResult("No imports indexed yet. Run /reindex first."), nil
		}
		return SuccessResult(formatDependentsReport(module, nil, false, maxResults, filters)), nil
	}

	content := formatDependentsReport(module, entries, truncated, maxResults, filters)
	res := SuccessResult(content)
	res.Metadata = map[string]interface{}{
		"module":           module,
		"entry_count":      len(entries),
		"max_results":      maxResults,
		"truncated":        truncated,
		"include_relative": filters.IncludeRelative,
		"exact_module":     filters.ExactModule,
		"import_kind":      filters.ImportKind,
		"imported_name":    filters.ImportedName,
		"alias":            filters.Alias,
		"file_path":        filters.FilePath,
		"scope":            filters.Scope,
		"wildcard_only":    filters.WildcardOnly,
	}
	return res, nil
}

func newFindDependentsFilters(args findDependentsArgs) findDependentsFilters {
	importKind := strings.ToLower(strings.TrimSpace(args.ImportKind))
	scope := strings.ToLower(strings.TrimSpace(args.Scope))
	importedName := strings.TrimSpace(args.ImportedName)
	alias := strings.TrimSpace(args.Alias)
	filePath := strings.TrimSpace(args.FilePath)
	return findDependentsFilters{
		ExactModule:     args.ExactModule,
		ImportKind:      importKind,
		ImportedName:    importedName,
		Alias:           alias,
		FilePath:        filePath,
		Scope:           scope,
		IncludeRelative: args.IncludeRelative,
		WildcardOnly:    args.WildcardOnly,
		HasFilters: args.ExactModule ||
			importKind != "" ||
			importedName != "" ||
			alias != "" ||
			filePath != "" ||
			scope != "" ||
			args.IncludeRelative ||
			args.WildcardOnly,
	}
}

func normalizeFindDependentsMaxResults(n int) int {
	if n <= 0 {
		return findDependentsDefaultMaxResults
	}
	if n > findDependentsMaxResults {
		return findDependentsMaxResults
	}
	return n
}

func formatDependentsReport(module string, entries []repository.DependentEntry, truncated bool, maxResults int, filters findDependentsFilters) string {
	files := make(map[string]bool)
	for _, entry := range entries {
		files[entry.FilePath] = true
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Module: %s\n", module))
	if filters.HasFilters {
		sb.WriteString(fmt.Sprintf("Filters: %s\n", formatFindDependentsFilters(filters)))
	}
	if len(entries) == 0 {
		sb.WriteString("Found 0 file(s) importing this module.")
		return sb.String()
	}
	sb.WriteString(fmt.Sprintf("Found %d file(s) importing this module", len(files)))
	if truncated {
		sb.WriteString(fmt.Sprintf(" (showing first %d import entries; more may exist)", maxResults))
	}
	sb.WriteString(":\n\n")

	currentFile := ""
	for _, entry := range entries {
		if entry.FilePath != currentFile {
			if currentFile != "" {
				sb.WriteString("\n")
			}
			currentFile = entry.FilePath
			sb.WriteString(currentFile)
			sb.WriteString("\n")
		}
		sb.WriteString(fmt.Sprintf("  L%d: %s\n", entry.LineNumber, formatDependentEntry(entry)))
	}
	return sb.String()
}

func formatFindDependentsFilters(filters findDependentsFilters) string {
	parts := make([]string, 0, 8)
	if filters.ExactModule {
		parts = append(parts, "exact_module=true")
	}
	if filters.ImportKind != "" {
		parts = append(parts, fmt.Sprintf("import_kind=%q", filters.ImportKind))
	}
	if filters.ImportedName != "" {
		parts = append(parts, fmt.Sprintf("imported_name=%q", filters.ImportedName))
	}
	if filters.Alias != "" {
		parts = append(parts, fmt.Sprintf("alias=%q", filters.Alias))
	}
	if filters.FilePath != "" {
		parts = append(parts, fmt.Sprintf("file_path=%q", filters.FilePath))
	}
	if filters.Scope != "" {
		parts = append(parts, fmt.Sprintf("scope=%q", filters.Scope))
	}
	if filters.IncludeRelative {
		parts = append(parts, "include_relative=true")
	}
	if filters.WildcardOnly {
		parts = append(parts, "wildcard_only=true")
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

func formatDependentEntry(entry repository.DependentEntry) string {
	var text string
	switch entry.ImportKind {
	case "from_import":
		name := entry.ImportedName
		if entry.IsWildcard {
			name = "*"
		}
		text = fmt.Sprintf("from %s import %s", entry.Module, name)
	default:
		text = fmt.Sprintf("import %s", entry.Module)
	}
	if entry.Alias != "" {
		text += fmt.Sprintf(" as %s", entry.Alias)
	}

	var attrs []string
	if entry.Alias != "" {
		attrs = append(attrs, "alias")
	}
	if entry.IsRelative {
		if entry.RelativeLevel > 0 {
			attrs = append(attrs, fmt.Sprintf("relative level=%d", entry.RelativeLevel))
		} else {
			attrs = append(attrs, "relative")
		}
	}
	if entry.IsWildcard {
		attrs = append(attrs, "wildcard")
	}
	if entry.Scope != "" && entry.Scope != "module" {
		attrs = append(attrs, "scope="+entry.Scope)
	}
	if len(attrs) > 0 {
		text += " (" + strings.Join(attrs, "; ") + ")"
	}
	return text
}
