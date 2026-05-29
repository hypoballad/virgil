package repository

import (
	"database/sql"
	"strings"
	"time"

	"github.com/hypoballad/virgil/internal/db"
	"github.com/hypoballad/virgil/internal/llm"
)

type Turn struct {
	ID               int64
	SessionID        string
	TurnNumber       int
	StartedAt        int64
	DurationMs       sql.NullInt64
	UserMessage      sql.NullString
	AssistantMessage sql.NullString
	FinishReason     sql.NullString
	PromptTokens     sql.NullInt64
	CompletionTokens sql.NullInt64
	Error            sql.NullString
	Summary          sql.NullString
}

type TurnRepository struct {
	db *db.DB
}

func NewTurnRepository(database *db.DB) *TurnRepository {
	return &TurnRepository{db: database}
}

func (r *TurnRepository) Create(sessionID string, turnNumber int, userMessage string) (*Turn, error) {
	t := &Turn{
		SessionID:   sessionID,
		TurnNumber:  turnNumber,
		StartedAt:   time.Now().Unix(),
		UserMessage: sql.NullString{String: userMessage, Valid: true},
	}

	res, err := r.db.SqlDB.Exec(`
		INSERT INTO turns (session_id, turn_number, started_at, user_message)
		VALUES (?, ?, ?, ?)`,
		t.SessionID, t.TurnNumber, t.StartedAt, t.UserMessage)
	if err != nil {
		return nil, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	t.ID = id

	return t, nil
}

func (r *TurnRepository) UpdateTurnResponse(turnID int64, assistantMessage, finishReason string, promptTokens, completionTokens int, durationMs int) error {
	_, err := r.db.SqlDB.Exec(`
		UPDATE turns SET assistant_message = ?, finish_reason = ?, prompt_tokens = ?, completion_tokens = ?, duration_ms = ?
		WHERE id = ?`,
		assistantMessage, finishReason, promptTokens, completionTokens, durationMs, turnID)
	return err
}

func (r *TurnRepository) UpdateTurnError(turnID int64, errMsg string) error {
	_, err := r.db.SqlDB.Exec(`
		UPDATE turns SET error = ? WHERE id = ?`,
		errMsg, turnID)
	return err
}

func (r *TurnRepository) UpdateTurnSummary(turnID int64, summary string) error {
	_, err := r.db.SqlDB.Exec(`
		UPDATE turns SET summary = ? WHERE id = ?`,
		summary, turnID)
	return err
}

func (r *TurnRepository) ListBySession(sessionID string) ([]*Turn, error) {
	rows, err := r.db.SqlDB.Query(`
		SELECT id, session_id, turn_number, started_at, duration_ms, user_message, assistant_message, finish_reason, prompt_tokens, completion_tokens, error, summary
		FROM turns WHERE session_id = ? ORDER BY turn_number ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var turns []*Turn
	for rows.Next() {
		t := &Turn{}
		if err := rows.Scan(&t.ID, &t.SessionID, &t.TurnNumber, &t.StartedAt, &t.DurationMs, &t.UserMessage, &t.AssistantMessage, &t.FinishReason, &t.PromptTokens, &t.CompletionTokens, &t.Error, &t.Summary); err != nil {
			return nil, err
		}
		turns = append(turns, t)
	}
	return turns, nil
}

func (r *TurnRepository) RebuildHistory(sessionID string, keepLastTurns int) ([]llm.Message, error) {
	turns, err := r.ListBySession(sessionID)
	if err != nil {
		return nil, err
	}
	if keepLastTurns < 0 {
		keepLastTurns = 0
	}

	rawStart := len(turns) - keepLastTurns
	if rawStart < 0 {
		rawStart = 0
	}

	messages := make([]llm.Message, 0, len(turns)*2)
	for i, turn := range turns {
		if i < rawStart {
			if turn.Summary.Valid {
				summary := strings.TrimSpace(turn.Summary.String)
				if summary != "" {
					messages = append(messages, llm.Message{
						Role:    "system",
						Content: "Previous conversation summary from saved session:\n\n" + summary,
					})
					continue
				}
			}
		}

		if turn.UserMessage.Valid && strings.TrimSpace(turn.UserMessage.String) != "" {
			messages = append(messages, llm.Message{
				Role:    "user",
				Content: turn.UserMessage.String,
			})
		}
		if turn.AssistantMessage.Valid && strings.TrimSpace(turn.AssistantMessage.String) != "" {
			messages = append(messages, llm.Message{
				Role:    "assistant",
				Content: turn.AssistantMessage.String,
			})
		}
	}

	return messages, nil
}
