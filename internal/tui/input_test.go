package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hypoballad/virgil/internal/agent"
	"github.com/hypoballad/virgil/internal/llm"
	"github.com/hypoballad/virgil/internal/tools"
)

func newInputTestModel() Model {
	return NewModel(agent.New(nil, tools.NewRegistry()), nil, nil, nil, "session", "/tmp/workspace", "model", "", 12000, 5, 30)
}

func TestInputEnterInsertsNewline(t *testing.T) {
	model := newInputTestModel()
	model.input.SetValue("first")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(Model)

	if next.waiting {
		t.Fatal("enter should insert a newline, not submit")
	}
	if got := next.input.Value(); got != "first\n" {
		t.Fatalf("expected enter to append newline, got %q", got)
	}
}

func TestInputHeightClampsToContent(t *testing.T) {
	model := newInputTestModel()

	model.input.SetValue("one")
	model.adjustInputHeight()
	if got := model.input.Height(); got != minInputHeight {
		t.Fatalf("expected min height %d, got %d", minInputHeight, got)
	}

	model.input.SetValue("1\n2\n3\n4\n5")
	model.adjustInputHeight()
	if got := model.input.Height(); got != 5 {
		t.Fatalf("expected height 5, got %d", got)
	}

	model.input.SetValue("1\n2\n3\n4\n5\n6\n7\n8\n9\n10")
	model.adjustInputHeight()
	if got := model.input.Height(); got != maxInputHeight {
		t.Fatalf("expected max height %d, got %d", maxInputHeight, got)
	}
}

func TestInputAllowsMoreLinesThanVisibleHeight(t *testing.T) {
	model := newInputTestModel()
	model.input.SetValue("1\n2\n3\n4\n5\n6\n7\n8")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(Model)

	if got := strings.Count(next.input.Value(), "\n") + 1; got != 9 {
		t.Fatalf("expected textarea to accept 9 logical lines, got %d in %q", got, next.input.Value())
	}
	if got := next.input.Height(); got != maxInputHeight {
		t.Fatalf("visible input height = %d, want %d", got, maxInputHeight)
	}
}

func TestInputPromptIsExternalFixedMarker(t *testing.T) {
	model := newInputTestModel()
	model.width = 80

	if model.input.Prompt != "" {
		t.Fatalf("expected textarea prompt to be empty, got %q", model.input.Prompt)
	}

	view := model.View()
	if strings.Contains(view, "💬") {
		t.Fatalf("input view should not contain chat icon: %q", view)
	}
	if got := strings.Count(view, inputMarker); got != 1 {
		t.Fatalf("expected exactly one fixed input marker, got %d in %q", got, view)
	}
	if !strings.Contains(view, "Message...") {
		t.Fatalf("expected short placeholder, got %q", view)
	}
	if !strings.Contains(view, "Alt+Enter send") {
		t.Fatalf("expected send hint, got %q", view)
	}
}

func TestSlashCompletionSuggestsCommandRemainder(t *testing.T) {
	model := newInputTestModel()
	model.input.SetValue("/t")
	model.updateSlashCompletion()

	if got := model.slashCompletion; got != "ask" {
		t.Fatalf("expected /task remainder, got %q", got)
	}
}

func TestSlashCompletionIncludesPromotedCommandsByDefault(t *testing.T) {
	model := newInputTestModel()
	model.input.SetValue("/rei")
	model.updateSlashCompletion()

	if got := model.slashCompletion; got != "ndex" {
		t.Fatalf("expected /reindex remainder by default, got %q", got)
	}
}

func TestSlashCompletionShowsAllCommandsInFullPower(t *testing.T) {
	model := newInputTestModel()
	model.SetFullPowerCommands(true)
	model.input.SetValue("/callg")
	model.updateSlashCompletion()

	if got := model.slashCompletion; got != "raph" {
		t.Fatalf("expected /callgraph remainder in fullpower, got %q", got)
	}
}

func TestSlashCompletionIgnoresPathsAndArguments(t *testing.T) {
	tests := []string{
		"cat /r",
		"/task task",
		" /p",
	}
	for _, input := range tests {
		if got := slashCompletionFor(input); got != "" {
			t.Fatalf("slashCompletionFor(%q) = %q, want empty", input, got)
		}
	}
}

func TestTabAcceptsSlashCompletion(t *testing.T) {
	model := newInputTestModel()
	model.input.SetValue("/t")
	model.updateSlashCompletion()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	next := updated.(Model)

	if got := next.input.Value(); got != "/task" {
		t.Fatalf("expected completed command, got %q", got)
	}
	if next.slashCompletion != "" {
		t.Fatalf("expected completion to clear after accepting, got %q", next.slashCompletion)
	}
}

