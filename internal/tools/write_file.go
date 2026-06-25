package tools

import (
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
	WriteFileMaxSize = 50 * 1024 * 1024 // 50MB
)

// WriteFileTool はファイルを書き込む
type WriteFileTool struct {
	AllowedRoot string
	indexer     *symbols.Indexer
	validator   *ASTValidator
}

func (t *WriteFileTool) SetIndexer(idx *symbols.Indexer) {
	t.indexer = idx
}

func (t *WriteFileTool) SetValidator(v *ASTValidator) {
	t.validator = v
}

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    string `json:"mode,omitempty"` // "overwrite" or "append"; empty creates new files only
}

// 保護されるパスパターン（書き込み禁止）
var protectedPaths = []string{
	".git",
	".agent",
	".env",
	".env.local",
	".env.home",
	".env.cloud",
	".env.work",
	".ssh",
	"id_rsa",
	"id_ed25519",
	".gitignore", // 慎重に編集すべきファイル
}

func NewWriteFileTool(allowedRoot string) *WriteFileTool {
	return &WriteFileTool{
		AllowedRoot: allowedRoot,
		validator:   NewASTValidator(),
	}
}

func (t *WriteFileTool) Name() string {
	return "write_file"
}

func (t *WriteFileTool) IsMutating() bool {
	return true
}

func (t *WriteFileTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        "write_file",
			Description: "Write content to a file. Creates the file if it doesn't exist. For existing files, use mode='append' to append or mode='overwrite' only when intentionally replacing the entire file. Prefer edit_file or edit_with_pattern for existing files. The file is written atomically (via temp file + rename).",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the file (relative to workspace root)",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "Content to write",
					},
					"mode": map[string]interface{}{
						"type":        "string",
						"description": "Write mode for existing files: 'append' or explicit full-file 'overwrite'. Omit for new file creation.",
						"enum":        []string{"overwrite", "append"},
					},
				},
				"required": []string{"path", "content"},
			},
		},
	}
}

func (t *WriteFileTool) Execute(ctx context.Context, rawArgs json.RawMessage) (*Result, error) {
	var args writeFileArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	if args.Path == "" {
		return ErrorResult("path is required"), nil
	}

	// サイズチェック
	if len(args.Content) > WriteFileMaxSize {
		return ErrorResult(fmt.Sprintf("content too large: %d bytes (max %d)", len(args.Content), WriteFileMaxSize)), nil
	}
	if ContainsOmittedToolArgument(args.Content) {
		return ErrorResult(OmittedToolArgumentError()), nil
	}
	if err := RejectSerializedLineListForCode(args.Path, args.Content); err != nil {
		return ErrorResult(err.Error()), nil
	}

	// パス解決と安全性チェック
	cleanPath, err := t.resolvePath(args.Path)
	if err != nil {
		return ErrorResult(err.Error()), nil
	}

	// 保護されるパスのチェック
	if err := t.checkProtectedPath(cleanPath); err != nil {
		return ErrorResult(err.Error()), nil
	}

	if args.Mode != "" && args.Mode != "overwrite" && args.Mode != "append" {
		return ErrorResult(fmt.Sprintf("invalid mode: %s (must be 'overwrite' or 'append')", args.Mode)), nil
	}

	// 既存ファイルチェック
	existingInfo, err := os.Stat(cleanPath)
	fileExists := err == nil
	isOverwrite := fileExists && args.Mode == "overwrite"

	if fileExists {
		if existingInfo.IsDir() {
			return ErrorResult(fmt.Sprintf("path is a directory: %s", args.Path)), nil
		}
		// シンボリックリンクの拒否
		linkInfo, err := os.Lstat(cleanPath)
		if err == nil && linkInfo.Mode()&os.ModeSymlink != 0 {
			return ErrorResult(fmt.Sprintf("symbolic links are not supported: %s", args.Path)), nil
		}
	}

	if fileExists && args.Content == "" {
		return ErrorResult(fmt.Sprintf(
			"refusing to write empty content to existing file: %s. "+
				"Use edit_file or edit_with_pattern for precise deletions, or retry write_file with explicit non-empty content and mode='append' or mode='overwrite'.",
			args.Path,
		)), nil
	}

	if fileExists && args.Mode == "" {
		return ErrorResult(fmt.Sprintf(
			"refusing to overwrite existing file without explicit mode: %s. "+
				"Use edit_file or edit_with_pattern for existing files. If you intended to append to the end, retry with mode='append' and the exact non-empty content. Use mode='overwrite' only to intentionally replace the entire file.",
			args.Path,
		)), nil
	}

	if !fileExists && args.Mode == "" {
		args.Mode = "overwrite"
	}

	// 親ディレクトリの作成
	parentDir := filepath.Dir(cleanPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return ErrorResult(fmt.Sprintf("failed to create parent directory: %v", err)), nil
	}

	// 書き込み実行
	var bytesWritten int
	if args.Mode == "append" && fileExists {
		bytesWritten, err = t.appendToFile(cleanPath, args.Content)
	} else {
		bytesWritten, err = t.writeAtomic(cleanPath, args.Content)
	}

	if err != nil {
		return ErrorResult(fmt.Sprintf("write failed: %v", err)), nil
	}

	// AST バリデーション
	var validationWarning string
	if t.validator != nil {
		if err := t.validator.Validate(cleanPath); err != nil {
			log.Printf("write_file: syntax validation failed: %v", err)
			validationWarning = fmt.Sprintf("\n\n⚠️  Warning: %v\nReview the file content or use /rewind to undo.", err)
		}
	}

	// 書き込み成功後、インデックス更新（ベストエフォート）
	if t.indexer != nil {
		if err := t.indexer.IndexFileForce(cleanPath); err != nil {
			log.Printf("write_file: index update failed (non-fatal): %v", err)
		}
	}

	// 成功メッセージ
	lineCount := countWrittenLines(args.Content)
	lineLabel := formatLineCount(lineCount)
	var msg string
	if args.Mode == "append" {
		msg = fmt.Sprintf("Appended %d bytes (%s) to %s", bytesWritten, lineLabel, args.Path)
	} else if isOverwrite {
		msg = fmt.Sprintf("Overwrote %s (%d bytes, %s)", args.Path, bytesWritten, lineLabel)
	} else {
		msg = fmt.Sprintf("Created %s (%d bytes, %s)", args.Path, bytesWritten, lineLabel)
	}

	if validationWarning != "" {
		msg += validationWarning
	}

	result := SuccessResult(msg)
	result.Metadata = map[string]interface{}{
		"path":          args.Path,
		"bytes_written": bytesWritten,
		"line_count":    lineCount,
		"mode":          args.Mode,
		"was_existing":  fileExists,
	}
	return result, nil
}

