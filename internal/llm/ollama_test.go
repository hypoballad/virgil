package llm

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestOllamaClient_HandleStream(t *testing.T) {
	chunks := []ChatResponse{
		{
			Message: Message{Content: "Thinking..."},
			Done:    false,
		},
		{
			Message: Message{
				ToolCalls: []ToolCall{
					{
						Function: FunctionCall{
							Name:      "read_file",
							Arguments: map[string]interface{}{"path": "test.txt"},
						},
					},
				},
			},
			Done: false,
		},
		{
			Message: Message{Content: " More thinking."},
			Done:    false,
		},
		{
			PromptTokens:       100,
			CompletionTokens:   50,
			TotalDuration:      1000,
			LoadDuration:       100,
			PromptEvalDuration: 300,
			EvalDuration:       600,
			Done:               true,
		},
	}

	var buf bytes.Buffer
	for _, c := range chunks {
		data, _ := json.Marshal(c)
		buf.Write(data)
		buf.WriteByte('\n')
	}

	client := &OllamaClient{}
	var streamedContent string
	streamFunc := func(partial string) {
		streamedContent += partial
	}

	resp, err := client.handleStream(&buf, streamFunc)
	if err != nil {
		t.Fatalf("handleStream failed: %v", err)
	}

	// Verify content
	expectedContent := "Thinking... More thinking."
	if resp.Message.Content != expectedContent {
		t.Errorf("Content = %q, want %q", resp.Message.Content, expectedContent)
	}
	if streamedContent != expectedContent {
		t.Errorf("streamedContent = %q, want %q", streamedContent, expectedContent)
	}

	// Verify tool calls
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("Expected 1 tool call, got %d", len(resp.Message.ToolCalls))
	}
	if resp.Message.ToolCalls[0].Function.Name != "read_file" {
		t.Errorf("Tool call name = %q, want read_file", resp.Message.ToolCalls[0].Function.Name)
	}
	if resp.Message.ToolCalls[0].ID == "" {
		t.Error("Tool call ID should not be empty")
	}

	// Verify metadata
	if resp.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", resp.PromptTokens)
	}
	if resp.CompletionTokens != 50 {
		t.Errorf("CompletionTokens = %d, want 50", resp.CompletionTokens)
	}
	if resp.PromptEvalDuration != 300 {
		t.Errorf("PromptEvalDuration = %d, want 300", resp.PromptEvalDuration)
	}
	if resp.EvalDuration != 600 {
		t.Errorf("EvalDuration = %d, want 600", resp.EvalDuration)
	}
}
