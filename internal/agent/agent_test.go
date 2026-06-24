package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/hypoballad/virgil/internal/db"
	"github.com/hypoballad/virgil/internal/llm"
	"github.com/hypoballad/virgil/internal/repository"
	"github.com/hypoballad/virgil/internal/shadow"
	"github.com/hypoballad/virgil/internal/tools"
)

// mockLLM はテスト用のLLMクライアント
type mockLLM struct {
	responses []llm.ChatResponse
	callCount int
	requests  []llm.ChatRequest
}

func (m *mockLLM) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	m.requests = append(m.requests, req)
	if m.callCount >= len(m.responses) {
		return nil, errors.New("no more mock responses")
	}
	resp := m.responses[m.callCount]
	m.callCount++
	return &resp, nil
}

func joinMessageContent(messages []llm.Message) string {
	var sb strings.Builder
	for _, msg := range messages {
		sb.WriteString(msg.Role)
		sb.WriteString(": ")
		sb.WriteString(msg.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}

// dummyTool はテスト用のシンプルツール
type dummyTool struct {
	name       string
	response   string
	isMutating bool
	calls      int
	isError    bool
	lastArgs   json.RawMessage
	metadata   map[string]interface{}
}

func (d *dummyTool) Name() string { return d.name }
func (d *dummyTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Type: "function",
		Function: tools.FunctionDefinition{
			Name:        d.name,
			Description: "test tool",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}
}
func (d *dummyTool) Execute(ctx context.Context, args json.RawMessage) (*tools.Result, error) {
	d.calls++
	d.lastArgs = append(d.lastArgs[:0], args...)
	if d.isError {
		res := tools.ErrorResult(d.response)
		res.Metadata = d.metadata
		return res, nil
	}
	res := tools.SuccessResult(d.response)
	res.Metadata = d.metadata
	return res, nil
}
func (d *dummyTool) IsMutating() bool {
	return d.isMutating
}

type failingShadowSnapshotter struct {
	preCalls  int
	postCalls int
	diffCalls int
}

func (s *failingShadowSnapshotter) CommitPre(ctx context.Context, toolName string) (string, error) {
	s.preCalls++
	return "", errors.New("shadow git locked")
}

func (s *failingShadowSnapshotter) CommitPost(ctx context.Context, toolName string) (string, error) {
	s.postCalls++
	return "post", nil
}

func (s *failingShadowSnapshotter) Diff(ctx context.Context, from, to string, maxLines int) (string, error) {
	s.diffCalls++
	return "", nil
}

type diffShadowSnapshotter struct {
	preCalls  int
	postCalls int
	diffCalls int
}

func (s *diffShadowSnapshotter) CommitPre(ctx context.Context, toolName string) (string, error) {
	s.preCalls++
	return "pre", nil
}

func (s *diffShadowSnapshotter) CommitPost(ctx context.Context, toolName string) (string, error) {
	s.postCalls++
	return "post", nil
}

func (s *diffShadowSnapshotter) Diff(ctx context.Context, from, to string, maxLines int) (string, error) {
	s.diffCalls++
	return "diff --git a/report.md b/report.md\n+large report", nil
}

func TestAgentNoTools(t *testing.T) {
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{Message: llm.Message{Role: "assistant", Content: "Hello!"}},
		},
	}

	registry := tools.NewRegistry()
	agent := New(mockLLM, registry)

	resp, err := agent.Run(context.Background(), nil, "Hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.FinalContent != "Hello!" {
		t.Errorf("expected 'Hello!', got %q", resp.FinalContent)
	}
	if resp.Iterations != 1 {
		t.Errorf("expected 1 iteration, got %d", resp.Iterations)
	}
}

func TestRunReplacesStaleModeSystemPrompt(t *testing.T) {
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{Message: llm.Message{Role: "assistant", Content: "done"}},
		},
	}
	agent := New(mockLLM, tools.NewRegistry())
	agent.SetPlanMode(false)

	_, err := agent.Run(context.Background(), []llm.Message{
		{Role: "system", Content: SystemPromptModePlan},
		{Role: "system", Content: "Previous conversation summary, compressed by /shrink:\n\nsummary"},
		{Role: "user", Content: "old instruction"},
	}, "continue")
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}

	req := mockLLM.requests[0]
	if len(req.Messages) < 2 {
		t.Fatalf("request messages = %#v", req.Messages)
	}
	if !strings.Contains(req.Messages[0].Content, "You are in EDIT mode") {
		t.Fatalf("first system prompt did not reflect current edit mode:\n%s", req.Messages[0].Content)
	}
	if strings.Contains(req.Messages[0].Content, "You are in PLAN mode") {
		t.Fatalf("first system prompt still contains stale plan mode:\n%s", req.Messages[0].Content)
	}
	if !strings.Contains(req.Messages[0].Content, "Previous conversation summary") {
		t.Fatalf("shrink summary system message should be merged into leading system prompt: %#v", req.Messages)
	}
	for i, msg := range req.Messages[1:] {
		if msg.Role == "system" {
			t.Fatalf("non-leading system message at request[%d]: %#v", i+1, req.Messages)
		}
	}
}

func TestBuildSystemPromptStableForSameMode(t *testing.T) {
	agent := New(&mockLLM{}, tools.NewRegistry())
	agent.SetWorkspaceRoot("/tmp/workspace")
	agent.SetPlanMode(false)

	first := agent.buildSystemPrompt()
	second := agent.buildSystemPrompt()
	if first != second {
		t.Fatal("system prompt should be stable across repeated builds in the same mode")
	}
	if strings.Contains(first, "Previous conversation summary") {
		t.Fatalf("system prompt should not include history-derived dynamic content:\n%s", first)
	}
}

func TestBuildSystemPromptIncludesResponseLanguageInstruction(t *testing.T) {
	agent := New(&mockLLM{}, tools.NewRegistry())

	if prompt := agent.buildSystemPrompt(); !strings.Contains(prompt, "Match the user's language") {
		t.Fatalf("auto response language instruction missing:\n%s", prompt)
	}

	agent.SetResponseLanguage("ja")
	if prompt := agent.buildSystemPrompt(); !strings.Contains(prompt, "Respond to the user in Japanese") {
		t.Fatalf("Japanese response language instruction missing:\n%s", prompt)
	}

	agent.SetResponseLanguage("en")
	if prompt := agent.buildSystemPrompt(); !strings.Contains(prompt, "Respond to the user in English") {
		t.Fatalf("English response language instruction missing:\n%s", prompt)
	}
}

func TestBuildTaskSystemPromptIncludesResponseLanguageInstruction(t *testing.T) {
	agent := New(&mockLLM{}, tools.NewRegistry())
	agent.SetResponseLanguage("ja")

	prompt := agent.buildTaskSystemPrompt()
	if !strings.Contains(prompt, "Respond to the user in Japanese") {
		t.Fatalf("task prompt missing response language instruction:\n%s", prompt)
	}
}

func TestTokenCalibrationAppliesEMA(t *testing.T) {
	agent := New(&mockLLM{}, tools.NewRegistry())

	raw := 100
	if got := agent.applyTokenCalibration(raw); got != 100 {
		t.Fatalf("initial calibrated estimate = %d, want 100", got)
	}

	agent.updateTokenCalibration(raw, 80)
	if got, want := agent.tokenCalibration, 0.96; got < want-0.0000001 || got > want+0.0000001 {
		t.Fatalf("tokenCalibration = %v, want approximately %v", got, want)
	}
	if got := agent.applyTokenCalibration(raw); got != 96 {
		t.Fatalf("calibrated estimate after update = %d, want 96", got)
	}

	agent.updateTokenCalibration(raw, 120)
	if got, want := agent.tokenCalibration, 1.008; got < want-0.0000001 || got > want+0.0000001 {
		t.Fatalf("tokenCalibration = %v, want approximately %v", got, want)
	}
	if got := agent.applyTokenCalibration(raw); got != 101 {
		t.Fatalf("calibrated estimate after second update = %d, want 101", got)
	}
}

func TestSummarizeHistory(t *testing.T) {
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{Message: llm.Message{Role: "assistant", Content: "- Updated internal/agent/agent.go\n- Pending: run tests"}},
		},
	}

	agent := New(mockLLM, tools.NewRegistry())
	summary, err := agent.SummarizeHistory(context.Background(), []llm.Message{
		{Role: "user", Content: "Fix context compression"},
		{Role: "assistant", Content: "Changed internal/agent/agent.go"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
	if mockLLM.callCount != 1 {
		t.Fatalf("expected 1 LLM call, got %d", mockLLM.callCount)
	}
	req := mockLLM.requests[0]
	if req.Stream {
		t.Fatal("expected non-streaming summary request")
	}
	if len(req.Tools) != 0 {
		t.Fatalf("expected no tools for summary request, got %d", len(req.Tools))
	}
	if len(req.Messages) != 2 {
		t.Fatalf("expected summary prompt and input, got %d messages", len(req.Messages))
	}
	if !strings.Contains(req.Messages[1].Content, "internal/agent/agent.go") {
		t.Fatalf("summary input did not include message content: %q", req.Messages[1].Content)
	}
}

func TestSummarizeHistoryRecordsLLMExchangeWhenTurnIsSet(t *testing.T) {
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{
				Message:          llm.Message{Role: "assistant", Content: "- compressed"},
				PromptTokens:     11,
				CompletionTokens: 3,
			},
		},
	}

	database, err := db.New(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatalf("db.New error = %v", err)
	}
	defer database.Close()
	repo := repository.New(database)
	session, err := repo.Sessions.Create("model", "/tmp/workspace", "test")
	if err != nil {
		t.Fatalf("create session error = %v", err)
	}
	turn, err := repo.Turns.Create(session.ID, 1, "hello")
	if err != nil {
		t.Fatalf("create turn error = %v", err)
	}

	agent := New(mockLLM, tools.NewRegistry())
	agent.SetRepository(repo)
	agent.SetCurrentTurnID(turn.ID)

	summary, err := agent.SummarizeHistory(context.Background(), []llm.Message{
		{Role: "user", Content: "older message"},
	})
	if err != nil {
		t.Fatalf("SummarizeHistory error = %v", err)
	}
	if summary != "- compressed" {
		t.Fatalf("summary = %q", summary)
	}

	exchanges, err := repo.LLMExchanges.ListByTurn(turn.ID)
	if err != nil {
		t.Fatalf("ListByTurn error = %v", err)
	}
	if len(exchanges) != 1 {
		t.Fatalf("exchange count = %d, want 1", len(exchanges))
	}
	exchange := exchanges[0]
	if exchange.Iteration != ExchangeIterationShrink {
		t.Fatalf("exchange iteration = %d, want %d", exchange.Iteration, ExchangeIterationShrink)
	}
	if !strings.Contains(exchange.RequestMessages, "compressing older conversation history") {
		t.Fatalf("request messages did not include summarization prompt: %s", exchange.RequestMessages)
	}
	if exchange.ResponseContent != "- compressed" {
		t.Fatalf("response content = %q", exchange.ResponseContent)
	}
}

func TestPreflightShrinkRunsOnlyWhenEnabled(t *testing.T) {
	history := []llm.Message{
		{Role: "user", Content: strings.Repeat("older context ", 200)},
		{Role: "assistant", Content: "older answer"},
		{Role: "user", Content: "recent question"},
	}
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{Message: llm.Message{Role: "assistant", Content: "done"}},
		},
	}
	agentInst := New(mockLLM, tools.NewRegistry())

	resp, err := agentInst.RunWithOptions(context.Background(), history, "new task", RunOptions{
		ContextLimitTokens:     100,
		PreflightShrinkPercent: 1,
	})
	if err != nil {
		t.Fatalf("RunWithOptions error = %v", err)
	}
	if resp.FinalContent != "done" {
		t.Fatalf("FinalContent = %q, want done", resp.FinalContent)
	}
	if mockLLM.callCount != 1 {
		t.Fatalf("LLM calls = %d, want 1", mockLLM.callCount)
	}
}

