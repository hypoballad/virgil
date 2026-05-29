package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	SearchMaxResults    = 100
	SearchMaxResultSize = 50 * 1024 // 50KB
)

type SearchTextTool struct {
	AllowedRoot string
}

type searchTextArgs struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path,omitempty"`      // 検索対象ディレクトリ（省略時はAllowedRoot）
	FileType   string `json:"file_type,omitempty"` // "go", "py", "lua" など
	IgnoreCase bool   `json:"ignore_case,omitempty"`
	MaxResults int    `json:"max_results,omitempty"` // デフォルト100
}

func NewSearchTextTool(allowedRoot string) *SearchTextTool {
	return &SearchTextTool{AllowedRoot: allowedRoot}
}

func (t *SearchTextTool) Name() string {
	return "search_text"
}

func (t *SearchTextTool) IsMutating() bool {
	return false
}

func (t *SearchTextTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name: "search_text",
			Description: "[FALLBACK ONLY] Full-text grep search across files. " +
				"Use this ONLY when find_symbol cannot help: " +
				"- Searching inside comments or string literals (e.g., error messages) " +
				"- Non-Go/Python files (.md, config files, etc.) " +
				"- After find_symbol returns 0 results. " +
				"Slow and noisy: returns many false positives. Avoid as a first choice for finding function or type definitions.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pattern": map[string]interface{}{
						"type":        "string",
						"description": "Search pattern (regex supported). Examples: 'func.*Read', 'TODO', 'import \"fmt\"'",
					},
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Optional. Directory to search in. Defaults to project root.",
					},
					"file_type": map[string]interface{}{
						"type":        "string",
						"description": "Optional. Filter by file type. Examples: 'go', 'py', 'lua', 'md'.",
					},
					"ignore_case": map[string]interface{}{
						"type":        "boolean",
						"description": "Optional. Case-insensitive search.",
					},
					"max_results": map[string]interface{}{
						"type":        "integer",
						"description": "Optional. Maximum number of matches. Default 100.",
					},
				},
				"required": []string{"pattern"},
			},
		},
	}
}

func (t *SearchTextTool) Execute(ctx context.Context, rawArgs json.RawMessage) (*Result, error) {
	var args searchTextArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	if args.Pattern == "" {
		return ErrorResult("pattern is required"), nil
	}

	// 検索対象ディレクトリの解決
	searchPath := t.AllowedRoot
	if args.Path != "" {
		var err error
		searchPath, err = t.resolvePath(args.Path)
		if err != nil {
			return ErrorResult(err.Error()), nil
		}
	}

	if args.MaxResults <= 0 {
		args.MaxResults = SearchMaxResults
	}
	if args.MaxResults > SearchMaxResults {
		args.MaxResults = SearchMaxResults
	}

	// ripgrepコマンド構築
	rgArgs := []string{
		"--line-number",
		"--no-heading",
		"--color", "never",
		fmt.Sprintf("--max-count=%d", args.MaxResults),
	}

	if args.IgnoreCase {
		rgArgs = append(rgArgs, "--ignore-case")
	}
	if args.FileType != "" {
		rgArgs = append(rgArgs, "--type", args.FileType)
	}

	rgArgs = append(rgArgs, args.Pattern, searchPath)

	// 実行
	cmd := exec.CommandContext(ctx, "rg", rgArgs...)
	output, err := cmd.Output()

	if err != nil {
		if isCommandNotFound(err) {
			return t.executeGoFallback(ctx, args, searchPath)
		}
		// exit code 1 はripgrepの「マッチなし」の正常終了
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return SuccessResult(fmt.Sprintf("No matches found for pattern: %s", args.Pattern)), nil
		}
		return ErrorResult(fmt.Sprintf("search failed: %v", err)), nil
	}

	// 結果のサイズ制限
	result := string(output)
	matchCount := strings.Count(result, "\n")

	if len(result) > SearchMaxResultSize {
		truncated := result[:SearchMaxResultSize]
		// 最後の改行で切る
		if idx := strings.LastIndex(truncated, "\n"); idx > 0 {
			truncated = truncated[:idx]
		}
		result = fmt.Sprintf(
			"%s\n\n[Truncated. %d total matches, showing first portion. Use file_type filter or more specific pattern to narrow down.]",
			truncated,
			matchCount,
		)
	}

	if result == "" {
		result = fmt.Sprintf("No matches found for pattern: %s", args.Pattern)
	} else {
		result = fmt.Sprintf("Found matches for pattern '%s':\n\n%s", args.Pattern, result)
	}

	// ヒントの追加: 識別子っぽいパターンの場合、find_symbol を促す
	if isSimpleIdentifier(args.Pattern) {
		result += "\n\n💡 Hint: If you're looking for a specific function, type, or method definition, try:\n" +
			fmt.Sprintf("  find_symbol(name=%q)\n", args.Pattern) +
			"This returns precise definitions and is much faster than search_text."
	}

	res := SuccessResult(result)
	res.Metadata = map[string]interface{}{
		"pattern":     args.Pattern,
		"match_count": matchCount,
		"search_path": searchPath,
		"truncated":   len(string(output)) > SearchMaxResultSize,
	}
	return res, nil
}

