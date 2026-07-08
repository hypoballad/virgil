package tui

import (
	"testing"

	"github.com/hypoballad/virgil/internal/agent"
	"github.com/hypoballad/virgil/internal/llm"
	"github.com/hypoballad/virgil/internal/tools"
)

func TestNewModelStartsInPlanMode(t *testing.T) {
	agentInst := agent.New(nil, tools.NewRegistry())
	model := NewModel(agentInst, nil, nil, nil, "session", "/tmp/workspace", "model", "", 12000, 5, 30)

	if !model.planMode {
		t.Fatal("expected model to start in plan mode")
	}
	if !agentInst.IsPlanMode() {
		t.Fatal("expected agent to start in plan mode")
	}
}

func TestSplitHistoryForShrinkKeepsSystemAndRecentMessages(t *testing.T) {
	history := []llm.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "old user 1"},
		{Role: "assistant", Content: "old assistant 1"},
		{Role: "user", Content: "old user 2"},
		{Role: "assistant", Content: "old assistant 2"},
		{Role: "user", Content: "recent user 1"},
		{Role: "assistant", Content: "recent assistant 1"},
		{Role: "user", Content: "recent user 2"},
		{Role: "assistant", Content: "recent assistant 2"},
	}

	base, older, recent := splitHistoryForShrink(history)

	if len(base) != 1 || base[0].Content != "system prompt" {
		t.Fatalf("expected original system prompt to be preserved, got %#v", base)
	}
	if len(older) != 2 {
		t.Fatalf("expected 2 older messages, got %d", len(older))
	}
	if older[0].Content != "old user 1" || older[1].Content != "old assistant 1" {
		t.Fatalf("unexpected older messages: %#v", older)
	}
	if len(recent) != shrinkRecentMessages {
		t.Fatalf("expected %d recent messages, got %d", shrinkRecentMessages, len(recent))
	}
	if recent[0].Content != "old user 2" {
		t.Fatalf("unexpected first recent message: %#v", recent[0])
	}
}

func TestSplitHistoryForShrinkKeepsRecentToolFailure(t *testing.T) {
	history := []llm.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "old user 1"},
		{Role: "assistant", Content: "old assistant 1"},
		{Role: "user", Content: "old user 2"},
		{Role: "assistant", Content: "old assistant 2"},
		{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Function: llm.FunctionCall{Name: "read_file"}},
			},
		},
		{Role: "tool", Content: "path is required", ToolCallID: "call-1"},
		{Role: "user", Content: "recent user 1"},
		{Role: "assistant", Content: "recent assistant 1"},
		{Role: "user", Content: "recent user 2"},
		{Role: "assistant", Content: "recent assistant 2"},
		{Role: "user", Content: "recent user 3"},
		{Role: "assistant", Content: "recent assistant 3"},
		{Role: "user", Content: "latest"},
	}

	_, older, recent := splitHistoryForShrink(history)
	if len(older) == 0 {
		t.Fatal("expected older messages")
	}
	if recent[0].Role != "assistant" || len(recent[0].ToolCalls) != 1 {
		t.Fatalf("recent should start at failed assistant tool-call message, got %#v", recent[0])
	}
	if recent[1].Role != "tool" || recent[1].Content != "path is required" {
		t.Fatalf("recent should keep failed tool result, got %#v", recent)
	}
}

func TestBuildCompressedHistory(t *testing.T) {
	base := []llm.Message{{Role: "system", Content: "system prompt"}}
	recent := []llm.Message{{Role: "user", Content: "latest"}}

	compressed := buildCompressedHistory(base, "summary text", nil, recent)
	if len(compressed) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(compressed))
	}
	if compressed[0].Content != "system prompt" {
		t.Fatalf("system prompt was not first: %#v", compressed[0])
	}
	if compressed[1].Role != "system" || compressed[1].Content == "" {
		t.Fatalf("expected summary system message, got %#v", compressed[1])
	}
	if compressed[2].Content != "latest" {
		t.Fatalf("recent message was not preserved: %#v", compressed[2])
	}
}