func TestPreflightShrinkCompressesOlderMessages(t *testing.T) {
	history := make([]llm.Message, 0, 12)
	for i := 0; i < 12; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		history = append(history, llm.Message{
			Role:    role,
			Content: fmt.Sprintf("old-%02d %s", i, strings.Repeat("context ", 100)),
		})
	}

	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{Message: llm.Message{Role: "assistant", Content: "compressed older work"}},
			{Message: llm.Message{Role: "assistant", Content: "done"}},
		},
	}
	agentInst := New(mockLLM, tools.NewRegistry())

	resp, err := agentInst.RunWithOptions(context.Background(), history, "new task", RunOptions{
		PreflightShrink:                   true,
		ContextLimitTokens:                100,
		PreflightShrinkPercent:            1,
		PreflightShrinkCooldownIterations: 5,
	})
	if err != nil {
		t.Fatalf("RunWithOptions error = %v", err)
	}
	if resp.FinalContent != "done" {
		t.Fatalf("FinalContent = %q, want done", resp.FinalContent)
	}
	if mockLLM.callCount != 2 {
		t.Fatalf("LLM calls = %d, want 2", mockLLM.callCount)
	}

	joined := joinMessageContent(mockLLM.requests[1].Messages)
	if !strings.Contains(joined, "compressed older work") {
		t.Fatalf("compressed request missing summary: %s", joined)
	}
	if strings.Contains(joined, "old-01") {
		t.Fatalf("compressed request retained oldest message: %s", joined)
	}
	if !strings.Contains(joined, "new task") {
		t.Fatalf("compressed request missing latest user task: %s", joined)
	}
}

func TestPreflightShrinkPinsOlderUserInstructions(t *testing.T) {
	activeInstruction := "プランは計画書としてtrain/.virgil/plan 配下に名前をつけて保存してください"
	history := []llm.Message{
		{Role: "user", Content: activeInstruction},
	}
	for i := 0; i < 12; i++ {
		history = append(history, llm.Message{
			Role:    "assistant",
			Content: fmt.Sprintf("work-%02d %s", i, strings.Repeat("context ", 100)),
		})
	}

	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{Message: llm.Message{Role: "assistant", Content: "compressed assistant work"}},
			{Message: llm.Message{Role: "assistant", Content: "done"}},
		},
	}
	agentInst := New(mockLLM, tools.NewRegistry())

	resp, err := agentInst.RunWithOptions(context.Background(), history, "continue task", RunOptions{
		PreflightShrink:                   true,
		ContextLimitTokens:                100,
		PreflightShrinkPercent:            1,
		PreflightShrinkCooldownIterations: 5,
	})
	if err != nil {
		t.Fatalf("RunWithOptions error = %v", err)
	}
	if resp.FinalContent != "done" {
		t.Fatalf("FinalContent = %q, want done", resp.FinalContent)
	}
	if mockLLM.callCount != 2 {
		t.Fatalf("LLM calls = %d, want 2", mockLLM.callCount)
	}

	summaryInput := joinMessageContent(mockLLM.requests[0].Messages)
	if strings.Contains(summaryInput, activeInstruction) {
		t.Fatalf("active user instruction should not be summarized:\n%s", summaryInput)
	}
	chatInput := joinMessageContent(mockLLM.requests[1].Messages)
	if !strings.Contains(chatInput, activeInstruction) {
		t.Fatalf("compressed request missing pinned active instruction:\n%s", chatInput)
	}
	if !strings.Contains(chatInput, "compressed assistant work") {
		t.Fatalf("compressed request missing summary:\n%s", chatInput)
	}
}

func TestSplitMessagesForPreflightShrinkKeepsToolPair(t *testing.T) {
	messages := []llm.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "old-1"},
		{Role: "assistant", Content: "old-2"},
		{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Function: llm.FunctionCall{Name: "read_file"}},
				{ID: "call-2", Function: llm.FunctionCall{Name: "search_text"}},
			},
		},
		{Role: "tool", Content: "tool result 1", ToolCallID: "call-1"},
		{Role: "tool", Content: "tool result 2", ToolCallID: "call-2"},
		{Role: "user", Content: "latest"},
	}

	_, older, recent := splitMessagesForPreflightShrink(messages, 3)
	if len(older) == 0 {
		t.Fatal("expected older messages")
	}
	if recent[0].Role != "assistant" || len(recent[0].ToolCalls) != 2 {
		t.Fatalf("recent should start at assistant tool-call message, got %#v", recent[0])
	}
	if recent[1].Role != "tool" || recent[2].Role != "tool" {
		t.Fatalf("recent should keep following tool results, got %#v", recent)
	}
}

func TestLargeEditFilePayloadExecutesAndIsScrubbedFromHistory(t *testing.T) {
	largeLines := make([]interface{}, 0, 10)
	for i := 0; i < 10; i++ {
		largeLines = append(largeLines, fmt.Sprintf("line %d %s", i, strings.Repeat("x", 700)))
	}
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						{
							ID: "call_large_edit",
							Function: llm.FunctionCall{
								Name: "edit_file",
								Arguments: map[string]interface{}{
									"path":       "target.py",
									"start_line": 1,
									"end_line":   1,
									"new_lines":  largeLines,
								},
							},
						},
					},
				},
			},
			{Message: llm.Message{Role: "assistant", Content: "done"}},
		},
	}

	registry := tools.NewRegistry()
	editTool := &dummyTool{name: "edit_file", response: "edited", isMutating: true}
	registry.Register(editTool)
	agentInst := New(mockLLM, registry)

	resp, err := agentInst.RunWithOptions(context.Background(), nil, "large edit", RunOptions{
		MaxIterations:         VMaxIterations,
		AutoConfirmRunCommand: true,
		PreflightShrink:       true,
	})
	if err != nil {
		t.Fatalf("RunWithOptions error = %v", err)
	}
	if resp.FinalContent != "done" {
		t.Fatalf("FinalContent = %q", resp.FinalContent)
	}
	if editTool.calls != 1 {
		t.Fatalf("edit_file calls=%d, want 1", editTool.calls)
	}
	var executed struct {
		NewLines []string `json:"new_lines"`
	}
	if err := json.Unmarshal(editTool.lastArgs, &executed); err != nil {
		t.Fatalf("failed to unmarshal executed args: %v", err)
	}
	if len(executed.NewLines) != len(largeLines) {
		t.Fatalf("executed new_lines=%d, want %d", len(executed.NewLines), len(largeLines))
	}
	assertHistoryHasScrubbedToolArgument(t, resp.Messages, "edit_file", strings.Join(executed.NewLines, "\n"), func(t *testing.T, args map[string]interface{}, raw string) {
		t.Helper()
		got, ok := args["new_lines"].([]interface{})
		if !ok || len(got) != 1 || !strings.Contains(fmt.Sprint(got[0]), "discarded") {
			t.Fatalf("new_lines was not scrubbed: %#v", args)
		}
		if strings.Contains(fmt.Sprint(got), raw) {
			t.Fatalf("new_lines retained raw payload")
		}
	})
}

func TestLargeEditWithPatternPayloadExecutesAndIsScrubbedFromHistory(t *testing.T) {
	largeReplacement := strings.Repeat("x", 12000)
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						testToolCallWithArgs("edit_with_pattern", map[string]interface{}{
							"path":         "target.py",
							"find_text":    "old",
							"replace_with": largeReplacement,
						}),
					},
				},
			},
			{Message: llm.Message{Role: "assistant", Content: "done"}},
		},
	}
	registry := tools.NewRegistry()
	editTool := &dummyTool{name: "edit_with_pattern", response: "edited", isMutating: true}
	registry.Register(editTool)
	agentInst := New(mockLLM, registry)

	resp, err := agentInst.Run(context.Background(), nil, "large edit")
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if resp.FinalContent != "done" {
		t.Fatalf("FinalContent = %q", resp.FinalContent)
	}
	if editTool.calls != 1 {
		t.Fatalf("edit_with_pattern calls=%d, want 1", editTool.calls)
	}
	var executed struct {
		ReplaceWith string `json:"replace_with"`
	}
	if err := json.Unmarshal(editTool.lastArgs, &executed); err != nil {
		t.Fatalf("failed to unmarshal executed args: %v", err)
	}
	if executed.ReplaceWith != largeReplacement {
		t.Fatalf("tool execution did not receive raw replacement")
	}
	assertHistoryHasScrubbedToolArgument(t, resp.Messages, "edit_with_pattern", largeReplacement, func(t *testing.T, args map[string]interface{}, raw string) {
		t.Helper()
		got, ok := args["replace_with"].(string)
		if !ok || !strings.Contains(got, "discarded") {
			t.Fatalf("replace_with was not scrubbed: %#v", args)
		}
		if strings.Contains(got, raw) {
			t.Fatalf("replace_with retained raw payload")
		}
	})
}

func TestRepeatedOmittedPlaceholderSafetyFailuresEscalate(t *testing.T) {
	placeholder := tools.OmittedToolArgumentMarker + " different payload"
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						{
							ID: "call_omitted_1",
							Function: llm.FunctionCall{
								Name: "edit_file",
								Arguments: map[string]interface{}{
									"path":       "target.py",
									"start_line": 1,
									"end_line":   1,
									"new_lines":  placeholder + " one",
								},
							},
						},
					},
				},
			},
			{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						{
							ID: "call_omitted_2",
							Function: llm.FunctionCall{
								Name: "edit_file",
								Arguments: map[string]interface{}{
									"path":       "target.py",
									"start_line": 2,
									"end_line":   2,
									"new_lines":  placeholder + " two",
								},
							},
						},
					},
				},
			},
			{Message: llm.Message{Role: "assistant", Content: "stopping after repeated safety guard"}},
		},
	}

	registry := tools.NewRegistry()
	editTool := &dummyTool{name: "edit_file", response: "should not run", isMutating: true}
	registry.Register(editTool)
	agentInst := New(mockLLM, registry)

	resp, err := agentInst.RunWithOptions(context.Background(), nil, "repeat omitted", RunOptions{MaxIterations: 5})
	if err != nil {
		t.Fatalf("RunWithOptions error = %v", err)
	}
	if editTool.calls != 0 {
		t.Fatalf("edit_file should have been blocked before execution, calls=%d", editTool.calls)
	}
	if resp.WatchdogStop == nil {
		t.Fatal("expected watchdog stop after repeated semantic safety failures")
	}
	if resp.WatchdogStop.Reason != StopReasonLoopDetected {
		t.Fatalf("reason = %s, want %s", resp.WatchdogStop.Reason, StopReasonLoopDetected)
	}
	if !strings.Contains(resp.WatchdogStop.Detail, "omitted_tool_argument") {
		t.Fatalf("unexpected watchdog detail: %s", resp.WatchdogStop.Detail)
	}
	if resp.FinalContent != "stopping after repeated safety guard" {
		t.Fatalf("FinalContent = %q", resp.FinalContent)
	}
}

func TestCheckerUnavailableHiddenForRestOfRun(t *testing.T) {
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						{
							ID: "call_checker",
							Function: llm.FunctionCall{
								Name: "check_typescript",
								Arguments: map[string]interface{}{
									"path": ".",
								},
							},
						},
					},
				},
			},
			{Message: llm.Message{Role: "assistant", Content: "reported unavailable checker"}},
		},
	}

	registry := tools.NewRegistry()
	checker := &dummyTool{
		name:     "check_typescript",
		response: "typescript checker not found",
		isError:  true,
		metadata: map[string]interface{}{
			"checker_unavailable": true,
		},
	}
	registry.Register(checker)
	registry.Register(&dummyTool{name: "run_tests", response: "ok"})
	agentInst := New(mockLLM, registry)

	resp, err := agentInst.RunWithOptions(context.Background(), nil, "check ts", RunOptions{MaxIterations: 3})
	if err != nil {
		t.Fatalf("RunWithOptions error = %v", err)
	}
	if resp.FinalContent != "reported unavailable checker" {
		t.Fatalf("FinalContent = %q", resp.FinalContent)
	}
	if len(mockLLM.requests) < 2 {
		t.Fatalf("expected second request after checker failure")
	}
	secondTools := map[string]bool{}
	for _, def := range mockLLM.requests[1].Tools {
		secondTools[def.Function.Name] = true
	}
	if secondTools["check_typescript"] {
		t.Fatalf("unavailable checker should be hidden in second request: %#v", secondTools)
	}
	if !secondTools["run_tests"] {
		t.Fatalf("run_tests should remain available after checker unavailable: %#v", secondTools)
	}
	if len(resp.ToolCalls) == 0 || !strings.Contains(resp.ToolCalls[0].Result.Content, "hidden for the rest of this run") {
		t.Fatalf("checker result did not explain hiding: %#v", resp.ToolCalls)
	}
}

