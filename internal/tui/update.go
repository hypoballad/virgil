package tui

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/hypoballad/virgil/internal/agent"
	"github.com/hypoballad/virgil/internal/debugctx"
	"github.com/hypoballad/virgil/internal/llm"
	"github.com/hypoballad/virgil/internal/repository"
	"github.com/hypoballad/virgil/internal/shadow"
	"github.com/hypoballad/virgil/internal/tools"
)

const doFlowMaxContinuationWindows = 12

type indexerTickMsg struct{}

type clockTickMsg struct{}

func tickIndexer() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return indexerTickMsg{}
	})
}

func tickClock() tea.Cmd {
	return tea.Tick(time.Minute, func(t time.Time) tea.Msg {
		return clockTickMsg{}
	})
}

func newAgentRunContext() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

func (m Model) Init() tea.Cmd {
	banner := styleBanner.Render(fmt.Sprintf(
		" %s\n\n 📂 Workspace: %s\n 🤖 Model: %s\n 🧰 Tool Profile: %s\n 🧠 Context Limit: %d tokens\n Type /help for available commands",
		m.bannerTitle(), m.workspaceRoot, m.modelName, m.toolProfile, m.contextLimit,
	))
	return tea.Batch(
		tea.Printf("%s\n", banner),
		m.waitProgress(),
		tickIndexer(),
		tickClock(),
	)
}