func TestBuildCompressedHistoryKeepsPinnedUserInstructions(t *testing.T) {
	base := []llm.Message{{Role: "system", Content: "system prompt"}}
	pinned := []llm.Message{{Role: "user", Content: "絶対に実装ファイルは編集しないでください"}}
	recent := []llm.Message{{Role: "assistant", Content: "latest"}}

	compressed := buildCompressedHistory(base, "summary text", pinned, recent)
	if len(compressed) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(compressed))
	}
	if compressed[2].Role != "user" || compressed[2].Content != pinned[0].Content {
		t.Fatalf("pinned user instruction was not preserved raw: %#v", compressed)
	}
}

func TestPinUserMessagesForShrinkExcludesUserInstructionsFromSummary(t *testing.T) {
	instruction := "この制約は必ず守ってください"
	summarizable, pinned := pinUserMessagesForShrink([]llm.Message{
		{Role: "assistant", Content: "old work"},
		{Role: "user", Content: instruction},
		{Role: "tool", Content: "old tool result"},
	})
	if len(pinned) != 1 || pinned[0].Content != instruction {
		t.Fatalf("pinned = %#v", pinned)
	}
	for _, msg := range summarizable {
		if msg.Content == instruction {
			t.Fatalf("user instruction should not be summarized: %#v", summarizable)
		}
	}
}

func TestPinUserMessagesForShrinkSummarizesDebugContext(t *testing.T) {
	debugContext := "<debug_context>\nsource: vscode-debugpy\ncurrent_frame:\n  file: train.py\n</debug_context>"
	summarizable, pinned := pinUserMessagesForShrink([]llm.Message{
		{Role: "user", Content: debugContext},
		{Role: "assistant", Content: "old work"},
	})
	if len(pinned) != 0 {
		t.Fatalf("debug context should not be pinned raw: %#v", pinned)
	}
	if len(summarizable) != 2 {
		t.Fatalf("summarizable len = %d, want 2: %#v", len(summarizable), summarizable)
	}
	if summarizable[0].Content != debugContext {
		t.Fatalf("debug context should be summarized, got %#v", summarizable)
	}
}

func TestPinUserMessagesForShrinkCapsPinnedInstructions(t *testing.T) {
	messages := make([]llm.Message, 0, shrinkMaxPinnedUserMessages+2)
	for i := 0; i < shrinkMaxPinnedUserMessages+2; i++ {
		messages = append(messages, llm.Message{Role: "user", Content: "必ず守ってください"})
	}
	_, pinned := pinUserMessagesForShrink(messages)
	if len(pinned) != shrinkMaxPinnedUserMessages {
		t.Fatalf("pinned len = %d, want %d", len(pinned), shrinkMaxPinnedUserMessages)
	}
}

func TestShrinkCompleteUpdatesCurrentTokens(t *testing.T) {
	agentInst := agent.New(nil, tools.NewRegistry())
	model := NewModel(agentInst, nil, nil, nil, "session", "/tmp/workspace", "model", "", 12000, 5, 30)
	model.currentTokens = 9000

	updated, _ := model.Update(shrinkCompleteMsg{
		summary: "summary text",
		newHistory: []llm.Message{
			{Role: "system", Content: "system prompt"},
			{Role: "system", Content: "compressed summary"},
			{Role: "user", Content: "recent message"},
		},
		summarizedMessages: 4,
		keptMessages:       1,
	})
	got := updated.(Model)

	if got.currentTokens == 0 {
		t.Fatal("currentTokens should be recalculated after /shrink, not reset to zero")
	}
	if got.currentTokens >= 9000 {
		t.Fatalf("currentTokens = %d, want compressed estimate below previous value", got.currentTokens)
	}
}

func TestShouldAutoShrinkBelowInfoThreshold(t *testing.T) {
	model := testModel()
	model.history = shrinkableHistory(10)

	decision := model.shouldAutoShrink(29, 100)

	if decision.trigger {
		t.Fatal("should not trigger below 30%")
	}
	if decision.info != "" {
		t.Fatalf("info = %q, want empty", decision.info)
	}
}

