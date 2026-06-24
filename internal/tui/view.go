package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/hypoballad/virgil/internal/agent"
	"github.com/hypoballad/virgil/internal/llm"
)

func (m Model) View() string {
	// コンテキスト情報の計算
	percent := 0
	if m.contextLimit > 0 {
		percent = (m.currentTokens * 100) / m.contextLimit
	}

	// 警告色の判定
	tokenColor := "245" // グレー
	if percent > 90 {
		tokenColor = "196" // 赤
	} else if percent > 70 {
		tokenColor = "214" // オレンジ
	}

	contextStr := fmt.Sprintf("🧠 Context: %d / %d (%d%%)",
		m.currentTokens, m.contextLimit, percent)

	// モード表示
	var modeStr string
	var modeColor string
	if m.planMode {
		modeStr = "📋 PLAN"
		modeColor = "214" // オレンジ（注意喚起）
	} else {
		modeStr = "✏️  EDIT"
		modeColor = "245" // グレー（通常）
	}

	// ステータス（スピナーとタイマー）の構築
	var statusView string
	if m.waiting {
		elapsed := int(time.Since(m.waitingStartedAt).Seconds())
		statusView = fmt.Sprintf(" %s Thinking %ds ", m.spinner.View(), elapsed)
	}

	// イテレーション表示
	iterStr := fmt.Sprintf("Iter %d", m.currentIteration)

	// インデックス鮮度表示
	var indexStr string
	if m.indexer != nil {
		status := m.indexer.Status()
		indexStr = formatIndexStatus(status.Active, status.LastIndexedAt, status.IndexedFiles, status.TotalFiles)
	}

	var debugStr string
	if m.debugContext != nil {
		debugStr = "🐞 " + m.debugContext.ActiveLabel()
	}

	var vmaxStr string
	if m.vmaxActive {
		vmaxStr = fmt.Sprintf("⚡ VMAX active %d auto-run", agent.VMaxIterations)
	} else if m.vmaxArmed {
		vmaxStr = "⚡ VMAX ready"
	}

	separator := renderSeparator(m.width, []separatorPart{
		{text: modeStr, color: modeColor, priority: 0},
		{text: contextStr, color: tokenColor, priority: 2},
		{text: iterStr, color: "245", priority: 1},
		{text: vmaxStr, color: "196", priority: 3},
		{text: debugStr, color: "111", priority: 4},
		{text: indexStr, color: "245", priority: 5},
		{text: statusView, color: "245", priority: 6},
	})

	// ストリーミングプレビューとアクティビティログ
	var preview string
	if m.waiting {
		llmLine := latestNonEmptyLine(m.partialAssistantContent)
		activityLine := m.lastActivityMessage
		if activityLine == "" {
			activityLine = "Waiting..."
		}
		preview = "\n" +
			styleThinking.Render(truncatePreviewLine("LLM:    ", llmLine, m.width)) + "\n" +
			styleThinking.Render(truncatePreviewLine("Agent:  ", activityLine, m.width)) + "\n"
	} else {
		preview = "\n\n\n" // 高さを一定に保つための空行
	}

	inputPanelWidth := m.width
	if inputPanelWidth < 1 {
		inputPanelWidth = 1
	}
	marker := styleInputMarker.Render(inputMarker + inputMarkerGap)
	inputContent := lipgloss.JoinHorizontal(lipgloss.Top, marker, m.inputView())
	// 端末幅いっぱいに背景色を広げるため、Place を使用して確実にパディングする
	// styleInput の Padding(0, 1) を考慮し、内部幅を調整
	innerContent := lipgloss.PlaceHorizontal(m.width-2, lipgloss.Left, inputContent)
	inputView := styleInput.Copy().
		Width(m.width).
		Height(m.input.Height()).
		Render(innerContent)

	hintWidth := m.width
	if hintWidth <= 0 {
		hintWidth = defaultInputOuterWidth
	}
	inputHint := styleInputHint.Copy().Width(hintWidth).Render("Enter newline · Alt+Enter send · Ctrl+D send")

	var footer string
	if m.quitConfirm {
		footer = "\n" + styleQuitConfirm.Render(" Press Ctrl+C again to quit, or Esc to cancel")
	} else if m.err != nil {
		footer = "\n" + styleError.Render(fmt.Sprintf(" Error: %v", m.err))
	}

	result := preview + separator + "\n" + inputView + "\n"
	if pendingActions := m.pendingActionView(); pendingActions != "" {
		result += pendingActions + "\n"
	}
	return result + inputHint + footer
}