func TestRunTaskUsesTemplatePromptAndHistory(t *testing.T) {
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{Message: llm.Message{Role: "assistant", Content: "TODO:\n1. [x] 確認する\n\n## 結果報告\n完了"}},
		},
	}

	agent := New(mockLLM, tools.NewRegistry())
	agent.SetWorkspaceRoot(filepath.Join("home", "agent", "src", "virgil"))

	resp, err := agent.RunTask(context.Background(), []llm.Message{
		{Role: "system", Content: "old system prompt"},
		{Role: "assistant", Content: "prior context"},
	}, "internal/agent/agent.go を確認して")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.FinalContent == "" {
		t.Fatal("expected final content")
	}

	req := mockLLM.requests[0]
	if len(req.Messages) < 3 {
		t.Fatalf("expected system, history, and user messages, got %d", len(req.Messages))
	}
	if !strings.Contains(req.Messages[0].Content, "TODO list") {
		t.Fatalf("system prompt does not include task template: %q", req.Messages[0].Content)
	}
	if strings.Contains(req.Messages[0].Content, "old system prompt") {
		t.Fatal("task prompt should replace the existing conversation system prompt")
	}
	if !strings.Contains(req.Messages[0].Content, SystemPromptModeEdit) {
		t.Fatal("task prompt should include edit mode instructions by default")
	}
	if req.Messages[1].Content != "prior context" {
		t.Fatalf("history was not preserved: %#v", req.Messages)
	}
	if req.Messages[2].Content != "internal/agent/agent.go を確認して" {
		t.Fatalf("user task message = %q", req.Messages[2].Content)
	}
}

func TestRunTaskIncludesPromptAppend(t *testing.T) {
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{Message: llm.Message{Role: "assistant", Content: "TODO:\n1. [x] 確認する\n\n## 結果報告\n完了"}},
		},
	}

	agent := New(mockLLM, tools.NewRegistry())
	agent.SetSystemPrompt(SystemPromptWithAppend(SystemPromptDefault, "custom planning rule"))

	if _, err := agent.RunTask(context.Background(), nil, "計画書を作成して"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := mockLLM.requests[0]
	if !strings.Contains(req.Messages[0].Content, promptAppendSectionTitle) {
		t.Fatalf("task prompt missing prompt append section:\n%s", req.Messages[0].Content)
	}
	if !strings.Contains(req.Messages[0].Content, "custom planning rule") {
		t.Fatalf("task prompt missing custom prompt append:\n%s", req.Messages[0].Content)
	}
}

func TestRunTaskContinuesWhenModelOnlyReturnsTodoList(t *testing.T) {
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{Message: llm.Message{Role: "assistant", Content: "TODO:\n1. [ ] タスクを理解する\n2. [ ] 動作を確認する"}},
			{Message: llm.Message{Role: "assistant", Content: "TODO:\n1. [x] タスクを理解する\n2. [x] 動作を確認する\n\n## 結果報告\n完了"}},
		},
	}

	agent := New(mockLLM, tools.NewRegistry())
	resp, err := agent.RunTask(context.Background(), nil, "小さなテストを追加して")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mockLLM.callCount != 2 {
		t.Fatalf("LLM calls = %d, want 2", mockLLM.callCount)
	}
	if !strings.Contains(resp.FinalContent, "## 結果報告") {
		t.Fatalf("final content = %q, want result report", resp.FinalContent)
	}

	secondReq := mockLLM.requests[1]
	foundContinuePrompt := false
	for _, msg := range secondReq.Messages {
		if msg.Role == "user" && strings.Contains(msg.Content, "stopped after outputting only a TODO list") {
			foundContinuePrompt = true
			break
		}
	}
	if !foundContinuePrompt {
		t.Fatalf("second request did not include continue prompt: %#v", secondReq.Messages)
	}
}

func TestRunTaskRequiresSavedArtifactWhenRequested(t *testing.T) {
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{Message: llm.Message{Role: "assistant", Content: "TODO:\n1. [x] 調査\n\n## 結果報告\n計画しました"}},
			{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						testToolCallWithArgs("write_file", map[string]interface{}{
							"path":    "train/.virgil/plan/ae_scenario_test_plan.md",
							"content": "# Plan\n",
						}),
					},
				},
			},
			{Message: llm.Message{Role: "assistant", Content: "TODO:\n1. [x] 保存\n\n## 結果報告\n保存しました"}},
		},
	}

	registry := tools.NewRegistry()
	writeTool := &dummyTool{name: "write_file", response: "written", isMutating: true}
	registry.Register(writeTool)
	agent := New(mockLLM, registry)

	resp, err := agent.RunTask(context.Background(), nil, "プランは計画書としてtrain/.virgil/plan 配下に名前をつけて保存してください。具体的なコードは書かないでください。")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mockLLM.callCount != 3 {
		t.Fatalf("LLM calls = %d, want 3", mockLLM.callCount)
	}
	if writeTool.calls != 1 {
		t.Fatalf("write_file calls=%d, want 1", writeTool.calls)
	}
	if !strings.Contains(resp.FinalContent, "保存しました") {
		t.Fatalf("final content = %q", resp.FinalContent)
	}

	secondReq := mockLLM.requests[1]
	foundSavePrompt := false
	for _, msg := range secondReq.Messages {
		if msg.Role == "user" && strings.Contains(msg.Content, "Save the requested artifact") {
			foundSavePrompt = true
			break
		}
	}
	if !foundSavePrompt {
		t.Fatalf("second request did not include save-artifact prompt: %#v", secondReq.Messages)
	}
}

func TestTaskRequiresSavedArtifact(t *testing.T) {
	if !taskRequiresSavedArtifact("プランは計画書としてtrain/.virgil/plan 配下に名前をつけて保存してください") {
		t.Fatal("expected saved artifact requirement")
	}
	if taskRequiresSavedArtifact("実使用シナリオテストのプランニングをしてください。具体的なコードは書かないでください") {
		t.Fatal("plain planning request should not require saved artifact")
	}
}

func TestAgentSingleToolCall(t *testing.T) {
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			// 1回目: ツール呼び出し
			{
				Message: llm.Message{
					Role:    "assistant",
					Content: "",
					ToolCalls: []llm.ToolCall{
						{
							ID: "call_1",
							Function: llm.FunctionCall{
								Name:      "test_tool",
								Arguments: map[string]interface{}{},
							},
						},
					},
				},
			},
			// 2回目: 最終応答
			{Message: llm.Message{Role: "assistant", Content: "Done!"}},
		},
	}

	registry := tools.NewRegistry()
	registry.Register(&dummyTool{name: "test_tool", response: "tool result"})

	agent := New(mockLLM, registry)
	resp, err := agent.Run(context.Background(), nil, "Use the tool")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.FinalContent != "Done!" {
		t.Errorf("expected 'Done!', got %q", resp.FinalContent)
	}
	if resp.Iterations != 2 {
		t.Errorf("expected 2 iterations, got %d", resp.Iterations)
	}
	if len(resp.ToolCalls) != 1 {
		t.Errorf("expected 1 tool call record, got %d", len(resp.ToolCalls))
	}
}

func TestInlineToolCallMarkupIsExecuted(t *testing.T) {
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{
				Message: llm.Message{
					Role:    "assistant",
					Content: "I need one more check.\n\n<tool_call> <function=read_file> <parameter=end_line> 151  <parameter=path> src/MAE_pytorch.py  <parameter=start_line> 145   </tool_call>",
				},
			},
			{Message: llm.Message{Role: "assistant", Content: "Done!"}},
		},
	}

	registry := tools.NewRegistry()
	readTool := &dummyTool{name: "read_file", response: "tail"}
	registry.Register(readTool)
	agent := New(mockLLM, registry)

	resp, err := agent.Run(context.Background(), nil, "Use the inline tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.FinalContent != "Done!" {
		t.Fatalf("FinalContent = %q", resp.FinalContent)
	}
	if readTool.calls != 1 {
		t.Fatalf("read_file calls=%d, want 1", readTool.calls)
	}
	var args struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
	}
	if err := json.Unmarshal(readTool.lastArgs, &args); err != nil {
		t.Fatalf("failed to unmarshal read_file args: %v", err)
	}
	if args.Path != "src/MAE_pytorch.py" || args.StartLine != 145 || args.EndLine != 151 {
		t.Fatalf("inline tool args not parsed correctly: %#v", args)
	}
	foundInlineCall := false
	for _, msg := range resp.Messages {
		if strings.Contains(msg.Content, "<tool_call>") {
			t.Fatalf("history retained inline tool markup: %q", msg.Content)
		}
		for _, tc := range msg.ToolCalls {
			if tc.ID == "inline_tool_call_1" && tc.Function.Name == "read_file" {
				foundInlineCall = true
			}
		}
	}
	if !foundInlineCall {
		t.Fatalf("inline tool call was not preserved in history: %#v", resp.Messages)
	}
}

func TestLimitToolCallsAllowsReadOnlyParallelism(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(&dummyTool{name: "read_tool", response: "ok"})
	agent := New(&mockLLM{}, registry)

	calls := make([]llm.ToolCall, 12)
	for i := range calls {
		calls[i] = testToolCall("read_tool")
	}

	limited := agent.limitToolCallsPerIteration(calls)
	if len(limited) != MaxReadOnlyToolCallsPerIteration {
		t.Fatalf("limited tool calls = %d, want %d", len(limited), MaxReadOnlyToolCallsPerIteration)
	}
}

func TestToolDefinitionsSmallProfileFiltersAdvancedTools(t *testing.T) {
	registry := tools.NewRegistry()
	for _, name := range []string{
		"find_symbol",
		"get_file_outline",
		"get_symbol_outline",
		"read_symbol",
		"get_json_outline",
		"read_json_path",
		"get_markdown_outline",
		"read_markdown_section",
		"read_file",
		"search_text",
		"list_files",
		"edit_with_pattern",
		"edit_file",
		"write_file",
		"run_tests",
		"check_python_syntax",
		"check_go_package",
		"check_javascript_syntax",
		"check_typescript",
		"get_call_graph",
		"find_dependents",
		"get_diff_summary",
		"fetch_docs",
		"run_command",
	} {
		if err := registry.Register(&dummyTool{name: name}); err != nil {
			t.Fatalf("Register(%s) error = %v", name, err)
		}
	}

	agent := New(&mockLLM{}, registry)
	agent.SetToolProfile(ToolProfileSmall)
	defs := agent.toolDefinitions()

	got := map[string]bool{}
	for _, def := range defs {
		got[def.Function.Name] = true
	}
	for _, want := range []string{"find_symbol", "get_file_outline", "get_symbol_outline", "read_symbol", "get_json_outline", "read_json_path", "get_markdown_outline", "read_markdown_section", "read_file", "search_text", "list_files", "edit_with_pattern", "edit_file", "write_file", "run_tests", "check_python_syntax", "check_go_package", "check_javascript_syntax", "check_typescript"} {
		if !got[want] {
			t.Fatalf("small profile missing %s; got %#v", want, got)
		}
	}
	for _, hidden := range []string{"get_call_graph", "find_dependents", "get_diff_summary", "fetch_docs", "run_command"} {
		if got[hidden] {
			t.Fatalf("small profile should hide %s; got %#v", hidden, got)
		}
	}
}

func TestBuildSystemPromptMentionsSmallProfile(t *testing.T) {
	agent := New(&mockLLM{}, tools.NewRegistry())
	agent.SetToolProfile(ToolProfileSmall)
	prompt := agent.buildSystemPrompt()
	if !strings.Contains(prompt, "Tool profile: small") {
		t.Fatalf("small profile prompt missing note")
	}
}

func TestSystemPromptWithAppendAddsLocalInstructions(t *testing.T) {
	got := SystemPromptWithAppend("base prompt\n", "\nprefer mermaid diagrams\n")
	for _, want := range []string{
		"base prompt",
		"# Local Environment Instructions",
		"prefer mermaid diagrams",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q:\n%s", want, got)
		}
	}
}

