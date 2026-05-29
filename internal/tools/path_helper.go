package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// formatPathError はパスが見つからない場合の改善されたエラーメッセージを生成する
// workspaceRoot: ツールに設定されたワークスペースルート
// requestedPath: LLMが指定したパス
// resolvedPath: 実際に解決されたフルパス
func formatPathError(workspaceRoot, requestedPath, resolvedPath string) string {
	msg := fmt.Sprintf("file not found: %s\nResolved to: %s\nWorkspace root: %s\n",
		requestedPath, resolvedPath, workspaceRoot)

	// ワークスペースのディレクトリ名がパスの先頭にある場合、除去した候補を提案
	wsBase := filepath.Base(workspaceRoot)
	if strings.HasPrefix(requestedPath, wsBase+"/") {
		suggested := strings.TrimPrefix(requestedPath, wsBase+"/")
		suggestedFull := filepath.Join(workspaceRoot, suggested)
		if _, err := os.Stat(suggestedFull); err == nil {
			msg += fmt.Sprintf("Hint: Did you mean \"%s\"? (without the \"%s/\" prefix)", suggested, wsBase)
		} else {
			msg += fmt.Sprintf("Hint: Try removing the \"%s/\" prefix. Use paths relative to the workspace root.", wsBase)
		}
	} else {
		msg += "Hint: Use paths relative to the workspace root."
	}

	return msg
}

// truncateSignature は長すぎるシグネチャを省略し、Markdownテーブル用にエスケープする
func truncateSignature(sig string, maxLen int) string {
	// Markdown テーブル内でパイプ記号があると壊れるのでエスケープ
	sig = strings.ReplaceAll(sig, "|", "\\|")
	// バッククォートもエスケープ（コードブロック内なので）
	sig = strings.ReplaceAll(sig, "`", "'")

	if len(sig) <= maxLen {
		return sig
	}
	const suffix = "... [truncated; use read_symbol]"
	if maxLen <= len(suffix) {
		return suffix[:maxLen]
	}
	return sig[:maxLen-len(suffix)] + suffix
}
