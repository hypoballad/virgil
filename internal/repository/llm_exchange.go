package repository

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/hypoballad/virgil/internal/db"
)

// LLMExchangeRecord はDB記録用の構造体
type LLMExchangeRecord struct {
	ID        int64
	TurnID    int64
	Iteration int

	// リクエスト
	RequestMessages string // JSON
	RequestTools    string // JSON（空文字列ならツールなし）
	RequestFormat   string // JSON（空文字列なら未指定）

	// レスポンス
	ResponseContent   string // LLMの応答テキスト
	ResponseToolCalls string // JSON（空文字列ならツール呼び出しなし）
	ResponseMetadata  string // JSON（finish_reason等の軽量メタデータ）

	// メタデータ
	PromptTokens     int
	CompletionTokens int
	DurationMs       int64
	CreatedAt        int64
}

// 保存サイズの上限（1レコードあたり）
const MaxExchangeSize = 5 * 1024 * 1024 // 5MB

type LLMExchangeRepository struct {
	db *db.DB
}

func NewLLMExchangeRepository(database *db.DB) *LLMExchangeRepository {
	return &LLMExchangeRepository{db: database}
}

// Create はLLM交換記録を保存する
func (r *LLMExchangeRepository) Create(record LLMExchangeRecord) (*LLMExchangeRecord, error) {
	// メッセージサイズの制限
	reqMessages := record.RequestMessages
	if len(reqMessages) > MaxExchangeSize {
		reqMessages = fmt.Sprintf(
			"[Truncated, original size: %d bytes]\n%s",
			len(record.RequestMessages),
			reqMessages[:MaxExchangeSize-200],
		)
	}

	record.CreatedAt = time.Now().Unix()

	query := `
		INSERT INTO llm_exchanges
		(turn_id, iteration, request_messages, request_tools, request_format,
		 response_content, response_tool_calls, response_metadata,
		 prompt_tokens, completion_tokens, duration_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	res, err := r.db.SqlDB.Exec(
		query,
		record.TurnID,
		record.Iteration,
		reqMessages,
		nullIfEmpty(record.RequestTools),
		nullIfEmpty(record.RequestFormat),
		nullIfEmpty(record.ResponseContent),
		nullIfEmpty(record.ResponseToolCalls),
		nullIfEmpty(record.ResponseMetadata),
		record.PromptTokens,
		record.CompletionTokens,
		record.DurationMs,
		record.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert llm_exchange: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("get last insert id: %w", err)
	}

	record.ID = id
	record.RequestMessages = reqMessages
	return &record, nil
}

// ListByTurn はターンに属するLLM交換記録を取得する
func (r *LLMExchangeRepository) ListByTurn(turnID int64) ([]*LLMExchangeRecord, error) {
	query := `
		SELECT id, turn_id, iteration,
		       request_messages, request_tools, request_format,
		       response_content, response_tool_calls, response_metadata,
		       prompt_tokens, completion_tokens, duration_ms, created_at
		FROM llm_exchanges
		WHERE turn_id = ?
		ORDER BY iteration ASC
	`

	rows, err := r.db.SqlDB.Query(query, turnID)
	if err != nil {
		return nil, fmt.Errorf("query llm_exchanges: %w", err)
	}
	defer rows.Close()

	var records []*LLMExchangeRecord
	for rows.Next() {
		var rec LLMExchangeRecord
		var reqTools, reqFormat sql.NullString
		var respContent, respToolCalls, respMetadata sql.NullString

		err := rows.Scan(
			&rec.ID,
			&rec.TurnID,
			&rec.Iteration,
			&rec.RequestMessages,
			&reqTools,
			&reqFormat,
			&respContent,
			&respToolCalls,
			&respMetadata,
			&rec.PromptTokens,
			&rec.CompletionTokens,
			&rec.DurationMs,
			&rec.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan llm_exchange: %w", err)
		}

		rec.RequestTools = nullStringValue(reqTools)
		rec.RequestFormat = nullStringValue(reqFormat)
		rec.ResponseContent = nullStringValue(respContent)
		rec.ResponseToolCalls = nullStringValue(respToolCalls)
		rec.ResponseMetadata = nullStringValue(respMetadata)

		records = append(records, &rec)
	}
	return records, rows.Err()
}

// ListBySession はセッション内の全LLM交換記録を取得する
func (r *LLMExchangeRepository) ListBySession(sessionID string) ([]*LLMExchangeRecord, error) {
	query := `
		SELECT e.id, e.turn_id, e.iteration,
		       e.request_messages, e.request_tools, e.request_format,
		       e.response_content, e.response_tool_calls, e.response_metadata,
		       e.prompt_tokens, e.completion_tokens, e.duration_ms, e.created_at
		FROM llm_exchanges e
		JOIN turns t ON e.turn_id = t.id
		WHERE t.session_id = ?
		ORDER BY t.turn_number ASC, e.iteration ASC
	`

	rows, err := r.db.SqlDB.Query(query, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query llm_exchanges by session: %w", err)
	}
	defer rows.Close()

	var records []*LLMExchangeRecord
	for rows.Next() {
		var rec LLMExchangeRecord
		var reqTools, reqFormat sql.NullString
		var respContent, respToolCalls, respMetadata sql.NullString

		err := rows.Scan(
			&rec.ID,
			&rec.TurnID,
			&rec.Iteration,
			&rec.RequestMessages,
			&reqTools,
			&reqFormat,
			&respContent,
			&respToolCalls,
			&respMetadata,
			&rec.PromptTokens,
			&rec.CompletionTokens,
			&rec.DurationMs,
			&rec.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan llm_exchange: %w", err)
		}

		rec.RequestTools = nullStringValue(reqTools)
		rec.RequestFormat = nullStringValue(reqFormat)
		rec.ResponseContent = nullStringValue(respContent)
		rec.ResponseToolCalls = nullStringValue(respToolCalls)
		rec.ResponseMetadata = nullStringValue(respMetadata)

		records = append(records, &rec)
	}
	return records, rows.Err()
}

// GetByID はIDでLLM交換記録を取得する
func (r *LLMExchangeRepository) GetByID(id int64) (*LLMExchangeRecord, error) {
	query := `
		SELECT id, turn_id, iteration,
		       request_messages, request_tools, request_format,
		       response_content, response_tool_calls, response_metadata,
		       prompt_tokens, completion_tokens, duration_ms, created_at
		FROM llm_exchanges
		WHERE id = ?
	`

	var rec LLMExchangeRecord
	var reqTools, reqFormat sql.NullString
	var respContent, respToolCalls, respMetadata sql.NullString

	err := r.db.SqlDB.QueryRow(query, id).Scan(
		&rec.ID,
		&rec.TurnID,
		&rec.Iteration,
		&rec.RequestMessages,
		&reqTools,
		&reqFormat,
		&respContent,
		&respToolCalls,
		&respMetadata,
		&rec.PromptTokens,
		&rec.CompletionTokens,
		&rec.DurationMs,
		&rec.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	rec.RequestTools = nullStringValue(reqTools)
	rec.RequestFormat = nullStringValue(reqFormat)
	rec.ResponseContent = nullStringValue(respContent)
	rec.ResponseToolCalls = nullStringValue(respToolCalls)
	rec.ResponseMetadata = nullStringValue(respMetadata)

	return &rec, nil
}

// ヘルパー関数

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullStringValue(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}