func TestSystemPromptWithAppendIgnoresEmptyExtra(t *testing.T) {
	const base = "base prompt\n"
	if got := SystemPromptWithAppend(base, " \n\t "); got != base {
		t.Fatalf("prompt = %q, want base unchanged %q", got, base)
	}
}

func TestSystemPromptWithAppendFromEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompt.md")
	if err := os.WriteFile(path, []byte("company rule"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIRGIL_PROMPT_APPEND", path)

	got := SystemPromptWithAppendFromEnv("base")
	if !strings.Contains(got, "company rule") {
		t.Fatalf("prompt missing appended content:\n%s", got)
	}
}

func TestSystemPromptWithAppendFromEnvMissingFileKeepsBase(t *testing.T) {
	t.Setenv("VIRGIL_PROMPT_APPEND", filepath.Join(t.TempDir(), "missing.md"))
	if got := SystemPromptWithAppendFromEnv("base"); got != "base" {
		t.Fatalf("prompt = %q, want base", got)
	}
}

func TestBuildSystemPromptIncludesPromptAppend(t *testing.T) {
	agent := New(&mockLLM{}, tools.NewRegistry())
	agent.SetSystemPrompt(SystemPromptWithAppend(SystemPromptDefault, "local instruction"))

	prompt := agent.buildSystemPrompt()
	if !strings.Contains(prompt, "local instruction") {
		t.Fatalf("system prompt missing appended instruction")
	}
}

func TestToolDefinitionsAreCompactedForLLMRequests(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(tools.NewFindSymbolTool(nil)); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(tools.NewSearchTextTool(t.TempDir())); err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(registry.Definitions())
	if err != nil {
		t.Fatal(err)
	}

	agent := New(&mockLLM{}, registry)
	compact, err := json.Marshal(agent.toolDefinitions())
	if err != nil {
		t.Fatal(err)
	}

	if len(compact) >= len(raw) {
		t.Fatalf("compact definitions length = %d, want less than raw %d", len(compact), len(raw))
	}
	if strings.Contains(string(compact), "Equivalent to") {
		t.Fatalf("compact definitions still contain verbose guidance: %s", compact)
	}
	if !strings.Contains(string(compact), "Use before search_text") {
		t.Fatalf("compact definitions lost essential guidance: %s", compact)
	}
}

func TestSystemPromptAndToolDefinitionsAvoidXMLStylePlaceholders(t *testing.T) {
	registry := tools.NewRegistry()
	for _, tool := range []tools.Tool{
		tools.NewReadFileTool(t.TempDir()),
		tools.NewReadSymbolTool(t.TempDir()),
		tools.NewGetFileOutlineTool(t.TempDir()),
		tools.NewGetSymbolOutlineTool(t.TempDir()),
	} {
		if err := registry.Register(tool); err != nil {
			t.Fatal(err)
		}
	}

	agent := New(&mockLLM{}, registry)
	payloads := []string{agent.buildSystemPrompt()}
	defs, err := json.Marshal(agent.toolDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	payloads = append(payloads, string(defs))

	xmlLikePlaceholder := regexp.MustCompile(`<([A-Za-z][A-Za-z0-9_-]*)>`)
	for _, payload := range payloads {
		if xmlLikePlaceholder.MatchString(payload) {
			t.Fatalf("prompt/tool definitions should avoid XML-style placeholders:\n%s", payload)
		}
	}
}

func TestMarkdownToolsArePrioritizedBeforeReadFile(t *testing.T) {
	registry := tools.NewRegistry()
	for _, tool := range []tools.Tool{
		tools.NewReadFileTool(t.TempDir()),
		tools.NewGetMarkdownOutlineTool(t.TempDir()),
		tools.NewReadMarkdownSectionTool(t.TempDir()),
	} {
		if err := registry.Register(tool); err != nil {
			t.Fatal(err)
		}
	}

	agent := New(&mockLLM{}, registry)
	defs := agent.toolDefinitions()
	got := make([]string, 0, len(defs))
	for _, def := range defs {
		got = append(got, def.Function.Name)
	}
	want := []string{"get_markdown_outline", "read_markdown_section", "read_file"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tool order = %v, want %v", got, want)
	}
}

func TestMarkdownToolDescriptionsSteerAwayFromReadFile(t *testing.T) {
	registry := tools.NewRegistry()
	for _, tool := range []tools.Tool{
		tools.NewReadFileTool(t.TempDir()),
		tools.NewGetMarkdownOutlineTool(t.TempDir()),
		tools.NewReadMarkdownSectionTool(t.TempDir()),
	} {
		if err := registry.Register(tool); err != nil {
			t.Fatal(err)
		}
	}

	agent := New(&mockLLM{}, registry)
	descriptions := map[string]string{}
	for _, def := range agent.toolDefinitions() {
		descriptions[def.Function.Name] = def.Function.Description
	}

	if !strings.Contains(descriptions["read_file"], "Do not use without a range for .md files") {
		t.Fatalf("read_file description does not steer Markdown reads: %q", descriptions["read_file"])
	}
	if !strings.Contains(descriptions["get_markdown_outline"], "Use before read_file for Markdown") {
		t.Fatalf("get_markdown_outline description = %q", descriptions["get_markdown_outline"])
	}
	if !strings.Contains(descriptions["read_markdown_section"], "Use instead of read_file for .md sections") {
		t.Fatalf("read_markdown_section description = %q", descriptions["read_markdown_section"])
	}
}

func TestSystemPromptMentionsMarkdownException(t *testing.T) {
	agent := New(&mockLLM{}, tools.NewRegistry())
	prompt := agent.buildSystemPrompt()
	for _, want := range []string{
		"Markdown exception",
		"never call read_file(path) without start_line/end_line for .md documents",
		"read_markdown_section",
		"Do not start with read_file for .md reference documents",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing %q", want)
		}
	}
}

func TestSystemPromptMentionsTargetedEditPolicy(t *testing.T) {
	agent := New(&mockLLM{}, tools.NewRegistry())
	prompt := agent.buildSystemPrompt()
	for _, want := range []string{
		"Targeted Edit Policy",
		"edit_with_pattern directly",
		"exact problematic line",
		"do not read the entire file first",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing %q", want)
		}
	}
}

func TestLimitToolCallsLimitsMutatingTools(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(&dummyTool{name: "write_tool", response: "ok", isMutating: true})
	agent := New(&mockLLM{}, registry)

	calls := make([]llm.ToolCall, 5)
	for i := range calls {
		calls[i] = testToolCall("write_tool")
	}

	limited := agent.limitToolCallsPerIteration(calls)
	if len(limited) != MaxMutatingToolCallsPerIteration {
		t.Fatalf("limited tool calls = %d, want %d", len(limited), MaxMutatingToolCallsPerIteration)
	}
}

func TestLimitToolCallsLimitsReadSymbol(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(&dummyTool{name: "read_symbol", response: "ok"})
	agent := New(&mockLLM{}, registry)

	calls := make([]llm.ToolCall, 5)
	for i := range calls {
		calls[i] = testToolCall("read_symbol")
	}

	limited := agent.limitToolCallsPerIteration(calls)
	if len(limited) != MaxHeavyReadToolCallsPerIteration {
		t.Fatalf("limited tool calls = %d, want %d", len(limited), MaxHeavyReadToolCallsPerIteration)
	}
}

func TestLimitToolCallsAllowsHeavyReadAndNormalReadWithinLimits(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(&dummyTool{name: "read_symbol", response: "ok"})
	registry.Register(&dummyTool{name: "read_tool", response: "ok"})
	agent := New(&mockLLM{}, registry)

	calls := make([]llm.ToolCall, 0, 8)
	for i := 0; i < MaxHeavyReadToolCallsPerIteration; i++ {
		calls = append(calls, testToolCall("read_symbol"))
	}
	for i := 0; i < 5; i++ {
		calls = append(calls, testToolCall("read_tool"))
	}

	limited := agent.limitToolCallsPerIteration(calls)
	if len(limited) != len(calls) {
		t.Fatalf("limited tool calls = %d, want %d", len(limited), len(calls))
	}
}

func TestLimitToolCallsLimitsHeavyReadAndNormalReadSeparately(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(&dummyTool{name: "read_symbol", response: "ok"})
	registry.Register(&dummyTool{name: "read_tool", response: "ok"})
	agent := New(&mockLLM{}, registry)

	calls := make([]llm.ToolCall, 0, 17)
	for i := 0; i < 5; i++ {
		calls = append(calls, testToolCall("read_symbol"))
	}
	for i := 0; i < 12; i++ {
		calls = append(calls, testToolCall("read_tool"))
	}

	limited := agent.limitToolCallsPerIteration(calls)
	want := MaxHeavyReadToolCallsPerIteration + MaxReadOnlyToolCallsPerIteration
	if len(limited) != want {
		t.Fatalf("limited tool calls = %d, want %d", len(limited), want)
	}

	heavy := 0
	normal := 0
	for _, tc := range limited {
		if isHeavyReadToolCall(tc) {
			heavy++
		} else if tc.Function.Name == "read_tool" {
			normal++
		}
	}
	if heavy != MaxHeavyReadToolCallsPerIteration {
		t.Fatalf("heavy read calls = %d, want %d", heavy, MaxHeavyReadToolCallsPerIteration)
	}
	if normal != MaxReadOnlyToolCallsPerIteration {
		t.Fatalf("normal read calls = %d, want %d", normal, MaxReadOnlyToolCallsPerIteration)
	}
}

