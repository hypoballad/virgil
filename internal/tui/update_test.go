package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/hypoballad/virgil/internal/agent"
	"github.com/hypoballad/virgil/internal/llm"
)

func TestSlashCommandInputTrimsWhitespace(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantInput   string
		wantCommand bool
	}{
		{
			name:        "plain slash command",
			input:       "/help",
			wantInput:   "/help",
			wantCommand: true,
		},
		{
			name:        "leading spaces",
			input:       "  /help",
			wantInput:   "/help",
			wantCommand: true,
		},
		{
			name:        "leading newline task",
			input:       "\n/task fix foo",
			wantInput:   "/task fix foo",
			wantCommand: true,
		},
		{
			name:        "normal message keeps non command",
			input:       " normal message",
			wantInput:   "normal message",
			wantCommand: false,
		},
		{
			name:        "blank input",
			input:       "  \n\t ",
			wantInput:   "",
			wantCommand: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotInput, gotCommand := slashCommandInput(tt.input)
			if gotInput != tt.wantInput {
				t.Fatalf("input = %q, want %q", gotInput, tt.wantInput)
			}
			if gotCommand != tt.wantCommand {
				t.Fatalf("is command = %v, want %v", gotCommand, tt.wantCommand)
			}
		})
	}
}

func TestIterationPausePromptMentionsContinueAndAbort(t *testing.T) {
	prompt := iterationPausePrompt(20)
	for _, want := range []string{"maximum of 20 iterations", "/continue", "/abort"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
}

func TestAbortClearsAwaitingContinuation(t *testing.T) {
	m := testModel()
	m.awaitingContinuation = true
	m.lastIterationLimitReached = true
	m.doFlowActive = true
	m.doFlowRemaining = 3
	updated, _ := m.handleSlashCommand("/abort")
	got := updated.(*Model)
	if got.awaitingContinuation {
		t.Fatal("awaitingContinuation should be false after /abort")
	}
	if got.lastIterationLimitReached {
		t.Fatal("lastIterationLimitReached should be false after /abort")
	}
	if got.doFlowActive || got.doFlowRemaining != 0 {
		t.Fatal("do flow state should be cleared after /abort")
	}
}

func TestDoFlowAutoContinuesAtIterationLimit(t *testing.T) {
	m := testModel()
	m.doFlowActive = true
	m.doFlowRemaining = 1
	m.doFlowContinueOptions = agent.RunOptions{MaxIterations: agent.MaxIterations}

	updated, cmd := m.continueDoFlowAfterIterationLimit()
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("expected auto continuation command")
	}
	if !got.waiting {
		t.Fatal("model should be waiting during auto continuation")
	}
	if got.awaitingContinuation {
		t.Fatal("auto flow should not prompt for manual continuation")
	}
	if got.lastIterationLimitReached {
		t.Fatal("lastIterationLimitReached should be cleared while auto continuing")
	}
	if got.doFlowRemaining != 0 {
		t.Fatalf("doFlowRemaining = %d, want 0", got.doFlowRemaining)
	}
}

func TestDoFlowFallsBackToManualPauseAtContinuationLimit(t *testing.T) {
	m := testModel()
	m.doFlowActive = true
	m.doFlowRemaining = 0

	updated, cmd := m.continueDoFlowAfterIterationLimit()
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("expected pause command")
	}
	if got.doFlowActive {
		t.Fatal("do flow should be disabled after hitting continuation limit")
	}
	if !got.awaitingContinuation {
		t.Fatal("model should fall back to manual continuation")
	}
	if !got.lastIterationLimitReached {
		t.Fatal("iteration limit should remain visible after flow limit")
	}
}

