-- llm_exchanges: 各LLM呼び出しのリクエスト/レスポンス全文を記録
CREATE TABLE llm_exchanges (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    turn_id INTEGER NOT NULL,
    iteration INTEGER NOT NULL,           -- このturn内の何回目のLLM呼び出しか（0始まり）

    -- リクエスト
    request_messages TEXT NOT NULL,        -- JSON: messages配列の全文
    request_tools TEXT,                    -- JSON: tools定義（nullならツールなし）
    request_format TEXT,                   -- JSON: formatオプション（nullなら未指定）

    -- レスポンス
    response_content TEXT,                 -- LLMの応答テキスト
    response_tool_calls TEXT,             -- JSON: tool_calls配列（nullならツール呼び出しなし）

    -- メタデータ
    prompt_tokens INTEGER,
    completion_tokens INTEGER,
    duration_ms INTEGER,
    created_at INTEGER NOT NULL,          -- Unix timestamp

    FOREIGN KEY (turn_id) REFERENCES turns(id)
);

CREATE INDEX idx_llm_exchanges_turn ON llm_exchanges(turn_id, iteration);