func (m Model) bannerTitle() string {
	if m.appVersion == "" {
		return "Virgil 🤖"
	}
	return fmt.Sprintf("Virgil 🤖 ver%s", m.appVersion)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.setInputSize(msg.Width)

	case tea.MouseMsg:
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "shift+tab":
			// プランモード/編集モードのトグル
			m.planMode = !m.planMode
			m.agent.SetPlanMode(m.planMode)
			if m.planMode {
				return m, m.printSystem("📋 Plan mode ON — file modifications disabled")
			} else {
				return m, m.printSystem("✏️  Edit mode ON — file modifications enabled")
			}
		}

		// Handle Ctrl+C twice to quit
		if msg.String() == "ctrl+c" {
			if m.waiting {
				// 待機中の場合は処理をキャンセル
				if m.cancelAgent != nil {
					m.cancelAgent()
					m.cancelAgent = nil
				}
				return m, nil
			}
			if m.quitConfirm {
				return m, tea.Quit
			}
			m.quitConfirm = true
			return m, tea.Tick(5*time.Second, func(t time.Time) tea.Msg {
				return quitConfirmTimeoutMsg{}
			})
		}

		if m.waiting && m.pendingRunCommand == nil {
			if msg.String() == "esc" {
				if m.cancelAgent != nil {
					m.cancelAgent()
					m.cancelAgent = nil
				}
			}
			return m, nil
		}

		// Handle Esc to cancel quit confirmation
		if msg.String() == "esc" {
			if m.quitConfirm {
				m.quitConfirm = false
				return m, nil
			}
		}

		// Any other key resets quit confirmation
		if m.quitConfirm {
			m.quitConfirm = false
		}

		if handled, next, actionCmd := m.handlePendingActionKey(msg.String()); handled {
			return next, actionCmd
		}

		switch msg.String() {
		case "tab":
			if m.slashCompletion != "" {
				m.acceptSlashCompletion()
			}
			return m, nil

		case "alt+up", "alt+pgup":
			return m.navigateInputHistory(-1)

		case "alt+down", "alt+pgdown":
			return m.navigateInputHistory(1)

		case "alt+enter", "ctrl+d":
			return m.submitInput()
		}

	case quitConfirmTimeoutMsg:
		m.quitConfirm = false
		return m, nil

	case indexerTickMsg:
		if m.indexer == nil {
			return m, nil
		}
		if m.indexer.Status().Active {
			return m, tickIndexer()
		}
		return m, nil

	case clockTickMsg:
		return m, tickClock()

	case btwRequestMsg:
		m.awaitingContinuation = false
		m.waiting = true
		m.waitingStartedAt = time.Now()
		m.partialAssistantContent = "" // リセット
		m.lastActivityMessage = ""     // リセット
		m.currentIteration = 1         // 初期化
		ctx, cancel := newAgentRunContext()
		m.cancelAgent = cancel
		return m, tea.Batch(
			m.spinner.Tick,
			m.callBtw(ctx, msg.question),
		)

	case agentBtwResponseMsg:
		m.waiting = false
		m.cancelAgent = nil
		if msg.err != nil {
			if errors.Is(msg.err, context.Canceled) {
				return m, m.printSystem("⚠️  By-the-way request cancelled by user.")
			}
			return m, m.printSystem(fmt.Sprintf("❌ /btw error: %v", msg.err))
		}
		return m, tea.Printf("%s", m.renderBtwMessage(msg.question, msg.response.FinalContent, msg.response.ToolCalls))

	case taskRequestMsg:
		m.awaitingContinuation = false
		m.lastIterationLimitReached = false
		if msg.description == "" {
			return m, m.printSystem("⚠️ /task requires a description. Example: /task add tests for tokenizer")
		}
		display := msg.display
		if display == "" {
			display = "/task " + msg.description
		}
		runOpts := m.consumeVMaxRunOptions()
		if msg.flow {
			m.doFlowActive = true
			m.doFlowRemaining = doFlowMaxContinuationWindows
			if runOpts.MaxIterations == 0 {
				runOpts.MaxIterations = agent.MaxIterations
			}
			m.doFlowContinueOptions = runOpts
		} else {
			m.doFlowActive = false
			m.doFlowRemaining = 0
			m.doFlowContinueOptions = agent.RunOptions{}
		}

		m.turnNumber++
		t, err := m.repo.Turns.Create(m.sessionID, m.turnNumber, display)
		if err == nil {
			m.currentTurnID = t.ID
		}

		userMsg := llm.Message{
			Role:    "user",
			Content: "🧩 " + display,
		}
		m.messages = append(m.messages, userMsg)
		m.waiting = true
		m.waitingStartedAt = time.Now()
		m.partialAssistantContent = ""
		m.lastActivityMessage = "Starting task..."
		m.currentIteration = 1

		ctx, cancel := newAgentRunContext()
		m.cancelAgent = cancel

		return m, tea.Batch(
			tea.Printf("%s", m.renderSingleMessage(userMsg, nil)),
			m.spinner.Tick,
			m.callTask(ctx, m.promptWithDebugContext(msg.description), runOpts),
		)

	case agentTaskResponseMsg:
		m.waiting = false
		m.cancelAgent = nil
		autoOffCmd := m.vmaxAutoOffCommand()
		m.lastActivityMessage = ""
		m.partialAssistantContent = ""
		if msg.err != nil {
			if errors.Is(msg.err, context.Canceled) {
				return m, tea.Batch(m.printSystem("⚠️  Task cancelled by user."), autoOffCmd)
			}
			if m.currentTurnID != 0 {
				m.repo.Turns.UpdateTurnError(m.currentTurnID, msg.err.Error())
			}
			return m, tea.Batch(m.printSystem(fmt.Sprintf("❌ /task error: %v", msg.err)), autoOffCmd)
		}
		m.history = msg.response.Messages
		m.lastToolCalls = msg.response.ToolCalls
		m.lastIterationLimitReached = msg.response.MaxIterationsReached
		m.awaitingContinuation = msg.response.MaxIterationsReached && !m.doFlowActive
		if m.currentTurnID != 0 {
			_ = m.repo.Turns.UpdateTurnResponse(
				m.currentTurnID,
				msg.response.FinalContent,
				"stop",
				msg.response.PromptTokens,
				msg.response.CompletionTokens,
				0,
			)
			for _, tc := range msg.response.ToolCalls {
				record := repository.ToolCallRecord{
					TurnID:     m.currentTurnID,
					ToolCallID: tc.ToolCallID,
					ToolName:   tc.ToolName,
					Arguments:  string(tc.Arguments),
					DurationMs: tc.DurationMs,
					PreCommit:  tc.PreCommit,
					PostCommit: tc.PostCommit,
				}
				if tc.Error != nil {
					record.IsError = true
					record.Error = tc.Error.Error()
				} else if tc.Result != nil {
					record.IsError = tc.Result.IsError
					record.Result = tc.Result.Content
					if tc.Result.IsError {
						record.Error = tc.Result.Content
					}
				}
				if _, err := m.repo.ToolCalls.Create(record); err != nil {
					log.Printf("failed to record task tool_call: %v", err)
				}
			}
		}
		cmd := m.printAssistant(msg.response.FinalContent, msg.response.ToolCalls)
		if msg.response.MaxIterationsReached {
			if m.doFlowActive {
				return m.continueDoFlowAfterIterationLimit(cmd)
			}
			return m, tea.Batch(cmd, m.printSystemDisplayOnly(iterationPausePrompt(m.activeRunMaxIterations())), autoOffCmd)
		}
		m.doFlowActive = false
		m.doFlowRemaining = 0
		m.doFlowContinueOptions = agent.RunOptions{}
		cmds := []tea.Cmd{cmd, autoOffCmd}
		cmds = m.appendAutoShrinkCommands(cmds)
		return m, tea.Batch(cmds...)

	case runCommandConfirmRequestMsg:
		m.pendingRunCommand = &pendingRunCommand{
			command:     msg.command,
			dir:         msg.dir,
			requestedAt: time.Now(),
		}
		return m, m.printSystem(fmt.Sprintf(
			"⚠️  Confirm command execution:\n"+
				"  $ %s\n"+
				"  (in %s)\n"+
				"Type /confirm-run to execute, /reject-run to cancel, or type guidance to reject with an instruction.",
			msg.command, msg.dir,
		))

	case runCommandConfirmMsg:
		if m.pendingRunCommand == nil {
			return m, nil
		}

		m.agent.NotifyRunCommandConfirmationWithFeedback(msg.approved, msg.feedback)

		if msg.approved {
			m.appendSystemMessage(fmt.Sprintf("✓ Command approved: %s", m.pendingRunCommand.command))
		} else if strings.TrimSpace(msg.feedback) != "" {
			m.appendSystemMessage(fmt.Sprintf("✗ Command rejected with instruction: %s", strings.TrimSpace(msg.feedback)))
		} else {
			m.appendSystemMessage(fmt.Sprintf("✗ Command rejected: %s", m.pendingRunCommand.command))
		}
		m.pendingRunCommand = nil
		return m, nil

	case agentResponseMsg:
		m.waiting = false
		m.cancelAgent = nil
		autoOffCmd := m.vmaxAutoOffCommand()
		var cmds []tea.Cmd

		if msg.err != nil {
			if errors.Is(msg.err, context.Canceled) {
				cmds = append(cmds, m.printSystem("⚠️  Request cancelled by user."))
			} else {
				log.Printf("agent error: %v", msg.err)
				m.err = msg.err
				if m.currentTurnID != 0 {
					m.repo.Turns.UpdateTurnError(m.currentTurnID, msg.err.Error())
				}
				cmds = append(cmds, m.printSystem(fmt.Sprintf("❌ Error: %v", msg.err)))
			}
		} else {
			log.Printf("agent response: %d iterations, %d tool calls",
				msg.response.Iterations, len(msg.response.ToolCalls))

			// Update history and tool calls
			m.history = msg.response.Messages
			m.lastToolCalls = msg.response.ToolCalls
			m.lastIterationLimitReached = msg.response.MaxIterationsReached
			m.awaitingContinuation = msg.response.MaxIterationsReached && !m.doFlowActive

			// Print assistant message
			cmds = append(cmds, m.printAssistant(msg.response.FinalContent, msg.response.ToolCalls))
			if msg.response.MaxIterationsReached {
				if m.doFlowActive {
					return m.continueDoFlowAfterIterationLimit(cmds...)
				}
				cmds = append(cmds, m.printSystemDisplayOnly(iterationPausePrompt(m.activeRunMaxIterations())))
			}

			// ウォッチドッグ停止の表示
			if msg.response.WatchdogStop != nil {
				signal := msg.response.WatchdogStop
				var icon, label string
				switch signal.Reason {
				case agent.StopReasonLoopDetected:
					icon = "🔁"
					label = "Loop detected"
				case agent.StopReasonEmptyResponse:
					icon = "📭"
					label = "Empty response"
				case agent.StopReasonContextLimit:
					icon = "📦"
					label = "Context limit"
				default:
					icon = "⚠️"
					label = "Watchdog stop"
				}
				cmds = append(cmds, m.printSystem(fmt.Sprintf(
					"%s %s: %s\nThe agent stopped automatically. Review the partial result above.",
					icon, label, signal.Detail,
				)))
				if signal.Reason == agent.StopReasonContextLimit {
					cmds = append(cmds, m.printSystem("Context is at the limit. Type /shrink to compress older history, then continue."))
				}
			}

			// Record in DB
			if m.currentTurnID != 0 {
				m.repo.Turns.UpdateTurnResponse(
					m.currentTurnID,
					msg.response.FinalContent,
					"stop",
					msg.response.PromptTokens,
					msg.response.CompletionTokens,
					0, // duration
				)

				// Record tool calls
				for _, tc := range msg.response.ToolCalls {
					record := repository.ToolCallRecord{
						TurnID:     m.currentTurnID,
						ToolCallID: tc.ToolCallID,
						ToolName:   tc.ToolName,
						Arguments:  string(tc.Arguments),
						DurationMs: tc.DurationMs,
						PreCommit:  tc.PreCommit,
						PostCommit: tc.PostCommit,
					}

					if tc.Error != nil {
						record.IsError = true
						record.Error = tc.Error.Error()
						record.Result = ""
					} else if tc.Result != nil {
						record.IsError = tc.Result.IsError
						record.Result = tc.Result.Content
						if tc.Result.IsError {
							record.Error = tc.Result.Content
						}
					}

					if _, err := m.repo.ToolCalls.Create(record); err != nil {
						log.Printf("failed to record tool_call: %v", err)
					}
				}
			}

			if !msg.response.MaxIterationsReached {
				m.doFlowActive = false
				m.doFlowRemaining = 0
				m.doFlowContinueOptions = agent.RunOptions{}
				cmds = m.appendAutoShrinkCommands(cmds)
			}
		}
		cmds = append(cmds, autoOffCmd)
		return m, tea.Batch(cmds...)

	case progressMsg:
		ev := msg.event
		var cmds []tea.Cmd
		switch ev.Type {
		case agent.EventTokenUpdate:
			// 現在のコンテキストトークン数を更新
			if ev.PromptTokens > 0 {
				m.currentTokens = ev.PromptTokens
			}

		case agent.EventPartialResponse:
			m.partialAssistantContent += ev.PartialContent

		case agent.EventAgentActivity:
			if ev.Iteration > 0 {
				m.currentIteration = ev.Iteration
			}
			if ev.ActivityMessage != "" {
				m.lastActivityMessage = ev.ActivityMessage
			}

		case agent.EventRunCommandConfirmNeeded:
			// 確認要求を runCommandConfirmRequestMsg として再ディスパッチ
			return m, tea.Batch(
				m.waitProgress(),
				func() tea.Msg {
					return runCommandConfirmRequestMsg{
						command: ev.PendingCommand,
						dir:     ev.PendingDir,
					}
				},
			)
		}

		// 次のイベントを待つ
		cmds = append(cmds, m.waitProgress())
		return m, tea.Batch(cmds...)

	case clearSessionRequestMsg:
		// 1. End current session
		_ = m.repo.Sessions.End(m.sessionID, "cleared")

		// 2. Create new session
		newSession, err := m.repo.Sessions.Create(m.modelName, m.workspaceRoot, "Session cleared manually")
		if err != nil {
			return m, m.printSystem(fmt.Sprintf("❌ Failed to create new session: %v", err))
		}

		// 3. Reset internal state
		m.sessionID = newSession.ID
		m.turnNumber = 0
		m.currentTurnID = 0
		m.history = nil
		m.messages = nil
		m.lastToolCalls = nil
		m.currentTokens = 0
		m.err = nil
		m.awaitingContinuation = false
		m.lastIterationLimitReached = false
		m.debugContext = nil
		m.vmaxArmed = false
		m.vmaxActive = false
		m.currentRunMaxIterations = agent.MaxIterations

		// 4. Notify user
		return m, m.printSystem(fmt.Sprintf("🔄 Context cleared. Started new session (ID: %s)", newSession.ID[:8]))

	case reindexRequestMsg:
		if m.indexer == nil {
			return m, nil
		}
		if msg.force {
			m.indexer.StartFullScanWithForce(context.Background(), true)
			return m, tea.Batch(
				m.printSystem("🔄 Force re-indexing started in background (ignoring mtime cache; useful after parser/indexer changes or suspicious symbol results)..."),
				tickIndexer(),
			)
		}
		m.indexer.StartFullScan(context.Background())
		return m, tea.Batch(
			m.printSystem("🔄 Re-indexing started in background (mtime-based diff; use /reindex --force after parser/indexer changes or suspicious symbol results)..."),
			tickIndexer(),
		)

	case rewindRequestMsg:
		if m.shadow == nil {
			return m, m.printSystem("Shadow git is not initialized.")
		}

		if msg.target == "" {
			// /rewind 単独 → 履歴表示
			return m, m.showRewindHistory()
		}

		// /rewind <target> → 確認状態に
		return m, m.prepareRewind(msg.target)

	case historyDisplayMsg:
		return m, m.printSystem(msg.content)

	case shrinkCompleteMsg:
		m.waiting = false
		m.cancelAgent = nil
		m.shrinking = false
		if msg.err != nil {
			return m, m.printSystem(fmt.Sprintf("❌ /shrink failed: %v", msg.err))
		}

		m.history = msg.newHistory
		m.currentTokens = m.agent.EstimateContextTokens(m.history)
		m.shrinkInfoShown = false
		if msg.auto {
			m.lastAutoShrinkHistoryLen = len(m.history)
			afterPercent := contextUsagePercent(m.currentTokens, m.contextLimit)
			return m, m.printSystemDisplayOnly(fmt.Sprintf(
				"🔻 Auto-shrink: compressed %d older messages, kept %d recent/pinned (%d user instructions). Context: %d%% → %d%%.",
				msg.summarizedMessages, msg.keptMessages, msg.pinnedMessages, msg.beforePercent, afterPercent,
			))
		}

		saveNote := ""
		if msg.saved {
			saveNote = " Summary saved to the current turn."
		}
		cmd := m.printSystem(fmt.Sprintf(
			"✅ Context compressed: summarized %d older messages, kept %d recent/pinned messages (%d user instructions).%s",
			msg.summarizedMessages, msg.keptMessages, msg.pinnedMessages, saveNote,
		))
		return m, cmd
	case rewindPreparedMsg:
		m.pendingRewind = &pendingRewind{
			targetHash:    msg.hash,
			targetMessage: msg.message,
			requestedAt:   time.Now(),
		}

		shortHash := msg.hash
		if len(shortHash) > 7 {
			shortHash = shortHash[:7]
		}

		var sb strings.Builder
		sb.WriteString("⚠️  Pending rewind to:\n")
		sb.WriteString(fmt.Sprintf("  Hash:    %s\n", shortHash))
		sb.WriteString(fmt.Sprintf("  Message: %s\n\n", msg.message))

		// 差分情報を追加
		if msg.diff != nil {
			hasChanges := false

			if len(msg.diff.AddedFiles) > 0 {
				sb.WriteString("Files that will be DELETED:\n")
				for _, f := range msg.diff.AddedFiles {
					sb.WriteString(fmt.Sprintf("  - %s\n", f))
				}
				sb.WriteString("\n")
				hasChanges = true
			}

			if len(msg.diff.DeletedFiles) > 0 {
				sb.WriteString("Files that will be RESTORED:\n")
				for _, f := range msg.diff.DeletedFiles {
					sb.WriteString(fmt.Sprintf("  - %s\n", f))
				}
				sb.WriteString("\n")
				hasChanges = true
			}

			if len(msg.diff.ModifiedFiles) > 0 {
				sb.WriteString("Files that will be MODIFIED:\n")
				for _, f := range msg.diff.ModifiedFiles {
					sb.WriteString(fmt.Sprintf("  - %s\n", f))
				}
				sb.WriteString("\n")
				hasChanges = true
			}

			if !hasChanges {
				sb.WriteString("No file changes (current state matches target).\n\n")
			}
		}

		sb.WriteString("This will overwrite your current files. Type /confirm to proceed, or any other input to cancel.")
		return m, m.printSystem(sb.String())

	case rewindConfirmMsg:
		if m.pendingRewind == nil {
			return m, m.printSystem("No pending rewind operation.")
		}

		// タイムアウトチェック（5分以上経過したら期限切れ）
		if time.Since(m.pendingRewind.requestedAt) > 5*time.Minute {
			m.pendingRewind = nil
			return m, m.printSystem("Rewind request has expired.")
		}

		// rewind実行
		return m, m.executeRewind(m.pendingRewind.targetHash)

	case rewindCompleteMsg:
		m.pendingRewind = nil // クリア

		if msg.err != nil {
			m.err = msg.err
			return m, m.printSystem(fmt.Sprintf("❌ Rewind failed: %v", msg.err))
		}

		if !msg.success {
			return m, m.printSystem("❌ Rewind was not successful")
		}

		shortHash := msg.targetHash
		if len(shortHash) > 7 {
			shortHash = shortHash[:7]
		}
		return m, m.printSystem(fmt.Sprintf("✅ Rewound to %s. Files have been restored.", shortHash))

	case spinner.TickMsg:
		if m.waiting {
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	}

	if !m.waiting || m.pendingRunCommand != nil {
		// ペースト時の改行正規化（CRLF -> LF）と、複数行ペースト時の不要な隙間を防ぐ
		if key, ok := msg.(tea.KeyMsg); ok && len(key.Runes) > 1 {
			normalized := strings.ReplaceAll(string(key.Runes), "\r\n", "\n")
			msg = tea.KeyMsg{
				Type:  tea.KeyRunes,
				Runes: []rune(normalized),
			}
		}

		m.input, cmd = m.input.Update(msg)
		m.adjustInputHeight()
		m.updateSlashCompletion()
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m Model) submitInput() (tea.Model, tea.Cmd) {
	inputVal := m.input.Value()
	dispatchInput, isSlashCommand := slashCommandInput(inputVal)
	if dispatchInput == "" {
		return m, nil
	}

	log.Printf("user input: %q", inputVal)

	if m.pendingRunCommand != nil {
		if !isSlashCommand {
			feedback := strings.TrimSpace(inputVal)
			m.input.SetValue("")
			m.updateSlashCompletion()
			return m, func() tea.Msg {
				return runCommandConfirmMsg{approved: false, feedback: feedback}
			}
		}
		fields := strings.Fields(dispatchInput)
		if len(fields) == 0 {
			return m, nil
		}
		cmd := strings.ToLower(fields[0])
		if cmd != "/confirm-run" && cmd != "/reject-run" {
			return m, m.printSystemDisplayOnly("Command confirmation is pending. Type /confirm-run to execute, /reject-run to cancel, or type guidance without a slash to reject with an instruction.")
		}
	}

	// 入力履歴に追加（重複する直前の入力は追加しない）
	if len(m.inputHistory) == 0 || m.inputHistory[len(m.inputHistory)-1] != inputVal {
		m.inputHistory = append(m.inputHistory, inputVal)
	}
	m.historyIndex = -1 // 履歴ナビゲーションをリセット

	// スラッシュコマンドのチェック
	if isSlashCommand {
		log.Printf("slash command detected: %s", strings.Fields(dispatchInput)[0])
		return m.handleSlashCommand(dispatchInput)
	}

	return m.startChatTurn(inputVal, m.promptWithDebugContext(inputVal))
}

