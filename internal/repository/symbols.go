package repository

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/hypoballad/virgil/internal/db"
	"github.com/hypoballad/virgil/internal/symbols"
)

// SymbolRecord はDBから読み出されたシンボルレコード
type SymbolRecord struct {
	ID         int64
	FilePath   string
	Name       string
	Type       string
	Receiver   string
	Signature  string
	Doc        string
	StartLine  int
	EndLine    int
	Language   string
	FileMtime  int64
	IndexedAt  int64
	IsFallback bool
}

// SymbolRepository はシンボルテーブルの操作を提供する
type SymbolRepository struct {
	db *db.DB
}

type SymbolSearchOptions struct {
	Pattern      string
	SymbolType   string
	Receiver     string
	FilePath     string
	FallbackOnly bool
	Limit        int
}

func NewSymbolRepository(database *db.DB) *SymbolRepository {
	return &SymbolRepository{db: database}
}

// UpsertFile は1ファイル分のシンボルを置き換える
// 既存のレコードを削除してから新しいレコードを INSERT する（トランザクション内）
func (r *SymbolRepository) UpsertFile(filePath string, outline *symbols.FileOutline, fileMtime int64) error {
	tx, err := r.db.SqlDB.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// 既存のレコードを削除
	if _, err := tx.Exec("DELETE FROM codebase_symbols WHERE file_path = ?", filePath); err != nil {
		return fmt.Errorf("delete old symbols: %w", err)
	}

	// 新しいレコードを挿入
	now := time.Now().Unix()
	stmt, err := tx.Prepare(`
		INSERT INTO codebase_symbols
		(file_path, symbol_name, symbol_type, receiver, signature, doc,
		 start_line, end_line, language, file_mtime, indexed_at, is_fallback)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, sym := range outline.Symbols {
		_, err := stmt.Exec(
			filePath, sym.Name, string(sym.Type), sym.Receiver, sym.Signature, sym.Doc,
			sym.StartLine, sym.EndLine, outline.Language, fileMtime, now, sym.IsFallback,
		)
		if err != nil {
			return fmt.Errorf("insert symbol %q: %w", sym.Name, err)
		}
	}

	return tx.Commit()
}

// DeleteByFilePath は指定ファイルのシンボルを全削除する
// ファイル削除時に使用
func (r *SymbolRepository) DeleteByFilePath(filePath string) error {
	_, err := r.db.SqlDB.Exec("DELETE FROM codebase_symbols WHERE file_path = ?", filePath)
	return err
}

// GetFileMtime は指定ファイルの記録されている mtime を返す
// 差分更新の判定に使用。レコードがなければ 0 を返す
func (r *SymbolRepository) GetFileMtime(filePath string) (int64, error) {
	var mtime int64
	err := r.db.SqlDB.QueryRow(
		"SELECT file_mtime FROM codebase_symbols WHERE file_path = ? LIMIT 1",
		filePath,
	).Scan(&mtime)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return mtime, err
}

// GetIndexVersion は保存済みのシンボルインデックス仕様バージョンを返す。
func (r *SymbolRepository) GetIndexVersion() (string, error) {
	var version string
	err := r.db.SqlDB.QueryRow(
		"SELECT value FROM index_metadata WHERE key = ?",
		"symbols.index_version",
	).Scan(&version)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return version, err
}

// SetIndexVersion はシンボルインデックス仕様バージョンを保存する。
func (r *SymbolRepository) SetIndexVersion(version string) error {
	_, err := r.db.SqlDB.Exec(`
		INSERT INTO index_metadata (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at
	`, "symbols.index_version", version, time.Now().Unix())
	return err
}

// FindByName は名前でシンボルを検索する
// pattern: '%' を含む LIKE パターン、または完全一致
// symbolType: 空文字なら全種別、それ以外は種別フィルタ
// limit: 結果の最大件数
func (r *SymbolRepository) FindByName(pattern string, symbolType string, limit int) ([]SymbolRecord, error) {
	return r.FindSymbols(SymbolSearchOptions{
		Pattern:    pattern,
		SymbolType: symbolType,
		Limit:      limit,
	})
}

// FindSymbols は名前・種類・receiver・ファイルパス・fallback でシンボルを検索する。
func (r *SymbolRepository) FindSymbols(opts SymbolSearchOptions) ([]SymbolRecord, error) {
	var query strings.Builder
	args := []interface{}{}

	query.WriteString(`
		SELECT id, file_path, symbol_name, symbol_type, receiver, signature, doc,
		       start_line, end_line, language, file_mtime, indexed_at, is_fallback
		FROM codebase_symbols
		WHERE symbol_name LIKE ?
	`)
	args = append(args, opts.Pattern)

	if opts.SymbolType != "" {
		query.WriteString(" AND symbol_type = ?")
		args = append(args, opts.SymbolType)
	}
	if opts.Receiver != "" {
		query.WriteString(" AND receiver = ?")
		args = append(args, opts.Receiver)
	}
	if opts.FilePath != "" {
		query.WriteString(" AND file_path LIKE ?")
		args = append(args, "%"+opts.FilePath+"%")
	}
	if opts.FallbackOnly {
		query.WriteString(" AND is_fallback = 1")
	}

	query.WriteString(" ORDER BY file_path, start_line LIMIT ?")
	args = append(args, opts.Limit)

	rows, err := r.db.SqlDB.Query(query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SymbolRecord
	for rows.Next() {
		var rec SymbolRecord
		var receiver, signature, doc sql.NullString
		err := rows.Scan(
			&rec.ID, &rec.FilePath, &rec.Name, &rec.Type,
			&receiver, &signature, &doc,
			&rec.StartLine, &rec.EndLine, &rec.Language,
			&rec.FileMtime, &rec.IndexedAt, &rec.IsFallback,
		)
		if err != nil {
			return nil, err
		}
		rec.Receiver = receiver.String
		rec.Signature = signature.String
		rec.Doc = doc.String
		results = append(results, rec)
	}

	return results, rows.Err()
}

// CountAll はインデックスされている総シンボル数を返す（デバッグ・統計用）
func (r *SymbolRepository) CountAll() (int, error) {
	var count int
	err := r.db.SqlDB.QueryRow("SELECT COUNT(*) FROM codebase_symbols").Scan(&count)
	return count, err
}
