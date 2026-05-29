CREATE TABLE IF NOT EXISTS codebase_imports (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_path TEXT NOT NULL,
    line_number INTEGER NOT NULL,
    import_kind TEXT NOT NULL,
    module TEXT NOT NULL,
    imported_name TEXT,
    alias TEXT,
    is_relative BOOLEAN NOT NULL DEFAULT 0,
    relative_level INTEGER NOT NULL DEFAULT 0,
    is_wildcard BOOLEAN NOT NULL DEFAULT 0,
    scope TEXT NOT NULL DEFAULT 'module',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_codebase_imports_file ON codebase_imports(file_path);
CREATE INDEX IF NOT EXISTS idx_codebase_imports_module ON codebase_imports(module);
