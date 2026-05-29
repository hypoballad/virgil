package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	// FullReadSizeLimit はサマリーモードに切り替える閾値
	FullReadSizeLimit = 50 * 1024 // 50KB

	// MaxFileSize は絶対上限
	MaxFileSize = 10 * 1024 * 1024 // 10MB

	// FullCodeReadLineLimit はコードファイルの範囲なし全文読みを許可する最大行数
	FullCodeReadLineLimit = 100
)

type ReadFileTool struct {
	// 作業ディレクトリ制限（セキュリティ）
	AllowedRoot string
}

type readFileArgs struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line,omitempty"` // 1-indexed、0なら先頭から
	EndLine   int    `json:"end_line,omitempty"`   // inclusive、0なら末尾まで
}

func NewReadFileTool(allowedRoot string) *ReadFileTool {
	return &ReadFileTool{AllowedRoot: allowedRoot}
}

func (t *ReadFileTool) Name() string {
	return "read_file"
}

func (t *ReadFileTool) IsMutating() bool {
	return false
}

func (t *ReadFileTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        "read_file",
			Description: "Read the contents of a file. For large files (over 50KB), returns a summary with line count. Use start_line and end_line for partial reads.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Absolute or relative path to the file",
					},
					"start_line": map[string]interface{}{
						"type":        "integer",
						"description": "Optional. Start line number (1-indexed). Default: read from beginning.",
					},
					"end_line": map[string]interface{}{
						"type":        "integer",
						"description": "Optional. End line number (inclusive). Default: read to end of file.",
					},
				},
				"required": []string{"path"},
			},
		},
	}
}

func (t *ReadFileTool) Execute(ctx context.Context, rawArgs json.RawMessage) (*Result, error) {
	var args readFileArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	// パスの正規化と安全性チェック
	cleanPath, err := t.resolvePath(args.Path)
	if err != nil {
		return ErrorResult(err.Error()), nil
	}

	// ファイル情報取得
	info, err := os.Stat(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrorResult(formatPathError(t.AllowedRoot, args.Path, cleanPath)), nil
		}
		return ErrorResult(fmt.Sprintf("stat error: %v", err)), nil
	}

	if info.IsDir() {
		return ErrorResult(fmt.Sprintf("path is a directory, not a file: %s", args.Path)), nil
	}

	if info.Size() > MaxFileSize {
		return ErrorResult(fmt.Sprintf("file too large: %d bytes (max %d)", info.Size(), MaxFileSize)), nil
	}

	// バイナリファイル検出
	if isBinary, _ := t.isBinaryFile(cleanPath); isBinary {
		return ErrorResult(fmt.Sprintf("binary file not supported: %s", args.Path)), nil
	}

	if isMarkdownFile(cleanPath) && args.StartLine == 0 && args.EndLine == 0 {
		return ErrorResult(formatMarkdownFullReadRefusal(args.Path)), nil
	}

	// 範囲指定がある場合
	if args.StartLine > 0 || args.EndLine > 0 {
		return t.readRange(cleanPath, args.StartLine, args.EndLine)
	}

	if isCodeFile(cleanPath) {
		lineCount, err := countFileLines(cleanPath)
		if err != nil {
			return ErrorResult(fmt.Sprintf("scan error: %v", err)), nil
		}
		if lineCount > FullCodeReadLineLimit {
			return SuccessResult(formatCodeFullReadSummary(args.Path, info.Size(), lineCount)), nil
		}
	}

	// サイズ判定
	if info.Size() < FullReadSizeLimit {
		return t.readFull(cleanPath)
	}

	// 大きいファイルはサマリー
	return t.readSummary(cleanPath, info.Size())
}

func isMarkdownFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".md" || ext == ".markdown" || ext == ".mdown" || ext == ".mkd"
}

func isCodeFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".py", ".js", ".jsx", ".ts", ".tsx", ".rs":
		return true
	default:
		return false
	}
}

func countFileLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	lineCount := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		lineCount++
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return lineCount, nil
}

func formatCodeFullReadSummary(displayPath string, size int64, lineCount int) string {
	return fmt.Sprintf(
		"Code file is %d lines (%.1f KB). Refusing full read_file without a range to protect context.\n\n"+
			"Suggested approach:\n"+
			"- get_file_outline(path=%q, include_methods=false) for a high-level outline\n"+
			"- get_file_imports(path=%q) for Python imports when technology/dependency context matters\n"+
			"- get_symbol_outline(path=%q, symbol_name=\"CLASS_OR_SYMBOL\") for a large symbol's children\n"+
			"- read_symbol(path=%q, symbol_name=\"SYMBOL_NAME\") for a focused symbol summary\n"+
			"- read_file(path=%q, start_line=START_LINE, end_line=END_LINE) only for a justified narrow range",
		lineCount, float64(size)/1024, displayPath, displayPath, displayPath, displayPath, displayPath,
	)
}