func latestNonEmptyLine(content string) string {
	lines := strings.Split(content, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func truncatePreviewLine(prefix, content string, width int) string {
	availableWidth := width - len(prefix) - 10
	if availableWidth < 10 {
		availableWidth = 10
	}
	runes := []rune(content)
	if len(runes) > availableWidth {
		content = "..." + string(runes[len(runes)-availableWidth:])
	}
	return prefix + content
}

func (m Model) pendingActionView() string {
	title, actions := m.pendingActions()
	if len(actions) == 0 {
		return ""
	}

	parts := make([]string, 0, len(actions)+1)
	parts = append(parts, stylePendingActionMuted.Render(title))
	for _, action := range actions {
		parts = append(parts, fmt.Sprintf(
			"%s %s",
			stylePendingActionKey.Render("["+action.key+"]"),
			action.label,
		))
	}
	parts = append(parts, stylePendingActionMuted.Render("[Esc] Cancel"))

	content := strings.Join(parts, "  ")
	width := m.width
	if width <= 0 {
		width = defaultInputOuterWidth
	}
	return stylePendingAction.Copy().Width(width).Render(content)
}

func (m Model) inputView() string {
	if m.input.Value() == "" {
		return m.emptyInputView()
	}

	if m.slashCompletion == "" || strings.Contains(m.input.Value(), "\n") {
		return m.input.View()
	}

	width := m.input.Width()
	if width < absoluteMinInputWidth {
		width = absoluteMinInputWidth
	}
	value := m.input.Value()
	cursor := m.input.Cursor
	cursor.TextStyle = m.input.FocusedStyle.Text
	cursor.SetChar(" ")
	line := m.input.FocusedStyle.Text.Render(value) + cursor.View() + styleInputGhost.Render(m.slashCompletion)
	if padding := width - lipgloss.Width(value+" "+m.slashCompletion); padding > 0 {
		line += m.input.FocusedStyle.Text.Render(strings.Repeat(" ", padding))
	}

	height := m.input.Height()
	if height < minInputHeight {
		height = minInputHeight
	}
	lines := make([]string, 0, height)
	lines = append(lines, line)
	blank := m.input.FocusedStyle.Text.Render(strings.Repeat(" ", width))
	for len(lines) < height {
		lines = append(lines, blank)
	}
	return strings.Join(lines, "\n")
}

func (m Model) emptyInputView() string {
	width := m.input.Width()
	if width < absoluteMinInputWidth {
		width = absoluteMinInputWidth
	}

	placeholder := m.input.Placeholder
	line := ""
	if placeholder != "" {
		runes := []rune(placeholder)
		cursor := m.input.Cursor
		cursor.TextStyle = m.input.FocusedStyle.Placeholder
		cursor.SetChar(string(runes[0]))
		line = cursor.View()
		if len(runes) > 1 {
			line += m.input.FocusedStyle.Placeholder.Render(string(runes[1:]))
		}
	}
	if padding := width - lipgloss.Width(placeholder); padding > 0 {
		line += m.input.FocusedStyle.Placeholder.Render(strings.Repeat(" ", padding))
	}

	height := m.input.Height()
	if height < minInputHeight {
		height = minInputHeight
	}
	lines := make([]string, 0, height)
	lines = append(lines, line)
	blank := m.input.FocusedStyle.Text.Render(strings.Repeat(" ", width))
	for len(lines) < height {
		lines = append(lines, blank)
	}
	return strings.Join(lines, "\n")
}

type separatorPart struct {
	text     string
	color    string
	priority int
}

func renderSeparator(width int, parts []separatorPart) string {
	sepColor := lipgloss.Color("238")
	dashStyle := lipgloss.NewStyle().Foreground(sepColor)
	partStyle := func(color string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(color))
	}

	if width <= 0 {
		width = 80
	}

	active := make([]separatorPart, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part.text) != "" {
			active = append(active, part)
		}
	}
	for len(active) > 0 && separatorPlainWidth(active) > width {
		dropIndex := 0
		for i := range active {
			if active[i].priority > active[dropIndex].priority {
				dropIndex = i
			}
		}
		active = append(active[:dropIndex], active[dropIndex+1:]...)
	}
	if len(active) == 0 {
		return dashStyle.Render(strings.Repeat("─", width))
	}

	dash := dashStyle.Render("───")
	bracketL := dashStyle.Render("[ ")
	bracketR := dashStyle.Render(" ]")
	var sb strings.Builder
	sb.WriteString(dash)
	for _, part := range active {
		sb.WriteString(bracketL)
		sb.WriteString(partStyle(part.color).Render(part.text))
		sb.WriteString(bracketR)
		sb.WriteString(dash)
	}

	separator := sb.String()
	remainingWidth := width - lipgloss.Width(separator)
	if remainingWidth > 0 {
		separator += dashStyle.Render(strings.Repeat("─", remainingWidth))
	}
	return separator
}

