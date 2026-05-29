package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hypoballad/virgil/internal/llm"
	"github.com/hypoballad/virgil/internal/repository"
)

func TestAnalyzeExchangeContextBreaksDownToolResultsAndRedactsSecrets(t *testing.T) {
	fakeOpenAIKey := "sk-" + "1234567890abcdef1234567890abcdef"
	messages := []llm.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "please inspect"},
		{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{
				{
					Function: llm.FunctionCall{
						Name:      "read_file",
						Arguments: map[string]interface{}{"path": "secret.py"},
					},
				},
			},
		},
		{Role: "tool", Content: "API_KEY=" + fakeOpenAIKey},
		{Role: "assistant", Content: "done"},
	}
	rawMessages, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}

	analysis, err := analyzeExchangeContext("session", &repository.LLMExchangeRecord{
		ID:              1,
		TurnID:          2,
		Iteration:       3,
		RequestMessages: string(rawMessages),
		RequestTools:    `[{"function":{"name":"read_file"}}]`,
		PromptTokens:    100,
	})
	if err != nil {
		t.Fatalf("analyzeExchangeContext error = %v", err)
	}

	if len(analysis.Breakdown) == 0 {
		t.Fatal("expected breakdown")
	}
	if len(analysis.ToolResultBreakdown) != 1 || analysis.ToolResultBreakdown[0].ToolName != "read_file" {
		t.Fatalf("tool result breakdown = %#v", analysis.ToolResultBreakdown)
	}
	if analysis.RedactionCount == 0 {
		t.Fatal("expected redactions")
	}
	if analysis.ToolDefinitionTokens == 0 || analysis.ToolDefinitionBytes == 0 {
		t.Fatalf("tool definition stats = %d tokens, %d bytes; want non-zero", analysis.ToolDefinitionTokens, analysis.ToolDefinitionBytes)
	}

	redacted, err := json.Marshal(analysis.RedactedContext)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(redacted), fakeOpenAIKey[:18]) {
		t.Fatalf("redacted context still contains secret: %s", redacted)
	}
}

func TestAnalyzeExchangeContextBuildsSanitizedCopyPayload(t *testing.T) {
	longContent := strings.Repeat("x", sanitizedContentMaxLen+25) + " train/src/AE.py agent.py"
	messages := []llm.Message{
		{
			Role:    "user",
			Content: "please inspect train/src/AE.py and agent.py",
		},
		{
			Role:    "tool",
			Content: longContent,
		},
		{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{
				{
					Function: llm.FunctionCall{
						Name: "read_symbol",
						Arguments: map[string]interface{}{
							"path":        "train/src/AE.py",
							"symbol_name": "SecretTrainer",
							"content":     longContent,
						},
					},
				},
			},
		},
	}
	rawMessages, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}

	analysis, err := analyzeExchangeContext("session", &repository.LLMExchangeRecord{
		ID:              1,
		RequestMessages: string(rawMessages),
	})
	if err != nil {
		t.Fatalf("analyzeExchangeContext error = %v", err)
	}

	sanitized, err := json.Marshal(analysis.SanitizedContext)
	if err != nil {
		t.Fatal(err)
	}
	text := string(sanitized)
	for _, forbidden := range []string{"train/src/AE.py", "agent.py", "SecretTrainer"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("sanitized context still contains %q: %s", forbidden, text)
		}
	}
	for _, want := range []string{"[sanitized path]", "[sanitized filename]", "[sanitized symbol]", "[cut "} {
		if !strings.Contains(text, want) {
			t.Fatalf("sanitized context missing %q: %s", want, text)
		}
	}
}

func TestAnalyzeExchangeContextReportsCompactedToolResults(t *testing.T) {
	messages := []llm.Message{
		{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{
				{
					ID: "call_1",
					Function: llm.FunctionCall{
						Name:      "read_file",
						Arguments: map[string]interface{}{"path": "large.md"},
					},
				},
			},
		},
		{
			Role:       "tool",
			ToolCallID: "call_1",
			Content: "[tool result omitted to save context]\n" +
				"Tool: read_file\n" +
				"Tool call ID: call_1\n" +
				"Original chars: 12000\n" +
				"Original estimated tokens: 3000\n" +
				"Preview: large content\n" +
				"Use a focused read/range/outline tool if the omitted details are needed again.",
		},
	}
	rawMessages, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}

	analysis, err := analyzeExchangeContext("session", &repository.LLMExchangeRecord{
		ID:              1,
		RequestMessages: string(rawMessages),
	})
	if err != nil {
		t.Fatalf("analyzeExchangeContext error = %v", err)
	}
	if analysis.CompactedToolResults != 1 {
		t.Fatalf("compacted results = %d, want 1", analysis.CompactedToolResults)
	}
	if analysis.CompactionOriginalTokens != 3000 {
		t.Fatalf("original tokens = %d, want 3000", analysis.CompactionOriginalTokens)
	}
	if analysis.CompactionSavedTokens <= 0 {
		t.Fatalf("saved tokens = %d, want > 0", analysis.CompactionSavedTokens)
	}
	if len(analysis.ToolResultBreakdown) != 1 {
		t.Fatalf("tool result breakdown = %#v, want one item", analysis.ToolResultBreakdown)
	}
	row := analysis.ToolResultBreakdown[0]
	if row.ToolName != "read_file" || row.CompactedCount != 1 || row.CompactionOriginalTokens != 3000 || row.CompactionSavedTokens <= 0 {
		t.Fatalf("tool compaction row = %#v", row)
	}
}

func TestSanitizeToolArgumentsForCopyHandlesJSONStringArguments(t *testing.T) {
	args := `{"path":"src/train.py","symbol_name":"Trainer","content":"see src/train.py and helper.py"}`

	got, ok := sanitizeToolArgumentsForCopy(args).(string)
	if !ok {
		t.Fatalf("sanitized arguments type = %T, want string", got)
	}
	if strings.Contains(got, "src/train.py") || strings.Contains(got, "helper.py") || strings.Contains(got, "Trainer") {
		t.Fatalf("sanitized arguments still contain sensitive values: %s", got)
	}
	for _, want := range []string{"[sanitized path]", "[sanitized filename]", "[sanitized symbol]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("sanitized arguments missing %q: %s", want, got)
		}
	}
}
