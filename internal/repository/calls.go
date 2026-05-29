package repository

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/hypoballad/virgil/internal/db"
	"github.com/hypoballad/virgil/internal/symbols"
)

// CallRecord はDBから読み出された呼び出しレコード
type CallRecord struct {
	ID             int64
	CallerFile     string
	CallerName     string
	CallerReceiver string
	CalleeName     string
	CalleeReceiver string
	CallLine       int
	Language       string
	IndexedAt      int64
}

// CallRepository は呼び出しテーブルの操作を提供する
type CallRepository struct {
	db *db.DB
}

func NewCallRepository(database *db.DB) *CallRepository {
	return &CallRepository{db: database}
}

// UpsertFile は1ファイル分の呼び出しを置き換える
func (r *CallRepository) UpsertFile(filePath string, graph *symbols.FileCallGraph) error {
	tx, err := r.db.SqlDB.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM codebase_calls WHERE caller_file = ?", filePath); err != nil {
		return fmt.Errorf("delete old calls: %w", err)
	}

	now := time.Now().Unix()
	stmt, err := tx.Prepare(`
		INSERT INTO codebase_calls
		(caller_file, caller_name, caller_receiver, callee_name, callee_receiver,
		 call_line, language, indexed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, c := range graph.Calls {
		_, err := stmt.Exec(
			filePath, c.CallerName, c.CallerReceiver,
			c.CalleeName, c.CalleeReceiver,
			c.CallLine, graph.Language, now,
		)
		if err != nil {
			return fmt.Errorf("insert call: %w", err)
		}
	}

	return tx.Commit()
}

// DeleteByFilePath は指定ファイルの呼び出しを全削除する
func (r *CallRepository) DeleteByFilePath(filePath string) error {
	_, err := r.db.SqlDB.Exec("DELETE FROM codebase_calls WHERE caller_file = ?", filePath)
	return err
}

// FindOutgoing は指定された関数からの呼び出し（呼び出し先）を返す。
// name で検索し、receiver は空文字なら不問。
func (r *CallRepository) FindOutgoing(callerName, callerReceiver string, limit int) ([]CallRecord, error) {
	query := `
		SELECT id, caller_file, caller_name, caller_receiver,
		       callee_name, callee_receiver, call_line, language, indexed_at
		FROM codebase_calls
		WHERE caller_name = ?
	`
	args := []interface{}{callerName}

	if callerReceiver != "" {
		query += " AND caller_receiver = ?"
		args = append(args, callerReceiver)
	}

	query += " ORDER BY caller_file, call_line LIMIT ?"
	args = append(args, limit)

	return r.queryCallRecords(query, args...)
}

// FindIncoming は指定された関数への呼び出し（呼び出し元）を返す。
func (r *CallRepository) FindIncoming(calleeName string, limit int) ([]CallRecord, error) {
	query := `
		SELECT id, caller_file, caller_name, caller_receiver,
		       callee_name, callee_receiver, call_line, language, indexed_at
		FROM codebase_calls
		WHERE callee_name = ?
		ORDER BY caller_file, call_line
		LIMIT ?
	`
	return r.queryCallRecords(query, calleeName, limit)
}

func (r *CallRepository) queryCallRecords(query string, args ...interface{}) ([]CallRecord, error) {
	rows, err := r.db.SqlDB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []CallRecord
	for rows.Next() {
		var rec CallRecord
		var callerReceiver, calleeReceiver sql.NullString
		err := rows.Scan(
			&rec.ID, &rec.CallerFile, &rec.CallerName, &callerReceiver,
			&rec.CalleeName, &calleeReceiver,
			&rec.CallLine, &rec.Language, &rec.IndexedAt,
		)
		if err != nil {
			return nil, err
		}
		rec.CallerReceiver = callerReceiver.String
		rec.CalleeReceiver = calleeReceiver.String
		results = append(results, rec)
	}
	return results, rows.Err()
}

// CountAll はインデックスされている総呼び出し数を返す（デバッグ用）
func (r *CallRepository) CountAll() (int, error) {
	var count int
	err := r.db.SqlDB.QueryRow("SELECT COUNT(*) FROM codebase_calls").Scan(&count)
	return count, err
}
