package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/hypoballad/virgil/internal/repository"
	"github.com/hypoballad/virgil/internal/tokenizer"
)

type Handler struct {
	repo *repository.Repository
}

// writeJSON はJSONレスポンスを書き込むヘルパー
func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("JSON encode error: %v", err)
	}
}

// writeError はエラーレスポンスを返すヘルパー
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// indexPage はHTML viewerを返す
func (h *Handler) indexPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(indexHTML))
}

// listSessions はセッション一覧を返す
func (h *Handler) listSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := h.repo.Sessions.ListRecent(50)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	type sessionJSON struct {
		ID              string `json:"id"`
		StartedAt       int64  `json:"started_at"`
		EndedAt         *int64 `json:"ended_at,omitempty"`
		ProjectPath     string `json:"project_path"`
		TaskDescription string `json:"task_description"`
		Status          string `json:"status"`
		Model           string `json:"model"`
	}

	result := make([]sessionJSON, 0, len(sessions))
	for _, s := range sessions {
		item := sessionJSON{
			ID:              s.ID,
			StartedAt:       s.StartedAt,
			ProjectPath:     s.ProjectPath,
			TaskDescription: s.TaskDescription,
			Status:          s.Status,
			Model:           s.Model,
		}
		if s.EndedAt.Valid {
			item.EndedAt = &s.EndedAt.Int64
		}
		result = append(result, item)
	}
	writeJSON(w, result)
}

// getSession はセッション詳細（turns一覧含む）を返す
func (h *Handler) getSession(w http.ResponseWriter, r *http.Request) {
	// /api/sessions/{id} からIDを抽出
	id := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if id == "" {
		writeError(w, 400, "session ID required")
		return
	}

	session, err := h.repo.Sessions.Get(id)
	if err != nil {
		writeError(w, 404, "session not found")
		return
	}

	turns, err := h.repo.Turns.ListBySession(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	type turnJSON struct {
		ID               int64  `json:"id"`
		TurnNumber       int    `json:"turn_number"`
		StartedAt        int64  `json:"started_at"`
		UserMessage      string `json:"user_message,omitempty"`
		AssistantMessage string `json:"assistant_message,omitempty"`
		PromptTokens     int64  `json:"prompt_tokens"`
		CompletionTokens int64  `json:"completion_tokens"`
		Error            string `json:"error,omitempty"`
	}

	turnList := make([]turnJSON, 0, len(turns))
	for _, t := range turns {
		item := turnJSON{
			ID:         t.ID,
			TurnNumber: t.TurnNumber,
			StartedAt:  t.StartedAt,
		}
		if t.UserMessage.Valid {
			item.UserMessage = t.UserMessage.String
		}
		if t.AssistantMessage.Valid {
			item.AssistantMessage = t.AssistantMessage.String
		}
		if t.PromptTokens.Valid {
			item.PromptTokens = t.PromptTokens.Int64
		}
		if t.CompletionTokens.Valid {
			item.CompletionTokens = t.CompletionTokens.Int64
		}
		if t.Error.Valid {
			item.Error = t.Error.String
		}
		turnList = append(turnList, item)
	}

	writeJSON(w, map[string]interface{}{
		"session": map[string]interface{}{
			"id":               session.ID,
			"started_at":       session.StartedAt,
			"status":           session.Status,
			"model":            session.Model,
			"project_path":     session.ProjectPath,
			"task_description": session.TaskDescription,
		},
		"turns": turnList,
	})
}

// listExchanges はセッション内の全LLM交換記録を返す
func (h *Handler) listExchanges(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimPrefix(r.URL.Path, "/api/exchanges/")
	if sessionID == "" {
		writeError(w, 400, "session ID required")
		return
	}

	exchanges, err := h.repo.LLMExchanges.ListBySession(sessionID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	type exchangeSummary struct {
		ID                  int64       `json:"id"`
		TurnID              int64       `json:"turn_id"`
		Iteration           int         `json:"iteration"`
		PromptTokens        int         `json:"prompt_tokens"`
		CompletionTokens    int         `json:"completion_tokens"`
		DurationMs          int64       `json:"duration_ms"`
		RequestSize         int         `json:"request_size"` // bytes
		ResponseContentSize int         `json:"response_content_size"`
		HasToolCalls        bool        `json:"has_tool_calls"`
		HasFormat           bool        `json:"has_format"`
		ResponseMetadata    interface{} `json:"response_metadata"`
		CreatedAt           int64       `json:"created_at"`
	}

	result := make([]exchangeSummary, 0, len(exchanges))
	for _, e := range exchanges {
		result = append(result, exchangeSummary{
			ID:                  e.ID,
			TurnID:              e.TurnID,
			Iteration:           e.Iteration,
			PromptTokens:        e.PromptTokens,
			CompletionTokens:    e.CompletionTokens,
			DurationMs:          e.DurationMs,
			RequestSize:         len(e.RequestMessages),
			ResponseContentSize: len(e.ResponseContent),
			HasToolCalls:        e.ResponseToolCalls != "",
			HasFormat:           e.RequestFormat != "",
			ResponseMetadata:    jsonOrNull(e.ResponseMetadata),
			CreatedAt:           e.CreatedAt,
		})
	}
	writeJSON(w, result)
}

// getExchange は個別のLLM交換記録の全文を返す
func (h *Handler) getExchange(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/exchange/")
	if idStr == "" {
		writeError(w, 400, "exchange ID required")
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, 400, "invalid exchange ID")
		return
	}

	// リポジトリの GetByID を使用して安全に取得
	e, err := h.repo.LLMExchanges.GetByID(id)
	if err != nil {
		writeError(w, 404, "exchange not found")
		return
	}

	// messages配列をパースしてrole別の統計を計算
	var messages []map[string]interface{}
	json.Unmarshal([]byte(e.RequestMessages), &messages)

	type roleStats struct {
		Count  int `json:"count"`
		Tokens int `json:"tokens"`
	}
	stats := make(map[string]*roleStats)
	totalTokens := 0

	for _, msg := range messages {
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)
		tokens := tokenizer.EstimateTokens(content)
		totalTokens += tokens

		if stats[role] == nil {
			stats[role] = &roleStats{}
		}
		stats[role].Count++
		stats[role].Tokens += tokens
	}

	writeJSON(w, map[string]interface{}{
		"id":                  e.ID,
		"turn_id":             e.TurnID,
		"iteration":           e.Iteration,
		"request_messages":    json.RawMessage(e.RequestMessages),
		"request_tools":       jsonOrNull(e.RequestTools),
		"request_format":      jsonOrNull(e.RequestFormat),
		"response_content":    e.ResponseContent,
		"response_tool_calls": jsonOrNull(e.ResponseToolCalls),
		"response_metadata":   jsonOrNull(e.ResponseMetadata),
		"prompt_tokens":       e.PromptTokens,
		"completion_tokens":   e.CompletionTokens,
		"duration_ms":         e.DurationMs,
		"created_at":          e.CreatedAt,
		"role_stats":          stats,
		"total_tokens":        totalTokens,
		"message_count":       len(messages),
	})
}