func slashCommandInput(input string) (string, bool) {
	trimmed := strings.TrimSpace(input)
	return trimmed, strings.HasPrefix(trimmed, "/")
}

func (m *Model) handleHistoryCommand(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		return m, m.printSystemDisplayOnly(formatInputHistory(m.inputHistory, 10))
	}
	if len(args) != 1 {
		return m, m.printSystemDisplayOnly("Usage: /history [number]")
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n <= 0 {
		return m, m.printSystemDisplayOnly("Usage: /history [number]")
	}
	return m.restoreInputHistoryEntry(len(m.inputHistory) - n)
}

func (m *Model) restoreInputHistoryEntry(index int) (tea.Model, tea.Cmd) {
	if len(m.inputHistory) == 0 {
		return m, m.printSystemDisplayOnly("Input history is empty.")
	}
	if index < 0 || index >= len(m.inputHistory) {
		return m, m.printSystemDisplayOnly(fmt.Sprintf("Input history entry must be between 1 and %d.", len(m.inputHistory)))
	}
	m.input.SetValue(m.inputHistory[index])
	m.input.CursorEnd()
	m.historyIndex = index
	m.adjustInputHeight()
	m.updateSlashCompletion()
	return m, m.printSystemDisplayOnly("Restored input history entry. Edit if needed, then send with Alt+Enter or Ctrl+D.")
}