func separatorPlainWidth(parts []separatorPart) int {
	width := 3
	for _, part := range parts {
		width += 7 + lipgloss.Width(part.text)
	}
	return width
}

func formatIndexStatus(active bool, lastIndexedAt time.Time, indexedFiles int, totalFiles int) string {
	if active {
		if totalFiles > 0 {
			return fmt.Sprintf("📚 Indexing: %d/%d", indexedFiles, totalFiles)
		}
		return "📚 Indexing..."
	}
	if lastIndexedAt.IsZero() {
		return "📚 Indexed: Not indexed"
	}
	return fmt.Sprintf("📚 Indexed: %s", formatIndexAge(time.Since(lastIndexedAt)))
}

func formatIndexAge(age time.Duration) string {
	if age < time.Minute {
		return "Just now"
	}
	if age < time.Hour {
		minutes := int(age.Minutes())
		if minutes == 1 {
			return "1 min ago"
		}
		return fmt.Sprintf("%d min ago", minutes)
	}
	if age < 24*time.Hour {
		hours := int(age.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	}
	days := int(age.Hours() / 24)
	if days == 1 {
		return "1 day ago"
	}
	return fmt.Sprintf("%d days ago", days)
}

func (m *Model) renderSingleMessage(msg llm.Message, toolCalls []agent.ToolCallRecord) string {
	var sb strings.Builder

	switch msg.Role {
	case "user":
		sb.WriteString(styleUser.Render("You:"))
		sb.WriteString("\n")
		sb.WriteString(m.renderMarkdown(strings.TrimSpace(msg.Content)))
		sb.WriteString("\n\n")

	case "system":
		sb.WriteString(systemMessageStyle.Render(msg.Content))
		sb.WriteString("\n\n")

	case "assistant":
		sb.WriteString(styleAssistant.Render("Virgil: "))
		sb.WriteString("\n")

		// If we have tool calls, show them
		if len(toolCalls) > 0 {
			for _, tc := range toolCalls {
				sb.WriteString("\n")
				sb.WriteString(styleToolCall.Render(fmt.Sprintf(
					"  🔧 %s(%s)",
					tc.ToolName,
					formatArgs(tc.Arguments),
				)))
				if tc.Error != nil {
					sb.WriteString(styleToolError.Render(
						fmt.Sprintf(" → Error: %v", tc.Error),
					))
				} else if tc.Result != nil {
					var status string
					if tc.Result.IsError {
						status = fmt.Sprintf(" → Error: %s", tc.Result.Content)
					} else {
						status = fmt.Sprintf(" → %d bytes", len(tc.Result.Content))
					}
					sb.WriteString(styleToolResult.Render(status))
				}

				// シャドウgit情報の表示（書き込み系のみ）
				if tc.PreCommit != "" && tc.PostCommit != "" {
					var snapshot string
					if tc.PreCommit == tc.PostCommit {
						snapshot = fmt.Sprintf("📸 %s (no changes)", tc.PreCommit[:7])
					} else {
						snapshot = fmt.Sprintf("📸 %s → %s", tc.PreCommit[:7], tc.PostCommit[:7])
					}
					sb.WriteString("\n     ")
					sb.WriteString(snapshotStyle.Render(snapshot))
				}
			}
			sb.WriteString("\n\n")
		}

		sb.WriteString(m.renderMarkdown(msg.Content))
		sb.WriteString("\n\n")
	}

	return sb.String()
}

func (m *Model) renderMarkdown(content string) string {
	if strings.TrimSpace(content) == "" {
		return content
	}

	width := m.width - 2
	if width < 20 {
		width = 20
	}

	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return styleMarkdownFallback.Render(content)
	}

	rendered, err := renderer.Render(content)
	if err != nil {
		return styleMarkdownFallback.Render(content)
	}
	return strings.TrimRight(rendered, "\n")
}

