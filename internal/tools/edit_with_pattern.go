package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/hypoballad/virgil/internal/shadow"
	"github.com/hypoballad/virgil/internal/symbols"
)

// EditWithPatternTool は文字列パターンの厳密マッチで安全に編集するツール
//
// find_text がファイル内に「ちょうど1回」出現する場合のみ置換する。
// 0回または2回以上ならエラー。
// 行番号に依存せず、文字列の一意性で位置を特定する。
type EditWithPatternTool struct {
	workspaceRoot string
	shadow        *shadow.ShadowRepo
	validator     *ASTValidator
	indexer       *symbols.Indexer // nullable
}

func NewEditWithPatternTool(workspaceRoot string) *EditWithPatternTool {
	return &EditWithPatternTool{
		workspaceRoot: workspaceRoot,
		validator:     NewASTValidator(),
	}
}

func (t *EditWithPatternTool) SetShadowRepo(repo *shadow.ShadowRepo) {
	t.shadow = repo
}

func (t *EditWithPatternTool) SetIndexer(idx *symbols.Indexer) {
	t.indexer = idx
}

func (t *EditWithPatternTool) Name() string {
	return "edit_with_pattern"
}

func (t *EditWithPatternTool) Description() string {
	return "Edit a file by finding and replacing a UNIQUE text pattern. " +
		"This is the PREFERRED edit method — safer than edit_file (no line number dependency) and more precise than write_file (no risk of accidental changes). " +
		"find_text must appear EXACTLY ONCE in the file. Include surrounding context to ensure uniqueness. " +
		"Returns error if find_text is not found or appears multiple times. " +
		"Automatically validates syntax after edit (for Go/Python files)."
}

func (t *EditWithPatternTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		},
	}
}

func (t *EditWithPatternTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to the file (relative to workspace root)",
			},
			"find_text": map[string]interface{}{
				"type":        "string",
				"description": "Text to find. Must be UNIQUE in the file. Include surrounding context if needed (e.g., the entire line or function signature) to ensure uniqueness.",
			},
			"replace_with": map[string]interface{}{
				"type":        "string",
				"description": "Text to replace find_text with. Can be empty string to delete the matched text.",
			},
		},
		"required": []string{"path", "find_text", "replace_with"},
	}
}

func (t *EditWithPatternTool) IsMutating() bool {
	return true
}

type editWithPatternArgs struct {
	Path        string `json:"path"`
	FindText    string `json:"find_text"`
	ReplaceWith string `json:"replace_with"`
}

func (t *EditWithPatternTool) Execute(ctx context.Context, argsJSON json.RawMessage) (*Result, error) {
	var args editWithPatternArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	if strings.TrimSpace(args.Path) == "" {
		return ErrorResult("path is required"), nil
	}
	if args.FindText == "" {
		return ErrorResult("find_text cannot be empty"), nil
	}
	if ContainsOmittedToolArgument(args.FindText) || ContainsOmittedToolArgument(args.ReplaceWith) {
		return ErrorResult(OmittedToolArgumentError()), nil
	}
	if err := RejectSerializedLineListForCode(args.Path, args.ReplaceWith); err != nil {
		return ErrorResult(err.Error()), nil
	}

	// パス解決
	resolvedPath := args.Path
	if !filepath.IsAbs(resolvedPath) {
		resolvedPath = filepath.Join(t.workspaceRoot, args.Path)
	}

	// ファイル存在確認
	src, err := os.ReadFile(resolvedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrorResult(formatPathError(t.workspaceRoot, args.Path, resolvedPath)), nil
		}
		return ErrorResult(fmt.Sprintf("failed to read file: %v", err)), nil
	}

	content := string(src)

	// find_text の出現回数チェック
	count := strings.Count(content, args.FindText)
	if count == 0 {
		// 見つからない場合、デバッグヒント
		hint := buildNotFoundHint(content, args.FindText)
		return ErrorResult(fmt.Sprintf(
			"find_text not found in %s.\n\n%s\n\n"+
				"Tip: Use read_file to see the exact content, then copy the text precisely (including whitespace).",
			args.Path, hint,
		)), nil
	}
	if count > 1 {
		return ErrorResult(fmt.Sprintf(
			"find_text appears %d times in %s, must be UNIQUE. "+
				"Include more surrounding context (the entire line, function signature, or nearby comment) to make it unique.",
			count, args.Path,
		)), nil
	}

	// 置換実行
	newContent := strings.Replace(content, args.FindText, args.ReplaceWith, 1)

	// 書き込み
	if err := os.WriteFile(resolvedPath, []byte(newContent), 0644); err != nil {
		return ErrorResult(fmt.Sprintf("failed to write file: %v", err)), nil
	}

	// AST バリデーション
	if err := t.validator.Validate(resolvedPath); err != nil {
		log.Printf("edit_with_pattern: syntax validation failed: %v", err)
		// 警告として返すが、書き込み自体は完了している
		// ユーザーが /rewind で戻せる
		return &Result{
			IsError: false, // 編集自体は成功、警告のみ
			Content: fmt.Sprintf(
				"Edit applied to %s (1 replacement)\n\n"+
					"⚠️  Warning: %v\n"+
					"The file may have syntax errors. Review with read_file or use /rewind to undo.",
				args.Path, err),
		}, nil
	}

	// インデックス更新（ベストエフォート）
	if t.indexer != nil {
		if err := t.indexer.IndexFileForce(resolvedPath); err != nil {
			log.Printf("edit_with_pattern: index update failed (non-fatal): %v", err)
		}
	}

	return &Result{
		IsError: false,
		Content: fmt.Sprintf("Edit applied to %s (1 replacement, syntax validated)", args.Path),
	}, nil
}

// buildNotFoundHint は find_text が見つからない時のデバッグヒントを返す
// 類似する行を検索して提案する
func buildNotFoundHint(content, findText string) string {
	// find_text の最初の行を取得
	firstLine := strings.SplitN(findText, "\n", 2)[0]
	firstLine = strings.TrimSpace(firstLine)

	if len(firstLine) < 5 {
		return "(no hint available, find_text too short)"
	}

	// 短すぎる場合は最初の20文字に切る
	if len(firstLine) > 30 {
		firstLine = firstLine[:30]
	}

	// ファイル内に部分的に一致する行があるか
	lines := strings.Split(content, "\n")
	var matches []string
	for i, line := range lines {
		if strings.Contains(line, firstLine) {
			matches = append(matches, fmt.Sprintf("  Line %d: %s", i+1, strings.TrimSpace(line)))
			if len(matches) >= 3 {
				break
			}
		}
	}

	if len(matches) == 0 {
		return "Hint: No similar lines found. Use read_file to see actual content."
	}

	return "Hint: Found similar lines (partial match for first line of find_text):\n" +
		strings.Join(matches, "\n")
}
