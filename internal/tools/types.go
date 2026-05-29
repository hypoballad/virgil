package tools

import (
	"context"
	"encoding/json"
)

// Tool はエージェントが呼び出せるツールのインターフェース
type Tool interface {
	// Name はツール名（LLMがtool_callで指定する識別子）
	Name() string

	// Definition はLLMに渡すツール定義（JSON Schema形式）
	Definition() ToolDefinition

	// Execute はツールを実行する
	Execute(ctx context.Context, args json.RawMessage) (*Result, error)

	// IsMutating はこのツールがファイルシステムに変更を加えるかどうかを返す
	IsMutating() bool
}

// ToolDefinition はOpenAI/Ollama互換のツール定義
type ToolDefinition struct {
	Type     string             `json:"type"` // "function"固定
	Function FunctionDefinition `json:"function"`
}

type FunctionDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"` // 英語推奨
	Parameters  map[string]interface{} `json:"parameters"`  // JSON Schema
}

// HumanMetadata は人間向けの追加情報（LLMには渡さない）
type HumanMetadata struct {
	JapaneseDescription string // ユーザーへのエスカレーション時の表示用
	Category            string // 分類（"file", "search", "shell"等）
}