func TestCtrlPNNavigateInputHistory(t *testing.T) {
	model := newInputTestModel()
	model.inputHistory = []string{
		"/do RPT-001 tasks.md --flow",
		"/task-status RPT-001 done-pending-user-test tasks.md",
	}
	model.historyIndex = -1

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	next := updated.(Model)
	if got := next.input.Value(); got != "/task-status RPT-001 done-pending-user-test tasks.md" {
		t.Fatalf("Ctrl+P latest history = %q", got)
	}
	if next.historyIndex != 1 {
		t.Fatalf("historyIndex after first Ctrl+P = %d, want 1", next.historyIndex)
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	next = updated.(Model)
	if got := next.input.Value(); got != "/do RPT-001 tasks.md --flow" {
		t.Fatalf("Ctrl+P older history = %q", got)
	}
	if next.historyIndex != 0 {
		t.Fatalf("historyIndex after second Ctrl+P = %d, want 0", next.historyIndex)
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	next = updated.(Model)
	if got := next.input.Value(); got != "/task-status RPT-001 done-pending-user-test tasks.md" {
		t.Fatalf("Ctrl+N newer history = %q", got)
	}
	if next.historyIndex != 1 {
		t.Fatalf("historyIndex after Ctrl+N = %d, want 1", next.historyIndex)
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	next = updated.(Model)
	if got := next.input.Value(); got != "" {
		t.Fatalf("Ctrl+N past latest should clear input, got %q", got)
	}
	if next.historyIndex != -1 {
		t.Fatalf("historyIndex after clearing = %d, want -1", next.historyIndex)
	}
}

func TestInputAcceptsRunCommandConfirmationWhileWaiting(t *testing.T) {
	model := newInputTestModel()
	model.waiting = true
	model.pendingRunCommand = &pendingRunCommand{
		command:     "jq -f filter.jq input.json > output.json",
		requestedAt: time.Now(),
	}
	model.input.SetValue("/confirm-run")

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	next := *updated.(*Model)
	if cmd == nil {
		t.Fatal("expected /confirm-run to dispatch while waiting for run_command confirmation")
	}
	if got := next.input.Value(); got != "" {
		t.Fatalf("expected input to be cleared after /confirm-run, got %q", got)
	}
	msg := cmd()
	if confirm, ok := msg.(runCommandConfirmMsg); !ok || !confirm.approved {
		t.Fatalf("expected approved runCommandConfirmMsg, got %#v", msg)
	}
}

func TestInputRejectsRunCommandWithFeedbackWhilePending(t *testing.T) {
	model := newInputTestModel()
	model.waiting = true
	model.pendingRunCommand = &pendingRunCommand{
		command:     "jq -f filter.jq input.json > output.json",
		requestedAt: time.Now(),
	}
	model.input.SetValue("use go test ./internal/tools instead")

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	next := updated.(Model)
	if cmd == nil {
		t.Fatal("expected feedback rejection command for non-confirmation input")
	}
	if got := next.input.Value(); got != "" {
		t.Fatalf("expected input to be cleared after feedback rejection, got %q", got)
	}
	msg := cmd()
	confirm, ok := msg.(runCommandConfirmMsg)
	if !ok {
		t.Fatalf("expected runCommandConfirmMsg, got %#v", msg)
	}
	if confirm.approved {
		t.Fatal("feedback input should reject the pending command")
	}
	if confirm.feedback != "use go test ./internal/tools instead" {
		t.Fatalf("feedback = %q", confirm.feedback)
	}
}

func TestPendingActionShortcutApprovesRunCommand(t *testing.T) {
	model := newInputTestModel()
	model.waiting = true
	model.pendingRunCommand = &pendingRunCommand{
		command:     "jq -f filter.jq input.json > output.json",
		requestedAt: time.Now(),
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	if cmd == nil {
		t.Fatal("expected action shortcut to dispatch confirmation")
	}
	if got := updated.(Model).input.Value(); got != "" {
		t.Fatalf("expected shortcut to leave input empty, got %q", got)
	}
	msg := cmd()
	if confirm, ok := msg.(runCommandConfirmMsg); !ok || !confirm.approved {
		t.Fatalf("expected approved runCommandConfirmMsg, got %#v", msg)
	}
}

func TestPendingActionShortcutDoesNotInterruptTypedInput(t *testing.T) {
	model := newInputTestModel()
	model.pendingRewind = &pendingRewind{
		targetHash:  "abc1234",
		requestedAt: time.Now(),
	}
	model.input.SetValue("/help")

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	if cmd == nil {
		t.Fatal("expected textarea update command")
	}
	next := updated.(Model)
	if next.pendingRewind == nil {
		t.Fatal("pending rewind should not be confirmed or cancelled while input has text")
	}
	if got := next.input.Value(); got != "/help1" {
		t.Fatalf("expected key to edit input, got %q", got)
	}
}

func TestSlashCompletionViewShowsGhostText(t *testing.T) {
	model := newInputTestModel()
	model.width = 80
	model.input.SetValue("/t")
	model.updateSlashCompletion()

	view := model.View()
	if !strings.Contains(view, "/t") || !strings.Contains(view, "ask") {
		t.Fatalf("expected view to contain typed text and ghost completion, got %q", view)
	}
}

func TestUserMessageMarkdownTrimsDisplayOnly(t *testing.T) {
	model := newInputTestModel()
	model.width = 80

	rendered := model.renderSingleMessage(llm.Message{
		Role:    "user",
		Content: "\n\n- item\n\n",
	}, nil)

	if !strings.Contains(rendered, "item") {
		t.Fatalf("expected rendered markdown content, got %q", rendered)
	}
	if strings.Contains(rendered, "\n\n\n\n- item") {
		t.Fatalf("expected display rendering to trim excessive leading blank lines, got %q", rendered)
	}
}
