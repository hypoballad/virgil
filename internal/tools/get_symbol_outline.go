package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hypoballad/virgil/internal/symbols"
)

const getSymbolOutlineMaxChildren = 120

// GetSymbolOutlineTool returns child symbols for one large symbol without reading its body.
type GetSymbolOutlineTool struct {
	workspaceRoot string
	extractor     *symbols.Extractor
}

type getSymbolOutlineArgs struct {
	Path       string `json:"path"`
	SymbolName string `json:"symbol_name"`
	Receiver   string `json:"receiver,omitempty"`
}

func NewGetSymbolOutlineTool(workspaceRoot string) *GetSymbolOutlineTool {
	return &GetSymbolOutlineTool{
		workspaceRoot: workspaceRoot,
		extractor:     symbols.NewExtractor(),
	}
}

func (t *GetSymbolOutlineTool) Name() string {
	return "get_symbol_outline"
}

func (t *GetSymbolOutlineTool) Description() string {
	return "Get the child-symbol outline for one symbol without reading its body. Use this after a large read_symbol SUMMARY or full=true refusal to inspect methods/members before choosing narrow read_symbol/read_file calls."
}

func (t *GetSymbolOutlineTool) Definition() ToolDefinition {
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
						"description": "Exact parent symbol name, for example a class or struct name",
					},
					"receiver": map[string]interface{}{
						"type":        "string",
						"description": "Optional exact receiver/class filter when parent symbol names are ambiguous",
					},
				},
				"required": []string{"path", "symbol_name"},
			},
		},
	}
}

func (t *GetSymbolOutlineTool) IsMutating() bool {
	return false
}

func (t *GetSymbolOutlineTool) Execute(ctx context.Context, argsJSON json.RawMessage) (*Result, error) {
	var args getSymbolOutlineArgs
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

	resolvedPath, err := resolveToolPath(t.workspaceRoot, args.Path)
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
			"unsupported file type: %s. get_symbol_outline currently supports: %s.",
			ext, strings.Join(symbols.SupportedExtensions(), ", "),
		)), nil
	}

	outline, err := t.extractor.ExtractFromFile(resolvedPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to extract symbols: %v", err)), nil
	}

	parents := make([]symbols.Symbol, 0)
	for _, sym := range outline.Symbols {
		if sym.Name != args.SymbolName {
			continue
		}
		if args.Receiver != "" && sym.Receiver != args.Receiver {
			continue
		}
		parents = append(parents, sym)
	}
	if len(parents) == 0 {
		if args.Receiver != "" {
			return ErrorResult(fmt.Sprintf("Symbol %q with receiver %q not found in %s", args.SymbolName, args.Receiver, args.Path)), nil
		}
		return ErrorResult(fmt.Sprintf("Symbol %q not found in %s", args.SymbolName, args.Path)), nil
	}

	output := formatSymbolOutline(args.Path, outline.Language, parents, outline.Symbols)
	result := SuccessResult(output)
	result.Metadata = map[string]interface{}{
		"path":        args.Path,
		"symbol_name": args.SymbolName,
		"receiver":    args.Receiver,
		"matches":     len(parents),
		"language":    outline.Language,
	}
	return result, nil
}

func formatSymbolOutline(displayPath, language string, parents []symbols.Symbol, allSymbols []symbols.Symbol) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Symbol Outline: %s\n\n", displayPath))
	sb.WriteString(fmt.Sprintf("Language: %s | Matches: %d\n", language, len(parents)))

	for i, parent := range parents {
		if len(parents) > 1 {
			sb.WriteString(fmt.Sprintf("\nMatch %d of %d\n", i+1, len(parents)))
		} else {
			sb.WriteString("\n")
		}
		receiver := parent.Receiver
		if receiver == "" {
			receiver = "-"
		}
		sb.WriteString(fmt.Sprintf("Parent: %s | Type: %s | Receiver: %s | Lines: %d-%d (%d lines)\n",
			parent.Name, formatSymbolType(string(parent.Type), parent.IsFallback), receiver, parent.StartLine, parent.EndLine, symbolLineCount(parent)))
		if strings.TrimSpace(parent.Signature) != "" {
			sb.WriteString(fmt.Sprintf("Signature: %s\n", truncateSignature(parent.Signature, 180)))
		}
		if strings.TrimSpace(parent.Doc) != "" {
			sb.WriteString(fmt.Sprintf("Doc: %s\n", truncateSignature(parent.Doc, 180)))
		}

		children := childSymbolsFor(parent, allSymbols)
		if len(children) == 0 {
			sb.WriteString("Children: none indexed. Use a justified narrow read_file range only if body structure is still needed.\n")
			continue
		}
		sb.WriteString(fmt.Sprintf("Children: %d indexed", len(children)))
		if len(children) > getSymbolOutlineMaxChildren {
			sb.WriteString(fmt.Sprintf(" (showing first %d)", getSymbolOutlineMaxChildren))
		}
		sb.WriteString("\n")
		sb.WriteString("| Line | Type | Receiver | Name | Signature | Doc |\n")
		sb.WriteString("|------|------|----------|------|-----------|-----|\n")
		for idx, child := range children {
			if idx >= getSymbolOutlineMaxChildren {
				break
			}
			childReceiver := child.Receiver
			if childReceiver == "" {
				childReceiver = "-"
			}
			doc := truncateSignature(child.Doc, 100)
			if doc == "" {
				doc = "-"
			}
			sb.WriteString(fmt.Sprintf("| %d | %s | %s | `%s` | `%s` | %s |\n",
				child.StartLine, formatSymbolType(string(child.Type), child.IsFallback), childReceiver, child.Name, truncateSignature(child.Signature, 140), doc))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Next steps: read only relevant children with `read_symbol(path=\"PATH\", symbol_name=\"CHILD_SYMBOL\", receiver=\"RECEIVER\")`. Do not reconstruct the parent by adjacent read_file ranges.\n")
	return sb.String()
}

func childSymbolsFor(parent symbols.Symbol, allSymbols []symbols.Symbol) []symbols.Symbol {
	children := make([]symbols.Symbol, 0)
	for _, sym := range allSymbols {
		if sym.Name == parent.Name && sym.StartLine == parent.StartLine && sym.EndLine == parent.EndLine && sym.Type == parent.Type {
			continue
		}
		if sym.StartLine <= parent.StartLine || sym.EndLine > parent.EndLine {
			continue
		}
		if parent.Type == symbols.SymbolClass || parent.Type == symbols.SymbolStruct || parent.Type == symbols.SymbolInterface {
			if sym.Type == symbols.SymbolMethod && (sym.Receiver == parent.Name || sym.Receiver == "" || strings.TrimPrefix(sym.Receiver, "*") == parent.Name) {
				children = append(children, sym)
				continue
			}
		}
		if sym.Receiver == "" {
			children = append(children, sym)
		}
	}
	return children
}

func resolveToolPath(workspaceRoot, path string) (string, error) {
	var abs string
	var err error
	if filepath.IsAbs(path) {
		abs = filepath.Clean(path)
	} else {
		abs, err = filepath.Abs(filepath.Join(workspaceRoot, path))
		if err != nil {
			return "", fmt.Errorf("failed to resolve path: %v", err)
		}
	}

	if workspaceRoot != "" {
		root, err := filepath.Abs(workspaceRoot)
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
