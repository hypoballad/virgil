package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/hypoballad/virgil/internal/symbols"
)

const (
	EditFileMaxLines    = 200000 // 1ファイルあたりの最大行数
	EditFileMaxNewLines = 50000  // 1回の編集で挿入できる最大行数
)

type EditFileTool struct {
	AllowedRoot string
	indexer     *symbols.Indexer
	validator   *ASTValidator
}

func (t *EditFileTool) SetIndexer(idx *symbols.Indexer) {
	t.indexer = idx
}

func (t *EditFileTool) SetValidator(v *ASTValidator) {
	t.validator = v
}

type editFileArgs struct {
	Path              string          `json:"path"`
	StartLine         int             `json:"start_line"` // 1-indexed
	EndLine           int             `json:"end_line"`   // 1-indexed, inclusive
	ExpectedStartHash string          `json:"expected_start_hash,omitempty"`
	ExpectedEndHash   string          `json:"expected_end_hash,omitempty"`
	NewLines          json.RawMessage `json:"new_lines"` // 改行を含む文字列または配列を許容するためRawMessageにする
}

func NewEditFileTool(allowedRoot string) *EditFileTool {
	return &EditFileTool{
		AllowedRoot: allowedRoot,
		validator:   NewASTValidator(),
	}
}

func (t *EditFileTool) Name() string {
	return "edit_file"
}

func (t *EditFileTool) IsMutating() bool {
	return true
}

func (t *EditFileTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        "edit_file",
			Description: "Edit a specific line range in a file. Replace lines from start_line to end_line (inclusive, 1-indexed) with new_lines. Use read_file first to see line numbers. The file is automatically backed up via shadow git.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the file (relative to workspace root)",
					},
					"start_line": map[string]interface{}{
						"type":        "integer",
						"description": "Start line number (1-indexed, inclusive)",
						"minimum":     1,
					},
					"end_line": map[string]interface{}{
						"type":        "integer",
						"description": "End line number (1-indexed, inclusive)",
						"minimum":     1,
					},
					"expected_start_hash": map[string]interface{}{
						"type":        "string",
						"description": "Optional short hash for start_line copied from read_file output, e.g. h:abcd1234. When provided, edit_file rejects the edit if the line has changed.",
					},
					"expected_end_hash": map[string]interface{}{
						"type":        "string",
						"description": "Optional short hash for end_line copied from read_file output, e.g. h:abcd1234. When provided, edit_file rejects the edit if the line has changed.",
					},
					"new_lines": map[string]interface{}{
						"type":        "array",
						"description": "Array of new lines (or a single string with newlines) to replace the range",
						"items": map[string]interface{}{
							"type": "string",
						},
					},
				},
				"required": []string{"path", "start_line", "end_line", "new_lines"},
			},
		},
	}
}