func formatMarkdownFullReadRefusal(displayPath string) string {
	return fmt.Sprintf(
		"Refusing full Markdown read to protect context: %s\n\n"+
			"Use a focused Markdown read instead:\n"+
			"- get_markdown_outline(path=%q) to inspect headings and line ranges\n"+
			"- read_markdown_section(path=%q, heading=\"HEADING\") when the user provided a heading\n"+
			"- read_file(path=%q, start_line=START_LINE, end_line=END_LINE) only for a known narrow range\n\n"+
			"If the user provided the exact line to change, use edit_with_pattern directly instead of reading the full file.",
		displayPath, displayPath, displayPath, displayPath,
	)
}

// resolvePath はパスを解決し、AllowedRoot配下にあることを確認
func (t *ReadFileTool) resolvePath(path string) (string, error) {
	var abs string
	var err error
	if filepath.IsAbs(path) {
		abs = filepath.Clean(path)
	} else {
		abs, err = filepath.Abs(filepath.Join(t.AllowedRoot, path))
		if err != nil {
			return "", fmt.Errorf("failed to resolve path: %v", err)
		}
	}

	// AllowedRoot配下チェック（パストラバーサル攻撃対策）
	if t.AllowedRoot != "" {
		relPath, err := filepath.Rel(t.AllowedRoot, abs)
		if err != nil || strings.HasPrefix(relPath, "..") {
			return "", fmt.Errorf("path outside allowed root: %s", path)
		}
	}

	return abs, nil
}

// isBinaryFile は先頭バイトでバイナリかどうか判定
func (t *ReadFileTool) isBinaryFile(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	// 先頭8KBで判定
	buf := make([]byte, 8192)
	n, _ := f.Read(buf)
	buf = buf[:n]

	// NULバイトが含まれていればバイナリ
	if bytes.IndexByte(buf, 0) >= 0 {
		return true, nil
	}

	// UTF-8として有効でないならバイナリ
	// ただし、バッファの末尾でマルチバイト文字が切れている可能性があるため、
	// 最大3バイトまで削って再試行する
	if !utf8.Valid(buf) {
		for i := 1; i < 4 && i < len(buf); i++ {
			if utf8.Valid(buf[:len(buf)-i]) {
				return false, nil
			}
		}
		return true, nil
	}

	return false, nil
}

// readFull は小さいファイルの全文を読む
func (t *ReadFileTool) readFull(path string) (*Result, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return ErrorResult(fmt.Sprintf("read error: %v", err)), nil
	}

	lines := strings.Split(string(content), "\n")
	lineCount := len(lines)

	// 末尾の空行を除く調整
	if lineCount > 0 && lines[lineCount-1] == "" {
		lineCount--
	}

	// 行番号付きに整形
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("File: %s (%d lines)\n", filepath.Base(path), lineCount))
	sb.WriteString(strings.Repeat("-", 40))
	sb.WriteString("\n")

	for i, line := range lines {
		if i >= lineCount {
			break
		}
		sb.WriteString(fmt.Sprintf("%4d | %s\n", i+1, line))
	}

	result := SuccessResult(sb.String())
	result.Metadata = map[string]interface{}{
		"size_bytes": len(content),
		"line_count": lineCount,
		"mode":       "full",
	}
	return result, nil
}

// readRange は指定範囲を読む
func (t *ReadFileTool) readRange(path string, startLine, endLine int) (*Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return ErrorResult(fmt.Sprintf("open error: %v", err)), nil
	}
	defer f.Close()

	if startLine < 1 {
		startLine = 1
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("File: %s (lines %d-%d)\n", filepath.Base(path), startLine, endLine))
	sb.WriteString(strings.Repeat("-", 40))
	sb.WriteString("\n")

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MBバッファ

	lineNum := 0
	actualLines := 0
	for scanner.Scan() {
		lineNum++
		if lineNum < startLine {
			continue
		}
		if endLine > 0 && lineNum > endLine {
			break
		}
		sb.WriteString(fmt.Sprintf("%4d | %s\n", lineNum, scanner.Text()))
		actualLines++
	}

	if err := scanner.Err(); err != nil {
		return ErrorResult(fmt.Sprintf("scan error: %v", err)), nil
	}

	result := SuccessResult(sb.String())
	result.Metadata = map[string]interface{}{
		"start_line":   startLine,
		"end_line":     endLine,
		"actual_lines": actualLines,
		"mode":         "range",
	}
	return result, nil
}

// readSummary は大きいファイルのサマリーを返す
func (t *ReadFileTool) readSummary(path string, size int64) (*Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return ErrorResult(fmt.Sprintf("open error: %v", err)), nil
	}
	defer f.Close()

	lineCount := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		lineCount++
	}

	summary := fmt.Sprintf(
		"File is large (%.1f KB, %d lines). Use start_line and end_line parameters to read specific sections.\n\n"+
			"Suggested approach:\n"+
			"- Read first 50 lines: read_file(path=%q, start_line=1, end_line=50)\n"+
			"- Read by ranges of 100-200 lines as needed",
		float64(size)/1024,
		lineCount,
		filepath.Base(path),
	)

	result := SuccessResult(summary)
	result.Metadata = map[string]interface{}{
		"size_bytes": size,
		"line_count": lineCount,
		"mode":       "summary",
	}
	return result, nil
}
