package tui

import "github.com/charmbracelet/lipgloss"

var (
	styleUser = lipgloss.NewStyle().
			Foreground(lipgloss.Color("12")).
			Bold(true)

	styleAssistant = lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")).
			Bold(true)

	styleMessage = lipgloss.NewStyle().
			PaddingLeft(2)

	styleSpinner = lipgloss.NewStyle().
			Foreground(lipgloss.Color("13"))

	styleError = lipgloss.NewStyle().
			Foreground(lipgloss.Color("9")).
			Bold(true)

	styleThinking = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Italic(true)

	styleTimestamp = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245"))

	styleBorder = lipgloss.Color("62")

	styleViewport = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true)

	styleInput = lipgloss.NewStyle().
			Background(lipgloss.Color("235")).
			Padding(0, 1).
			MarginTop(1)

	styleInputMarker = lipgloss.NewStyle().
				Background(lipgloss.Color("235")).
				Foreground(lipgloss.Color("111")).
				Bold(true)

	styleInputHint = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			MarginTop(1)

	stylePendingAction = lipgloss.NewStyle().
				Foreground(lipgloss.Color("252")).
				Background(lipgloss.Color("236")).
				Padding(0, 1)

	stylePendingActionKey = lipgloss.NewStyle().
				Foreground(lipgloss.Color("111")).
				Bold(true)

	stylePendingActionMuted = lipgloss.NewStyle().
				Foreground(lipgloss.Color("244"))

	styleInputGhost = lipgloss.NewStyle().
			Background(lipgloss.Color("235")).
			Foreground(lipgloss.Color("240"))

	styleQuitConfirm = lipgloss.NewStyle().
				Foreground(lipgloss.Color("226")).
				Bold(true)

	styleToolCall = lipgloss.NewStyle().
			Foreground(lipgloss.Color("13")) // Magenta

	styleToolResult = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")) // Gray

	styleToolError = lipgloss.NewStyle().
			Foreground(lipgloss.Color("9")) // Red

	toolArgsStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Italic(true)

	warningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")).
			Bold(true) // Orange

	snapshotStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Italic(true)

	systemMessageStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245")).
				Italic(true)

	styleBanner = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(0, 1).
			MarginBottom(1)

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("46")).
			Bold(true)

	styleBtwMessage = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderLeft(true).
			BorderForeground(lipgloss.Color("99")). // Purple-ish
			PaddingLeft(1).
			MarginLeft(1).
			MarginBottom(1)

	styleBtwHeader = lipgloss.NewStyle().
			Foreground(lipgloss.Color("99")).
			Bold(true)

	styleMarkdownFallback = lipgloss.NewStyle().
				Foreground(lipgloss.Color("252"))
)
