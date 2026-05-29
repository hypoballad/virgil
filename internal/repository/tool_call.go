package repository

import (
	"database/sql"
	"fmt"

	"github.com/hypoballad/virgil/internal/db"
)

// ToolCallRecord はDB記録用の構造体
type ToolCallRecord struct {
	ID         int64
	TurnID     int64
	ToolCallID string // Agent側で生成したUUID
	ToolName   string
	Arguments  string // JSON
	Result     string // JSON or text
	IsError    bool
	Error      string // エラーメッセージ
	DurationMs int64
	PreCommit  string // フェーズ2用、現在は空
	PostCommit string // フェーズ2用、現在は空
}

// 結果保存サイズの上限
const MaxResultSize = 1 * 1024 * 1024 // 1MB

type ToolCallRepository struct {
	db *db.DB
}

func NewToolCallRepository(database *db.DB) *ToolCallRepository {
	return &ToolCallRepository{db: database}
}

// Create はツール呼び出しを記録
func (r *ToolCallRepository) Create(record ToolCallRecord) (*ToolCallRecord, error) {
	// 結果サイズの制限
	result := record.Result
	if len(result) > MaxResultSize {
		result = fmt.Sprintf(
			"[Result truncated, original size: %d bytes]\n%s",
			len(record.Result),
			result[:MaxResultSize-200],
		)
	}

	query := `
        INSERT INTO tool_calls 
        (turn_id, tool_call_id, tool_name, arguments, result, error, duration_ms, pre_commit, post_commit)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
    `

	res, err := r.db.SqlDB.Exec(
		query,
		record.TurnID,
		record.ToolCallID,
		record.ToolName,
		record.Arguments,
		result,
		record.Error,
		record.DurationMs,
		record.PreCommit,
		record.PostCommit,
	)

	if err != nil {
		return nil, fmt.Errorf("insert tool_call: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("get last insert id: %w", err)
	}

	record.ID = id
	record.Result = result
	return &record, nil
}

// ListByTurn はターンに属するツール呼び出しを取得
func (r *ToolCallRepository) ListByTurn(turnID int64) ([]*ToolCallRecord, error) {
	query := `
        SELECT id, turn_id, tool_name, arguments, result, error, duration_ms, pre_commit, post_commit
        FROM tool_calls
        WHERE turn_id = ?
        ORDER BY id ASC
    `

	rows, err := r.db.SqlDB.Query(query, turnID)
	if err != nil {
		return nil, fmt.Errorf("query tool_calls: %w", err)
	}
	defer rows.Close()

	var records []*ToolCallRecord
	for rows.Next() {
		var rec ToolCallRecord
		var errMsg sql.NullString
		var preCommit, postCommit sql.NullString

		err := rows.Scan(
			&rec.ID,
			&rec.TurnID,
			&rec.ToolName,
			&rec.Arguments,
			&rec.Result,
			&errMsg,
			&rec.DurationMs,
			&preCommit,
			&postCommit,
		)
		if err != nil {
			return nil, fmt.Errorf("scan tool_call: %w", err)
		}

		if errMsg.Valid {
			rec.Error = errMsg.String
			rec.IsError = true
		}
		if preCommit.Valid {
			rec.PreCommit = preCommit.String
		}
		if postCommit.Valid {
			rec.PostCommit = postCommit.String
		}

		records = append(records, &rec)
	}

	return records, rows.Err()
}

// CountByTool はツール別の呼び出し回数を集計（統計用）
func (r *ToolCallRepository) CountByTool(sessionID string) (map[string]int, error) {
	query := `
        SELECT tc.tool_name, COUNT(*) as count
        FROM tool_calls tc
        JOIN turns t ON tc.turn_id = t.id
        WHERE t.session_id = ?
        GROUP BY tc.tool_name
    `

	rows, err := r.db.SqlDB.Query(query, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			return nil, err
		}
		counts[name] = count
	}
	return counts, nil
}
