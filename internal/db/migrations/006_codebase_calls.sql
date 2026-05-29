CREATE TABLE IF NOT EXISTS codebase_calls (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    caller_file TEXT NOT NULL,
    caller_name TEXT NOT NULL,
    caller_receiver TEXT,
    callee_name TEXT NOT NULL,
    callee_receiver TEXT,
    call_line INTEGER NOT NULL,
    language TEXT NOT NULL,
    indexed_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_calls_caller_file ON codebase_calls(caller_file);
CREATE INDEX IF NOT EXISTS idx_calls_caller_name ON codebase_calls(caller_name);
CREATE INDEX IF NOT EXISTS idx_calls_callee_name ON codebase_calls(callee_name);
