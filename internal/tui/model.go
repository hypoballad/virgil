package tui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hypoballad/virgil/internal/agent"
	"github.com/hypoballad/virgil/internal/debugctx"
	"github.com/hypoballad/virgil/internal/llm"
	"github.com/hypoballad/virgil/internal/repository"
	"github.com/hypoballad/virgil/internal/shadow"
	"github.com/hypoballad/virgil/internal/symbols"
)

const (
	defaultInputOuterWidth = 80
	minInputWidth          = 12
	absoluteMinInputWidth  = 1
	minInputHeight         = 2
	maxInputHeight         = 8
	inputMarker            = "❯"
	inputMarkerGap         = "  "
)

var coreSlashCommands = []string{
	"/rewind",
	"/task",
	"/tasks",
	"/do",
	"/breakdown",
	"/btw",
	"/reindex",
	"/shrink",
	"/history",
	"/last",
	"/clear",
	"/continue",
	"/abort",
	"/debug-context",
	"/vmax",
	"/help",
}

var allSlashCommands = []string{
	"/rewind",
	"/confirm",
	"/continue",
	"/abort",
	"/confirm-run",
	"/reject-run",
	"/clear",
	"/debug-context",
	"/vmax",
	"/task",
	"/tasks",
	"/do",
	"/task-status",
	"/breakdown",
	"/reindex",
	"/callers",
	"/callgraph",
	"/shrink",
	"/history",
	"/last",
	"/btw",
	"/help",
}

type Model struct {
	input                   textarea.Model
	slashCompletion         string
	spinner                 spinner.Model
	width                   int
	height                  int
	messages                []llm.Message // Display messages (excluding system)
	history                 []llm.Message // Full history for LLM (including system)
	lastToolCalls           []agent.ToolCallRecord
	waiting                 bool
	waitingStartedAt        time.Time // 待機開始時刻
	agent                   *agent.Agent
	cancelAgent             context.CancelFunc // 実行中の処理をキャンセルする関数
	err                     error
	repo                    *repository.Repository
	shadow                  *shadow.ShadowRepo
	indexer                 *symbols.Indexer
	callRepo                *repository.CallRepository
	sessionID               string
	turnNumber              int
	currentTokens           int // 追加: 現在のコンテキストトークン数
	currentIteration        int // 追加: 現在のイテレーション数
	currentTurnID           int64
	partialAssistantContent string // 追加: ストリーミング中の文字列
	lastActivityMessage     string // 追加: 最新のアクティビティログ

	quitConfirm               bool
	lastIterationLimitReached bool
	awaitingContinuation      bool
	shrinkInfoShown           bool
	shrinking                 bool
	lastAutoShrinkHistoryLen  int
	mouseEnabled              bool   // マウス操作のオン/off（初期: true）
	workspaceRoot             string // 追加: ワークスペースのパス
	modelName                 string // 追加: モデル名
	appVersion                string
	toolProfile               string // 追加: ツールプロファイル
	contextLimit              int    // 追加: コンテキスト制限
	fullPowerCommands         bool
	planMode                  bool // 追加: プランモード状態
	debugContext              *debugctx.Context
	vmaxAvailable             bool
	vmaxArmed                 bool
	vmaxActive                bool
	currentRunMaxIterations   int
	doFlowActive              bool
	doFlowRemaining           int
	doFlowContinueOptions     agent.RunOptions

	agentTimeout time.Duration // 追加: エージェントのタイムアウト
	runTimeout   time.Duration // 追加: プラン実行のタイムアウト

	// /rewind 関連
	pendingRewind *pendingRewind // /rewind が予約された時の状態

	// run_command 確認待ち状態
	pendingRunCommand *pendingRunCommand

	// 入力履歴
	inputHistory []string // 入力履歴
	historyIndex int      // 履歴ナビゲーションの現在のインデックス (-1: 最新入力)

	progressCh chan agent.ProgressEvent // 進捗イベント受信用
}

type pendingRewind struct {
	targetHash    string
	targetMessage string // commit message
	requestedAt   time.Time
}

type pendingRunCommand struct {
	command     string
	dir         string
	requestedAt time.Time
}

type pendingAction struct {
	key   string
	label string
}

func (m Model) pendingActions() (string, []pendingAction) {
	switch {
	case m.pendingRunCommand != nil:
		return "Pending command", []pendingAction{
			{key: "1", label: "Approve"},
			{key: "2", label: "Reject"},
		}
	case m.awaitingContinuation:
		return "Paused at iteration limit", []pendingAction{
			{key: "1", label: "Continue"},
			{key: "2", label: "Stop"},
		}
	case m.pendingRewind != nil:
		return "Pending rewind", []pendingAction{
			{key: "1", label: "Confirm"},
			{key: "2", label: "Cancel"},
		}
	default:
		return "", nil
	}
}

