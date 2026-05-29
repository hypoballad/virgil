package agent

// ProgressEventType は進捗イベントの種別
type ProgressEventType string

const (
	// EventTokenUpdate はLLM呼び出し後のトークン数更新
	EventTokenUpdate ProgressEventType = "token_update"

	// run_command の確認待ち通知
	EventRunCommandConfirmNeeded ProgressEventType = "run_command_confirm_needed"

	// EventPartialResponse はLLM生成中のテキストの断片（ストリーミング用）
	EventPartialResponse ProgressEventType = "partial_response"

	// EventAgentActivity はAgentの内部的な活動（ログ相当）の通知
	EventAgentActivity ProgressEventType = "agent_activity"
)

// ProgressEvent は Agent から TUI への進捗通知
// 全フィールドは optional。Type に応じて関連するフィールドのみ使用される。
type ProgressEvent struct {
	Type ProgressEventType

	// EventPartialResponse 用
	PartialContent string

	// EventAgentActivity 用
	ActivityMessage string

	// EventTokenUpdate 用
	PromptTokens     int
	CompletionTokens int
	Iteration        int

	// EventRunCommandConfirmNeeded 用
	PendingCommand string
	PendingDir     string
}