func formatInputHistory(history []string, limit int) string {
	if len(history) == 0 {
		return "Input history is empty."
	}
	if limit <= 0 || limit > len(history) {
		limit = len(history)
	}
	start := len(history) - limit
	var b strings.Builder
	b.WriteString("Input history:\n")
	for i := len(history) - 1; i >= start; i-- {
		n := len(history) - i
		entry := strings.ReplaceAll(strings.TrimSpace(history[i]), "\n", "\\n")
		if len([]rune(entry)) > 120 {
			runes := []rune(entry)
			entry = string(runes[:120]) + "..."
		}
		fmt.Fprintf(&b, "%d. %s\n", n, entry)
	}
	b.WriteString("\nUse /history <number> to restore an entry without sending it.")
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) navigateInputHistory(direction int) (tea.Model, tea.Cmd) {
	switch {
	case direction < 0:
		if len(m.inputHistory) == 0 {
			return m, nil
		}
		if m.historyIndex == -1 {
			m.historyIndex = len(m.inputHistory) - 1
		} else if m.historyIndex > 0 {
			m.historyIndex--
		} else {
			return m, nil
		}
		m.input.SetValue(m.inputHistory[m.historyIndex])
		m.input.CursorEnd()
	case direction > 0:
		if m.historyIndex == -1 {
			return m, nil
		}
		if m.historyIndex < len(m.inputHistory)-1 {
			m.historyIndex++
			m.input.SetValue(m.inputHistory[m.historyIndex])
			m.input.CursorEnd()
		} else {
			m.historyIndex = -1
			m.input.SetValue("")
		}
	default:
		return m, nil
	}
	m.adjustInputHeight()
	m.updateSlashCompletion()
	return m, nil
}

func (m Model) startChatTurn(displayInput string, agentInput string) (tea.Model, tea.Cmd) {
	return m.startChatTurnWithOptions(displayInput, agentInput, m.consumeVMaxRunOptions())
}

func (m Model) startChatTurnWithOptions(displayInput string, agentInput string, runOpts agent.RunOptions) (tea.Model, tea.Cmd) {
	if m.awaitingContinuation {
		return m, m.printSystemDisplayOnly("The previous task is paused at the iteration limit. Type /continue to proceed, or /abort to stop before starting a new request.")
	}
	m.doFlowActive = false
	m.doFlowRemaining = 0
	m.doFlowContinueOptions = agent.RunOptions{}

	// Reset pending rewind if user sends a normal message
	m.pendingRewind = nil

	// Turn start
	m.turnNumber++
	t, err := m.repo.Turns.Create(m.sessionID, m.turnNumber, displayInput)
	if err != nil {
		log.Printf("failed to create turn: %v", err)
	} else {
		m.currentTurnID = t.ID
	}

	// Add user message for display
	userMsg := llm.Message{
		Role:    "user",
		Content: displayInput,
	}
	m.messages = append(m.messages, userMsg)

	m.input.SetValue("")
	m.adjustInputHeight()
	m.updateSlashCompletion()
	m.waiting = true
	m.waitingStartedAt = time.Now()
	m.err = nil
	m.lastToolCalls = nil
	m.partialAssistantContent = "" // リセット
	m.lastActivityMessage = ""     // リセット
	m.currentIteration = 1         // 初期化

	ctx, cancel := newAgentRunContext()
	m.cancelAgent = cancel

	return m, tea.Batch(
		tea.Printf("%s", m.renderSingleMessage(userMsg, nil)),
		m.spinner.Tick,
		m.callAgent(ctx, agentInput, runOpts),
	)
}

func (m Model) startUnstuckTurn() (tea.Model, tea.Cmd) {
	m.awaitingContinuation = false
	m.lastIterationLimitReached = false
	m.doFlowActive = false
	m.doFlowRemaining = 0
	m.doFlowContinueOptions = agent.RunOptions{}
	m.partialAssistantContent = ""
	return m.startChatTurn("/unstuck", unstuckPrompt())
}

func unstuckPrompt() string {
	return `UNSTUCK MODE.

The previous local-LLM attempt appears to have stalled or was cancelled. Do not continue hidden reasoning, partial text, or the same long analysis path.

Reset your next step:
- If the next concrete action is clear, make exactly one focused tool call.
- If no tool is needed, answer with at most 5 concise bullets.
- Do not generate long code or a long plan.
- Do not repeat prior analysis. State only the next decision/action and proceed.
- Preserve the user's active task constraints from the conversation history.`
}

func (m Model) handlePendingActionKey(key string) (bool, tea.Model, tea.Cmd) {
	if _, actions := m.pendingActions(); len(actions) == 0 {
		return false, m, nil
	}
	if key != "esc" && strings.TrimSpace(m.input.Value()) != "" {
		return false, m, nil
	}

	switch key {
	case "1":
		switch {
		case m.pendingRunCommand != nil:
			return true, m, func() tea.Msg {
				return runCommandConfirmMsg{approved: true}
			}
		case m.awaitingContinuation:
			next, cmd := m.continuePausedAgent()
			return true, next, cmd
		case m.pendingRewind != nil:
			return true, m, func() tea.Msg {
				return rewindConfirmMsg{}
			}
		}
	case "2", "esc":
		switch {
		case m.pendingRunCommand != nil:
			return true, m, func() tea.Msg {
				return runCommandConfirmMsg{approved: false}
			}
		case m.awaitingContinuation:
			m.awaitingContinuation = false
			m.lastIterationLimitReached = false
			m.doFlowActive = false
			m.doFlowRemaining = 0
			m.doFlowContinueOptions = agent.RunOptions{}
			return true, m, m.printSystemDisplayOnly("Stopped. The paused task will not be continued.")
		case m.pendingRewind != nil:
			m.pendingRewind = nil
			return true, m, m.printSystemDisplayOnly("Rewind cancelled.")
		}
	}

	return false, m, nil
}