func TestIsHeavyReadToolCall(t *testing.T) {
	tests := []struct {
		name string
		call llm.ToolCall
		want bool
	}{
		{
			name: "read_symbol full true",
			call: testToolCallWithArgs("read_symbol", map[string]interface{}{"full": true}),
			want: true,
		},
		{
			name: "read_symbol full false",
			call: testToolCallWithArgs("read_symbol", map[string]interface{}{"full": false}),
			want: true,
		},
		{
			name: "read_symbol full missing",
			call: testToolCall("read_symbol"),
			want: true,
		},
		{
			name: "read_file without range is heavy",
			call: testToolCall("read_file"),
			want: true,
		},
		{
			name: "read_file open ended range is heavy",
			call: testToolCallWithArgs("read_file", map[string]interface{}{"path": "a.go", "start_line": 10}),
			want: true,
		},
		{
			name: "read_file narrow explicit range is not heavy",
			call: testToolCallWithArgs("read_file", map[string]interface{}{"path": "a.go", "start_line": 10, "end_line": 40}),
			want: false,
		},
		{
			name: "read_file broad explicit range is heavy",
			call: testToolCallWithArgs("read_file", map[string]interface{}{"path": "a.go", "start_line": 10, "end_line": 120}),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isHeavyReadToolCall(tt.call); got != tt.want {
				t.Fatalf("isHeavyReadToolCall() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLimitToolCallsPreservesOrderAcrossKinds(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(&dummyTool{name: "read_tool", response: "ok"})
	registry.Register(&dummyTool{name: "write_tool", response: "ok", isMutating: true})
	agent := New(&mockLLM{}, registry)

	calls := []llm.ToolCall{
		testToolCall("read_tool"),
		testToolCall("write_tool"),
		testToolCall("read_tool"),
		testToolCall("write_tool"),
		testToolCall("read_tool"),
		testToolCall("write_tool"),
	}

	limited := agent.limitToolCallsPerIteration(calls)
	if len(limited) != 5 {
		t.Fatalf("limited tool calls = %d, want 5", len(limited))
	}
	got := make([]string, 0, len(limited))
	for _, tc := range limited {
		got = append(got, tc.Function.Name)
	}
	want := []string{"read_tool", "write_tool", "read_tool", "write_tool", "read_tool"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tool call order = %v, want %v", got, want)
	}
}

func TestLimitToolCallsAppliesTotalLimitFirst(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(&dummyTool{name: "read_tool", response: "ok"})
	registry.Register(&dummyTool{name: "write_tool", response: "ok", isMutating: true})
	agent := New(&mockLLM{}, registry)

	calls := make([]llm.ToolCall, 0, 20)
	for i := 0; i < 20; i++ {
		if i%5 == 0 {
			calls = append(calls, testToolCall("write_tool"))
			continue
		}
		calls = append(calls, testToolCall("read_tool"))
	}

	limited := agent.limitToolCallsPerIteration(calls)
	if len(limited) != MaxReadOnlyToolCallsPerIteration+MaxMutatingToolCallsPerIteration {
		t.Fatalf("limited tool calls = %d, want %d", len(limited), MaxReadOnlyToolCallsPerIteration+MaxMutatingToolCallsPerIteration)
	}
}

func testToolCall(name string) llm.ToolCall {
	return testToolCallWithArgs(name, map[string]interface{}{})
}

func testToolCallWithArgs(name string, args map[string]interface{}) llm.ToolCall {
	return llm.ToolCall{
		ID: name + "_call",
		Function: llm.FunctionCall{
			Name:      name,
			Arguments: args,
		},
	}
}

func testToolCallWithID(id, name string) llm.ToolCall {
	return llm.ToolCall{
		ID: id,
		Function: llm.FunctionCall{
			Name:      name,
			Arguments: map[string]interface{}{"id": id},
		},
	}
}

func TestCompactToolResultMessagesCompactsOldLargeResultsOnly(t *testing.T) {
	large := strings.Repeat("large result ", 500)
	keepRecent := toolResultCompactionPolicyFor("read_file").KeepRecent
	messages := []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{testToolCallWithID("old_call", "read_file")}},
		{Role: "tool", ToolCallID: "old_call", Content: large},
	}
	for i := 0; i < keepRecent; i++ {
		callID := "recent_call_" + string(rune('a'+i))
		messages = append(messages,
			llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{testToolCallWithID(callID, "read_file")}},
			llm.Message{Role: "tool", ToolCallID: callID, Content: large},
		)
	}

	compacted := compactToolResultMessages(messages)
	if compacted[1].Content == large {
		t.Fatalf("old large tool result was not compacted")
	}
	if !strings.Contains(compacted[1].Content, "[tool result omitted to save context]") {
		t.Fatalf("old result missing compaction marker:\n%s", compacted[1].Content)
	}
	lastTool := compacted[len(compacted)-1]
	if lastTool.Content != large {
		t.Fatalf("recent tool result should remain raw")
	}
	if messages[1].Content != large {
		t.Fatalf("compactToolResultMessages mutated original messages")
	}
}

func TestCompactToolResultMessagesEnforcesTotalBudget(t *testing.T) {
	chunk := strings.Repeat("medium result ", 180)
	messages := []llm.Message{}
	for i := 0; i < 8; i++ {
		callID := "symbol_call_" + strconv.Itoa(i)
		messages = append(messages,
			llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{testToolCallWithID(callID, "read_symbol")}},
			llm.Message{Role: "tool", ToolCallID: callID, Content: chunk},
		)
	}

	compacted := compactToolResultMessages(messages)
	omitted := 0
	raw := 0
	for _, msg := range compacted {
		if msg.Role != "tool" {
			continue
		}
		if strings.Contains(msg.Content, "[tool result omitted to save context]") {
			omitted++
		}
		if msg.Content == chunk {
			raw++
		}
	}
	if omitted == 0 {
		t.Fatalf("expected total-budget compaction to omit older tool results")
	}
	if raw < toolResultMinRawRecent {
		t.Fatalf("raw recent tool results = %d, want at least %d", raw, toolResultMinRawRecent)
	}
	if messages[1].Content != chunk {
		t.Fatalf("compactToolResultMessages mutated original messages")
	}
}

func TestCompactToolResultMessagesCompactsHugeRecentResult(t *testing.T) {
	huge := strings.Repeat("huge result line\n", 3000)
	messages := []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{testToolCallWithID("huge_call", "read_file")}},
		{Role: "tool", ToolCallID: "huge_call", Content: huge},
	}

	compacted := compactToolResultMessages(messages)
	if compacted[1].Content == huge {
		t.Fatal("huge recent tool result remained raw")
	}
	for _, want := range []string{
		"[tool result omitted to save context]",
		"Tool: read_file",
		"Original chars:",
	} {
		if !strings.Contains(compacted[1].Content, want) {
			t.Fatalf("compacted result missing %q:\n%s", want, compacted[1].Content)
		}
	}
	if messages[1].Content != huge {
		t.Fatal("compactToolResultMessages mutated original messages")
	}
}

func TestPrepareMessagesDropsEmptyAssistantMessages(t *testing.T) {
	messages := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "assistant", Content: ""},
		{Role: "assistant", Content: "   "},
		{Role: "assistant", ToolCalls: []llm.ToolCall{testToolCall("read_file")}},
		{Role: "user", Content: "hi"},
	}

	prepared := prepareMessagesForLLMRequest(messages)
	if len(prepared) != 3 {
		t.Fatalf("prepared messages = %d, want 3: %#v", len(prepared), prepared)
	}
	for _, msg := range prepared {
		if msg.Role == "assistant" && strings.TrimSpace(msg.Content) == "" && len(msg.ToolCalls) == 0 {
			t.Fatalf("empty assistant message was not dropped: %#v", prepared)
		}
	}
	if messages[1].Role != "assistant" || messages[1].Content != "" {
		t.Fatalf("prepareMessagesForLLMRequest mutated original messages")
	}
}

func TestPrepareMessagesMergesSystemMessagesAtStart(t *testing.T) {
	messages := []llm.Message{
		{Role: "system", Content: "primary system"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "system", Content: "slash command status"},
		{Role: "user", Content: "next task"},
		{Role: "system", Content: "compressed summary"},
	}

	prepared := prepareMessagesForLLMRequest(messages)
	if len(prepared) != 4 {
		t.Fatalf("prepared messages = %d, want 4: %#v", len(prepared), prepared)
	}
	if prepared[0].Role != "system" {
		t.Fatalf("first message role = %q, want system", prepared[0].Role)
	}
	for _, want := range []string{"primary system", "slash command status", "compressed summary"} {
		if !strings.Contains(prepared[0].Content, want) {
			t.Fatalf("merged system prompt missing %q:\n%s", want, prepared[0].Content)
		}
	}
	for i, msg := range prepared[1:] {
		if msg.Role == "system" {
			t.Fatalf("non-leading system message at prepared[%d]: %#v", i+1, prepared)
		}
	}
	if messages[3].Role != "system" || messages[3].Content != "slash command status" {
		t.Fatalf("prepareMessagesForLLMRequest mutated original messages")
	}
}

func TestCompactToolCallArgumentsOmitsLargeWriteContent(t *testing.T) {
	largeContent := strings.Repeat("report body\n", 300)
	messages := []llm.Message{
		{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{
				testToolCallWithArgs("write_file", map[string]interface{}{
					"path":    "report.md",
					"content": largeContent,
				}),
			},
		},
	}

	prepared := prepareMessagesForLLMRequest(messages)
	got := prepared[0].ToolCalls[0].Function.Arguments["content"].(string)
	for _, want := range []string{
		"[large tool argument omitted before LLM resend]",
		"write_file content omitted from conversation history",
		"Path: report.md",
		"Original chars:",
		"SHA256:",
		"read_file with this path",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("compacted argument missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Preview:") {
		t.Fatalf("compacted argument should not include a payload preview:\n%s", got)
	}
	if strings.Contains(got, strings.Repeat("report body\n", 100)) {
		t.Fatalf("compacted argument retained too much content:\n%s", got)
	}
	if messages[0].ToolCalls[0].Function.Arguments["content"] != largeContent {
		t.Fatalf("prepareMessagesForLLMRequest mutated original tool call args")
	}
}

func TestCompactToolCallArgumentsKeepsSmallWriteContent(t *testing.T) {
	messages := []llm.Message{
		{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{
				testToolCallWithArgs("write_file", map[string]interface{}{
					"path":    "small.md",
					"content": "short",
				}),
			},
		},
	}

	prepared := prepareMessagesForLLMRequest(messages)
	if got := prepared[0].ToolCalls[0].Function.Arguments["content"]; got != "short" {
		t.Fatalf("small content should remain raw, got %#v", got)
	}
}

func TestCompactToolCallArgumentsOmitsLargeEditArguments(t *testing.T) {
	largeReplacement := strings.Repeat("new line\n", 300)
	messages := []llm.Message{
		{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{
				testToolCallWithArgs("edit_with_pattern", map[string]interface{}{
					"path":         "report.md",
					"find_text":    "old",
					"replace_with": largeReplacement,
				}),
				testToolCallWithArgs("edit_file", map[string]interface{}{
					"path":       "report.md",
					"start_line": 1,
					"end_line":   2,
					"new_lines":  []interface{}{largeReplacement, largeReplacement},
				}),
			},
		},
	}

	prepared := prepareMessagesForLLMRequest(messages)
	replaceWith := prepared[0].ToolCalls[0].Function.Arguments["replace_with"].(string)
	if !strings.Contains(replaceWith, "Tool: edit_with_pattern") || !strings.Contains(replaceWith, "Field: replace_with") {
		t.Fatalf("edit_with_pattern replacement was not compacted:\n%s", replaceWith)
	}
	if strings.Contains(replaceWith, "Preview:") {
		t.Fatalf("edit_with_pattern replacement should not include a payload preview:\n%s", replaceWith)
	}
	newLines := prepared[0].ToolCalls[1].Function.Arguments["new_lines"].(string)
	if !strings.Contains(newLines, "Tool: edit_file") || !strings.Contains(newLines, "Field: new_lines") {
		t.Fatalf("edit_file new_lines was not compacted:\n%s", newLines)
	}
	if strings.Contains(newLines, "Preview:") {
		t.Fatalf("edit_file new_lines should not include a payload preview:\n%s", newLines)
	}
}

func TestLargeToolArgumentsAreScrubbedFromHistoryAfterExecution(t *testing.T) {
	for _, tt := range []struct {
		name string
		opts RunOptions
	}{
		{name: "normal", opts: RunOptions{MaxIterations: 3}},
		{name: "vmax", opts: RunOptions{MaxIterations: VMaxIterations, AutoConfirmRunCommand: true, PreflightShrink: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			largeContent := strings.Repeat("generated report body\n", 120)
			mockLLM := &mockLLM{
				responses: []llm.ChatResponse{
					{
						Message: llm.Message{
							Role: "assistant",
							ToolCalls: []llm.ToolCall{
								testToolCallWithArgs("write_file", map[string]interface{}{
									"path":    "report.md",
									"content": largeContent,
								}),
							},
						},
					},
					{Message: llm.Message{Role: "assistant", Content: "done"}},
				},
			}

			registry := tools.NewRegistry()
			writeTool := &dummyTool{name: "write_file", response: "written", isMutating: true}
			registry.Register(writeTool)
			agentInst := New(mockLLM, registry)

			resp, err := agentInst.RunWithOptions(context.Background(), nil, "write report", tt.opts)
			if err != nil {
				t.Fatalf("RunWithOptions error = %v", err)
			}
			if resp.FinalContent != "done" {
				t.Fatalf("FinalContent = %q", resp.FinalContent)
			}
			if writeTool.calls != 1 {
				t.Fatalf("write_file calls=%d, want 1", writeTool.calls)
			}
			var executedArgs struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal(writeTool.lastArgs, &executedArgs); err != nil {
				t.Fatalf("failed to unmarshal executed args: %v", err)
			}
			if executedArgs.Content != largeContent {
				t.Fatalf("tool execution did not receive raw content")
			}

			assertHistoryHasScrubbedWriteContent(t, resp.Messages, largeContent)
			if len(mockLLM.requests) < 2 {
				t.Fatalf("expected second request after write_file")
			}
			assertHistoryHasScrubbedWriteContent(t, mockLLM.requests[1].Messages, largeContent)
		})
	}
}

func TestOmittedWriteFileArgumentDoesNotRequireStructuralRead(t *testing.T) {
	placeholder := tools.OmittedToolArgumentMarker + "\nwrite_file content omitted from conversation history"
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						testToolCallWithArgs("write_file", map[string]interface{}{
							"path":    "report.md",
							"content": placeholder,
						}),
					},
				},
			},
			{Message: llm.Message{Role: "assistant", Content: "regenerated next"}},
		},
	}

	registry := tools.NewRegistry()
	writeTool := &dummyTool{name: "write_file", response: "should not run", isMutating: true}
	readTool := &dummyTool{name: "read_file", response: "current", isMutating: false}
	registry.Register(writeTool)
	registry.Register(readTool)
	agentInst := New(mockLLM, registry)

	resp, err := agentInst.RunWithOptions(context.Background(), nil, "write report", RunOptions{MaxIterations: 3})
	if err != nil {
		t.Fatalf("RunWithOptions error = %v", err)
	}
	if writeTool.calls != 0 {
		t.Fatalf("write_file should not execute omitted placeholder, calls=%d", writeTool.calls)
	}
	if readTool.calls != 0 {
		t.Fatalf("write_file placeholder should not force structural read, read_file calls=%d", readTool.calls)
	}
	if resp.FinalContent != "regenerated next" {
		t.Fatalf("FinalContent = %q", resp.FinalContent)
	}
	if len(resp.ToolCalls) == 0 || resp.ToolCalls[0].Result == nil || !resp.ToolCalls[0].Result.IsError {
		t.Fatalf("expected blocked write_file tool result, got %#v", resp.ToolCalls)
	}
	if !strings.Contains(resp.ToolCalls[0].Result.Content, "Regenerate the full content") {
		t.Fatalf("unexpected block message: %s", resp.ToolCalls[0].Result.Content)
	}
	if len(mockLLM.requests) < 2 {
		t.Fatalf("expected second request after blocked placeholder")
	}
	if got := joinMessageContent(mockLLM.requests[1].Messages); strings.Contains(got, "Recovery step required") {
		t.Fatalf("write_file placeholder should not add structural recovery prompt:\n%s", got)
	}
}

