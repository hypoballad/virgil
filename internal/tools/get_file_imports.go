package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hypoballad/virgil/internal/repository"
)

// GetFileImportsTool は指定 Python ファイルの import 一覧を返す。
type GetFileImportsTool struct {
	workspaceRoot string
	imports       *repository.ImportRepository
	symbols       *repository.SymbolRepository
}

func NewGetFileImportsTool(workspaceRoot string, imports *repository.ImportRepository, symbols *repository.SymbolRepository) *GetFileImportsTool {
	return &GetFileImportsTool{workspaceRoot: workspaceRoot, imports: imports, symbols: symbols}
}

func (t *GetFileImportsTool) Name() string {
	return "get_file_imports"
}

func (t *GetFileImportsTool) Description() string {
	return "List Python imports for a file from the Tree-sitter index without reading the full file. " +
		"Use this to understand module dependencies before opening code. " +
		"Returns import/from-import statements grouped by module/function/class/conditional scope."
}

func (t *GetFileImportsTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		},
	}
}

func (t *GetFileImportsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to a Python file (relative to workspace root, or absolute)",
			},
		},
		"required": []string{"path"},
	}
}

func (t *GetFileImportsTool) IsMutating() bool {
	return false
}

type getFileImportsArgs struct {
	Path string `json:"path"`
}

func (t *GetFileImportsTool) Execute(ctx context.Context, argsJSON json.RawMessage) (*Result, error) {
	var args getFileImportsArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}
	if strings.TrimSpace(args.Path) == "" {
		return ErrorResult("path is required"), nil
	}
	if t.imports == nil {
		return ErrorResult("import repository is not available"), nil
	}

	resolvedPath := args.Path
	if !filepath.IsAbs(resolvedPath) {
		resolvedPath = filepath.Join(t.workspaceRoot, args.Path)
	}
	if _, err := os.Stat(resolvedPath); err != nil {
		if os.IsNotExist(err) {
			return ErrorResult(formatPathError(t.workspaceRoot, args.Path, resolvedPath)), nil
		}
		return ErrorResult(fmt.Sprintf("failed to stat file: %v", err)), nil
	}
	if strings.ToLower(filepath.Ext(resolvedPath)) != ".py" {
		return ErrorResult(fmt.Sprintf("not a Python file: %s", args.Path)), nil
	}

	records, err := t.imports.ListByFilePath(resolvedPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to query imports: %v", err)), nil
	}
	if len(records) == 0 {
		if t.symbols != nil {
			mtime, err := t.symbols.GetFileMtime(resolvedPath)
			if err != nil {
				return ErrorResult(fmt.Sprintf("failed to check index status: %v", err)), nil
			}
			if mtime != 0 {
				return &Result{IsError: false, Content: fmt.Sprintf("File: %s\n\n(no imports found)\n", args.Path)}, nil
			}
		}
		return ErrorResult(fmt.Sprintf("not indexed yet: %s. Run /reindex first.", args.Path)), nil
	}

	return &Result{
		IsError: false,
		Content: formatImportsReport(args.Path, records),
	}, nil
}

func formatImportsReport(displayPath string, records []repository.ImportRecord) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("File: %s\n\n", displayPath))

	sections := []struct {
		scope string
		title string
	}{
		{"module", "Module-level imports"},
		{"function", "Function-level imports"},
		{"class", "Class-level imports"},
		{"conditional", "Conditional imports"},
	}

	wroteAny := false
	for _, section := range sections {
		var scoped []repository.ImportRecord
		for _, rec := range records {
			if rec.Scope == section.scope {
				scoped = append(scoped, rec)
			}
		}
		if len(scoped) == 0 {
			continue
		}
		if wroteAny {
			sb.WriteString("\n")
		}
		wroteAny = true
		sb.WriteString(section.title)
		sb.WriteString(":\n")
		for _, rec := range scoped {
			sb.WriteString(fmt.Sprintf("  L%d: %s\n", rec.LineNumber, formatImportRecord(rec)))
		}
	}
	return sb.String()
}

func formatImportRecord(rec repository.ImportRecord) string {
	var text string
	switch rec.Kind {
	case "from_import":
		name := rec.ImportedName
		if rec.IsWildcard {
			name = "*"
		}
		text = fmt.Sprintf("from %s import %s", rec.Module, name)
	default:
		text = fmt.Sprintf("import %s", rec.Module)
	}
	if rec.Alias != "" {
		text += fmt.Sprintf(" as %s", rec.Alias)
	}

	var attrs []string
	if rec.Alias != "" {
		attrs = append(attrs, "alias")
	}
	if rec.IsRelative {
		attrs = append(attrs, fmt.Sprintf("relative, level=%d", rec.RelativeLevel))
	}
	if rec.IsWildcard {
		attrs = append(attrs, "wildcard")
	}
	if len(attrs) > 0 {
		text += " (" + strings.Join(attrs, "; ") + ")"
	}
	return text
}