func (m *Model) handleSlashCommand(input string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return m, nil
	}

	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	m.input.SetValue("")
	m.updateSlashCompletion()

	switch cmd {
	case "/rewind":
		target := ""
		if len(args) > 0 {
			target = args[0]
		}
		return m, func() tea.Msg {
			return rewindRequestMsg{target: target}
		}

	case "/confirm":
		return m, func() tea.Msg {
			return rewindConfirmMsg{}
		}

	case "/continue":
		if !m.awaitingContinuation {
			return m, m.printSystemDisplayOnly("No paused task to continue.")
		}
		return m.continuePausedAgent()

	case "/unstuck":
		if len(args) > 0 {
			return m, m.printSystemDisplayOnly("Usage: /unstuck")
		}
		return m.startUnstuckTurn()

	case "/abort":
		if !m.awaitingContinuation {
			return m, m.printSystemDisplayOnly("No paused task to abort.")
		}
		m.awaitingContinuation = false
		m.lastIterationLimitReached = false
		m.doFlowActive = false
		m.doFlowRemaining = 0
		m.doFlowContinueOptions = agent.RunOptions{}
		return m, m.printSystemDisplayOnly("Stopped. The paused task will not be continued.")

	case "/confirm-run":
		if m.pendingRunCommand == nil {
			return m, m.printSystem("No pending command to confirm.")
		}
		return m, func() tea.Msg {
			return runCommandConfirmMsg{approved: true}
		}

	case "/reject-run":
		if m.pendingRunCommand == nil {
			return m, m.printSystem("No pending command to reject.")
		}
		return m, func() tea.Msg {
			return runCommandConfirmMsg{approved: false}
		}

	case "/history":
		return m.handleHistoryCommand(args)

	case "/last":
		return m.restoreInputHistoryEntry(len(m.inputHistory) - 1)

	case "/clear":
		m.awaitingContinuation = false
		m.debugContext = nil
		m.vmaxArmed = false
		m.vmaxActive = false
		return m, func() tea.Msg {
			return clearSessionRequestMsg{}
		}

	case "/debug-context":
		if len(args) > 0 && strings.ToLower(args[0]) == "clear" {
			m.debugContext = nil
			return m, m.printSystemDisplayOnly("Debug context cleared.")
		}
		ctx, err := m.loadDebugContext()
		if err != nil {
			return m, m.printSystemDisplayOnly(fmt.Sprintf("❌ Failed to load debug context: %v", err))
		}
		m.debugContext = ctx
		question := strings.TrimSpace(strings.TrimPrefix(input, "/debug-context"))
		if question != "" {
			return m.startChatTurn(question, m.promptWithDebugContext(question))
		}
		return m, m.printSystemDisplayOnly(ctx.Summary())

	case "/vmax":
		if !m.vmaxAvailable {
			return m, m.printSystemDisplayOnly("VMAX is disabled. Start Virgil with --dangerous-vmax to enable /vmax.")
		}
		if len(args) > 0 {
			return m, m.printSystemDisplayOnly("Usage: /vmax")
		}
		m.vmaxArmed = true
		m.vmaxActive = false
		return m, m.printSystemDisplayOnly("VMAX ready! The next chat or /task will use 60 iterations and auto-accept run_command confirmations.")

	case "/task":
		description := strings.TrimSpace(strings.TrimPrefix(input, "/task"))
		if description == "" {
			return m, m.printSystem("⚠️ /task requires a description. Example: /task add tests for tokenizer")
		}
		return m, func() tea.Msg {
			return taskRequestMsg{description: description}
		}
	case "/tasks":
		if len(args) != 1 {
			return m, m.printSystem("Usage: /tasks <task_document.md>")
		}
		path, tasks, err := loadTaskBreakdown(m.workspaceRoot, args[0])
		if err != nil {
			return m, m.printSystem(fmt.Sprintf("❌ /tasks error: %v", err))
		}
		return m, m.printSystemDisplayOnly(formatTaskList(path, tasks))

	case "/do":
		flow := false
		if len(args) == 3 && args[2] == "--flow" {
			flow = true
		}
		if len(args) != 2 && !flow {
			return m, m.printSystem("Usage: /do <task-id> <task_document.md> [--flow]")
		}
		taskID := args[0]
		path, tasks, err := loadTaskBreakdown(m.workspaceRoot, args[1])
		if err != nil {
			return m, m.printSystem(fmt.Sprintf("❌ /do error: %v", err))
		}
		task, ok := findBreakdownTask(tasks, taskID)
		if !ok {
			return m, m.printSystem(fmt.Sprintf("❌ /do error: task %s not found in %s", taskID, path))
		}
		switch strings.ToLower(strings.TrimSpace(task.Status)) {
		case "done":
			return m, m.printSystemDisplayOnly(fmt.Sprintf("Task %s is already done.", task.ID))
		case "skipped":
			return m, m.printSystemDisplayOnly(fmt.Sprintf("Task %s is skipped.", task.ID))
		}
		prompt := buildDoTaskPrompt(path, task, dependencyWarnings(task, tasks))
		display := fmt.Sprintf("/do %s %s", task.ID, args[1])
		if flow {
			display += " --flow"
			prompt += "\nFlow mode: Continue working until the task is genuinely complete. If you reach an iteration window limit, resume without repeating completed work.\n"
		}
		return m, func() tea.Msg {
			return taskRequestMsg{description: prompt, display: display, flow: flow}
		}

	case "/task-status":
		if len(args) != 3 {
			return m, m.printSystem("Usage: /task-status <task-id> <status> <task_document.md>")
		}
		path, old, err := updateTaskStatus(m.workspaceRoot, args[2], args[0], args[1])
		if err != nil {
			return m, m.printSystem(fmt.Sprintf("❌ /task-status error: %v", err))
		}
		return m, m.printSystemDisplayOnly(fmt.Sprintf("Updated %s: %s %s -> %s", path, args[0], old, args[1]))

	case "/breakdown":
		breakdown, err := parseBreakdownCommand(input)
		if err != nil {
			return m, m.printSystem(fmt.Sprintf("Usage: /breakdown <source> [--output <task_document.md>]\n❌ %v", err))
		}
		if breakdown.Output == "" {
			breakdown.Output = defaultBreakdownOutputPath(breakdown.Source)
		}
		if _, err := ensureBreakdownOutputPath(m.workspaceRoot, breakdown.Output); err != nil {
			return m, m.printSystem(fmt.Sprintf("❌ /breakdown error: %v", err))
		}
		if _, err := ensureTaskBreakdownTemplate(m.workspaceRoot); err != nil {
			return m, m.printSystem(fmt.Sprintf("❌ /breakdown error: %v", err))
		}
		prompt := buildBreakdownPrompt(breakdown)
		display := "/breakdown " + breakdown.Source
		display += " --output " + breakdown.Output
		return m.startChatTurnWithOptions(display, prompt, agent.RunOptions{ForceEditMode: true})

	case "/breakdown-last":
		output, err := parseOutputOnlyCommand(input)
		if err != nil {
			return m, m.printSystem(fmt.Sprintf("Usage: /breakdown-last [--output <task_document.md>]\n❌ %v", err))
		}
		source, ok := lastAssistantContent(m.history)
		if !ok {
			return m, m.printSystem("❌ /breakdown-last error: no previous assistant response found")
		}
		if output == "" {
			output = defaultBreakdownLastOutputPath(m.workspaceRoot, source)
		}
		if _, err := ensureBreakdownOutputPath(m.workspaceRoot, output); err != nil {
			return m, m.printSystem(fmt.Sprintf("❌ /breakdown-last error: %v", err))
		}
		if _, err := ensureTaskBreakdownTemplate(m.workspaceRoot); err != nil {
			return m, m.printSystem(fmt.Sprintf("❌ /breakdown-last error: %v", err))
		}
		prompt := buildBreakdownPrompt(breakdownCommand{Source: source, Output: output})
		return m.startChatTurnWithOptions("/breakdown-last --output "+output, prompt, agent.RunOptions{ForceEditMode: true})

	case "/copy-last":
		content, ok := lastAssistantContent(m.history)
		if !ok {
			return m, m.printSystem("❌ /copy-last error: no previous assistant response found")
		}
		if err := clipboard.WriteAll(content); err != nil {
			return m, m.printSystem(fmt.Sprintf("❌ /copy-last error: %v", err))
		}
		return m, m.printSystemDisplayOnly(fmt.Sprintf("Copied last assistant response to clipboard (%d chars).", len([]rune(content))))
	case "/reindex":
		if m.indexer == nil {
			return m, m.printSystem("⚠️ Symbol indexer is not available.")
		}
		force := false
		for _, arg := range args {
			if arg == "--force" || arg == "-f" {
				force = true
			}
		}
		return m, func() tea.Msg {
			return reindexRequestMsg{force: force}
		}

	case "/callers":
		if m.callRepo == nil {
			return m, m.printSystem("⚠️ Call graph is not available.")
		}
		if len(args) == 0 {
			return m, m.printSystem("Usage: /callers <function_name>")
		}
		name := args[0]
		records, err := m.callRepo.FindIncoming(name, 30)
		if err != nil {
			return m, m.printSystem(fmt.Sprintf("Error: %v", err))
		}
		return m, m.printSystem(tools.FormatCallersResult(name, records, 30))

	case "/callgraph":
		if m.callRepo == nil {
			return m, m.printSystem("⚠️ Call graph is not available.")
		}
		if len(args) == 0 {
			return m, m.printSystem("Usage: /callgraph <function_name> [depth]")
		}
		name := args[0]
		depth := 3
		if len(args) > 1 {
			if n, err := strconv.Atoi(args[1]); err == nil && n > 0 {
				depth = n
			}
		}
		return m, m.printSystem(tools.BuildCallGraphReport(m.callRepo, name, depth))

	case "/btw":
		question := strings.TrimSpace(strings.TrimPrefix(input, "/btw"))
		if question == "" {
			return m, m.printSystem("⚠️ /btw requires a question. Example: /btw What does this function do?")
		}
		return m, func() tea.Msg {
			return btwRequestMsg{question: question}
		}

	case "/shrink":
		if m.shrinking {
			return m, m.printSystem("⚠️ /shrink is already running.")
		}
		base, older, recent := splitHistoryForShrink(m.history)
		if len(older) == 0 {
			return m, m.printSystem("⚠️ Nothing to shrink yet. Continue for a few more turns, then run /shrink.")
		}
		m.shrinking = true
		m.waiting = true
		m.waitingStartedAt = time.Now()
		m.partialAssistantContent = ""
		m.lastActivityMessage = "Compressing old context..."
		ctx, cancel := newAgentRunContext()
		m.cancelAgent = cancel
		return m, tea.Batch(
			m.spinner.Tick,
			m.shrinkHistory(ctx, base, older, recent, false, m.currentTokens, contextUsagePercent(m.currentTokens, m.contextLimit)),
		)

	case "/help":
		return m, m.printSystem(m.slashCommandHelp())

	default:
		return m, m.printSystem(fmt.Sprintf("Unknown command: %s. Type /help for available commands.", cmd))
	}
}