func TestRawArgsToolCallIsNormalizedBeforeExecutionAndHistory(t *testing.T) {
	rawArgs := `{"path":"report.md","content":"hello\nworld","mode":"append"}`
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						testToolCallWithArgs("write_file", map[string]interface{}{
							"raw_args": rawArgs,
						}),
					},
				},
			},
			{Message: llm.Message{Role: "assistant", Content: "done"}},
		},
	}

	registry := tools.NewRegistry()
	writeTool := &dummyTool{name: "write_file", response: "written", isMutating: true}
	registry.Register(writeTool)
	agentInst := New(mockLLM, registry)

	resp, err := agentInst.RunWithOptions(context.Background(), nil, "write report", RunOptions{MaxIterations: 3})
	if err != nil {
		t.Fatalf("RunWithOptions error = %v", err)
	}
	if resp.FinalContent != "done" {
		t.Fatalf("FinalContent = %q", resp.FinalContent)
	}
	if writeTool.calls != 1 {
		t.Fatalf("write_file calls=%d, want 1", writeTool.calls)
	}
	var executed struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Mode    string `json:"mode"`
	}
	if err := json.Unmarshal(writeTool.lastArgs, &executed); err != nil {
		t.Fatalf("failed to unmarshal executed args: %v", err)
	}
	if executed.Path != "report.md" || executed.Content != "hello\nworld" || executed.Mode != "append" {
		t.Fatalf("raw_args was not normalized before execution: %#v", executed)
	}
	for _, messages := range [][]llm.Message{resp.Messages, mockLLM.requests[1].Messages} {
		found := false
		for _, msg := range messages {
			for _, tc := range msg.ToolCalls {
				if tc.Function.Name != "write_file" {
					continue
				}
				found = true
				if _, ok := tc.Function.Arguments["raw_args"]; ok {
					t.Fatalf("history retained raw_args: %#v", tc.Function.Arguments)
				}
				if got := tc.Function.Arguments["path"]; got != "report.md" {
					t.Fatalf("history path = %#v, want report.md", got)
				}
			}
		}
		if !found {
			t.Fatalf("write_file tool call missing from history")
		}
	}
}

func TestLargeEditArgumentsUnderSafetyLimitAreScrubbedFromHistoryAfterExecution(t *testing.T) {
	for _, tt := range []struct {
		name           string
		toolName       string
		args           map[string]interface{}
		assertScrubbed func(t *testing.T, args map[string]interface{}, raw string)
		assertExecuted func(t *testing.T, raw json.RawMessage, expected string)
		rawPayload     string
	}{
		{
			name:       "edit_with_pattern",
			toolName:   "edit_with_pattern",
			rawPayload: strings.Repeat("replacement line\n", 100),
			args: map[string]interface{}{
				"path":      "target.py",
				"find_text": "old",
			},
			assertScrubbed: func(t *testing.T, args map[string]interface{}, raw string) {
				t.Helper()
				got, ok := args["replace_with"].(string)
				if !ok || !strings.Contains(got, "discarded") {
					t.Fatalf("replace_with was not scrubbed: %#v", args)
				}
				if strings.Contains(got, raw) {
					t.Fatalf("replace_with retained raw payload")
				}
			},
			assertExecuted: func(t *testing.T, raw json.RawMessage, expected string) {
				t.Helper()
				var got struct {
					ReplaceWith string `json:"replace_with"`
				}
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatalf("failed to unmarshal executed args: %v", err)
				}
				if got.ReplaceWith != expected {
					t.Fatalf("tool execution did not receive raw replacement")
				}
			},
		},
		{
			name:       "edit_file",
			toolName:   "edit_file",
			rawPayload: strings.Repeat("new line body\n", 120),
			args: map[string]interface{}{
				"path":       "target.py",
				"start_line": 10,
				"end_line":   12,
			},
			assertScrubbed: func(t *testing.T, args map[string]interface{}, raw string) {
				t.Helper()
				got, ok := args["new_lines"].([]interface{})
				if !ok || len(got) != 1 || !strings.Contains(fmt.Sprint(got[0]), "discarded") {
					t.Fatalf("new_lines was not scrubbed: %#v", args)
				}
				if strings.Contains(fmt.Sprint(got), raw) {
					t.Fatalf("new_lines retained raw payload")
				}
			},
			assertExecuted: func(t *testing.T, raw json.RawMessage, expected string) {
				t.Helper()
				var got struct {
					NewLines []string `json:"new_lines"`
				}
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatalf("failed to unmarshal executed args: %v", err)
				}
				if len(got.NewLines) != 1 || got.NewLines[0] != expected {
					t.Fatalf("tool execution did not receive raw new_lines")
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := cloneMap(tt.args)
			if tt.toolName == "edit_file" {
				args["new_lines"] = []interface{}{tt.rawPayload}
			} else {
				args["replace_with"] = tt.rawPayload
			}
			mockLLM := &mockLLM{
				responses: []llm.ChatResponse{
					{
						Message: llm.Message{
							Role:      "assistant",
							ToolCalls: []llm.ToolCall{testToolCallWithArgs(tt.toolName, args)},
						},
					},
					{Message: llm.Message{Role: "assistant", Content: "done"}},
				},
			}

			registry := tools.NewRegistry()
			editTool := &dummyTool{name: tt.toolName, response: "edited", isMutating: true}
			registry.Register(editTool)
			agentInst := New(mockLLM, registry)

			resp, err := agentInst.RunWithOptions(context.Background(), nil, "edit target", RunOptions{MaxIterations: 3})
			if err != nil {
				t.Fatalf("RunWithOptions error = %v", err)
			}
			if editTool.calls != 1 {
				t.Fatalf("%s calls=%d, want 1", tt.toolName, editTool.calls)
			}
			tt.assertExecuted(t, editTool.lastArgs, tt.rawPayload)
			assertHistoryHasScrubbedToolArgument(t, resp.Messages, tt.toolName, tt.rawPayload, tt.assertScrubbed)
			if len(mockLLM.requests) < 2 {
				t.Fatalf("expected second request after %s", tt.toolName)
			}
			assertHistoryHasScrubbedToolArgument(t, mockLLM.requests[1].Messages, tt.toolName, tt.rawPayload, tt.assertScrubbed)
		})
	}
}

func assertHistoryHasScrubbedToolArgument(t *testing.T, messages []llm.Message, toolName string, rawPayload string, assertScrubbed func(*testing.T, map[string]interface{}, string)) {
	t.Helper()
	found := false
	for _, msg := range messages {
		for _, tc := range msg.ToolCalls {
			if tc.Function.Name != toolName {
				continue
			}
			found = true
			if _, ok := tc.Function.Arguments["_discarded_tool_arguments"]; !ok {
				t.Fatalf("%s history missing discard metadata: %#v", toolName, tc.Function.Arguments)
			}
			assertScrubMetadata(t, tc.Function.Arguments, rawPayload)
			assertScrubbed(t, tc.Function.Arguments, rawPayload)
		}
	}
	if !found {
		t.Fatalf("%s tool call not found in history", toolName)
	}
}

func assertHistoryHasScrubbedWriteContent(t *testing.T, messages []llm.Message, rawContent string) {
	t.Helper()
	found := false
	for _, msg := range messages {
		for _, tc := range msg.ToolCalls {
			if tc.Function.Name != "write_file" {
				continue
			}
			found = true
			content, ok := tc.Function.Arguments["content"].(string)
			if !ok {
				t.Fatalf("write_file content missing from scrubbed history: %#v", tc.Function.Arguments)
			}
			if strings.Contains(content, rawContent) {
				t.Fatalf("write_file history retained raw content")
			}
			for _, want := range []string{
				tools.OmittedToolArgumentMarker,
				"write_file content omitted from conversation history",
				"SHA256:",
				"read_file with this path",
			} {
				if !strings.Contains(content, want) {
					t.Fatalf("write_file history content missing %q: %q", want, content)
				}
			}
			if _, ok := tc.Function.Arguments["_discarded_tool_arguments"]; !ok {
				t.Fatalf("write_file history missing discard metadata: %#v", tc.Function.Arguments)
			}
			assertScrubMetadata(t, tc.Function.Arguments, rawContent)
		}
	}
	if !found {
		t.Fatalf("write_file tool call not found in history")
	}
}

func assertScrubMetadata(t *testing.T, args map[string]interface{}, rawPayload string) {
	t.Helper()
	metadata, ok := args["_scrubbed_tool_arguments"].([]map[string]interface{})
	if !ok || len(metadata) == 0 {
		t.Fatalf("scrub metadata missing: %#v", args)
	}
	found := false
	for _, item := range metadata {
		if item["original_chars"] == len(rawPayload) {
			found = true
		}
		if _, ok := item["sha256"].(string); !ok {
			t.Fatalf("scrub metadata missing sha256: %#v", item)
		}
		if _, ok := item["field"].(string); !ok {
			t.Fatalf("scrub metadata missing field: %#v", item)
		}
		if _, ok := item["reason"].(string); !ok {
			t.Fatalf("scrub metadata missing reason: %#v", item)
		}
		if _, ok := item["original_lines"].(int); !ok {
			t.Fatalf("scrub metadata missing original_lines: %#v", item)
		}
	}
	if !found {
		t.Fatalf("scrub metadata did not record raw payload size %d: %#v", len(rawPayload), metadata)
	}
}

func TestRunBtwScrubsLargeToolArgumentsFromHistory(t *testing.T) {
	largeContent := strings.Repeat("btw generated body\n", 120)
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						testToolCallWithArgs("write_file", map[string]interface{}{
							"path":    "btw.md",
							"content": largeContent,
						}),
					},
				},
			},
			{Message: llm.Message{Role: "assistant", Content: "blocked"}},
		},
	}

	registry := tools.NewRegistry()
	writeTool := &dummyTool{name: "write_file", response: "should not run", isMutating: true}
	registry.Register(writeTool)
	agentInst := New(mockLLM, registry)

	resp, err := agentInst.RunBtw(context.Background(), nil, "try writing")
	if err != nil {
		t.Fatalf("RunBtw error = %v", err)
	}
	if resp.FinalContent != "blocked" {
		t.Fatalf("FinalContent = %q", resp.FinalContent)
	}
	if writeTool.calls != 0 {
		t.Fatalf("write_file should be blocked in /btw mode, calls=%d", writeTool.calls)
	}
	if len(mockLLM.requests) < 2 {
		t.Fatalf("expected second request after blocked /btw tool call")
	}
	assertHistoryHasScrubbedWriteContent(t, mockLLM.requests[1].Messages, largeContent)
	assertHistoryHasScrubbedWriteContent(t, resp.Messages, largeContent)
}

