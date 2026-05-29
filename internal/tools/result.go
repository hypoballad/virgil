package tools

// Result はツール実行の結果
type Result struct {
	// Content はLLMに返す主たる結果
	Content string

	// IsError はエラーが発生したかどうか
	// trueの場合、Contentにエラー説明が入る
	IsError bool

	// Metadata は追加情報（ログや履歴記録用）
	Metadata map[string]interface{}
}

// SuccessResult は成功時の結果を作る
func SuccessResult(content string) *Result {
	return &Result{
		Content: content,
		IsError: false,
	}
}

// ErrorResult はエラー時の結果を作る
func ErrorResult(message string) *Result {
	return &Result{
		Content: message,
		IsError: true,
	}
}
