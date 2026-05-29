package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	// ID is internally generated for tracking (Ollama API doesn't provide it)
	ID       string       `json:"-"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ArgumentsJSON converts the map arguments to raw JSON message
func (f *FunctionCall) ArgumentsJSON() (json.RawMessage, error) {
	return json.Marshal(f.Arguments)
}

type ToolDefinition struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

type FunctionDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type StreamFunc func(partial string)

type ChatRequest struct {
	Model      string           `json:"model"`
	Messages   []Message        `json:"messages"`
	Stream     bool             `json:"stream"`
	Tools      []ToolDefinition `json:"tools,omitempty"`
	Format     interface{}      `json:"format,omitempty"`
	StreamFunc StreamFunc       `json:"-"` // Not serialized to JSON
}

type ChatResponse struct {
	Message            Message `json:"message"`
	PromptTokens       int     `json:"prompt_eval_count"`
	CompletionTokens   int     `json:"eval_count"`
	TotalDuration      int64   `json:"total_duration"`       // nanoseconds
	LoadDuration       int64   `json:"load_duration"`        // nanoseconds
	PromptEvalDuration int64   `json:"prompt_eval_duration"` // nanoseconds
	EvalDuration       int64   `json:"eval_duration"`        // nanoseconds
	Done               bool    `json:"done"`                 // For streaming
	FinishReason       string  `json:"done_reason,omitempty"`
	HadPartial         bool    `json:"-"`
}

type OllamaClient struct {
	BaseURL string
	Model   string
}

func (c *OllamaClient) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if req.Model == "" {
		req.Model = c.Model
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/api/chat", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama error: %s", string(body))
	}

	if req.Stream {
		return c.handleStream(resp.Body, req.StreamFunc)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return nil, err
	}

	// Generate IDs for tool calls as Ollama doesn't provide them
	for i := range chatResp.Message.ToolCalls {
		if chatResp.Message.ToolCalls[i].ID == "" {
			chatResp.Message.ToolCalls[i].ID = uuid.New().String()
		}
	}

	return &chatResp, nil
}

func (c *OllamaClient) handleStream(r io.Reader, streamFunc StreamFunc) (*ChatResponse, error) {
	var finalResp ChatResponse
	var fullContent strings.Builder
	var allToolCalls []ToolCall
	hadPartial := false

	decoder := json.NewDecoder(r)
	for {
		var chunk ChatResponse
		if err := decoder.Decode(&chunk); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode stream: %w", err)
		}

		if chunk.Message.Content != "" {
			fullContent.WriteString(chunk.Message.Content)
			hadPartial = true
			if streamFunc != nil {
				streamFunc(chunk.Message.Content)
			}
		}

		if len(chunk.Message.ToolCalls) > 0 {
			allToolCalls = append(allToolCalls, chunk.Message.ToolCalls...)
		}

		if chunk.Done {
			finalResp = chunk
			break
		}
	}

	finalResp.Message.Content = fullContent.String()
	finalResp.Message.ToolCalls = allToolCalls
	finalResp.Message.Role = "assistant"
	finalResp.HadPartial = hadPartial

	// Generate IDs for tool calls if any
	for i := range finalResp.Message.ToolCalls {
		if finalResp.Message.ToolCalls[i].ID == "" {
			finalResp.Message.ToolCalls[i].ID = uuid.New().String()
		}
	}

	return &finalResp, nil
}