// getStats はセッション全体の統計を返す
func (h *Handler) getStats(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimPrefix(r.URL.Path, "/api/stats/")
	if sessionID == "" {
		writeError(w, 400, "session ID required")
		return
	}

	exchanges, err := h.repo.LLMExchanges.ListBySession(sessionID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	// iteration ごとのコンテキスト膨張データ
	type iterationData struct {
		ExchangeID   int64 `json:"exchange_id"`
		TurnID       int64 `json:"turn_id"`
		Iteration    int   `json:"iteration"`
		PromptTokens int   `json:"prompt_tokens"`
		CompTokens   int   `json:"completion_tokens"`
		RequestSize  int   `json:"request_size_bytes"`
		DurationMs   int64 `json:"duration_ms"`
		MessageCount int   `json:"message_count"`
	}

	iterations := make([]iterationData, 0, len(exchanges))
	totalPromptTokens := 0
	totalCompTokens := 0
	var totalDuration int64

	for _, e := range exchanges {
		// messages 数をカウント
		var msgs []interface{}
		json.Unmarshal([]byte(e.RequestMessages), &msgs)

		iterations = append(iterations, iterationData{
			ExchangeID:   e.ID,
			TurnID:       e.TurnID,
			Iteration:    e.Iteration,
			PromptTokens: e.PromptTokens,
			CompTokens:   e.CompletionTokens,
			RequestSize:  len(e.RequestMessages),
			DurationMs:   e.DurationMs,
			MessageCount: len(msgs),
		})

		totalPromptTokens += e.PromptTokens
		totalCompTokens += e.CompletionTokens
		totalDuration += e.DurationMs
	}

	// ツール呼び出し統計
	toolCounts, _ := h.repo.ToolCalls.CountByTool(sessionID)

	writeJSON(w, map[string]interface{}{
		"session_id":          sessionID,
		"total_exchanges":     len(exchanges),
		"total_prompt_tokens": totalPromptTokens,
		"total_comp_tokens":   totalCompTokens,
		"total_duration_ms":   totalDuration,
		"iterations":          iterations,
		"tool_counts":         toolCounts,
	})
}

// getContextAnalysis returns a token breakdown for the latest recorded context
// in a session, plus raw and redacted copy payloads.
func (h *Handler) getContextAnalysis(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimPrefix(r.URL.Path, "/api/context/")
	if sessionID == "" {
		writeError(w, 400, "session ID required")
		return
	}

	exchanges, err := h.repo.LLMExchanges.ListBySession(sessionID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if len(exchanges) == 0 {
		writeError(w, 404, "no LLM exchanges recorded")
		return
	}

	latest := exchanges[len(exchanges)-1]
	analysis, err := analyzeExchangeContext(sessionID, latest)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, analysis)
}

// jsonOrNull はJSON文字列をRawMessageとして返す。空文字列ならnull
func jsonOrNull(s string) interface{} {
	if s == "" {
		return nil
	}
	return json.RawMessage(s)
}