func TestAgentSendsCompactedToolResultsToNextLLMCall(t *testing.T) {
	large := strings.Repeat("large result ", 500)
	keepRecent := toolResultCompactionPolicyFor("read_file").KeepRecent
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						testToolCallWithID("old_call", "read_file"),
						testToolCallWithID("recent_1", "read_file"),
						testToolCallWithID("recent_2", "read_file"),
						testToolCallWithID("recent_3", "read_file"),
						testToolCallWithID("recent_4", "read_file"),
					},
				},
			},
			{Message: llm.Message{Role: "assistant", Content: "Done"}},
		},
	}

	registry := tools.NewRegistry()
	registry.Register(&dummyTool{name: "read_file", response: large})

	agent := New(mockLLM, registry)
	resp, err := agent.Run(context.Background(), nil, "read files")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.FinalContent != "Done" {
		t.Fatalf("final content = %q", resp.FinalContent)
	}
	if len(mockLLM.requests) != 2 {
		t.Fatalf("llm requests = %d, want 2", len(mockLLM.requests))
	}

	secondRequest := mockLLM.requests[1]
	oldCompacted := false
	rawRecent := 0
	for _, msg := range secondRequest.Messages {
		if msg.Role != "tool" {
			continue
		}
		if msg.ToolCallID == "old_call" && strings.Contains(msg.Content, "[tool result omitted to save context]") {
			oldCompacted = true
		}
		if msg.Content == large {
			rawRecent++
		}
	}
	if !oldCompacted {
		t.Fatalf("old tool result was not compacted in second LLM request")
	}
	if rawRecent > keepRecent || rawRecent < toolResultMinRawRecent {
		t.Fatalf("raw recent tool results = %d, want between %d and %d", rawRecent, toolResultMinRawRecent, keepRecent)
	}
}

func TestCompactSearchTextResultKeepsRepresentativeMatches(t *testing.T) {
	lines := []string{"Found matches for pattern 'needle':", ""}
	for i := 0; i < 20; i++ {
		lines = append(lines, "file_a.py:"+strconv.Itoa(i+1)+": needle")
	}
	lines = append(lines, "file_b.py:1: needle", "file_c.py:1: needle")
	content := strings.Join(lines, "\n") + strings.Repeat(" padding", 1200)

	got := compactToolResultContent("search_text", "call_search", map[string]interface{}{
		"pattern": "needle",
	}, content)

	for _, want := range []string{
		"Tool: search_text",
		"Pattern: needle",
		"Representative match lines:",
		"file_a.py:1: needle",
		"file_b.py:1: needle",
		"file_c.py:1: needle",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("search_text placeholder missing %q:\n%s", want, got)
		}
	}
}

func TestCompactReadFileResultUsesToolArguments(t *testing.T) {
	content := "File: unknown.py (lines 1-999)\n" + strings.Repeat("body\n", 1200)

	got := compactToolResultContent("read_file", "call_read", map[string]interface{}{
		"path":       "src/model.py",
		"start_line": float64(200),
		"end_line":   float64(400),
	}, content)

	for _, want := range []string{
		"Tool: read_file",
		"Path: src/model.py",
		"Lines: 200-400",
		"Compressed code observation:",
		"Suggested next reads: prefer a narrower range than 200-400",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("read_file placeholder missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "unknown.py") && strings.Contains(got, "Lines: 1-999") {
		t.Fatalf("read_file placeholder should prefer tool arguments over result header:\n%s", got)
	}
}

func TestCompactReadSymbolResultKeepsCompressedObservation(t *testing.T) {
	content := strings.Join([]string{
		"Symbol: retrain in model.py",
		"Language: python | Matches: 1",
		"Type: method | Receiver: myMAE | Lines: 10-220 (211 lines) | Signature: def retrain(self):",
		"Mode: FULL (10000 chars, ~2500 tokens)",
		"----------------------------------------",
		"  10 | def retrain(self):",
		"  11 |     for epoch in range(self.epochs):",
		"  12 |         loss = self.optimizer.minimize(self.loss)",
		"  13 |         self.sess.run(loss)",
	}, "\n") + strings.Repeat("\n  14 |         self.sess.run(loss)", 500)

	got := compactToolResultContent("read_symbol", "call_symbol", map[string]interface{}{
		"path":        "model.py",
		"symbol_name": "retrain",
		"receiver":    "myMAE",
	}, content)

	for _, want := range []string{
		"Tool: read_symbol",
		"Path: model.py",
		"Symbol: retrain",
		"Receiver: myMAE",
		"Type: method",
		"Compressed symbol observation:",
		"Important calls:",
		"self.sess.run",
		"Suggested next reads:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("read_symbol placeholder missing %q:\n%s", want, got)
		}
	}
}

func TestCompactToolResultMessagesKeepsMoreRecentSearchResults(t *testing.T) {
	large := strings.Repeat("search result ", 700)
	keepRecent := toolResultCompactionPolicyFor("search_text").KeepRecent
	messages := []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{testToolCallWithID("old_search", "search_text")}},
		{Role: "tool", ToolCallID: "old_search", Content: large},
	}
	for i := 0; i < keepRecent; i++ {
		callID := "recent_search_" + string(rune('a'+i))
		messages = append(messages,
			llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{testToolCallWithID(callID, "search_text")}},
			llm.Message{Role: "tool", ToolCallID: callID, Content: large},
		)
	}

	compacted := compactToolResultMessages(messages)
	if !strings.Contains(compacted[1].Content, "[tool result omitted to save context]") {
		t.Fatalf("old search result was not compacted:\n%s", compacted[1].Content)
	}
	rawRecent := 0
	for _, msg := range compacted {
		if msg.Role == "tool" && msg.Content == large {
			rawRecent++
		}
	}
	if rawRecent > keepRecent || rawRecent < toolResultMinRawRecent {
		t.Fatalf("raw recent search results = %d, want between %d and %d", rawRecent, toolResultMinRawRecent, keepRecent)
	}
}

func TestAgentStopsExplorationAfterSuccessfulVerification(t *testing.T) {
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						{
							ID: "call_tests",
							Function: llm.FunctionCall{
								Name:      "run_tests",
								Arguments: map[string]interface{}{},
							},
						},
					},
				},
			},
			{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						{
							ID: "call_find",
							Function: llm.FunctionCall{
								Name:      "find_symbol",
								Arguments: map[string]interface{}{"name": "Agent"},
							},
						},
					},
				},
			},
		},
	}

	registry := tools.NewRegistry()
	runTests := &dummyTool{name: "run_tests", response: "ok"}
	findSymbol := &dummyTool{name: "find_symbol", response: "should not run"}
	registry.Register(runTests)
	registry.Register(findSymbol)

	agent := New(mockLLM, registry)
	resp, err := agent.Run(context.Background(), nil, "change and test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runTests.calls != 1 {
		t.Fatalf("run_tests calls = %d, want 1", runTests.calls)
	}
	if findSymbol.calls != 0 {
		t.Fatalf("find_symbol calls = %d, want 0", findSymbol.calls)
	}
	if !strings.Contains(resp.FinalContent, "Stopping extra exploration") {
		t.Fatalf("final content = %q", resp.FinalContent)
	}
}

func TestLocalizedResponseHonorsLanguageSetting(t *testing.T) {
	agent := New(&mockLLM{}, tools.NewRegistry())

	if got := agent.localizedResponse("日本語の依頼", "日本語", "English"); got != "日本語" {
		t.Fatalf("auto Japanese response = %q", got)
	}
	if got := agent.localizedResponse("English request", "日本語", "English"); got != "English" {
		t.Fatalf("auto English response = %q", got)
	}
	agent.SetResponseLanguage("ja")
	if got := agent.localizedResponse("English request", "日本語", "English"); got != "日本語" {
		t.Fatalf("forced Japanese response = %q", got)
	}
	agent.SetResponseLanguage("en")
	if got := agent.localizedResponse("日本語の依頼", "日本語", "English"); got != "English" {
		t.Fatalf("forced English response = %q", got)
	}
}

func TestAgentMaxIterations(t *testing.T) {
	// 21回連続でツール呼び出しを返す（無限ループをシミュレート）
	// ウォッチドッグにより3回目で停止するはず
	responses := make([]llm.ChatResponse, 21)
	for i := 0; i < 21; i++ {
		responses[i] = llm.ChatResponse{
			Message: llm.Message{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{
					{
						ID: "call_inf",
						Function: llm.FunctionCall{
							Name:      "test_tool",
							Arguments: map[string]interface{}{},
						},
					},
				},
			},
		}
	}
	// エスカレーション用 (3回目のイテレーション中のRecordToolCallでescalateが呼ばれ、その中のChatで使用される)
	responses[3] = llm.ChatResponse{
		Message: llm.Message{
			Role:    "assistant",
			Content: "Stopped due to loop",
		},
	}

	mockLLM := &mockLLM{responses: responses}
	registry := tools.NewRegistry()
	registry.Register(&dummyTool{name: "test_tool", response: "result"})

	agentInst := New(mockLLM, registry)
	resp, err := agentInst.Run(context.Background(), nil, "Loop")

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	// ウォッチドッグによって3回目で停止することを確認
	if resp.WatchdogStop == nil || resp.WatchdogStop.Reason != StopReasonLoopDetected {
		t.Errorf("expected watchdog loop detection, got %v", resp.WatchdogStop)
	}
	if resp.Iterations != 3 {
		t.Errorf("expected 3 iterations due to watchdog, got %d", resp.Iterations)
	}
	if resp.FinalContent != "Stopped due to loop" {
		t.Errorf("expected 'Stopped due to loop', got %q", resp.FinalContent)
	}
	escalationReq := mockLLM.requests[len(mockLLM.requests)-1]
	if escalationReq.Format != nil {
		t.Fatal("expected escalation request to use normal free-text response")
	}
	lastMessage := escalationReq.Messages[len(escalationReq.Messages)-1].Content
	if strings.Contains(strings.ToLower(lastMessage), "json") {
		t.Fatalf("escalation prompt should not force json: %q", lastMessage)
	}
}

func TestEscalationMergesSystemMessagesForLLMRequest(t *testing.T) {
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{Message: llm.Message{Role: "assistant", Content: "Stopped"}},
		},
	}
	agentInst := New(mockLLM, tools.NewRegistry())
	response := &Response{}
	messages := []llm.Message{
		{Role: "system", Content: "primary system"},
		{Role: "user", Content: "task"},
		{Role: "system", Content: "Previous conversation summary"},
		{Role: "assistant", Content: "working"},
		{Role: "system", Content: "VMAX enabled"},
	}

	resp, err := agentInst.escalate(context.Background(), messages, response, &StopSignal{
		Reason: StopReasonLoopDetected,
		Detail: "test loop",
	})
	if err != nil {
		t.Fatalf("escalate error = %v", err)
	}
	if resp.FinalContent != "Stopped" {
		t.Fatalf("FinalContent = %q, want Stopped", resp.FinalContent)
	}
	if len(mockLLM.requests) != 1 {
		t.Fatalf("LLM requests = %d, want 1", len(mockLLM.requests))
	}
	reqMessages := mockLLM.requests[0].Messages
	if reqMessages[0].Role != "system" {
		t.Fatalf("first message role = %q, want system", reqMessages[0].Role)
	}
	for _, want := range []string{"primary system", "Previous conversation summary", "VMAX enabled"} {
		if !strings.Contains(reqMessages[0].Content, want) {
			t.Fatalf("merged system message missing %q:\n%s", want, reqMessages[0].Content)
		}
	}
	for i, msg := range reqMessages[1:] {
		if msg.Role == "system" {
			t.Fatalf("non-leading system message at request[%d]: %#v", i+1, reqMessages)
		}
	}
}

func TestAgentRecoversAfterFirstEmptyResponse(t *testing.T) {
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{
				Message: llm.Message{Role: "assistant"},
			},
			{
				Message: llm.Message{Role: "assistant", Content: "Recovered"},
			},
		},
	}

	registry := tools.NewRegistry()
	agentInst := New(mockLLM, registry)
	resp, err := agentInst.Run(context.Background(), nil, "Do work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.FinalContent != "Recovered" {
		t.Fatalf("final content = %q", resp.FinalContent)
	}
	if mockLLM.callCount != 2 {
		t.Fatalf("llm calls = %d, want 2", mockLLM.callCount)
	}
	if len(mockLLM.requests) < 2 {
		t.Fatalf("expected recovery request")
	}
	secondReq := mockLLM.requests[1]
	last := secondReq.Messages[len(secondReq.Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "Your previous response was empty") {
		t.Fatalf("last recovery message = %#v", last)
	}
	if !strings.Contains(last.Content, "provide the final answer") {
		t.Fatalf("recovery prompt should allow final answers, got %q", last.Content)
	}
	if !strings.Contains(last.Content, "Do not make edits unless") {
		t.Fatalf("recovery prompt should avoid unsolicited edits, got %q", last.Content)
	}
	if !strings.Contains(last.Content, "waiting for user confirmation") || !strings.Contains(last.Content, "question mark") {
		t.Fatalf("recovery prompt should make confirmation waits explicit, got %q", last.Content)
	}
	for _, msg := range secondReq.Messages {
		if msg.Role == "assistant" && msg.Content == "" && len(msg.ToolCalls) == 0 {
			t.Fatalf("empty assistant message should not be kept in recovery request")
		}
	}
}

