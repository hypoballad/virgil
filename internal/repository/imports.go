package repository

import (
	"database/sql"
	"fmt"

	"github.com/hypoballad/virgil/internal/db"
	"github.com/hypoballad/virgil/internal/symbols"
)

// ImportRecord はDBから読み出された import レコード。
type ImportRecord struct {
	ID            int64
	FilePath      string
	LineNumber    int
	Kind          string
	Module        string
	ImportedName  string
	Alias         string
	IsRelative    bool
	RelativeLevel int
	IsWildcard    bool
	Scope         string
}

// DependentEntry は指定モジュールを import している箇所を表す。
type DependentEntry struct {
	FilePath      string
	LineNumber    int
	ImportKind    string
	Module        string
	ImportedName  string
	Alias         string
	IsRelative    bool
	RelativeLevel int
	IsWildcard    bool
	Scope         string
}

type DependentSearchOptions struct {
	Module          string
	IncludeRelative bool
	ExactModule     bool
	ImportKind      string
	ImportedName    string
	Alias           string
	FilePath        string
	Scope           string
	WildcardOnly    bool
	MaxResults      int
}

// ImportRepository は codebase_imports テーブルの操作を提供する。
type ImportRepository struct {
	db *db.DB
}

func NewImportRepository(database *db.DB) *ImportRepository {
	return &ImportRepository{db: database}
}

// UpsertFile は1ファイル分の import を置き換える。
func (r *ImportRepository) UpsertFile(filePath string, imports *symbols.FileImports) error {
	tx, err := r.db.SqlDB.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM codebase_imports WHERE file_path = ?", filePath); err != nil {
		return fmt.Errorf("delete old imports: %w", err)
	}

	stmt, err := tx.Prepare(`
		INSERT INTO codebase_imports
		(file_path, line_number, import_kind, module, imported_name, alias,
		 is_relative, relative_level, is_wildcard, scope)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	if imports != nil {
		for _, imp := range imports.Imports {
			_, err := stmt.Exec(
				filePath, imp.LineNumber, imp.Kind, imp.Module,
				nullString(imp.ImportedName), nullString(imp.Alias),
				imp.IsRelative, imp.RelativeLevel, imp.IsWildcard, imp.Scope,
			)
			if err != nil {
				return fmt.Errorf("insert import: %w", err)
			}
		}
	}

	return tx.Commit()
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// DeleteByFilePath は指定ファイルの import を全削除する。
func (r *ImportRepository) DeleteByFilePath(filePath string) error {
	_, err := r.db.SqlDB.Exec("DELETE FROM codebase_imports WHERE file_path = ?", filePath)
	return err
}

// ListByFilePath は指定ファイルの import 一覧を返す。
func (r *ImportRepository) ListByFilePath(filePath string) ([]ImportRecord, error) {
	rows, err := r.db.SqlDB.Query(`
		SELECT id, file_path, line_number, import_kind, module, imported_name, alias,
		       is_relative, relative_level, is_wildcard, scope
		FROM codebase_imports
		WHERE file_path = ?
		ORDER BY line_number, id
	`, filePath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []ImportRecord
	for rows.Next() {
		var rec ImportRecord
		var importedName, alias sql.NullString
		err := rows.Scan(
			&rec.ID, &rec.FilePath, &rec.LineNumber, &rec.Kind, &rec.Module,
			&importedName, &alias, &rec.IsRelative, &rec.RelativeLevel,
			&rec.IsWildcard, &rec.Scope,
		)
		if err != nil {
			return nil, err
		}
		rec.ImportedName = importedName.String
		rec.Alias = alias.String
		records = append(records, rec)
	}
	return records, rows.Err()
}

// FindDependents は指定モジュールを import している箇所を返す。
// module は完全一致、またはドット区切りの接頭辞一致で検索する。
func (r *ImportRepository) FindDependents(module string, includeRelative bool, maxResults int) ([]DependentEntry, error) {
	return r.FindDependentsWithOptions(DependentSearchOptions{
		Module:          module,
		IncludeRelative: includeRelative,
		MaxResults:      maxResults,
	})
}

// FindDependentsWithOptions は指定モジュールを import している箇所を追加条件付きで返す。
func (r *ImportRepository) FindDependentsWithOptions(opts DependentSearchOptions) ([]DependentEntry, error) {
	query := `
		SELECT file_path, line_number, import_kind, module, imported_name, alias,
		       is_relative, relative_level, is_wildcard, scope
		FROM codebase_imports
	`
	args := []interface{}{}
	if opts.ExactModule {
		query += " WHERE module = ?"
		args = append(args, opts.Module)
	} else {
		query += " WHERE (module = ? OR module LIKE ?)"
		args = append(args, opts.Module)
		args = append(args, opts.Module+".%")
	}
	if !opts.IncludeRelative {
		query += " AND is_relative = 0"
	}
	if opts.ImportKind != "" {
		query += " AND import_kind = ?"
		args = append(args, opts.ImportKind)
	}
	if opts.ImportedName != "" {
		query += " AND imported_name = ?"
		args = append(args, opts.ImportedName)
	}
	if opts.Alias != "" {
		query += " AND alias = ?"
		args = append(args, opts.Alias)
	}
	if opts.FilePath != "" {
		query += " AND file_path LIKE ?"
		args = append(args, "%"+opts.FilePath+"%")
	}
	if opts.Scope != "" {
		query += " AND scope = ?"
		args = append(args, opts.Scope)
	}
	if opts.WildcardOnly {
		query += " AND is_wildcard = 1"
	}
	query += " ORDER BY file_path, line_number, id"
	if opts.MaxResults > 0 {
		query += " LIMIT ?"
		args = append(args, opts.MaxResults)
	}

	rows, err := r.db.SqlDB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []DependentEntry
	for rows.Next() {
		var entry DependentEntry
		var importedName, alias sql.NullString
		err := rows.Scan(
			&entry.FilePath, &entry.LineNumber, &entry.ImportKind, &entry.Module,
			&importedName, &alias, &entry.IsRelative, &entry.RelativeLevel,
			&entry.IsWildcard, &entry.Scope,
		)
		if err != nil {
			return nil, err
		}
		entry.ImportedName = importedName.String
		entry.Alias = alias.String
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// CountAll はインデックスされている import 総数を返す。
func (r *ImportRepository) CountAll() (int, error) {
	var count int
	err := r.db.SqlDB.QueryRow("SELECT COUNT(*) FROM codebase_imports").Scan(&count)
	return count, err
}
