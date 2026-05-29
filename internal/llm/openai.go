package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type OpenAIClient struct {
	BaseURL string
	Model   string
	APIKey  string
}

type openaiMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	ToolCalls  []openaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openaiToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openaiFunctionCall `json:"function"`
}

type openaiFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openaiTool struct {
	Type     string             `json:"type"`
	Function openaiToolFunction `json:"function"`
}

type openaiToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type openaiChatRequest struct {
	Model          string          `json:"model"`
	Messages       []openaiMessage `json:"messages"`
	Stream         bool            `json:"stream"`
	StreamOptions  *streamOptions  `json:"stream_options,omitempty"`
	Tools          []openaiTool    `json:"tools,omitempty"`
	ResponseFormat interface{}     `json:"response_format,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openaiChatResponse struct {
	Choices []struct {
		Message struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			ToolCalls []openaiToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type openaiStreamChunk struct {
	Choices []struct {
		Delta struct {
			Role      string                 `json:"role"`
			Content   string                 `json:"content"`
			ToolCalls []openaiStreamToolCall `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage,omitempty"`
}

type openaiStreamToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (c *OpenAIClient) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if req.Model == "" {
		req.Model = c.Model
	}

	openaiReq := openaiChatRequest{
		Model:    req.Model,
		Stream:   req.Stream,
		Messages: make([]openaiMessage, len(req.Messages)),
	}
	if req.Stream {
		openaiReq.StreamOptions = &streamOptions{IncludeUsage: true}
	}

	for i, m := range req.Messages {
		om := openaiMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		if len(m.ToolCalls) > 0 {
			om.ToolCalls = make([]openaiToolCall, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				args, _ := json.Marshal(tc.Function.Arguments)
				om.ToolCalls[j] = openaiToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: openaiFunctionCall{
						Name:      tc.Function.Name,
						Arguments: string(args),
					},
				}
			}
		}
		openaiReq.Messages[i] = om
	}

	if len(req.Tools) > 0 {
		openaiReq.Tools = make([]openaiTool, len(req.Tools))
		for i, t := range req.Tools {
			openaiReq.Tools[i] = openaiTool{
				Type: "function",
				Function: openaiToolFunction{
					Name:        t.Function.Name,
					Description: t.Function.Description,
					Parameters:  t.Function.Parameters,
				},
			}
		}
	}

	if req.Format != nil {
		// 基本的には json_object を指定。より厳密なスキーマが必要なら拡張可能
		openaiReq.ResponseFormat = map[string]string{"type": "json_object"}
	}

	jsonData, err := json.Marshal(openaiReq)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, formatOpenAIHTTPError(resp.StatusCode, body)
	}

	if req.Stream {
		return c.handleStream(resp.Body, req)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var oResp openaiChatResponse
	if err := json.Unmarshal(body, &oResp); err != nil {
		return nil, err
	}

	if len(oResp.Choices) == 0 {
		return nil, fmt.Errorf("openai returned no choices")
	}

	choice := oResp.Choices[0]
	res := &ChatResponse{
		Message: Message{
			Role:    choice.Message.Role,
			Content: choice.Message.Content,
		},
		PromptTokens:     oResp.Usage.PromptTokens,
		CompletionTokens: oResp.Usage.CompletionTokens,
		FinishReason:     choice.FinishReason,
	}

	if len(choice.Message.ToolCalls) > 0 {
		res.Message.ToolCalls = make([]ToolCall, len(choice.Message.ToolCalls))
		for i, tc := range choice.Message.ToolCalls {
			var args map[string]interface{}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				// パース失敗時はエラーにせずログ出力のみ（モデルの不完全な出力を許容）
				args = map[string]interface{}{"raw_args": tc.Function.Arguments}
			}
			res.Message.ToolCalls[i] = ToolCall{
				ID: tc.ID,
				Function: FunctionCall{
					Name:      tc.Function.Name,
					Arguments: args,
				},
			}
		}
	}

	return res, nil
}

func formatOpenAIHTTPError(statusCode int, body []byte) error {
	bodyText := strings.TrimSpace(string(body))
	switch statusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("openai authentication failed (check OPENAI_API_KEY): %s", bodyText)
	case http.StatusForbidden:
		return fmt.Errorf("openai access forbidden (check API key permissions): %s", bodyText)
	case http.StatusTooManyRequests:
		return fmt.Errorf("openai rate limit exceeded (status %d): %s", statusCode, bodyText)
	case http.StatusBadRequest:
		return fmt.Errorf("openai bad request (status %d): %s", statusCode, bodyText)
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
		return fmt.Errorf("openai server error (status %d, retry may help): %s", statusCode, bodyText)
	default:
		return fmt.Errorf("openai error (status %d): %s", statusCode, bodyText)
	}
}

func (c *OpenAIClient) handleStream(r io.Reader, req ChatRequest) (*ChatResponse, error) {
	type toolCallAccumulator struct {
		id        string
		name      strings.Builder
		arguments strings.Builder
	}

	var content strings.Builder
	role := "assistant"
	toolCalls := make(map[int]*toolCallAccumulator)
	var toolCallOrder []int
	res := &ChatResponse{Done: true}
	hadPartial := false

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk openaiStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, fmt.Errorf("decode openai stream chunk: %w", err)
		}
		if chunk.Usage != nil {
			res.PromptTokens = chunk.Usage.PromptTokens
			res.CompletionTokens = chunk.Usage.CompletionTokens
		}
		if len(chunk.Choices) == 0 {
			continue
		}

		delta := chunk.Choices[0].Delta
		if delta.Role != "" {
			role = delta.Role
		}
		if delta.Content != "" {
			content.WriteString(delta.Content)
			hadPartial = true
			if req.StreamFunc != nil {
				req.StreamFunc(delta.Content)
			}
		}
		if chunk.Choices[0].FinishReason != "" {
			res.FinishReason = chunk.Choices[0].FinishReason
		}
		for _, tc := range delta.ToolCalls {
			acc, ok := toolCalls[tc.Index]
			if !ok {
				acc = &toolCallAccumulator{}
				toolCalls[tc.Index] = acc
				toolCallOrder = append(toolCallOrder, tc.Index)
			}
			if tc.ID != "" {
				acc.id = tc.ID
			}
			if tc.Function.Name != "" {
				acc.name.WriteString(tc.Function.Name)
			}
			if tc.Function.Arguments != "" {
				acc.arguments.WriteString(tc.Function.Arguments)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read openai stream: %w", err)
	}

	res.Message.Role = role
	res.Message.Content = content.String()
	res.HadPartial = hadPartial
	if len(toolCallOrder) > 0 {
		res.Message.ToolCalls = make([]ToolCall, 0, len(toolCallOrder))
		for _, index := range toolCallOrder {
			acc := toolCalls[index]
			argsJSON := acc.arguments.String()
			args := make(map[string]interface{})
			if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
				args = map[string]interface{}{"raw_args": argsJSON}
			}
			res.Message.ToolCalls = append(res.Message.ToolCalls, ToolCall{
				ID: acc.id,
				Function: FunctionCall{
					Name:      acc.name.String(),
					Arguments: args,
				},
			})
		}
	}

	return res, nil
}