func (m *Model) appendSystemMessage(text string) {
	msg := llm.Message{
		Role:    "system",
		Content: text,
	}
	m.messages = append(m.messages, msg)
	m.history = append(m.history, msg)
}

func lastAssistantContent(messages []llm.Message) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "assistant" {
			continue
		}
		content := strings.TrimSpace(messages[i].Content)
		if content != "" {
			return content, true
		}
	}
	return "", false
}

func iterationPausePrompt(maxIterations int) string {
	if maxIterations <= 0 {
		maxIterations = agent.MaxIterations
	}
	return fmt.Sprintf("⚠️ The agent has reached the maximum of %d iterations.\n", maxIterations) +
		"Review the partial result above. Do you want the agent to continue?\n" +
		fmt.Sprintf("Type /continue to proceed for another %d iterations, or /abort to stop.", agent.MaxIterations)
}

func (m Model) continuePausedAgent() (tea.Model, tea.Cmd) {
	m.awaitingContinuation = false
	m.lastIterationLimitReached = false
	m.doFlowActive = false
	m.doFlowRemaining = 0
	m.doFlowContinueOptions = agent.RunOptions{}
	m.waiting = true
	m.waitingStartedAt = time.Now()
	m.partialAssistantContent = ""
	m.lastActivityMessage = "Continuing from iteration limit..."
	m.currentIteration = 1
	m.err = nil

	ctx, cancel := newAgentRunContext()
	m.cancelAgent = cancel

	return m, tea.Batch(
		m.printSystemDisplayOnly("Continuing paused task for another iteration window..."),
		m.spinner.Tick,
		m.callAgent(ctx, "Please continue the task from where you left off. Do not repeat completed work. If the requested verification has already passed, provide the final report now.", agent.RunOptions{}),
	)
}

func (m Model) continueDoFlowAfterIterationLimit(previousCmds ...tea.Cmd) (tea.Model, tea.Cmd) {
	if m.doFlowRemaining <= 0 {
		m.doFlowActive = false
		m.doFlowContinueOptions = agent.RunOptions{}
		m.awaitingContinuation = true
		m.lastIterationLimitReached = true
		cmds := append([]tea.Cmd{}, previousCmds...)
		cmds = append(cmds, m.printSystemDisplayOnly("⚠️ /do --flow reached its automatic continuation limit. Review the partial result, then use /continue or /abort."))
		return m, tea.Batch(cmds...)
	}

	m.doFlowRemaining--
	m.awaitingContinuation = false
	m.lastIterationLimitReached = false
	m.waiting = true
	m.waitingStartedAt = time.Now()
	m.partialAssistantContent = ""
	m.lastActivityMessage = "Continuing /do --flow..."
	m.currentIteration = 1
	m.err = nil

	ctx, cancel := newAgentRunContext()
	m.cancelAgent = cancel

	cmds := append([]tea.Cmd{}, previousCmds...)
	cmds = append(cmds,
		m.printSystemDisplayOnly(fmt.Sprintf("Continuing /do --flow automatically (%d windows remaining)...", m.doFlowRemaining)),
		m.spinner.Tick,
		m.callAgent(ctx, "Continue the /do task from where you left off. Do not repeat completed work. Keep going until the task is genuinely complete. If the requested verification has already passed, provide the final report now.", m.doFlowContinueOptions),
	)
	return m, tea.Batch(cmds...)
}

func (m Model) slashCommandHelp() string {
	profile := m.toolProfile
	if profile == "" {
		profile = "default"
	}
	if !m.fullPowerCommands {
		return fmt.Sprintf(`Tool profile: %s

Available slash commands:
  /rewind          Show shadow git history
  /task <task>     Execute a task with structured TODO list and result report
  /tasks <path>    List task IDs and status from a task breakdown document
  /do <id> <path> [--flow]  Execute one task; --flow auto-continues at iteration limits
  /breakdown <source> [--output <path>]  Generate a task breakdown document
  /breakdown-last [--output <path>]  Generate tasks from the last assistant response
  /copy-last       Copy the last assistant response as raw Markdown
  /btw <task>      Execute a single quick task without TODO structure
  /reindex         Reindex workspace (mtime-based diff; auto-forces on index version changes)
  /shrink          Compress older context into a summary (auto at 50%% or 20+ messages)
  /history [n]     Show input history or restore entry n into the input box
  /last            Restore the previous input into the input box
  /clear           Clear context and start a new session
  /continue        Continue a task paused at the iteration limit
  /unstuck         Restart from a stalled/cancelled local-LLM attempt with a constrained next step
  /abort           Stop a task paused at the iteration limit
  /debug-context   Load debug context JSON and attach it to chat and /task
  /vmax            Arm one-shot VMAX mode when started with --dangerous-vmax
  /help            Show this help

Start with 'virgil fullpower' to show and complete all slash commands.

Keyboard shortcuts:
  Enter            Insert newline
  Alt+Enter        Send message
  Ctrl+D           Send message
  Alt+PageUp/PageDown or Alt+Up/Down  Navigate input history
  1 / 2            Choose pending action when the input is empty
  Esc              Cancel pending action
  Shift+Tab        Toggle Plan/Edit mode
  Ctrl+C (twice)   Quit Virgil`, profile)
	}
	return fmt.Sprintf(`Tool profile: %s

Available slash commands:
  /rewind          Show shadow git history
  /rewind <N>      Rewind to N-th recent commit
  /rewind <hash>   Rewind to specific commit hash
  /confirm         Confirm pending rewind operation
  /continue        Continue a task paused at the iteration limit
  /unstuck         Restart from a stalled/cancelled local-LLM attempt with a constrained next step
  /abort           Stop a task paused at the iteration limit
  /clear           Clear context and start a new session
  /task <task>     Execute a task with structured TODO list and result report
  /tasks <path>    List task IDs and status from a task breakdown document
  /do <id> <path> [--flow]  Execute one task; --flow auto-continues at iteration limits
  /task-status <id> <status> <path>  Update one task status line
  /breakdown <source> [--output <path>]  Generate a task breakdown document
  /breakdown-last [--output <path>]  Generate tasks from the last assistant response
  /copy-last       Copy the last assistant response as raw Markdown
  /reindex         Reindex workspace (mtime-based diff; auto-forces on index version changes)
  /reindex --force Force reindex (ignore mtime cache; use after parser/indexer changes)
  /callers <name>  Show callers of a function (reverse lookup)
  /callgraph <name> [depth]  Show call graph from a function (Mermaid)
  /shrink          Compress older context into a summary (auto at 50%% or 20+ messages)
  /history [n]     Show input history or restore entry n into the input box
  /last            Restore the previous input into the input box
  /confirm-run     Approve pending shell command
  /reject-run      Reject pending shell command
  <guidance>       While a shell command is pending, reject it and send guidance
  /debug-context   Load debug context JSON and attach it to chat and /task
  /debug-context clear  Clear the active debug context
  /vmax            Arm one-shot VMAX mode when started with --dangerous-vmax
  /btw <task>      Execute a single quick task without TODO structure
  /help            Show this help

Keyboard shortcuts:
  Enter            Insert newline
  Alt+Enter        Send message
  Ctrl+D           Send message
  Alt+PageUp/PageDown or Alt+Up/Down  Navigate input history
  1 / 2            Choose pending action when the input is empty
  Esc              Cancel pending action
  Shift+Tab        Toggle Plan/Edit mode
  Ctrl+C (twice)   Quit Virgil`, profile)
}