func TestUnstuckPromptConstrainsNextStep(t *testing.T) {
	prompt := unstuckPrompt()
	for _, want := range []string{
		"UNSTUCK MODE",
		"Do not continue hidden reasoning",
		"exactly one focused tool call",
		"at most 5 concise bullets",
		"Preserve the user's active task constraints",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("unstuck prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestSlashCommandHelpIsFilteredByDefault(t *testing.T) {
	m := testModel()
	help := m.slashCommandHelp()

	for _, want := range []string{"/rewind", "/task <task>", "/tasks <path>", "/do <id>", "/breakdown", "/breakdown-last", "/copy-last", "/btw <task>", "/reindex", "/shrink", "/unstuck", "/debug-context", "/vmax", "virgil fullpower"} {
		if !strings.Contains(help, want) {
			t.Fatalf("default help missing %q: %s", want, help)
		}
	}
	for _, hidden := range []string{"/confirm-run", "/reject-run", "/callers", "/callgraph"} {
		if strings.Contains(help, hidden) {
			t.Fatalf("default help should hide %q: %s", hidden, help)
		}
	}
}

func TestSlashCommandHelpShowsAllInFullPower(t *testing.T) {
	m := testModel()
	m.SetFullPowerCommands(true)
	help := m.slashCommandHelp()

	for _, want := range []string{"/rewind", "/reindex", "/debug-context"} {
		if !strings.Contains(help, want) {
			t.Fatalf("fullpower help missing %q: %s", want, help)
		}
	}
}

func TestLastAssistantContentReturnsRawLatestAssistantText(t *testing.T) {
	got, ok := lastAssistantContent([]llm.Message{
		{Role: "assistant", Content: "first"},
		{Role: "user", Content: "question"},
		{Role: "assistant", Content: "  latest plan\n\n"},
	})
	if !ok {
		t.Fatal("expected assistant content")
	}
	if got != "latest plan" {
		t.Fatalf("content = %q, want raw trimmed latest assistant content", got)
	}
}

func TestViewDoesNotShowContinuationFooter(t *testing.T) {
	m := testModel()
	m.awaitingContinuation = true
	m.width = 100
	m.contextLimit = 100
	view := m.View()
	if strings.Contains(view, "/continue") || strings.Contains(view, "/abort") {
		t.Fatalf("continuation prompt should be shown as a normal message, not footer:\n%s", view)
	}
}

func TestViewShowsPendingActionChoices(t *testing.T) {
	m := testModel()
	m.awaitingContinuation = true
	m.width = 100
	m.contextLimit = 100

	view := m.View()
	for _, want := range []string{"Paused at iteration limit", "[1]", "Continue", "[2]", "Stop", "[Esc]", "Cancel"} {
		if !strings.Contains(view, want) {
			t.Fatalf("pending action view missing %q:\n%s", want, view)
		}
	}
}

func TestWaitingPreviewShowsLLMAndAgentRows(t *testing.T) {
	m := testModel()
	m.waiting = true
	m.waitingStartedAt = time.Now()
	m.width = 100
	m.contextLimit = 100
	m.partialAssistantContent = "first line\nstreaming answer"
	m.lastActivityMessage = "Working: read_file({\"path\":\"README.md\"})"

	view := m.View()
	for _, want := range []string{
		"LLM:",
		"streaming answer",
		"Agent:",
		"Working: read_file",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("waiting preview missing %q:\n%s", want, view)
		}
	}
}

func TestWaitingPreviewKeepsLLMRowEmptyUntilStreamArrives(t *testing.T) {
	m := testModel()
	m.waiting = true
	m.waitingStartedAt = time.Now()
	m.width = 100
	m.contextLimit = 100

	view := m.View()
	for _, want := range []string{
		"LLM:",
		"Thinking 0s",
		"Agent:",
		"Waiting...",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("waiting fallback preview missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "LLM:    Thinking...") {
		t.Fatalf("LLM row should not show Thinking fallback:\n%s", view)
	}
}

func TestLatestNonEmptyLine(t *testing.T) {
	if got := latestNonEmptyLine("first\n\n second "); got != "second" {
		t.Fatalf("latestNonEmptyLine = %q, want second", got)
	}
	if got := latestNonEmptyLine(" \n\t "); got != "" {
		t.Fatalf("latestNonEmptyLine blank = %q, want empty", got)
	}
}

func TestProgressRowsPersistIndependently(t *testing.T) {
	m := testModel()
	m.waiting = true
	m.waitingStartedAt = time.Now()
	m.width = 100
	m.contextLimit = 100
	m.progressCh = make(chan agent.ProgressEvent)

	updated, _ := m.Update(progressMsg{event: agent.ProgressEvent{
		Type:           agent.EventPartialResponse,
		PartialContent: "first stream",
	}})
	m = updated.(Model)

	updated, _ = m.Update(progressMsg{event: agent.ProgressEvent{
		Type:            agent.EventAgentActivity,
		ActivityMessage: "Working: read_file({})",
	}})
	m = updated.(Model)

	view := m.View()
	for _, want := range []string{"first stream", "Working: read_file"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view should keep both LLM and Agent rows after activity %q:\n%s", want, view)
		}
	}

	updated, _ = m.Update(progressMsg{event: agent.ProgressEvent{
		Type:           agent.EventPartialResponse,
		PartialContent: "\nsecond stream",
	}})
	m = updated.(Model)

	view = m.View()
	for _, want := range []string{"second stream", "Working: read_file"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view should keep agent row until next agent activity %q:\n%s", want, view)
		}
	}
}

func TestBannerTitleIncludesVersionWhenProvided(t *testing.T) {
	m := testModel()
	if got := m.bannerTitle(); got != "Virgil 🤖" {
		t.Fatalf("banner title without version = %q", got)
	}

	m.appVersion = "1.2.1"
	if got := m.bannerTitle(); got != "Virgil 🤖 ver1.2.1" {
		t.Fatalf("banner title with version = %q", got)
	}
}

func TestPendingActionShortcutStopsContinuation(t *testing.T) {
	m := testModel()
	m.awaitingContinuation = true
	m.lastIterationLimitReached = true

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	next := updated.(Model)
	if cmd == nil {
		t.Fatal("expected stop command")
	}
	if next.awaitingContinuation {
		t.Fatal("awaitingContinuation should be false after selecting stop")
	}
	if next.lastIterationLimitReached {
		t.Fatal("lastIterationLimitReached should be false after selecting stop")
	}
}

func TestAgentRunContextHasNoDeadlineButCanCancel(t *testing.T) {
	ctx, cancel := newAgentRunContext()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("agent run context should not have a deadline; user confirmation must not time out")
	}
	cancel()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("agent run context should still be cancellable")
	}
}

func testModel() Model {
	ti := textarea.New()
	ti.SetWidth(80)
	ti.SetHeight(2)
	return Model{
		input:        ti,
		historyIndex: -1,
	}
}