func TestAgentStopsOnRepeatedIdenticalToolFailure(t *testing.T) {
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						{
							ID: "call_1",
							Function: llm.FunctionCall{
								Name:      "failing_tool",
								Arguments: map[string]interface{}{"path": "missing.go"},
							},
						},
					},
				},
			},
			{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						{
							ID: "call_2",
							Function: llm.FunctionCall{
								Name:      "failing_tool",
								Arguments: map[string]interface{}{"path": "missing.go"},
							},
						},
					},
				},
			},
			{
				Message: llm.Message{
					Role:    "assistant",
					Content: "Stopped after repeated tool failure",
				},
			},
		},
	}

	registry := tools.NewRegistry()
	registry.Register(&dummyTool{
		name:     "failing_tool",
		response: "failed to stat file",
		isError:  true,
	})

	agentInst := New(mockLLM, registry)
	resp, err := agentInst.Run(context.Background(), nil, "Use the failing tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.WatchdogStop == nil {
		t.Fatal("expected watchdog stop")
	}
	if resp.WatchdogStop.Reason != StopReasonLoopDetected {
		t.Fatalf("watchdog reason = %s, want %s", resp.WatchdogStop.Reason, StopReasonLoopDetected)
	}
	if !strings.Contains(resp.WatchdogStop.Detail, "failed 2 times") {
		t.Fatalf("watchdog detail = %q, want repeated failure detail", resp.WatchdogStop.Detail)
	}
	if resp.Iterations != 2 {
		t.Fatalf("iterations = %d, want 2", resp.Iterations)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("tool call records = %d, want 2", len(resp.ToolCalls))
	}
	if resp.FinalContent != "Stopped after repeated tool failure" {
		t.Fatalf("final content = %q", resp.FinalContent)
	}
}

func TestAgentMutatingToolWithShadow(t *testing.T) {
	// 一時ディレクトリでシャドウgit初期化
	tmpDir, err := os.MkdirTemp("", "agent-shadow-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// ダミーファイル作成
	if err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("v1"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	shadowRepo, err := shadow.New(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := shadowRepo.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// mockLLMで mutating tool を呼び出すレスポンス
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						{
							ID: "call_1",
							Function: llm.FunctionCall{
								Name:      "mutating_tool",
								Arguments: map[string]interface{}{},
							},
						},
					},
				},
			},
			{Message: llm.Message{Role: "assistant", Content: "Done!"}},
		},
	}

	registry := tools.NewRegistry()
	registry.Register(&dummyTool{
		name:       "mutating_tool",
		response:   "ok",
		isMutating: true,
	})

	agentInst := New(mockLLM, registry)
	agentInst.SetShadowRepo(shadowRepo)

	resp, err := agentInst.Run(ctx, nil, "Use the tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}

	record := resp.ToolCalls[0]
	if record.PreCommit == "" {
		t.Error("expected PreCommit to be set")
	}
	if record.PostCommit == "" {
		t.Error("expected PostCommit to be set")
	}
}

func TestAgentBlocksMutatingToolWhenShadowPreCommitFails(t *testing.T) {
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						{
							ID: "call_1",
							Function: llm.FunctionCall{
								Name:      "mutating_tool",
								Arguments: map[string]interface{}{},
							},
						},
					},
				},
			},
			{Message: llm.Message{Role: "assistant", Content: "Done!"}},
		},
	}

	registry := tools.NewRegistry()
	tool := &dummyTool{
		name:       "mutating_tool",
		response:   "ok",
		isMutating: true,
	}
	registry.Register(tool)

	agentInst := New(mockLLM, registry)
	shadow := &failingShadowSnapshotter{}
	agentInst.shadow = shadow

	resp, err := agentInst.Run(context.Background(), nil, "Use the tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tool.calls != 0 {
		t.Fatalf("mutating tool was executed %d times; want blocked before execution", tool.calls)
	}
	if shadow.preCalls != 1 {
		t.Fatalf("pre-commit calls = %d, want 1", shadow.preCalls)
	}
	if shadow.postCalls != 0 {
		t.Fatalf("post-commit calls = %d, want 0", shadow.postCalls)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool call records = %d, want 1", len(resp.ToolCalls))
	}
	record := resp.ToolCalls[0]
	if record.Result == nil || !record.Result.IsError {
		t.Fatalf("record result = %#v, want IsError=true", record.Result)
	}
	if !strings.Contains(record.Result.Content, "shadow snapshot failed") {
		t.Fatalf("result content %q does not mention shadow snapshot failure", record.Result.Content)
	}
}

func TestAgentDoesNotInjectDiffForWriteFile(t *testing.T) {
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						{
							ID: "call_write",
							Function: llm.FunctionCall{
								Name:      "write_file",
								Arguments: map[string]interface{}{"path": "report.md", "content": "hello"},
							},
						},
					},
				},
			},
			{Message: llm.Message{Role: "assistant", Content: "Done"}},
		},
	}

	registry := tools.NewRegistry()
	registry.Register(&dummyTool{
		name:       "write_file",
		response:   "Created report.md (5 bytes, 1 line)",
		isMutating: true,
	})
	shadow := &diffShadowSnapshotter{}
	agentInst := New(mockLLM, registry)
	agentInst.shadow = shadow

	if _, err := agentInst.Run(context.Background(), nil, "write report"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shadow.diffCalls != 0 {
		t.Fatalf("write_file should not request shadow diff, got %d diff calls", shadow.diffCalls)
	}
	if len(mockLLM.requests) < 2 {
		t.Fatalf("expected second LLM request")
	}
	for _, msg := range mockLLM.requests[1].Messages {
		if msg.Role == "tool" && msg.ToolCallID == "call_write" && strings.Contains(msg.Content, "diff --git") {
			t.Fatalf("write_file tool result should not include diff:\n%s", msg.Content)
		}
	}
}

func TestAgentDoesNotInjectDiffForEditWithPattern(t *testing.T) {
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						{
							ID: "call_edit_pattern",
							Function: llm.FunctionCall{
								Name:      "edit_with_pattern",
								Arguments: map[string]interface{}{"path": "report.md", "find_text": "old", "replace_with": "new"},
							},
						},
					},
				},
			},
			{Message: llm.Message{Role: "assistant", Content: "Done"}},
		},
	}

	registry := tools.NewRegistry()
	registry.Register(&dummyTool{
		name:       "edit_with_pattern",
		response:   "Edit applied to report.md (1 replacement, syntax validated)",
		isMutating: true,
	})
	shadow := &diffShadowSnapshotter{}
	agentInst := New(mockLLM, registry)
	agentInst.shadow = shadow

	if _, err := agentInst.Run(context.Background(), nil, "edit report"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shadow.diffCalls != 0 {
		t.Fatalf("edit_with_pattern should not request shadow diff, got %d diff calls", shadow.diffCalls)
	}
	if len(mockLLM.requests) < 2 {
		t.Fatalf("expected second LLM request")
	}
	for _, msg := range mockLLM.requests[1].Messages {
		if msg.Role == "tool" && msg.ToolCallID == "call_edit_pattern" && strings.Contains(msg.Content, "diff --git") {
			t.Fatalf("edit_with_pattern tool result should not include diff:\n%s", msg.Content)
		}
	}
}

func TestAgentBlocksOmittedToolArgumentPlaceholder(t *testing.T) {
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						{
							ID: "call_edit_pattern",
							Function: llm.FunctionCall{
								Name: "edit_with_pattern",
								Arguments: map[string]interface{}{
									"path":         "report.md",
									"find_text":    "old",
									"replace_with": tools.OmittedToolArgumentMarker,
								},
							},
						},
					},
				},
			},
			{Message: llm.Message{Role: "assistant", Content: "Done"}},
			{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						{
							ID: "call_structural_read",
							Function: llm.FunctionCall{
								Name: "read_symbol",
								Arguments: map[string]interface{}{
									"path":        "report.md",
									"symbol_name": "Report",
								},
							},
						},
					},
				},
			},
			{Message: llm.Message{Role: "assistant", Content: "Done after read"}},
		},
	}

	registry := tools.NewRegistry()
	editTool := &dummyTool{
		name:       "edit_with_pattern",
		response:   "should not run",
		isMutating: true,
	}
	readTool := &dummyTool{name: "read_symbol", response: "symbol source", isMutating: false}
	registry.Register(editTool)
	registry.Register(readTool)
	shadow := &diffShadowSnapshotter{}
	agentInst := New(mockLLM, registry)
	agentInst.shadow = shadow

	resp, err := agentInst.Run(context.Background(), nil, "edit report")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if editTool.calls != 0 {
		t.Fatalf("tool should not execute, calls=%d", editTool.calls)
	}
	if shadow.preCalls != 0 || shadow.postCalls != 0 {
		t.Fatalf("shadow should not run for blocked placeholder, pre=%d post=%d", shadow.preCalls, shadow.postCalls)
	}
	if len(resp.ToolCalls) < 1 || resp.ToolCalls[0].Result == nil || !resp.ToolCalls[0].Result.IsError {
		t.Fatalf("expected blocked tool call result, got %#v", resp.ToolCalls)
	}
	if !strings.Contains(resp.ToolCalls[0].Result.Content, "omitted-content placeholder") {
		t.Fatalf("unexpected block message: %s", resp.ToolCalls[0].Result.Content)
	}
}

func TestAgentNonMutatingToolNoShadow(t *testing.T) {
	// 読み取り系ツールではシャドウgitを呼ばないことを確認
	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						{
							ID: "call_1",
							Function: llm.FunctionCall{
								Name:      "readonly_tool",
								Arguments: map[string]interface{}{},
							},
						},
					},
				},
			},
			{Message: llm.Message{Role: "assistant", Content: "Done!"}},
		},
	}

	registry := tools.NewRegistry()
	registry.Register(&dummyTool{
		name:       "readonly_tool",
		response:   "ok",
		isMutating: false,
	})

	agentInst := New(mockLLM, registry)
	// ShadowRepoを設定しない（あえて）

	resp, err := agentInst.Run(context.Background(), nil, "Use the tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	record := resp.ToolCalls[0]
	if record.PreCommit != "" {
		t.Errorf("expected PreCommit to be empty for readonly tool, got %s", record.PreCommit)
	}
	if record.PostCommit != "" {
		t.Errorf("expected PostCommit to be empty for readonly tool, got %s", record.PostCommit)
	}
}

func TestAgentHeuristicMetadata(t *testing.T) {
	longFreetext := "I have completed the investigation. " +
		"I found that the bug was caused by X. " +
		"Should I proceed with the fix?"

	mockLLM := &mockLLM{
		responses: []llm.ChatResponse{
			{
				Message: llm.Message{
					Role:    "assistant",
					Content: longFreetext,
				},
			},
		},
	}

	registry := tools.NewRegistry()
	agentInst := New(mockLLM, registry)

	resp, err := agentInst.Run(context.Background(), nil, "Investigate bug")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 2パス目が走っていないことを確認 (callCountが1)
	if mockLLM.callCount != 1 {
		t.Errorf("expected 1 LLM call (natural Markdown), got %d", mockLLM.callCount)
	}

	// FinalContent がそのまま保持されている
	if resp.FinalContent != longFreetext {
		t.Errorf("expected FinalContent to be original text, got %q", resp.FinalContent)
	}

	// ヒューリスティックで RequestedAction が ask_user になっている
	if resp.Structured.RequestedAction != ActionAskUser {
		t.Errorf("expected ActionAskUser due to question at the end, got %s", resp.Structured.RequestedAction)
	}
}