func (m *Model) printSystem(text string) tea.Cmd {
	msg := llm.Message{
		Role:    "system",
		Content: text,
	}
	m.messages = append(m.messages, msg)
	m.history = append(m.history, msg) // システムメッセージも履歴に残す
	return tea.Printf("%s", m.renderSingleMessage(msg, nil))
}

func (m *Model) printSystemDisplayOnly(text string) tea.Cmd {
	msg := llm.Message{
		Role:    "system",
		Content: text,
	}
	m.messages = append(m.messages, msg)
	return tea.Printf("%s", m.renderSingleMessage(msg, nil))
}

func (m *Model) printAssistant(content string, toolCalls []agent.ToolCallRecord) tea.Cmd {
	msg := llm.Message{
		Role:    "assistant",
		Content: content,
	}
	m.messages = append(m.messages, msg)
	// assistantメッセージは history (LLM用) には追加しない。
	// なぜなら、agent.Run() が返す Messages がすでに最新の履歴を含んでいるから。
	return tea.Printf("%s", m.renderSingleMessage(msg, toolCalls))
}

func (m *Model) showRewindHistory() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		commits, err := m.shadow.LogRecent(ctx, 20)
		if err != nil {
			return rewindCompleteMsg{err: fmt.Errorf("failed to get log: %w", err)}
		}

		var sb strings.Builder
		sb.WriteString("Recent shadow commits:\n\n")
		for i, c := range commits {
			shortHash := c.Hash
			if len(shortHash) > 7 {
				shortHash = shortHash[:7]
			}
			sb.WriteString(fmt.Sprintf("  [%d] %s  %s\n", i+1, shortHash, c.Message))
		}
		sb.WriteString("\nUse /rewind <N> or /rewind <hash> to revert.\n")

		return historyDisplayMsg{content: sb.String()}
	}
}

const (
	shrinkRecentMessages     = 6
	shrinkMaxMessagesToInput = 24
	autoShrinkInfoPercent    = 30
	autoShrinkTriggerPercent = 50
	autoShrinkMessageLimit   = 20
	autoShrinkCooldown       = 6
)

type autoShrinkDecision struct {
	trigger       bool
	info          string
	reason        string
	base          []llm.Message
	older         []llm.Message
	recent        []llm.Message
	beforeTokens  int
	beforePercent int
}

func contextUsagePercent(tokens int, limit int) int {
	if tokens <= 0 || limit <= 0 {
		return 0
	}
	return tokens * 100 / limit
}

func (m *Model) shouldAutoShrink(estimatedTokens int, contextLimit int) autoShrinkDecision {
	if estimatedTokens <= 0 && m.agent != nil {
		estimatedTokens = m.agent.EstimateContextTokens(m.history)
	}
	percent := contextUsagePercent(estimatedTokens, contextLimit)
	base, older, recent := splitHistoryForShrink(m.history)

	decision := autoShrinkDecision{
		base:          base,
		older:         older,
		recent:        recent,
		beforeTokens:  estimatedTokens,
		beforePercent: percent,
	}

	if !m.shrinkInfoShown && contextLimit > 0 && percent >= autoShrinkInfoPercent && percent < autoShrinkTriggerPercent {
		decision.info = fmt.Sprintf(
			"Context usage: %d%% (%d / %d tokens). Auto-shrink will trigger at %d%%.",
			percent, estimatedTokens, contextLimit, autoShrinkTriggerPercent,
		)
	}

	triggerByTokens := contextLimit > 0 && percent >= autoShrinkTriggerPercent
	triggerByMessages := len(m.history) > autoShrinkMessageLimit
	trigger := triggerByTokens || triggerByMessages
	if triggerByTokens {
		decision.reason = fmt.Sprintf("context usage %d%%", percent)
	} else if triggerByMessages {
		decision.reason = fmt.Sprintf("history has %d messages", len(m.history))
	}

	cooldownActive := m.lastAutoShrinkHistoryLen > 0 && len(m.history)-m.lastAutoShrinkHistoryLen < autoShrinkCooldown
	if m.shrinking || cooldownActive || len(older) == 0 {
		trigger = false
	}
	decision.trigger = trigger

	log.Printf(
		"auto-shrink check: tokens=%d/%d (%d%%), messages=%d, trigger=%v",
		estimatedTokens, contextLimit, percent, len(m.history), decision.trigger,
	)

	return decision
}

func (m *Model) appendAutoShrinkCommands(cmds []tea.Cmd) []tea.Cmd {
	decision := m.shouldAutoShrink(m.currentTokens, m.contextLimit)
	if decision.info != "" {
		m.shrinkInfoShown = true
		cmds = append(cmds, m.printSystemDisplayOnly(decision.info))
	}
	if !decision.trigger {
		return cmds
	}

	m.shrinking = true
	m.waiting = true
	m.waitingStartedAt = time.Now()
	m.partialAssistantContent = ""
	m.lastActivityMessage = "Auto-shrinking old context..."
	ctx, cancel := newAgentRunContext()
	m.cancelAgent = cancel

	cmds = append(cmds,
		m.printSystemDisplayOnly(fmt.Sprintf("🔻 Auto-shrink triggered (%s): compressing older context...", decision.reason)),
		m.spinner.Tick,
		m.shrinkHistory(ctx, decision.base, decision.older, decision.recent, true, decision.beforeTokens, decision.beforePercent),
	)
	return cmds
}

func splitHistoryForShrink(history []llm.Message) (base []llm.Message, older []llm.Message, recent []llm.Message) {
	if len(history) == 0 {
		return nil, nil, nil
	}

	start := 0
	if history[0].Role == "system" {
		base = append(base, history[0])
		start = 1
	}

	body := history[start:]
	if len(body) <= shrinkRecentMessages {
		return base, nil, body
	}

	split := len(body) - shrinkRecentMessages
	older = body[:split]
	recent = body[split:]
	if len(older) > shrinkMaxMessagesToInput {
		older = older[len(older)-shrinkMaxMessagesToInput:]
	}

	return base, older, recent
}

func pinUserMessagesForShrink(messages []llm.Message) (summarizable []llm.Message, pinned []llm.Message) {
	if len(messages) == 0 {
		return nil, nil
	}
	summarizable = make([]llm.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == "user" && strings.TrimSpace(msg.Content) != "" {
			pinned = append(pinned, msg)
			continue
		}
		summarizable = append(summarizable, msg)
	}
	return summarizable, pinned
}

