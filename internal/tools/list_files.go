package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const ListFilesMaxEntries = 500

type ListFilesTool struct {
	AllowedRoot string
}

type listFilesArgs struct {
	Path        string `json:"path,omitempty"`
	Recursive   bool   `json:"recursive,omitempty"`
	MaxDepth    int    `json:"max_depth,omitempty"` // recursive=trueの時のみ有効
	ShowHidden  bool   `json:"show_hidden,omitempty"`
	FilePattern string `json:"file_pattern,omitempty"` // glob pattern
}

// デフォルトで除外するディレクトリ
var defaultIgnore = map[string]bool{
	".git":         true,
	"node_modules": true,
	".cache":       true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	"dist":         true,
	"build":        true,
	"target":       true, // Rust
}

func NewListFilesTool(allowedRoot string) *ListFilesTool {
	return &ListFilesTool{AllowedRoot: allowedRoot}
}

func (t *ListFilesTool) Name() string {
	return "list_files"
}

func (t *ListFilesTool) IsMutating() bool {
	return false
}

func (t *ListFilesTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        "list_files",
			Description: "List files and directories. Useful for exploring project structure. By default lists immediate children only. Use recursive=true for nested listing.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Optional. Directory path. Defaults to project root.",
					},
					"recursive": map[string]interface{}{
						"type":        "boolean",
						"description": "Optional. List recursively into subdirectories.",
					},
					"max_depth": map[string]interface{}{
						"type":        "integer",
						"description": "Optional. Max depth for recursive listing. Default 3.",
					},
					"show_hidden": map[string]interface{}{
						"type":        "boolean",
						"description": "Optional. Show hidden files (starting with '.').",
					},
					"file_pattern": map[string]interface{}{
						"type":        "string",
						"description": "Optional. Glob pattern to filter files. Example: '*.go'",
					},
				},
			},
		},
	}
}

func (t *ListFilesTool) Execute(ctx context.Context, rawArgs json.RawMessage) (*Result, error) {
	var args listFilesArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	targetPath := t.AllowedRoot
	if args.Path != "" {
		var err error
		targetPath, err = t.resolvePath(args.Path)
		if err != nil {
			return ErrorResult(err.Error()), nil
		}
	}

	info, err := os.Stat(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrorResult(formatPathError(t.AllowedRoot, args.Path, targetPath)), nil
		}
		return ErrorResult(fmt.Sprintf("stat error: %v", err)), nil
	}

	if !info.IsDir() {
		return ErrorResult(fmt.Sprintf("path is not a directory: %s", args.Path)), nil
	}

	if args.MaxDepth <= 0 {
		args.MaxDepth = 3
	}

	var entries []string
	if args.Recursive {
		entries, err = t.listRecursive(targetPath, args, 0)
	} else {
		entries, err = t.listShallow(targetPath, args)
	}

	if err != nil {
		return ErrorResult(fmt.Sprintf("list error: %v", err)), nil
	}

	if len(entries) == 0 {
		return SuccessResult("Directory is empty (or all entries filtered out)."), nil
	}

	if len(entries) > ListFilesMaxEntries {
		entries = entries[:ListFilesMaxEntries]
		entries = append(entries, fmt.Sprintf("\n[Truncated to %d entries. Use file_pattern to filter.]", ListFilesMaxEntries))
	}

	sort.Strings(entries)

	result := fmt.Sprintf("Contents of %s:\n\n%s", targetPath, strings.Join(entries, "\n"))

	res := SuccessResult(result)
	res.Metadata = map[string]interface{}{
		"path":        targetPath,
		"entry_count": len(entries),
		"recursive":   args.Recursive,
	}
	return res, nil
}

func (t *ListFilesTool) listShallow(dir string, args listFilesArgs) ([]string, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	dirEntries, err := f.Readdir(-1)
	if err != nil {
		return nil, err
	}

	var result []string
	for _, entry := range dirEntries {
		name := entry.Name()

		if !args.ShowHidden && strings.HasPrefix(name, ".") {
			continue
		}
		if defaultIgnore[name] {
			continue
		}

		if args.FilePattern != "" && !entry.IsDir() {
			matched, _ := filepath.Match(args.FilePattern, name)
			if !matched {
				continue
			}
		}

		if entry.IsDir() {
			result = append(result, fmt.Sprintf("%s/  (directory)", name))
		} else {
			result = append(result, fmt.Sprintf("%s  (%s)", name, formatSize(entry.Size())))
		}
	}

	return result, nil
}

func (t *ListFilesTool) listRecursive(root string, args listFilesArgs, depth int) ([]string, error) {
	var result []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 個別エラーはスキップ
		}

		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}

		// 深さチェック
		currentDepth := strings.Count(rel, string(filepath.Separator))
		if currentDepth >= args.MaxDepth {
			if info.IsDir() {
				return filepath.SkipDir
			}
			// Don't skip files at max depth
		}

		name := info.Name()
		if !args.ShowHidden && strings.HasPrefix(name, ".") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if defaultIgnore[name] {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if args.FilePattern != "" && !info.IsDir() {
			matched, _ := filepath.Match(args.FilePattern, name)
			if !matched {
				return nil
			}
		}

		if info.IsDir() {
			result = append(result, fmt.Sprintf("%s/", rel))
		} else {
			result = append(result, fmt.Sprintf("%s  (%s)", rel, formatSize(info.Size())))
		}

		return nil
	})

	return result, err
}

func (t *ListFilesTool) resolvePath(path string) (string, error) {
	var abs string
	var err error
	if filepath.IsAbs(path) {
		abs = filepath.Clean(path)
	} else {
		abs, err = filepath.Abs(filepath.Join(t.AllowedRoot, path))
		if err != nil {
			return "", err
		}
	}

	if t.AllowedRoot != "" {
		relPath, err := filepath.Rel(t.AllowedRoot, abs)
		if err != nil || strings.HasPrefix(relPath, "..") {
			return "", fmt.Errorf("path outside allowed root: %s", path)
		}
	}

	return abs, nil
}

func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGT"[exp])
}