func TestShouldAutoShrinkShowsInfoAtThirtyPercent(t *testing.T) {
	model := testModel()
	model.history = shrinkableHistory(10)

	decision := model.shouldAutoShrink(30, 100)

	if decision.trigger {
		t.Fatal("30% should only show info, not trigger")
	}
	if decision.info == "" {
		t.Fatal("expected 30% info message")
	}
}

func TestShouldAutoShrinkDoesNotRepeatInfo(t *testing.T) {
	model := testModel()
	model.history = shrinkableHistory(10)
	model.shrinkInfoShown = true

	decision := model.shouldAutoShrink(30, 100)

	if decision.info != "" {
		t.Fatalf("info = %q, want empty after it was shown", decision.info)
	}
}

func TestShouldAutoShrinkTriggersAtFiftyPercent(t *testing.T) {
	model := testModel()
	model.history = shrinkableHistory(10)

	decision := model.shouldAutoShrink(50, 100)

	if !decision.trigger {
		t.Fatal("expected auto-shrink at 50%")
	}
	if decision.reason == "" {
		t.Fatal("expected trigger reason")
	}
}

func TestShouldAutoShrinkDoesNotTriggerForLargeHistoryAtLowTokenUsage(t *testing.T) {
	model := testModel()
	model.history = shrinkableHistory(autoShrinkMessageLimit + 1)

	decision := model.shouldAutoShrink(10, 100)

	if decision.trigger {
		t.Fatal("should not auto-shrink large history at low token usage")
	}
}

func TestShouldAutoShrinkTriggersWhenHistoryIsLargeAndTokenUsageIsNotLow(t *testing.T) {
	model := testModel()
	model.history = shrinkableHistory(autoShrinkMessageLimit + 1)

	decision := model.shouldAutoShrink(30, 100)

	if !decision.trigger {
		t.Fatal("expected auto-shrink when history is large and token usage is not low")
	}
}

func TestShouldAutoShrinkSuppressesWhileShrinking(t *testing.T) {
	model := testModel()
	model.history = shrinkableHistory(10)
	model.shrinking = true

	decision := model.shouldAutoShrink(50, 100)

	if decision.trigger {
		t.Fatal("should not trigger while shrink is already running")
	}
}

func TestShouldAutoShrinkSuppressesDuringCooldown(t *testing.T) {
	model := testModel()
	model.history = shrinkableHistory(10)
	model.lastAutoShrinkHistoryLen = len(model.history) - autoShrinkCooldown + 1

	decision := model.shouldAutoShrink(50, 100)

	if decision.trigger {
		t.Fatal("should not trigger during auto-shrink cooldown")
	}

	model.lastAutoShrinkHistoryLen = len(model.history) - autoShrinkCooldown
	decision = model.shouldAutoShrink(50, 100)
	if !decision.trigger {
		t.Fatal("should trigger after cooldown has elapsed")
	}
}

func TestShrinkCompleteResetsAutoShrinkState(t *testing.T) {
	agentInst := agent.New(nil, tools.NewRegistry())
	model := NewModel(agentInst, nil, nil, nil, "session", "/tmp/workspace", "model", "", 1000, 5, 30)
	model.shrinking = true
	model.shrinkInfoShown = true

	updated, _ := model.Update(shrinkCompleteMsg{
		newHistory: []llm.Message{
			{Role: "system", Content: "system prompt"},
			{Role: "system", Content: "summary"},
			{Role: "user", Content: "recent"},
		},
		summarizedMessages: 8,
		keptMessages:       1,
		auto:               true,
		beforeTokens:       800,
		beforePercent:      80,
	})
	got := updated.(Model)

	if got.shrinking {
		t.Fatal("shrinking should be cleared after auto-shrink completes")
	}
	if got.shrinkInfoShown {
		t.Fatal("shrink info flag should reset after auto-shrink")
	}
	if got.lastAutoShrinkHistoryLen != len(got.history) {
		t.Fatalf("lastAutoShrinkHistoryLen = %d, want %d", got.lastAutoShrinkHistoryLen, len(got.history))
	}
}

func shrinkableHistory(n int) []llm.Message {
	history := []llm.Message{{Role: "system", Content: "system prompt"}}
	for i := 0; i < n; i++ {
		history = append(history, llm.Message{Role: "user", Content: "message"})
	}
	return history
}