func buildCompressedHistory(base []llm.Message, summary string, pinned []llm.Message, recent []llm.Message) []llm.Message {
	compressed := make([]llm.Message, 0, len(base)+1+len(pinned)+len(recent))
	compressed = append(compressed, base...)
	if strings.TrimSpace(summary) != "" {
		compressed = append(compressed, llm.Message{
			Role: "system",
			Content: fmt.Sprintf(
				"Previous conversation summary, compressed by /shrink at %s:\n\n%s",
				time.Now().Format(time.RFC3339),
				summary,
			),
		})
	}
	compressed = append(compressed, pinned...)
	compressed = append(compressed, recent...)
	return compressed
}

func (m *Model) shrinkHistory(ctx context.Context, base []llm.Message, older []llm.Message, recent []llm.Message, auto bool, beforeTokens int, beforePercent int) tea.Cmd {
	turnID := m.currentTurnID
	return func() tea.Msg {
		m.agent.SetCurrentTurnID(turnID)
		summarizable, pinnedUsers := pinUserMessagesForShrink(older)
		var summary string
		if len(summarizable) > 0 {
			var err error
			summary, err = m.agent.SummarizeHistory(ctx, summarizable)
			if err != nil {
				return shrinkCompleteMsg{auto: auto, err: err}
			}
		}

		saved := false
		if turnID != 0 && strings.TrimSpace(summary) != "" {
			if err := m.repo.Turns.UpdateTurnSummary(turnID, summary); err != nil {
				return shrinkCompleteMsg{auto: auto, err: fmt.Errorf("save summary: %w", err)}
			}
			saved = true
		}

		return shrinkCompleteMsg{
			summary:            summary,
			newHistory:         buildCompressedHistory(base, summary, pinnedUsers, recent),
			summarizedMessages: len(summarizable),
			keptMessages:       len(recent) + len(pinnedUsers),
			pinnedMessages:     len(pinnedUsers),
			saved:              saved,
			auto:               auto,
			beforeTokens:       beforeTokens,
			beforePercent:      beforePercent,
		}
	}
}

func (m *Model) prepareRewind(target string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// 履歴を取得
		commits, err := m.shadow.LogRecent(ctx, 50)
		if err != nil {
			return rewindCompleteMsg{err: err}
		}

		// ターゲットを解決（番号 or hash prefix）
		var targetCommit *shadow.CommitInfo
		if num, err := strconv.Atoi(target); err == nil {
			// 番号指定
			if num < 1 || num > len(commits) {
				return rewindCompleteMsg{err: fmt.Errorf("invalid index: %d (range: 1-%d)", num, len(commits))}
			}
			targetCommit = &commits[num-1]
		} else {
			// hash prefix
			for i := range commits {
				if strings.HasPrefix(commits[i].Hash, target) {
					targetCommit = &commits[i]
					break
				}
			}
			if targetCommit == nil {
				return rewindCompleteMsg{err: fmt.Errorf("commit not found: %s", target)}
			}
		}

		// 追加: 差分を取得
		diff, err := m.shadow.DiffFromCurrent(ctx, targetCommit.Hash)
		if err != nil {
			log.Printf("warning: failed to get diff: %v", err)
			// 差分取得失敗してもrewindは続行可能
			diff = &shadow.DiffSummary{}
		}

		return rewindPreparedMsg{
			hash:    targetCommit.Hash,
			message: targetCommit.Message,
			diff:    diff,
		}
	}
}

func (m *Model) executeRewind(targetHash string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// rewind前にpre-commitを取る（戻れるように）
		preRewindHash, err := m.shadow.CommitPre(ctx, "before-rewind")
		if err != nil {
			log.Printf("warning: failed to commit before rewind: %v", err)
			// 続行
		} else {
			log.Printf("created safety commit before rewind: %s", preRewindHash)
		}

		// rewind実行
		if err := m.shadow.Rewind(ctx, targetHash); err != nil {
			return rewindCompleteMsg{
				success: false,
				err:     err,
			}
		}

		return rewindCompleteMsg{
			success:    true,
			targetHash: targetHash,
		}
	}
}

func (m *Model) callBtw(ctx context.Context, question string) tea.Cmd {
	return func() tea.Msg {
		resp, err := m.agent.RunBtw(ctx, m.history, question)
		return agentBtwResponseMsg{
			question: question,
			response: resp,
			err:      err,
		}
	}
}

func (m *Model) callTask(ctx context.Context, description string, opts agent.RunOptions) tea.Cmd {
	turnID := m.currentTurnID
	return func() tea.Msg {
		m.agent.SetCurrentTurnID(turnID)
		resp, err := m.agent.RunTaskWithOptions(ctx, m.history, description, opts)
		return agentTaskResponseMsg{
			description: description,
			response:    resp,
			err:         err,
		}
	}
}

func (m *Model) callAgent(ctx context.Context, userInput string, opts agent.RunOptions) tea.Cmd {
	turnID := m.currentTurnID // クロージャでキャプチャ
	return func() tea.Msg {
		m.agent.SetCurrentTurnID(turnID)
		resp, err := m.agent.RunWithOptions(ctx, m.history, userInput, opts)
		return agentResponseMsg{
			response: resp,
			err:      err,
		}
	}
}

func (m Model) debugContextPath() string {
	candidates := m.debugContextCandidates()
	if len(candidates) == 0 {
		return filepath.Join(m.workspaceRoot, ".vscode", "debug-context.json")
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return candidates[0]
}

func (m Model) debugContextCandidates() []string {
	if configured := strings.TrimSpace(os.Getenv("VIRGIL_DEBUG_CONTEXT_PATH")); configured != "" {
		if filepath.IsAbs(configured) {
			return []string{configured}
		}
		return []string{filepath.Join(m.workspaceRoot, configured)}
	}

	var candidates []string
	seen := map[string]bool{}
	add := func(path string) {
		clean := filepath.Clean(path)
		if !seen[clean] {
			seen[clean] = true
			candidates = append(candidates, clean)
		}
	}

	root := filepath.Clean(m.workspaceRoot)
	add(filepath.Join(root, ".vscode", "debug-context.json"))
	add(filepath.Join(root, ".virgil", "debug-context.json"))

	for parent := filepath.Dir(root); parent != root && parent != "."; parent = filepath.Dir(parent) {
		add(filepath.Join(parent, ".vscode", "debug-context.json"))
		add(filepath.Join(parent, ".virgil", "debug-context.json"))
		if filepath.Dir(parent) == parent {
			break
		}
	}
	return candidates
}

func (m Model) loadDebugContext() (*debugctx.Context, error) {
	candidates := m.debugContextCandidates()
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return debugctx.Load(candidate, m.workspaceRoot)
		}
	}
	return nil, fmt.Errorf("debug context file not found; searched: %s", strings.Join(candidates, ", "))
}

func (m Model) promptWithDebugContext(prompt string) string {
	return debugctx.WithPrompt(m.debugContext, prompt)
}

func (m *Model) consumeVMaxRunOptions() agent.RunOptions {
	if !m.vmaxAvailable || !m.vmaxArmed {
		m.currentRunMaxIterations = agent.MaxIterations
		return agent.RunOptions{}
	}
	m.vmaxArmed = false
	m.vmaxActive = true
	m.currentRunMaxIterations = agent.VMaxIterations
	return agent.RunOptions{
		MaxIterations:                     agent.VMaxIterations,
		AutoConfirmRunCommand:             true,
		PreflightShrink:                   true,
		ContextLimitTokens:                m.contextLimit,
		PreflightShrinkPercent:            45,
		PreflightShrinkCooldownIterations: 5,
	}
}

func (m *Model) vmaxAutoOffCommand() tea.Cmd {
	if !m.vmaxActive {
		return nil
	}
	m.vmaxActive = false
	m.vmaxArmed = false
	m.currentRunMaxIterations = agent.MaxIterations
	return m.printSystemDisplayOnly("VMAX auto off.")
}

func (m Model) activeRunMaxIterations() int {
	if m.currentRunMaxIterations > 0 {
		return m.currentRunMaxIterations
	}
	return agent.MaxIterations
}

// waitProgress は進捗チャネルから次のイベントを取り出す tea.Cmd
// イベントが届くまでブロックする
func (m *Model) waitProgress() tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-m.progressCh
		if !ok {
			return nil // チャネルがクローズされた
		}
		return progressMsg{event: ev}
	}
}