func countWrittenLines(content string) int {
	if content == "" {
		return 0
	}
	lines := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		lines++
	}
	return lines
}

func formatLineCount(lines int) string {
	if lines == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", lines)
}

// writeAtomic はテンポラリファイル経由でアトミックに書き込む
func (t *WriteFileTool) writeAtomic(path string, content string) (int, error) {
	dir := filepath.Dir(path)

	// 一時ファイル作成
	tempFile, err := os.CreateTemp(dir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return 0, fmt.Errorf("create temp file: %w", err)
	}
	tempPath := tempFile.Name()

	// クリーンアップを設定（rename成功時には不要だが、失敗時に削除）
	defer func() {
		if _, err := os.Stat(tempPath); err == nil {
			os.Remove(tempPath)
		}
	}()

	// 書き込み
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

	// パーミッション設定（既存ファイルがあればそれに合わせる、なければ0644）
	perm := os.FileMode(0644)
	if existingInfo, err := os.Stat(path); err == nil {
		perm = existingInfo.Mode().Perm()
	}
	if err := os.Chmod(tempPath, perm); err != nil {
		return 0, fmt.Errorf("chmod temp file: %w", err)
	}

	// アトミックなrename
	if err := os.Rename(tempPath, path); err != nil {
		return 0, fmt.Errorf("rename: %w", err)
	}

	return n, nil
}

// appendToFile はファイルに追記
func (t *WriteFileTool) appendToFile(path string, content string) (int, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return 0, fmt.Errorf("open for append: %w", err)
	}
	defer f.Close()

	n, err := f.WriteString(content)
	if err != nil {
		return 0, fmt.Errorf("write: %w", err)
	}

	return n, nil
}

// checkProtectedPath は保護されるパスかチェック
func (t *WriteFileTool) checkProtectedPath(absPath string) error {
	relPath, err := filepath.Rel(t.AllowedRoot, absPath)
	if err != nil {
		return fmt.Errorf("path resolution failed: %v", err)
	}

	relPath = filepath.ToSlash(relPath)

	for _, protected := range protectedPaths {
		if relPath == protected || strings.HasPrefix(relPath, protected+"/") {
			return fmt.Errorf("write to protected path is not allowed: %s", protected)
		}
		// パスの最後の要素のみ一致するケース
		if filepath.Base(relPath) == protected {
			return fmt.Errorf("write to protected file is not allowed: %s", protected)
		}
	}

	return nil
}

// resolvePath はパスを解決し、AllowedRoot配下にあることを確認
func (t *WriteFileTool) resolvePath(path string) (string, error) {
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
