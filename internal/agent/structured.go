package agent

import (
	"encoding/json"
	"log"
	"strings"
)

// Confidence はLLMの自己評価レベル
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// RequestedAction はLLMが要求するアクション
type RequestedAction string

const (
	ActionContinue RequestedAction = "continue"
	ActionAskUser  RequestedAction = "ask_user"
	ActionAbort    RequestedAction = "abort"
)

// StructuredResponse はLLMの最終応答の構造化表現
type StructuredResponse struct {
	Summary         string          `json:"summary"`
	Confidence      Confidence      `json:"confidence"`
	RequestedAction RequestedAction `json:"requested_action"`
	Blocker         string          `json:"blocker,omitempty"`
	QuestionToUser  string          `json:"question_to_user,omitempty"`
	PrefixText      string          `json:"-"` // JSON の前のフリーテキスト（JSON出力に含めない）
}

// ChatRequest.Format にこれを設定すると、LLMはこのスキーマに従ったJSONを返す
var StructuredResponseSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"summary": map[string]interface{}{
			"type":        "string",
			"description": "Your answer, progress report, or summary of findings",
		},
		"confidence": map[string]interface{}{
			"type": "string",
			"enum": []string{"high", "medium", "low"},
		},
		"requested_action": map[string]interface{}{
			"type": "string",
			"enum": []string{"continue", "ask_user", "abort"},
		},
		"blocker": map[string]interface{}{
			"type":        "string",
			"description": "What is blocking progress (when confidence is low)",
		},
		"question_to_user": map[string]interface{}{
			"type":        "string",
			"description": "Specific question for the user (when requested_action is ask_user)",
		},
	},
	"required": []string{"summary", "confidence", "requested_action"},
}

// ParseStructuredResponse はLLMの応答テキストから構造化レスポンスをパースする
//
// format: object 指定時はLLMが必ずJSONを返すはずだが、
// 万が一パース失敗した場合はフォールバックする。
func ParseStructuredResponse(text string) *StructuredResponse {
	text = strings.TrimSpace(text)

	// 1. テキスト全体をJSONとしてパース（format: object 使用時はこれで成功するはず）
	if resp := tryParseJSON(text); resp != nil {
		return resp
	}

	// 2. ```json ... ``` ブロックを探す（format未使用のフォールバック経路）
	if idx := strings.Index(text, "```json"); idx >= 0 {
		start := idx + 7
		if end := strings.Index(text[start:], "```"); end >= 0 {
			candidate := strings.TrimSpace(text[start : start+end])
			if resp := tryParseJSON(candidate); resp != nil {
				prefix := strings.TrimSpace(text[:idx])
				if prefix != "" {
					resp.PrefixText = prefix
				}
				return resp
			}
		}
	}

	// 3. 末尾からJSONを探す（最後の { から最後の } ）
	//    LLMが「テキスト + 末尾JSON」の形式で応答するパターンに対応
	if lastBrace := strings.LastIndex(text, "{"); lastBrace >= 0 {
		if lastClose := strings.LastIndex(text, "}"); lastClose > lastBrace {
			candidate := text[lastBrace : lastClose+1]
			if resp := tryParseJSON(candidate); resp != nil {
				prefix := strings.TrimSpace(text[:lastBrace])
				if prefix != "" {
					resp.PrefixText = prefix
				}
				return resp
			}
		}
	}

	// 4. 最初の { から最後の } を探す（従来のフォールバック）
	//    ステップ3で見つからなかった場合のみ
	if start := strings.Index(text, "{"); start >= 0 {
		if end := strings.LastIndex(text, "}"); end > start {
			candidate := text[start : end+1]
			if resp := tryParseJSON(candidate); resp != nil {
				prefix := strings.TrimSpace(text[:start])
				if prefix != "" {
					resp.PrefixText = prefix
				}
				return resp
			}
		}
	}

	// 5. フォールバック: 自由文テキストをそのまま summary に格納
	log.Printf("structured: failed to parse JSON, using freetext fallback")
	return &StructuredResponse{
		Summary:         text,
		Confidence:      ConfidenceMedium,
		RequestedAction: ActionContinue,
	}
}

func tryParseJSON(text string) *StructuredResponse {
	var resp StructuredResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		return nil
	}
	// summary が空ならパース成功とみなさない
	if resp.Summary == "" {
		return nil
	}
	// 未知の値をデフォルトに正規化
	resp.Confidence = normalizeConfidence(resp.Confidence)
	resp.RequestedAction = normalizeAction(resp.RequestedAction)
	return &resp
}

// inferActionFromText は応答テキストから RequestedAction を推定する
// 末尾の質問形や疑問符の有無で判定
func inferActionFromText(content string) RequestedAction {
	trimmed := strings.TrimSpace(content)
	if len(trimmed) == 0 {
		return ActionContinue
	}

	// 末尾100文字程度を検査対象とする
	tail := trimmed
	if len(tail) > 200 {
		tail = tail[len(tail)-200:]
	}

	// 末尾の質問符（半角・全角）
	lastRune := []rune(trimmed)
	if len(lastRune) > 0 {
		last := lastRune[len(lastRune)-1]
		if last == '?' || last == '？' {
			return ActionAskUser
		}
	}

	// 末尾近くの日本語質問表現
	jpQuestions := []string{
		"ますか", "ですか", "でしょうか",
		"どうしますか", "進めますか", "よろしいですか",
		"教えてください", "教えて下さい",
	}
	for _, q := range jpQuestions {
		if strings.Contains(tail, q) {
			return ActionAskUser
		}
	}

	// 英語の疑問形
	enQuestions := []string{
		"should I", "would you", "do you want",
		"shall I", "let me know",
	}
	tailLower := strings.ToLower(tail)
	for _, q := range enQuestions {
		if strings.Contains(tailLower, q) {
			return ActionAskUser
		}
	}

	return ActionContinue
}

// summarizeForMetadata は応答本文から短い summary を作る
// LLM 生成ではなく、本文の先頭部分を切り出す
func summarizeForMetadata(content string, maxChars int) string {
	trimmed := strings.TrimSpace(content)
	if len(trimmed) == 0 {
		return ""
	}

	// Markdown のヘッダや装飾を除去（先頭の # など）
	lines := strings.Split(trimmed, "\n")
	var firstMeaningful string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// 空行、ヘッダ装飾、コードブロック開始をスキップ
		if line == "" ||
			strings.HasPrefix(line, "#") ||
			strings.HasPrefix(line, "```") ||
			strings.HasPrefix(line, "---") {
			continue
		}
		firstMeaningful = line
		break
	}

	if firstMeaningful == "" {
		// すべてのスキップ条件に該当した場合、最初の行をそのまま使う
		firstMeaningful = lines[0]
	}

	// 切り詰め
	runes := []rune(firstMeaningful)
	if len(runes) > maxChars {
		return string(runes[:maxChars]) + "..."
	}
	return firstMeaningful
}

func normalizeConfidence(c Confidence) Confidence {
	switch c {
	case ConfidenceHigh, ConfidenceMedium, ConfidenceLow:
		return c
	default:
		return ConfidenceMedium
	}
}

func normalizeAction(a RequestedAction) RequestedAction {
	switch a {
	case ActionContinue, ActionAskUser, ActionAbort:
		return a
	default:
		return ActionContinue
	}
}