func (t *EditFileTool) Execute(ctx context.Context, rawArgs json.RawMessage) (*Result, error) {
	var args editFileArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	if err := rejectUnsafeRawNewLines(args.Path, args.NewLines); err != nil {
		return ErrorResult(err.Error()), nil
	}

	// new_lines を柔軟にパース
	newLines, err := parseNewLines(args.NewLines)
	if err != nil {
		return ErrorResult(fmt.Sprintf("invalid new_lines: %v", err)), nil
	}

	// バリデーション
	if args.Path == "" {
		return ErrorResult("path is required"), nil
	}
	if args.StartLine < 1 {
		return ErrorResult("start_line must be >= 1"), nil
	}
	if args.EndLine < args.StartLine {
		return ErrorResult(fmt.Sprintf("end_line (%d) must be >= start_line (%d)", args.EndLine, args.StartLine)), nil
	}
	if len(newLines) > EditFileMaxNewLines {
		return ErrorResult(fmt.Sprintf("too many new_lines: %d (max %d)", len(newLines), EditFileMaxNewLines)), nil
	}
	if ContainsOmittedToolArgument(newLines) {
		return ErrorResult(OmittedToolArgumentError()), nil
	}
	if err := RejectSerializedLineListForCode(args.Path, strings.Join(newLines, "\n")); err != nil {
		return ErrorResult(err.Error()), nil
	}

	// パス解決と安全性チェック
	cleanPath, err := t.resolvePath(args.Path)
	if err != nil {
		return ErrorResult(err.Error()), nil
	}
	if err := t.checkProtectedPath(cleanPath); err != nil {
		return ErrorResult(err.Error()), nil
	}

	// ファイル存在チェック
	info, err := os.Stat(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			msg := formatPathError(t.AllowedRoot, args.Path, cleanPath)
			return ErrorResult(msg + "\nHint: use write_file to create new files"), nil
		}
		return ErrorResult(fmt.Sprintf("stat error: %v", err)), nil
	}
	if info.IsDir() {
		return ErrorResult(fmt.Sprintf("path is a directory: %s", args.Path)), nil
	}

	// シンボリックリンク拒否
	linkInfo, err := os.Lstat(cleanPath)
	if err == nil && linkInfo.Mode()&os.ModeSymlink != 0 {
		return ErrorResult(fmt.Sprintf("symbolic links are not supported: %s", args.Path)), nil
	}

	// 既存ファイルを行配列として読み込み
	existingLines, err := t.readLines(cleanPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("read error: %v", err)), nil
	}

	if len(existingLines) > EditFileMaxLines {
		return ErrorResult(fmt.Sprintf("file too large: %d lines (max %d)", len(existingLines), EditFileMaxLines)), nil
	}

	// 範囲チェック
	if args.StartLine > len(existingLines) {
		return ErrorResult(fmt.Sprintf("start_line (%d) exceeds file length (%d lines)", args.StartLine, len(existingLines))), nil
	}
	if args.EndLine > len(existingLines) {
		return ErrorResult(fmt.Sprintf("end_line (%d) exceeds file length (%d lines)", args.EndLine, len(existingLines))), nil
	}
	if err := validateExpectedLineHash(args.Path, args.StartLine, existingLines[args.StartLine-1], args.ExpectedStartHash); err != nil {
		return ErrorResult(err.Error()), nil
	}
	if err := validateExpectedLineHash(args.Path, args.EndLine, existingLines[args.EndLine-1], args.ExpectedEndHash); err != nil {
		return ErrorResult(err.Error()), nil
	}

	// 新しい行配列を構築
	var newContent []string

	// 1. 編集範囲より前の行
	newContent = append(newContent, existingLines[:args.StartLine-1]...)

	// 2. 新しい行
	newContent = append(newContent, newLines...)

	// 3. 編集範囲より後の行
	newContent = append(newContent, existingLines[args.EndLine:]...)

	// ファイルに書き込み（write_fileと同じアトミック方式）
	finalContent := strings.Join(newContent, "\n")
	if !strings.HasSuffix(finalContent, "\n") && len(newContent) > 0 {
		finalContent += "\n"
	}

	bytesWritten, err := t.writeAtomic(cleanPath, finalContent)
	if err != nil {
		return ErrorResult(fmt.Sprintf("write failed: %v", err)), nil
	}

	// AST バリデーション
	var validationWarning string
	if t.validator != nil {
		if err := t.validator.Validate(cleanPath); err != nil {
			log.Printf("edit_file: syntax validation failed: %v", err)
			validationWarning = fmt.Sprintf("\n\n⚠️  Warning: %v\nReview the file content or use /rewind to undo.", err)
		}
	}

	// 書き込み成功後、インデックス更新（ベストエフォート）
	if t.indexer != nil {
		if err := t.indexer.IndexFileForce(cleanPath); err != nil {
			log.Printf("edit_file: index update failed (non-fatal): %v", err)
		}
	}

	// 結果メッセージ
	linesDeleted := args.EndLine - args.StartLine + 1
	linesInserted := len(newLines)

	msg := fmt.Sprintf(
		"Edited %s: replaced lines %d-%d (deleted %d lines, inserted %d lines, %d bytes)",
		args.Path,
		args.StartLine,
		args.EndLine,
		linesDeleted,
		linesInserted,
		bytesWritten,
	)

	if validationWarning != "" {
		msg += validationWarning
	}

	result := SuccessResult(msg)
	result.Metadata = map[string]interface{}{
		"path":           args.Path,
		"start_line":     args.StartLine,
		"end_line":       args.EndLine,
		"lines_deleted":  linesDeleted,
		"lines_inserted": linesInserted,
		"bytes_written":  bytesWritten,
		"total_lines":    len(newContent),
	}
	return result, nil
}