func NewModel(agentInst *agent.Agent, repo *repository.Repository, shadowRepo *shadow.ShadowRepo, indexer *symbols.Indexer, sessionID string, workspaceRoot string, modelName string, appVersion string, contextLimit int, agentTimeoutMinutes int, runTimeoutMinutes int, callRepo ...*repository.CallRepository) Model {
	ti := textarea.New()
	ti.Placeholder = "Message..."
	ti.Prompt = ""
	ti.ShowLineNumbers = false
	ti.EndOfBufferCharacter = ' '
	ti.CharLimit = 20000
	ti.MaxHeight = 0

	focusedStyle, _ := textarea.DefaultStyles()
	focusedStyle.Base = lipgloss.NewStyle().
		Background(lipgloss.Color("235"))
	focusedStyle.Prompt = lipgloss.NewStyle().
		Background(lipgloss.Color("235")).
		Foreground(lipgloss.Color("111")).
		Bold(true)
	focusedStyle.Text = lipgloss.NewStyle().
		Background(lipgloss.Color("235")).
		Foreground(lipgloss.Color("255"))
	focusedStyle.Placeholder = lipgloss.NewStyle().
		Background(lipgloss.Color("235")).
		Foreground(lipgloss.Color("244"))
	focusedStyle.CursorLine = lipgloss.NewStyle().
		Background(lipgloss.Color("235"))
	focusedStyle.EndOfBuffer = lipgloss.NewStyle().
		Background(lipgloss.Color("235")).
		Foreground(lipgloss.Color("235"))
	ti.FocusedStyle = focusedStyle
	ti.BlurredStyle = focusedStyle
	ti.BlurredStyle.Base = focusedStyle.Base.Copy() // Ensure Base is copied
	_ = ti.Focus()
	ti.SetHeight(minInputHeight)

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = styleSpinner

	// 進捗通知用チャネル（バッファ512: ストリーミング中のイベント取りこぼしを防ぐ）
	progressCh := make(chan agent.ProgressEvent, 512)
	agentInst.SetProgressChannel(progressCh)
	agentInst.SetPlanMode(true)

	var calls *repository.CallRepository
	if len(callRepo) > 0 {
		calls = callRepo[0]
	} else if repo != nil {
		calls = repo.Calls
	}

	m := Model{
		input:         ti,
		spinner:       s,
		messages:      []llm.Message{},
		history:       []llm.Message{},
		agent:         agentInst,
		repo:          repo,
		shadow:        shadowRepo,
		indexer:       indexer,
		callRepo:      calls,
		sessionID:     sessionID,
		turnNumber:    0,
		mouseEnabled:  true,
		workspaceRoot: workspaceRoot,
		modelName:     modelName,
		appVersion:    appVersion,
		toolProfile:   agentInst.ToolProfile(),
		contextLimit:  contextLimit,
		historyIndex:  -1,
		progressCh:    progressCh,
		planMode:      true,
		agentTimeout:  time.Duration(agentTimeoutMinutes) * time.Minute,
		runTimeout:    time.Duration(runTimeoutMinutes) * time.Minute,
	}
	m.setInputSize(defaultInputOuterWidth)

	return m
}

func (m *Model) SetVMaxAvailable(enabled bool) {
	m.vmaxAvailable = enabled
}

func (m *Model) SetFullPowerCommands(enabled bool) {
	m.fullPowerCommands = enabled
	m.updateSlashCompletion()
}

func (m *Model) setInputSize(outerWidth int) {
	if outerWidth <= 0 {
		outerWidth = defaultInputOuterWidth
	}

	frameWidth := styleInput.GetHorizontalFrameSize()
	markerWidth := lipgloss.Width(inputMarker + inputMarkerGap)
	inputWidth := outerWidth - frameWidth - markerWidth
	if inputWidth < absoluteMinInputWidth {
		inputWidth = absoluteMinInputWidth
	} else if inputWidth < minInputWidth && outerWidth >= frameWidth+markerWidth+minInputWidth {
		inputWidth = minInputWidth
	}
	m.input.SetWidth(inputWidth)
	m.adjustInputHeight()
}

func (m *Model) adjustInputHeight() {
	lines := strings.Count(m.input.Value(), "\n") + 1
	if lines < minInputHeight {
		lines = minInputHeight
	} else if lines > maxInputHeight {
		lines = maxInputHeight
	}
	m.input.SetHeight(lines)
}

func (m *Model) updateSlashCompletion() {
	value := m.input.Value()
	m.slashCompletion = slashCompletionForCommands(value, m.slashCommands())
}

func (m *Model) acceptSlashCompletion() {
	if m.slashCompletion == "" {
		return
	}
	m.input.SetValue(m.input.Value() + m.slashCompletion)
	m.input.CursorEnd()
	m.slashCompletion = ""
	m.adjustInputHeight()
}

func slashCompletionFor(value string) string {
	return slashCompletionForCommands(value, coreSlashCommands)
}

func slashCompletionForCommands(value string, commands []string) string {
	if value == "" || value[0] != '/' || strings.ContainsAny(value, " \t\r\n") {
		return ""
	}
	for _, command := range commands {
		if strings.HasPrefix(command, value) && command != value {
			return strings.TrimPrefix(command, value)
		}
	}
	return ""
}

func (m Model) slashCommands() []string {
	if m.fullPowerCommands {
		return allSlashCommands
	}
	return coreSlashCommands
}