func isCommandNotFound(err error) bool {
	var execErr *exec.Error
	return errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound)
}

func (t *SearchTextTool) executeGoFallback(ctx context.Context, args searchTextArgs, searchPath string) (*Result, error) {
	re, err := compileSearchRegexp(args.Pattern, args.IgnoreCase)
	if err != nil {
		return ErrorResult(fmt.Sprintf("invalid pattern: %v", err)), nil
	}

	var matches []string
	err = filepath.WalkDir(searchPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		name := entry.Name()
		if entry.IsDir() {
			if path != searchPath && (defaultIgnore[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if defaultIgnore[name] || strings.HasPrefix(name, ".") {
			return nil
		}
		if args.FileType != "" && !matchesSearchFileType(path, args.FileType) {
			return nil
		}
		if len(matches) >= args.MaxResults {
			return filepath.SkipAll
		}

		fileMatches, err := searchFileWithRegexp(path, re, args.MaxResults-len(matches))
		if err != nil {
			return nil
		}
		matches = append(matches, fileMatches...)
		if len(matches) >= args.MaxResults {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil && !errors.Is(err, filepath.SkipAll) {
		return ErrorResult(fmt.Sprintf("search fallback failed: %v", err)), nil
	}

	var result string
	if len(matches) == 0 {
		result = fmt.Sprintf("No matches found for pattern: %s", args.Pattern)
	} else {
		result = fmt.Sprintf("Found matches for pattern '%s' using Go fallback because rg is not installed:\n\n%s", args.Pattern, strings.Join(matches, "\n"))
		if len(matches) >= args.MaxResults {
			result += fmt.Sprintf("\n\n[Truncated to %d matches. Use file_type filter or more specific pattern to narrow down.]", args.MaxResults)
		}
	}
	if len(result) > SearchMaxResultSize {
		truncated := result[:SearchMaxResultSize]
		if idx := strings.LastIndex(truncated, "\n"); idx > 0 {
			truncated = truncated[:idx]
		}
		result = truncated + "\n\n[Truncated due to output size limit.]"
	}

	if isSimpleIdentifier(args.Pattern) {
		result += "\n\n💡 Hint: If you're looking for a specific function, type, or method definition, try:\n" +
			fmt.Sprintf("  find_symbol(name=%q)\n", args.Pattern) +
			"This returns precise definitions and is much faster than search_text."
	}

	res := SuccessResult(result)
	res.Metadata = map[string]interface{}{
		"pattern":     args.Pattern,
		"match_count": len(matches),
		"search_path": searchPath,
		"truncated":   len(matches) >= args.MaxResults,
		"fallback":    "go",
	}
	return res, nil
}

func compileSearchRegexp(pattern string, ignoreCase bool) (*regexp.Regexp, error) {
	if ignoreCase {
		return regexp.Compile("(?i)" + pattern)
	}
	return regexp.Compile(pattern)
}

func searchFileWithRegexp(path string, re *regexp.Regexp, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > MaxFileSize {
		return nil, err
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	var matches []string
	lineNumber := 0
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			lineNumber++
			text := strings.TrimRight(line, "\r\n")
			if re.MatchString(text) {
				matches = append(matches, fmt.Sprintf("%s:%d:%s", path, lineNumber, text))
				if len(matches) >= limit {
					return matches, nil
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return matches, err
		}
	}
	return matches, nil
}

func matchesSearchFileType(path string, fileType string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch strings.ToLower(strings.TrimPrefix(fileType, ".")) {
	case "go":
		return ext == ".go"
	case "py", "python":
		return ext == ".py"
	case "js", "javascript":
		return ext == ".js" || ext == ".jsx"
	case "ts", "typescript":
		return ext == ".ts" || ext == ".tsx"
	case "md", "markdown":
		return ext == ".md" || ext == ".markdown"
	case "lua":
		return ext == ".lua"
	case "rs", "rust":
		return ext == ".rs"
	case "json":
		return ext == ".json"
	case "yaml", "yml":
		return ext == ".yaml" || ext == ".yml"
	case "txt", "text":
		return ext == ".txt"
	default:
		return ext == "."+strings.ToLower(strings.TrimPrefix(fileType, "."))
	}
}

func (t *SearchTextTool) resolvePath(path string) (string, error) {
	var abs string
	if filepath.IsAbs(path) {
		abs = filepath.Clean(path)
	} else {
		var err error
		abs, err = filepath.Abs(filepath.Join(t.AllowedRoot, path))
		if err != nil {
			return "", fmt.Errorf("failed to resolve path: %v", err)
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

// isSimpleIdentifier はパターンが単純な識別子（英数字とアンダースコアのみ）か判定
func isSimpleIdentifier(pattern string) bool {
	matched, _ := regexp.MatchString(`^[a-zA-Z_][a-zA-Z0-9_]*$`, pattern)
	return matched
}