// readLines はファイルを行配列として読み込む
func (t *EditFileTool) readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	return lines, scanner.Err()
}

// writeAtomic はテンポラリファイル経由でアトミックに書き込む
func (t *EditFileTool) writeAtomic(path string, content string) (int, error) {
	dir := filepath.Dir(path)

	tempFile, err := os.CreateTemp(dir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return 0, fmt.Errorf("create temp file: %w", err)
	}
	tempPath := tempFile.Name()

	defer func() {
		if _, err := os.Stat(tempPath); err == nil {
			os.Remove(tempPath)
		}
	}()

	n, err := tempFile.WriteString(content)
	if err != nil {
		tempFile.Close()
		return 0, fmt.Errorf("write temp file: %w", err)
	}

	if err := tempFile.Sync(); err != nil {
		tempFile.Close()
		return 0, fmt.Errorf("sync temp file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return 0, fmt.Errorf("close temp file: %w", err)
	}

	perm := os.FileMode(0644)
	if existingInfo, err := os.Stat(path); err == nil {
		perm = existingInfo.Mode().Perm()
	}
	if err := os.Chmod(tempPath, perm); err != nil {
		return 0, fmt.Errorf("chmod temp file: %w", err)
	}

	if err := os.Rename(tempPath, path); err != nil {
		return 0, fmt.Errorf("rename: %w", err)
	}

	return n, nil
}

func (t *EditFileTool) resolvePath(path string) (string, error) {
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

	if t.AllowedRoot != "" {
		relPath, err := filepath.Rel(t.AllowedRoot, abs)
		if err != nil || strings.HasPrefix(relPath, "..") {
			// 親パスが存在しない場合に、二重化プレフィックスが原因かチェック
			wsBase := filepath.Base(t.AllowedRoot)
			if strings.HasPrefix(path, wsBase+"/") {
				suggested := strings.TrimPrefix(path, wsBase+"/")
				return "", fmt.Errorf("path outside allowed root: %s (Hint: Did you mean \"%s\"? Workspace root is %s)", path, suggested, t.AllowedRoot)
			}
			return "", fmt.Errorf("path outside allowed root: %s (Workspace root: %s)", path, t.AllowedRoot)
		}
	}

	return abs, nil
}

func (t *EditFileTool) checkProtectedPath(absPath string) error {
	relPath, err := filepath.Rel(t.AllowedRoot, absPath)
	if err != nil {
		return fmt.Errorf("path resolution failed: %v", err)
	}

	relPath = filepath.ToSlash(relPath)

	for _, protected := range protectedPaths {
		if relPath == protected || strings.HasPrefix(relPath, protected+"/") {
			return fmt.Errorf("write to protected path is not allowed: %s", protected)
		}
		if filepath.Base(relPath) == protected {
			return fmt.Errorf("write to protected file is not allowed: %s", protected)
		}
	}

	return nil
}

// parseNewLines は new_lines を文字列配列として解釈する
// 配列が来た場合: そのまま返す
// 文字列が来た場合: 改行で分割して返す（LLMの不正確な出力への対応）
func parseNewLines(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}

	// まず配列として試行
	var asArray []string
	if err := json.Unmarshal(raw, &asArray); err == nil {
		return asArray, nil
	}

	// 配列としてパース失敗 → 文字列として試行
	var asString string
	if err := json.Unmarshal(raw, &asString); err != nil {
		return nil, fmt.Errorf("new_lines must be array of strings or a string: %w", err)
	}

	if asString == "" {
		return []string{}, nil
	}

	// 文字列を改行で分割
	lines := strings.Split(asString, "\n")

	// 末尾の空行は削除（"a\nb\n" の最後の "" は除去）
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	return lines, nil
}

func rejectUnsafeRawNewLines(path string, raw json.RawMessage) error {
	var asString string
	if err := json.Unmarshal(raw, &asString); err != nil {
		return nil
	}
	if ContainsOmittedToolArgument(asString) {
		return fmt.Errorf("%s", OmittedToolArgumentError())
	}
	if err := RejectSerializedLineListForCode(path, asString); err != nil {
		return err
	}
	return nil
}
