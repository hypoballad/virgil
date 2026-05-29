-- sessions: 1コーディングタスク = 1セッション
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,              -- UUID
    started_at INTEGER NOT NULL,      -- Unix timestamp (seconds)
    ended_at INTEGER,
    project_path TEXT,
    task_description TEXT,
    status TEXT NOT NULL DEFAULT 'running',  -- running/completed/aborted
    model TEXT NOT NULL,
    metadata TEXT                     -- JSON
);

CREATE INDEX idx_sessions_started ON sessions(started_at DESC);

-- turns: LLMとの1往復
CREATE TABLE turns (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    turn_number INTEGER NOT NULL,
    started_at INTEGER NOT NULL,
    duration_ms INTEGER,
    
    -- 入力
    user_message TEXT,
    
    -- 出力
    assistant_message TEXT,
    finish_reason TEXT,
    
    -- メタデータ
    prompt_tokens INTEGER,
    completion_tokens INTEGER,
    error TEXT,
    
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE INDEX idx_turns_session ON turns(session_id, turn_number);

-- tool_calls: ツール呼び出し記録
CREATE TABLE tool_calls (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    turn_id INTEGER NOT NULL,
    tool_name TEXT NOT NULL,
    arguments TEXT NOT NULL,         -- JSON
    result TEXT,
    error TEXT,
    duration_ms INTEGER,
    pre_commit TEXT,                 -- shadow git用
    post_commit TEXT,                -- shadow git用
    FOREIGN KEY (turn_id) REFERENCES turns(id)
);

CREATE INDEX idx_tool_calls_turn ON tool_calls(turn_id);
