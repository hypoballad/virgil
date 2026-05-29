package tools

import (
	"fmt"

	"github.com/hypoballad/virgil/internal/symbols"
)

// ASTValidator は編集後のファイルに対して構文チェックを行う
// Tree-sitter で対応している言語のみ検証する
type ASTValidator struct {
	extractor *symbols.Extractor
}

func NewASTValidator() *ASTValidator {
	return &ASTValidator{
		extractor: symbols.NewExtractor(),
	}
}

// Validate はファイルの構文を検証する
// 検証成功: nil
// 検証失敗: エラー（メッセージにロールバック推奨を含める）
// 非対応言語: nil（検証スキップ）
func (v *ASTValidator) Validate(filePath string) error {
	// 対応言語のみ検証
	if !symbols.IsSupportedFile(filePath) {
		// 非対応言語は検証スキップ（エラーではない）
		return nil
	}

	// Tree-sitter で抽出を試みる
	_, err := v.extractor.ExtractFromFile(filePath)
	if err != nil {
		return fmt.Errorf("syntax validation failed: %v. "+
			"The file may have syntax errors after the edit. "+
			"Consider using /rewind to roll back, or fix the syntax error",
			err)
	}

	return nil
}