func (m *Model) renderBtwMessage(question string, content string, toolCalls []agent.ToolCallRecord) string {
	var sb strings.Builder

	sb.WriteString(styleBtwHeader.Render(" 💡 By The Way "))
	sb.WriteString("\n\n")

	sb.WriteString(styleUser.Render("Question: "))
	sb.WriteString(question)
	sb.WriteString("\n\n")

	sb.WriteString(styleAssistant.Render("Answer: "))
	sb.WriteString("\n")

	if len(toolCalls) > 0 {
		for _, tc := range toolCalls {
			sb.WriteString("\n")
			sb.WriteString(styleToolCall.Render(fmt.Sprintf(
				"  🔧 %s(%s)",
				tc.ToolName,
				formatArgs(tc.Arguments),
			)))
			if tc.Result != nil {
				var status string
				if tc.Result.IsError {
					status = fmt.Sprintf(" → Error: %s", tc.Result.Content)
				} else {
					status = fmt.Sprintf(" → %d bytes", len(tc.Result.Content))
				}
				sb.WriteString(styleToolResult.Render(status))
			}
		}
		sb.WriteString("\n\n")
	}

	sb.WriteString(m.renderMarkdown(content))

	// Widthを制限して枠線内での折り返しを強制する
	w := m.width
	if w <= 0 {
		w = 80 // Fallback
	}
	return styleBtwMessage.Copy().Width(w-2).Render(sb.String()) + "\n"
}

func truncateOneLine(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if limit <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func taskDescriptionDisplayLimit(width int) int {
	if width <= 0 {
		return 80
	}
	if width < 30 {
		return 30
	}
	return width - 8
}

func taskResultPreview(result string) string {
	result = strings.Join(strings.Fields(result), " ")
	if result == "" {
		return ""
	}
	runes := []rune(result)
	const limit = 500
	if len(runes) <= limit {
		return result
	}
	return string(runes[:limit-3]) + "..."
}

func formatArgs(args []byte) string {
	var parsed map[string]interface{}
	if err := json.Unmarshal(args, &parsed); err != nil {
		return string(args)
	}

	var parts []string
	for k, v := range parsed {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return strings.Join(parts, ", ")
}
