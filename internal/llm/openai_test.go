package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIClient_ChatStream(t *testing.T) {
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		gotBody = string(body)

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"test.txt\\\"}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":11},\"choices\":[]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	var streamed strings.Builder
	client := &OpenAIClient{BaseURL: server.URL, Model: "test-model"}
	resp, err := client.Chat(context.Background(), ChatRequest{
		Stream:     true,
		Messages:   []Message{{Role: "user", Content: "hi"}},
		StreamFunc: func(partial string) { streamed.WriteString(partial) },
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	if !strings.Contains(gotBody, `"stream":true`) {
		t.Fatalf("request body = %s, want stream true", gotBody)
	}
	if !strings.Contains(gotBody, `"stream_options":{"include_usage":true}`) {
		t.Fatalf("request body = %s, want stream_options include_usage true", gotBody)
	}
	if resp.Message.Role != "assistant" {
		t.Errorf("role = %q, want assistant", resp.Message.Role)
	}
	if resp.Message.Content != "hello" {
		t.Errorf("content = %q, want hello", resp.Message.Content)
	}
	if streamed.String() != "hello" {
		t.Errorf("streamed content = %q, want hello", streamed.String())
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(resp.Message.ToolCalls))
	}
	tc := resp.Message.ToolCalls[0]
	if tc.ID != "call_1" {
		t.Errorf("tool call id = %q, want call_1", tc.ID)
	}
	if tc.Function.Name != "read_file" {
		t.Errorf("tool call name = %q, want read_file", tc.Function.Name)
	}
	if tc.Function.Arguments["path"] != "test.txt" {
		t.Errorf("tool call path = %v, want test.txt", tc.Function.Arguments["path"])
	}
	if resp.PromptTokens != 7 || resp.CompletionTokens != 11 {
		t.Errorf("tokens = %d/%d, want 7/11", resp.PromptTokens, resp.CompletionTokens)
	}
}

func TestOpenAIRequest_StreamOptions_IncludedWhenStreaming(t *testing.T) {
	req := openaiChatRequest{
		Model:         "gpt-5-mini",
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
		Messages:      []openaiMessage{},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	streamOpts, ok := parsed["stream_options"]
	if !ok {
		t.Fatal("stream_options not found in request JSON")
	}

	streamOptsMap, ok := streamOpts.(map[string]interface{})
	if !ok {
		t.Fatalf("stream_options is not a map: %T", streamOpts)
	}

	if includeUsage := streamOptsMap["include_usage"]; includeUsage != true {
		t.Errorf("include_usage = %v, want true", includeUsage)
	}
}

func TestOpenAIRequest_StreamOptions_OmittedWhenNotStreaming(t *testing.T) {
	req := openaiChatRequest{
		Model:    "gpt-5-mini",
		Stream:   false,
		Messages: []openaiMessage{},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if _, exists := parsed["stream_options"]; exists {
		t.Fatal("stream_options should be omitted when not streaming")
	}
}

func TestOpenAIClient_HandleStreamInvalidChunk(t *testing.T) {
	client := &OpenAIClient{}
	_, err := client.handleStream(strings.NewReader("data: not-json\n\n"), ChatRequest{Stream: true})
	if err == nil {
		t.Fatal("handleStream succeeded, want error")
	}
	if !strings.Contains(err.Error(), "decode openai stream chunk") {
		t.Fatalf("error = %v, want decode context", err)
	}
}

func TestOpenAIClient_HTTPErrorMessages(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		want       string
	}{
		{"unauthorized", http.StatusUnauthorized, "openai authentication failed"},
		{"forbidden", http.StatusForbidden, "openai access forbidden"},
		{"rate limit", http.StatusTooManyRequests, "openai rate limit exceeded"},
		{"bad request", http.StatusBadRequest, "openai bad request"},
		{"server error", http.StatusServiceUnavailable, "openai server error"},
		{"generic", http.StatusTeapot, "openai error (status 418)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := formatOpenAIHTTPError(tt.statusCode, []byte(`{"error":"boom"}`+"\n"))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.want)
			}
			if strings.Contains(err.Error(), "\n") {
				t.Fatalf("error should trim response body whitespace, got %q", err.Error())
			}
		})
	}
}
