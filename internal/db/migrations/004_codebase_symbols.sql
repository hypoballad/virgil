CREATE TABLE IF NOT EXISTS codebase_symbols (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_path TEXT NOT NULL,
    symbol_name TEXT NOT NULL,
    symbol_type TEXT NOT NULL,
    receiver TEXT,
    signature TEXT,
    start_line INTEGER NOT NULL,
    end_line INTEGER NOT NULL,
    language TEXT NOT NULL,
    file_mtime INTEGER NOT NULL,
    indexed_at INTEGER NOT NULL,
    UNIQUE(file_path, symbol_name, start_line)
);

CREATE INDEX IF NOT EXISTS idx_symbols_path ON codebase_symbols(file_path);
CREATE INDEX IF NOT EXISTS idx_symbols_name ON codebase_symbols(symbol_name);
CREATE INDEX IF NOT EXISTS idx_symbols_type ON codebase_symbols(symbol_type);
CREATE INDEX IF NOT EXISTS idx_symbols_mtime ON codebase_symbols(file_path, file_mtime);
