package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/hypoballad/virgil/internal/llm"
	"github.com/hypoballad/virgil/internal/repository"
	"github.com/hypoballad/virgil/internal/shadow"
	"github.com/hypoballad/virgil/internal/tokenizer"
	"github.com/hypoballad/virgil/internal/tools"
)

const MaxIterations = 20
const VMaxIterations = 100

const DefaultMetadataRequestTimeout = 120 * time.Second

const (
	ExchangeIterationEscalate = -1
	ExchangeIterationShrink   = -2
	ShadowOperationTimeout    = 30 * time.Second
)

const summaryInputMaxChars = 30000
const preflightShrinkRecentMessages = 8
const preflightShrinkMaxPinnedUserMessages = 8
const preflightShrinkMaxPinnedUserChars = 1200
const preflightShrinkMaxToolFailureLookback = 24

const (
	maxSemanticSafetyFailuresPerRun = 2
)

const historySummarizationPrompt = `You are compressing older conversation history for a coding agent.

Write a concise but useful summary that preserves:
- completed work, especially file paths and behavior changes
- important errors, blockers, and decisions
- failed or blocked tool calls: preserve the tool name, key arguments, exact error/block reason, and the recovery instruction or next safe alternative
- current progress and pending next steps
- facts the agent must remember to continue safely

Do not include generic commentary. Prefer compact bullets.`

// DefaultDiffMaxLines は LLM に返される差分（Diff）の最大行数
const DefaultDiffMaxLines = 500

func getMetadataTimeout() time.Duration {
	val := os.Getenv("VIRGIL_METADATA_TIMEOUT")
	if val == "" {
		return DefaultMetadataRequestTimeout
	}
	seconds, err := strconv.Atoi(val)
	if err != nil {
		log.Printf("agent: invalid VIRGIL_METADATA_TIMEOUT %q, using default", val)
		return DefaultMetadataRequestTimeout
	}
	return time.Duration(seconds) * time.Second
}

// MaxToolCallsPerIteration は 1 iteration あたりのツール呼び出し数の全体上限。
// 種別ごとの上限は、重い読み取り、通常の読み取り、書き込み/実行ツールで別々に管理する。
const MaxToolCallsPerIteration = 15

// MaxHeavyReadToolCallsPerIteration は 1 iteration あたりの重い読み取りツール呼び出し数の上限。
const MaxHeavyReadToolCallsPerIteration = 3

// MaxReadOnlyToolCallsPerIteration は 1 iteration あたりの読み取り専用ツール呼び出し数の上限。
const MaxReadOnlyToolCallsPerIteration = 10

// MaxMutatingToolCallsPerIteration は 1 iteration あたりの書き込み/実行ツール呼び出し数の上限。
const MaxMutatingToolCallsPerIteration = 2

var ErrMaxIterationsReached = errors.New("max iterations reached")

const (
	ToolProfileDefault = "default"
	ToolProfileSmall   = "small"
)

const (
	toolResultCompactionPreview = 240
	toolArgumentCompactionChars = 1200
	toolResultTotalBudgetTokens = 3000
	toolResultMinRawRecent      = 2
	toolResultHardMaxChars      = 20000
	toolResultHardMaxTokens     = 6000
)

type toolResultCompactionPolicy struct {
	KeepRecent int
	MinChars   int
	MinTokens  int
}

var smallToolAllowlist = map[string]bool{
	"find_symbol":             true,
	"get_file_outline":        true,
	"get_symbol_outline":      true,
	"read_symbol":             true,
	"get_json_outline":        true,
	"read_json_path":          true,
	"get_markdown_outline":    true,
	"read_markdown_section":   true,
	"read_file":               true,
	"search_text":             true,
	"list_files":              true,
	"edit_with_pattern":       true,
	"edit_file":               true,
	"write_file":              true,
	"run_tests":               true,
	"check_python_syntax":     true,
	"check_go_package":        true,
	"check_javascript_syntax": true,
	"check_typescript":        true,
}

// LLMClient はAgentが必要とするLLMクライアントのinterface
type LLMClient interface {
	Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error)
}

type shadowSnapshotter interface {
	CommitPre(ctx context.Context, toolName string) (string, error)
	CommitPost(ctx context.Context, toolName string) (string, error)
	Diff(ctx context.Context, from, to string, maxLines int) (string, error)
}

type Agent struct {
	llmClient            LLMClient
	tools                *tools.Registry
	systemPromptTemplate string
	workspaceRoot        string
	shadow               shadowSnapshotter
	watchdog             *Watchdog
	watchdogConfig       *WatchdogConfig        // 追加: nilならデフォルト使用
	repo                 *repository.Repository // 追加
	currentTurnID        int64                  // 追加: 記録先のturn ID
	progressCh           chan<- ProgressEvent   // 追加: 進捗通知用（write-only）
	planMode             bool                   // 追加: trueなら書き込み系ツールを除外
	tokenCalibration     float64
	toolProfile          string
	responseLanguage     string
}

type Response struct {
	FinalContent         string
	Messages             []llm.Message
	Iterations           int
	ToolCalls            []ToolCallRecord
	PromptTokens         int
	CompletionTokens     int
	MaxIterationsReached bool
	WatchdogStop         *StopSignal
	Structured           *StructuredResponse // 追加
}

type RunOptions struct {
	MaxIterations                     int
	AutoConfirmRunCommand             bool
	PreflightShrink                   bool
	ContextLimitTokens                int
	PreflightShrinkPercent            int
	PreflightShrinkCooldownIterations int
	ForceEditMode                     bool
}

type ToolCallRecord struct {
	Iteration  int
	ToolCallID string
	ToolName   string
	Arguments  json.RawMessage
	Result     *tools.Result
	Error      error
	DurationMs int64

	// シャドウgit関連
	PreCommit  string // ツール実行前のhash（書き込み系のみ）
	PostCommit string // ツール実行後のhash（書き込み系のみ）
}

func New(llmClient LLMClient, registry *tools.Registry) *Agent {
	return &Agent{
		llmClient:            llmClient,
		tools:                registry,
		systemPromptTemplate: SystemPromptDefault,
		tokenCalibration:     1.0,
		toolProfile:          ToolProfileDefault,
		responseLanguage:     ResponseLanguageAuto,
	}
}

// SetShadowRepo はシャドウgitリポジトリを設定
func (a *Agent) SetShadowRepo(repo *shadow.ShadowRepo) {
	a.shadow = repo
}

// SetSystemPrompt はシステムプロンプトをカスタマイズ
func (a *Agent) SetSystemPrompt(prompt string) {
	a.systemPromptTemplate = prompt
}

const (
	ResponseLanguageAuto = "auto"
	ResponseLanguageJA   = "ja"
	ResponseLanguageEN   = "en"
)

func normalizeResponseLanguage(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case ResponseLanguageJA, "japanese":
		return ResponseLanguageJA
	case ResponseLanguageEN, "english":
		return ResponseLanguageEN
	default:
		return ResponseLanguageAuto
	}
}

func (a *Agent) SetResponseLanguage(language string) {
	a.responseLanguage = normalizeResponseLanguage(language)
}

func (a *Agent) ResponseLanguage() string {
	return normalizeResponseLanguage(a.responseLanguage)
}

// SetWatchdogConfig はウォッチドッグ設定をカスタマイズ
func (a *Agent) SetWatchdogConfig(config WatchdogConfig) {
	a.watchdogConfig = &config
}

// SetRepository は記録用のリポジトリを設定する
func (a *Agent) SetRepository(repo *repository.Repository) {
	a.repo = repo
}

// SetCurrentTurnID は記録先のturn IDを設定する
// TUI側からRun呼び出し前に設定する
func (a *Agent) SetCurrentTurnID(turnID int64) {
	a.currentTurnID = turnID
}

// SetProgressChannel は進捗通知用のチャネルを設定する
// 設定しない場合、進捗イベントは送信されない
func (a *Agent) SetProgressChannel(ch chan<- ProgressEvent) {
	a.progressCh = ch
}

// SetWorkspaceRoot はワークスペースルートを設定する
func (a *Agent) SetWorkspaceRoot(root string) {
	a.workspaceRoot = root
}

// SetPlanMode はプランモードのON/OFFを切り替える
func (a *Agent) SetPlanMode(enabled bool) {
	a.planMode = enabled
}

func (a *Agent) SetToolProfile(profile string) {
	a.toolProfile = normalizeToolProfile(profile)
}

func (a *Agent) ToolProfile() string {
	return normalizeToolProfile(a.toolProfile)
}

func (a *Agent) SummarizeHistory(ctx context.Context, messages []llm.Message) (string, error) {
	if len(messages) == 0 {
		return "", nil
	}

	input := formatMessagesForSummary(messages, summaryInputMaxChars)
	summaryMessages := []llm.Message{
		{Role: "system", Content: historySummarizationPrompt},
		{Role: "user", Content: input},
	}
	resp, err := a.llmClient.Chat(ctx, llm.ChatRequest{
		Messages: summaryMessages,
		Stream:   false,
	})
	if err != nil {
		return "", fmt.Errorf("summarize history: %w", err)
	}
	a.recordExchange(ExchangeIterationShrink, summaryMessages, nil, nil, resp)

	summary := strings.TrimSpace(resp.Message.Content)
	if summary == "" {
		summary = fallbackHistorySummary(messages)
	}
	return summary, nil
}

func formatMessagesForSummary(messages []llm.Message, maxChars int) string {
	var sb strings.Builder
	sb.WriteString("Summarize these older conversation messages:\n\n")
	for i, msg := range messages {
		sb.WriteString(fmt.Sprintf("## Message %d (%s)\n", i+1, msg.Role))
		if msg.Content != "" {
			sb.WriteString(msg.Content)
			sb.WriteString("\n")
		}
		if len(msg.ToolCalls) > 0 {
			sb.WriteString(fmt.Sprintf("[tool calls: %d]\n", len(msg.ToolCalls)))
			for _, tc := range msg.ToolCalls {
				sb.WriteString(fmt.Sprintf("- %s\n", tc.Function.Name))
				if args := compactSummaryToolArguments(tc.Function.Arguments); args != "" {
					sb.WriteString(fmt.Sprintf("  args: %s\n", args))
				}
			}
		}
		sb.WriteString("\n")
	}

	content := sb.String()
	if maxChars <= 0 {
		return content
	}
	runes := []rune(content)
	if len(runes) <= maxChars {
		return content
	}
	return "[Earlier content omitted because the history was too large. Summarize the retained tail accurately.]\n" + string(runes[len(runes)-maxChars:])
}

func compactSummaryToolArguments(args interface{}) string {
	if args == nil {
		return ""
	}
	data, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(string(data))
	if text == "" || text == "null" || text == "{}" {
		return ""
	}
	const maxToolArgumentSummaryChars = 1000
	runes := []rune(text)
	if len(runes) <= maxToolArgumentSummaryChars {
		return text
	}
	return string(runes[:maxToolArgumentSummaryChars]) + "... [truncated]"
}

type preflightShrinkResult struct {
	messages           []llm.Message
	beforeTokens       int
	thresholdTokens    int
	summarizedMessages int
	keptRecentMessages int
	pinnedUserMessages int
}

func (a *Agent) shouldPreflightShrink(opts RunOptions, tokenEstimate int, iteration int, lastShrinkIteration int) bool {
	if !opts.PreflightShrink {
		return false
	}
	if opts.ContextLimitTokens <= 0 || tokenEstimate <= 0 {
		return false
	}
	percent := opts.PreflightShrinkPercent
	if percent <= 0 {
		return false
	}
	threshold := opts.ContextLimitTokens * percent / 100
	if threshold <= 0 || tokenEstimate < threshold {
		return false
	}
	cooldown := opts.PreflightShrinkCooldownIterations
	if cooldown > 0 && iteration-lastShrinkIteration < cooldown {
		return false
	}
	return true
}

func (a *Agent) preflightShrink(ctx context.Context, messages []llm.Message, opts RunOptions, beforeTokens int) (*preflightShrinkResult, error) {
	base, older, recent := splitMessagesForPreflightShrink(messages, preflightShrinkRecentMessages)
	older, pinnedUsers := pinUserMessagesForPreflightShrink(older)
	if len(older) == 0 {
		if len(pinnedUsers) == 0 {
			return nil, errors.New("no older messages available to summarize")
		}
		compressed := make([]llm.Message, 0, len(base)+len(pinnedUsers)+len(recent))
		compressed = append(compressed, base...)
		compressed = append(compressed, pinnedUsers...)
		compressed = append(compressed, recent...)
		return &preflightShrinkResult{
			messages:           compressed,
			beforeTokens:       beforeTokens,
			thresholdTokens:    opts.ContextLimitTokens * opts.PreflightShrinkPercent / 100,
			summarizedMessages: 0,
			keptRecentMessages: len(recent),
			pinnedUserMessages: len(pinnedUsers),
		}, nil
	}

	threshold := opts.ContextLimitTokens * opts.PreflightShrinkPercent / 100
	log.Printf(
		"agent: preflight shrink start: before=%d threshold=%d summarized=%d pinned_users=%d kept_recent=%d",
		beforeTokens,
		threshold,
		len(older),
		len(pinnedUsers),
		len(recent),
	)
	a.emitProgress(ProgressEvent{
		Type:            EventAgentActivity,
		ActivityMessage: "Auto-shrinking old context before next VMAX iteration...",
	})

	summary, err := a.SummarizeHistory(ctx, older)
	if err != nil {
		return nil, err
	}

	compressed := make([]llm.Message, 0, len(base)+1+len(pinnedUsers)+len(recent))
	compressed = append(compressed, base...)
	compressed = append(compressed, llm.Message{
		Role: "system",
		Content: fmt.Sprintf(
			"Previous conversation summary, compressed by Virgil preflight shrink at %s:\n\n%s",
			time.Now().Format(time.RFC3339),
			summary,
		),
	})
	compressed = append(compressed, pinnedUsers...)
	compressed = append(compressed, recent...)

	return &preflightShrinkResult{
		messages:           compressed,
		beforeTokens:       beforeTokens,
		thresholdTokens:    threshold,
		summarizedMessages: len(older),
		keptRecentMessages: len(recent),
		pinnedUserMessages: len(pinnedUsers),
	}, nil
}

func splitMessagesForPreflightShrink(messages []llm.Message, recentCount int) (base []llm.Message, older []llm.Message, recent []llm.Message) {
	if len(messages) == 0 {
		return nil, nil, nil
	}
	if recentCount <= 0 {
		recentCount = preflightShrinkRecentMessages
	}

	bodyStart := 0
	for bodyStart < len(messages) && messages[bodyStart].Role == "system" {
		base = append(base, messages[bodyStart])
		bodyStart++
	}

	bodyLen := len(messages) - bodyStart
	if bodyLen <= recentCount {
		return base, nil, messages[bodyStart:]
	}

	split := len(messages) - recentCount
	split = protectToolContextSplit(messages, bodyStart, split)
	if split <= bodyStart {
		return base, nil, messages[bodyStart:]
	}

	older = messages[bodyStart:split]
	recent = messages[split:]
	return base, older, recent
}

func protectToolContextSplit(messages []llm.Message, bodyStart int, split int) int {
	for split > bodyStart && split < len(messages) && messages[split].Role == "tool" {
		split--
	}
	return protectRecentToolFailure(messages, bodyStart, split)
}

func protectRecentToolFailure(messages []llm.Message, bodyStart int, split int) int {
	if split <= bodyStart {
		return split
	}
	lowerBound := split - preflightShrinkMaxToolFailureLookback
	if lowerBound < bodyStart {
		lowerBound = bodyStart
	}
	for i := split - 1; i >= lowerBound; i-- {
		msg := messages[i]
		if msg.Role != "tool" || i >= split || !looksLikeToolFailure(msg.Content) {
			continue
		}
		if assistantIdx := matchingAssistantToolCallIndex(messages, bodyStart, i, msg.ToolCallID); assistantIdx >= bodyStart {
			return min(split, assistantIdx)
		}
		return min(split, i)
	}
	return split
}

func matchingAssistantToolCallIndex(messages []llm.Message, start int, toolIndex int, toolCallID string) int {
	for i := toolIndex - 1; i >= start; i-- {
		msg := messages[i]
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		if toolCallID == "" {
			return i
		}
		for _, tc := range msg.ToolCalls {
			if tc.ID == toolCallID {
				return i
			}
		}
	}
	return -1
}

func looksLikeToolFailure(content string) bool {
	lower := strings.ToLower(content)
	for _, marker := range []string{
		"returned error",
		"tool \"",
		" blocked:",
		"refusing ",
		"failed",
		"error:",
		"path is required",
		"must be unique",
		"permission denied",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func pinUserMessagesForPreflightShrink(messages []llm.Message) (summarizable []llm.Message, pinned []llm.Message) {
	if len(messages) == 0 {
		return nil, nil
	}
	summarizable = make([]llm.Message, 0, len(messages))
	pinnedSet := make(map[int]bool, preflightShrinkMaxPinnedUserMessages)
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if shouldPinUserMessageForPreflightShrink(msg, len(pinnedSet), preflightShrinkMaxPinnedUserMessages, preflightShrinkMaxPinnedUserChars) {
			pinnedSet[i] = true
		}
	}
	for i, msg := range messages {
		if pinnedSet[i] {
			pinned = append(pinned, msg)
			continue
		}
		summarizable = append(summarizable, msg)
	}
	return summarizable, pinned
}

func shouldPinUserMessageForPreflightShrink(msg llm.Message, pinnedCount int, maxPinned int, maxChars int) bool {
	if msg.Role != "user" || pinnedCount >= maxPinned {
		return false
	}
	content := strings.TrimSpace(msg.Content)
	if content == "" || isDebugContextLikeMessage(content) {
		return false
	}
	if maxChars > 0 && len([]rune(content)) > maxChars {
		return false
	}
	return looksLikeDurableUserInstruction(content)
}

func isDebugContextLikeMessage(content string) bool {
	trimmed := strings.TrimSpace(content)
	return strings.Contains(trimmed, "<debug_context>") ||
		strings.Contains(trimmed, "source: vscode-debugpy") ||
		strings.Contains(trimmed, "current_frame:")
}

func looksLikeDurableUserInstruction(content string) bool {
	lower := strings.ToLower(content)
	markers := []string{
		"必ず",
		"絶対",
		"覚えて",
		"忘れ",
		"制約",
		"ルール",
		"以後",
		"今後",
		"してください",
		"しないで",
		"must",
		"always",
		"never",
		"remember",
		"constraint",
		"rule",
		"do not",
		"don't",
		"from now on",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// IsPlanMode は現在プランモードかを返す
func (a *Agent) IsPlanMode() bool {
	return a.planMode
}

// NotifyRunCommandConfirmation はTUI側からユーザーの確認結果を受け取る
func (a *Agent) NotifyRunCommandConfirmation(approved bool) {
	a.NotifyRunCommandConfirmationWithFeedback(approved, "")
}

// NotifyRunCommandConfirmationWithFeedback はrun_command確認結果と任意の拒否理由を受け取る
func (a *Agent) NotifyRunCommandConfirmationWithFeedback(approved bool, feedback string) {
	// run_command ツールの確認結果をセット
	tool, exists := a.tools.Get("run_command")
	if !exists {
		return
	}
	runTool, ok := tool.(*tools.RunCommandTool)
	if !ok {
		return
	}
	runTool.SetConfirmationResultWithFeedback(approved, feedback)
}

// buildSystemPrompt は現在のモード・ワークスペースに応じてシステムプロンプトを構築する
func (a *Agent) buildSystemPrompt() string {
	prompt := a.systemPromptTemplate

	// %WORKSPACE_ROOT% の置換
	if a.workspaceRoot != "" {
		prompt = strings.Replace(prompt, "%WORKSPACE_ROOT%", a.workspaceRoot, 1)
	}

	// %MODE% の置換
	var modeText string
	if a.planMode {
		modeText = SystemPromptModePlan
	} else {
		modeText = SystemPromptModeEdit
	}
	prompt = strings.Replace(prompt, "%MODE%", modeText, 1)

	if a.ToolProfile() == ToolProfileSmall {
		prompt += "\n\n# Tool Profile\n\nTool profile: small. Some advanced tools are hidden to save context. Use the available core tools first; if a hidden advanced tool is needed, explain that limitation to the user.\n"
	}

	prompt += responseLanguageInstruction(a.ResponseLanguage())

	return prompt
}

func responseLanguageInstruction(language string) string {
	switch normalizeResponseLanguage(language) {
	case ResponseLanguageJA:
		return "\n\n# User Response Language\n\nRespond to the user in Japanese. Keep internal tool-use decisions and tool-facing behavior governed by the English system instructions above.\n"
	case ResponseLanguageEN:
		return "\n\n# User Response Language\n\nRespond to the user in English. Keep internal tool-use decisions and tool-facing behavior governed by the English system instructions above.\n"
	default:
		return "\n\n# User Response Language\n\nMatch the user's language for visible responses: answer in Japanese when the user writes in Japanese, otherwise answer in English. Keep internal tool-use decisions and tool-facing behavior governed by the English system instructions above.\n"
	}
}

func (a *Agent) localizedResponse(userInput, japanese, english string) string {
	switch a.ResponseLanguage() {
	case ResponseLanguageJA:
		return japanese
	case ResponseLanguageEN:
		return english
	default:
		if containsJapaneseText(userInput) {
			return japanese
		}
		return english
	}
}

func containsJapaneseText(text string) bool {
	for _, r := range text {
		if unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han) {
			return true
		}
	}
	return false
}

func (a *Agent) RunBtw(ctx context.Context, history []llm.Message, question string) (*Response, error) {
	// 一時的にプランモード（読み取り専用）を強制
	oldPlanMode := a.planMode
	a.planMode = true
	defer func() { a.planMode = oldPlanMode }()

	// ウォッチドッグ
	config := DefaultWatchdogConfig()
	if a.watchdogConfig != nil {
		config = *a.watchdogConfig
	}
	a.watchdog = NewWatchdog(config)

	messages := []llm.Message{}

	// システムプロンプトを先頭に追加
	systemPrompt := a.buildSystemPrompt()
	// /btw 用の制約を追加
	systemPrompt += "\n\nCRITICAL: You are in /btw mode. This is an isolated question. Answer concisely and briefly based on the current context. Do NOT use any file modification tools."

	messages = append(messages, llm.Message{
		Role:    "system",
		Content: systemPrompt,
	})

	messages = append(messages, history...)
	messages = append(messages, llm.Message{
		Role:    "user",
		Content: "By the way: " + question,
	})

	response := &Response{
		Messages:  messages,
		ToolCalls: []ToolCallRecord{},
	}

	for iteration := 0; iteration < MaxIterations; iteration++ {
		toolDefs := a.toolDefinitions()
		requestMessages := prepareMessagesForLLMRequest(messages)
		heuristicEstimate := a.estimateTokenCountWithToolsRaw(requestMessages)
		localEstimate := a.applyTokenCalibration(heuristicEstimate)
		a.emitProgress(ProgressEvent{
			Type:         EventTokenUpdate,
			PromptTokens: localEstimate,
			Iteration:    iteration + 1,
		})

		chatResp, err := a.llmClient.Chat(ctx, llm.ChatRequest{
			Messages: requestMessages,
			Tools:    toolDefs,
			Stream:   true,
			StreamFunc: func(partial string) {
				a.emitProgress(ProgressEvent{
					Type:           EventPartialResponse,
					PartialContent: partial,
				})
			},
		})
		if err != nil {
			return response, err
		}

		promptTokens := chatResp.PromptTokens
		if promptTokens == 0 {
			promptTokens = localEstimate
		}
		a.updateTokenCalibration(heuristicEstimate, chatResp.PromptTokens)

		response.Iterations = iteration + 1
		response.PromptTokens += promptTokens
		response.CompletionTokens += chatResp.CompletionTokens

		chatResp.Message = normalizeInlineToolCallMarkup(chatResp.Message)

		a.emitProgress(ProgressEvent{
			Type:             EventTokenUpdate,
			PromptTokens:     promptTokens,
			CompletionTokens: chatResp.CompletionTokens,
			Iteration:        iteration + 1,
		})

		if chatResp.Message.Content == "" && len(chatResp.Message.ToolCalls) == 0 {
			a.logEmptyResponse(chatResp)
			if signal := a.watchdog.RecordEmptyResponse(); signal != nil {
				return a.escalate(ctx, messages, response, signal)
			}
			messages = append(messages, llm.Message{
				Role:    "user",
				Content: emptyResponseRecoveryPrompt(),
			})
			continue
		}
		a.watchdog.ResetEmptyCount()

		if len(chatResp.Message.ToolCalls) == 0 {
			content := chatResp.Message.Content
			response.FinalContent = content
			response.Structured = &StructuredResponse{
				Summary:         summarizeForMetadata(content, 100),
				Confidence:      ConfidenceHigh,
				RequestedAction: inferActionFromText(content),
			}
			return response, nil
		}

		toolCalls := a.limitToolCallsPerIteration(chatResp.Message.ToolCalls)
		chatResp.Message.ToolCalls = toolCalls
		messages = append(messages, scrubLargeToolCallArgumentsForHistory(chatResp.Message))

		for _, tc := range toolCalls {
			argsJSON, _ := tc.Function.ArgumentsJSON()
			tool, exists := a.tools.Get(tc.Function.Name)

			// /btw モードでは書き込みツールを常にブロック
			if exists && tool.IsMutating() {
				blockMsg := "Tool is disabled in /btw mode."
				messages = append(messages, llm.Message{
					Role:       "tool",
					Content:    blockMsg,
					ToolCallID: tc.ID,
				})
				continue
			}

			result, execErr := a.tools.Execute(ctx, tc.Function.Name, argsJSON)
			var resultContent string
			if execErr != nil {
				resultContent = fmt.Sprintf("Error: %v", execErr)
			} else {
				resultContent = result.Content
			}

			messages = append(messages, llm.Message{
				Role:       "tool",
				Content:    resultContent,
				ToolCallID: tc.ID,
			})

			response.ToolCalls = append(response.ToolCalls, ToolCallRecord{
				Iteration:  iteration,
				ToolCallID: tc.ID,
				ToolName:   tc.Function.Name,
				Arguments:  argsJSON,
				Result:     result,
				Error:      execErr,
			})
		}
		response.Messages = messages
	}

	return response, nil
}

func (a *Agent) Run(ctx context.Context, history []llm.Message, userInput string) (*Response, error) {
	return a.run(ctx, history, userInput, MaxIterations)
}

func (a *Agent) RunWithOptions(ctx context.Context, history []llm.Message, userInput string, opts RunOptions) (*Response, error) {
	return a.runWithSystemPromptAndOptions(ctx, history, userInput, normalizeMaxIterations(opts.MaxIterations), "", opts)
}

func (a *Agent) run(ctx context.Context, history []llm.Message, userInput string, maxIterations int) (*Response, error) {
	return a.runWithSystemPromptAndOptions(ctx, history, userInput, maxIterations, "", RunOptions{})
}

func (a *Agent) runWithSystemPrompt(ctx context.Context, history []llm.Message, userInput string, maxIterations int, systemPromptOverride string) (*Response, error) {
	return a.runWithSystemPromptAndOptions(ctx, history, userInput, maxIterations, systemPromptOverride, RunOptions{})
}

func (a *Agent) runWithSystemPromptAndOptions(ctx context.Context, history []llm.Message, userInput string, maxIterations int, systemPromptOverride string, opts RunOptions) (*Response, error) {
	maxIterations = normalizeMaxIterations(maxIterations)
	restoreRunCommandAutoConfirm := a.setRunCommandAutoConfirm(opts.AutoConfirmRunCommand)
	defer restoreRunCommandAutoConfirm()
	if opts.ForceEditMode {
		oldPlanMode := a.planMode
		a.planMode = false
		defer func() { a.planMode = oldPlanMode }()
	}

	// ウォッチドッグ初期化（カスタム設定があればそちらを使用）
	config := DefaultWatchdogConfig()
	if a.watchdogConfig != nil {
		config = *a.watchdogConfig
	}
	a.watchdog = NewWatchdog(config)

	messages := []llm.Message{}
	historyToAppend := history

	// システムプロンプトを先頭に追加（重複防止）
	hasSystem := len(history) > 0 && history[0].Role == "system"
	if systemPromptOverride != "" {
		messages = append(messages, llm.Message{
			Role:    "system",
			Content: systemPromptOverride,
		})
		if hasSystem {
			historyToAppend = history[1:]
		}
	} else {
		systemPrompt := systemPromptOverride
		systemPrompt = a.buildSystemPrompt()
		if systemPrompt != "" {
			messages = append(messages, llm.Message{
				Role:    "system",
				Content: systemPrompt,
			})
		}
		if hasSystem {
			historyToAppend = history[1:]
		}
	}

	messages = append(messages, historyToAppend...)
	if userInput != "" {
		messages = append(messages, llm.Message{
			Role:    "user",
			Content: userInput,
		})
	}

	response := &Response{
		Messages:  messages,
		ToolCalls: []ToolCallRecord{},
	}
	verificationSucceeded := false
	lastPreflightShrinkIteration := -1000000
	semanticSafetyFailures := map[string]int{}
	successfulExplorationCalls := map[string]int{}
	failedExplorationCalls := map[string]int{}
	refusedMarkdownFullReads := map[string]bool{}
	structuralRecovery := structuralReadRecovery{}
	markdownRecovery := markdownReadRecovery{}
	unavailableTools := map[string]bool{}

	for iteration := 0; iteration < maxIterations; iteration++ {
		log.Printf("agent: iteration %d/%d, %d messages", iteration+1, maxIterations, len(messages))

		// イテレーション開始通知
		a.emitProgress(ProgressEvent{
			Type:      EventAgentActivity,
			Iteration: iteration + 1,
		})

		// ツール定義を取得
		toolDefs := a.toolDefinitions()
		toolDefs = filterUnavailableToolDefinitions(toolDefs, unavailableTools)
		if structuralRecovery.Required {
			toolDefs = structuralRecoveryToolDefinitions(toolDefs)
		}
		if markdownRecovery.Required {
			toolDefs = markdownRecoveryToolDefinitions(toolDefs)
		}
		log.Printf("agent: passing %d tools to LLM", len(toolDefs))
		requestMessages := prepareMessagesForLLMRequest(messages)
		if structuralRecovery.Required {
			requestMessages = append(cloneMessageSlice(requestMessages), llm.Message{
				Role:    "user",
				Content: structuralRecoveryToolChoicePrompt(structuralRecovery.Reason),
			})
		}
		if markdownRecovery.Required {
			requestMessages = append(cloneMessageSlice(requestMessages), llm.Message{
				Role:    "user",
				Content: markdownRecoveryToolChoicePrompt(markdownRecovery.Path),
			})
		}

		// 推定トークン数を計算してTUIを先行更新（ユーザーへのフィードバック用）
		heuristicEstimate := a.estimateTokenCountWithToolsRaw(requestMessages)
		localEstimate := a.applyTokenCalibration(heuristicEstimate)
		if a.shouldPreflightShrink(opts, localEstimate, iteration, lastPreflightShrinkIteration) {
			shrinkResult, err := a.preflightShrink(ctx, messages, opts, localEstimate)
			if err != nil {
				log.Printf("agent: preflight shrink skipped: %v", err)
			} else {
				messages = shrinkResult.messages
				response.Messages = messages
				lastPreflightShrinkIteration = iteration
				requestMessages = prepareMessagesForLLMRequest(messages)
				toolDefs = filterUnavailableToolDefinitions(toolDefs, unavailableTools)
				if structuralRecovery.Required {
					requestMessages = append(cloneMessageSlice(requestMessages), llm.Message{
						Role:    "user",
						Content: structuralRecoveryToolChoicePrompt(structuralRecovery.Reason),
					})
				}
				if markdownRecovery.Required {
					requestMessages = append(cloneMessageSlice(requestMessages), llm.Message{
						Role:    "user",
						Content: markdownRecoveryToolChoicePrompt(markdownRecovery.Path),
					})
				}
				heuristicEstimate = a.estimateTokenCountWithToolsRaw(requestMessages)
				localEstimate = a.applyTokenCalibration(heuristicEstimate)
				log.Printf(
					"agent: preflight shrink complete: before=%d after=%d threshold=%d summarized=%d pinned_users=%d kept_recent=%d",
					shrinkResult.beforeTokens,
					localEstimate,
					shrinkResult.thresholdTokens,
					shrinkResult.summarizedMessages,
					shrinkResult.pinnedUserMessages,
					shrinkResult.keptRecentMessages,
				)
			}
		}
		a.emitProgress(ProgressEvent{
			Type:         EventTokenUpdate,
			PromptTokens: localEstimate,
			Iteration:    iteration + 1,
		})

		chatResp, err := a.llmClient.Chat(ctx, llm.ChatRequest{
			Messages: requestMessages,
			Tools:    toolDefs,
			Stream:   true,
			StreamFunc: func(partial string) {
				a.emitProgress(ProgressEvent{
					Type:           EventPartialResponse,
					PartialContent: partial,
				})
			},
		})
		if err != nil {
			return response, fmt.Errorf("llm chat failed: %w", err)
		}

		promptTokens := chatResp.PromptTokens
		if promptTokens == 0 {
			promptTokens = localEstimate
		}
		a.updateTokenCalibration(heuristicEstimate, chatResp.PromptTokens)

		// 推定精度をログ出力（Q3対応）
		errorRate := 0.0
		if chatResp.PromptTokens > 0 {
			errorRate = float64(chatResp.PromptTokens-localEstimate) / float64(chatResp.PromptTokens) * 100
		}
		log.Printf("agent: token estimate: %d, actual: %d, error: %.1f%%", localEstimate, chatResp.PromptTokens, errorRate)

		// ウォッチドッグ: コンテキストサイズチェック（APIの実測値ベースで判定 - 案B対応）
		if signal := a.watchdog.CheckContextSize(promptTokens); signal != nil {
			log.Printf("agent: watchdog stop: %s - %s", signal.Reason, signal.Detail)
			return a.escalate(ctx, messages, response, signal)
		}

		// LLM交換記録を保存
		a.recordExchange(iteration, requestMessages, toolDefs, nil, chatResp)

		response.Iterations = iteration + 1
		response.PromptTokens += promptTokens
		response.CompletionTokens += chatResp.CompletionTokens

		chatResp.Message = normalizeInlineToolCallMarkup(chatResp.Message)

		// 進捗通知: 正確なトークン数で上書き（PromptTokens は実測値）
		a.emitProgress(ProgressEvent{
			Type:             EventTokenUpdate,
			PromptTokens:     promptTokens,
			CompletionTokens: chatResp.CompletionTokens,
			Iteration:        iteration + 1,
		})

		// ウォッチドッグ: 空レスポンスチェック
		if chatResp.Message.Content == "" && len(chatResp.Message.ToolCalls) == 0 {
			a.logEmptyResponse(chatResp)
			if signal := a.watchdog.RecordEmptyResponse(); signal != nil {
				log.Printf("agent: watchdog stop: %s - %s", signal.Reason, signal.Detail)
				return a.escalate(ctx, messages, response, signal)
			}
			messages = append(messages, llm.Message{
				Role:    "user",
				Content: emptyResponseRecoveryPrompt(),
			})
			response.Messages = messages
			continue
		} else {
			a.watchdog.ResetEmptyCount()
		}

		// tool_callsがなければ最終応答
		if len(chatResp.Message.ToolCalls) == 0 {
			content := chatResp.Message.Content
			if markdownRecovery.Required {
				log.Printf("agent: final response blocked until focused markdown read after full-read refusal")
				messages = append(messages, chatResp.Message)
				messages = append(messages, llm.Message{
					Role:    "user",
					Content: markdownRecoveryFinalPrompt(markdownRecovery.Path),
				})
				response.Messages = messages
				continue
			}
			if structuralRecovery.Required {
				log.Printf("agent: final response blocked until structural read after safety guard")
				messages = append(messages, chatResp.Message)
				messages = append(messages, llm.Message{
					Role:    "user",
					Content: structuralRecoveryFinalPrompt(structuralRecovery.Reason),
				})
				response.Messages = messages
				continue
			}
			if systemPromptOverride != "" && isIncompleteTaskTemplateResponse(content) {
				log.Printf("agent: task template response stopped after TODO list; prompting model to continue")
				messages = append(messages, chatResp.Message)
				messages = append(messages, llm.Message{
					Role:    "user",
					Content: "You stopped after outputting only a TODO list. Do not provide the final answer yet. Start the first incomplete TODO now with the required tool call. After the requested verification passes, stop extra exploration and finish with ## Result.",
				})
				response.Messages = messages
				continue
			}
			if systemPromptOverride != "" && taskRequiresSavedArtifact(userInput) && !hasSuccessfulFileMutation(response.ToolCalls) {
				log.Printf("agent: task final response blocked because requested saved artifact is missing")
				messages = append(messages, chatResp.Message)
				messages = append(messages, llm.Message{
					Role:    "user",
					Content: "The user asked you to save a plan, document, or file at a specific path, but no file creation or edit tool has succeeded yet. Do not provide the final answer. Save the requested artifact with write_file/edit_file/edit_with_pattern. If the user said not to write concrete code, save only the Markdown plan or document. After saving it, finish with ## Result.",
				})
				response.Messages = messages
				continue
			}
			if shouldBlockIntentOnlyFinal(userInput, content, response.ToolCalls) {
				log.Printf("agent: intent-only final response blocked; prompting model to continue with tools")
				messages = append(messages, chatResp.Message)
				messages = append(messages, llm.Message{
					Role:    "user",
					Content: intentOnlyFinalRecoveryPrompt(),
				})
				response.Messages = messages
				continue
			}

			// 自由文応答をそのまま使う（2パス目は廃止）
			// メタデータはヒューリスティック判定で生成
			response.FinalContent = content
			response.Structured = &StructuredResponse{
				Summary:         summarizeForMetadata(content, 100),
				Confidence:      ConfidenceHigh, // 楽観的デフォルト
				RequestedAction: inferActionFromText(content),
			}

			messages = append(messages, chatResp.Message)
			response.Messages = messages
			return response, nil
		}

		log.Printf("agent: %d tool calls received", len(chatResp.Message.ToolCalls))

		toolCalls := chatResp.Message.ToolCalls
		toolCalls = a.limitToolCallsPerIteration(toolCalls)
		toolCalls = normalizeRawArgsToolCalls(toolCalls)
		// chatResp.Message.ToolCalls も切り詰めて履歴の整合性を保つ
		chatResp.Message.ToolCalls = toolCalls

		// assistantメッセージ（tool_calls含む）を履歴に追加
		messages = append(messages, scrubLargeToolCallArgumentsForHistory(chatResp.Message))

		// 各ツールを順次実行（制限後のリストを使う）
		for _, tc := range toolCalls {
			if blockMsg := malformedRawArgsBlockMessage(tc); blockMsg != "" {
				argsJSON, _ := tc.Function.ArgumentsJSON()
				log.Printf("agent: %s", blockMsg)
				messages = scrubToolCallArguments(messages, tc.ID, "malformed raw_args payload was discarded; regenerate valid structured tool arguments")
				record := ToolCallRecord{
					Iteration:  iteration,
					ToolCallID: tc.ID,
					ToolName:   tc.Function.Name,
					Arguments:  argsJSON,
					Result: &tools.Result{
						IsError: true,
						Content: blockMsg,
					},
				}
				response.ToolCalls = append(response.ToolCalls, record)
				messages = append(messages, llm.Message{
					Role:       "tool",
					Content:    blockMsg,
					ToolCallID: tc.ID,
				})
				a.emitProgress(ProgressEvent{
					Type:            EventAgentActivity,
					ActivityMessage: fmt.Sprintf("Warning: ⚠️ %s arguments malformed", tc.Function.Name),
				})
				if signal := a.watchdog.RecordToolFailure(tc.Function.Name, argsJSON, blockMsg); signal != nil {
					log.Printf("agent: watchdog stop: %s - %s", signal.Reason, signal.Detail)
					return a.escalate(ctx, messages, response, signal)
				}
				continue
			}

			argsJSON, err := tc.Function.ArgumentsJSON()
			if err != nil {
				log.Printf("agent: failed to serialize args: %v", err)
				continue
			}

			// ツール実行開始通知
			a.emitProgress(ProgressEvent{
				Type:            EventAgentActivity,
				ActivityMessage: fmt.Sprintf("Working: 🔧 %s(%s)", tc.Function.Name, string(argsJSON)),
			})

			log.Printf("agent: executing tool %q with args %s", tc.Function.Name, string(argsJSON))

			record := ToolCallRecord{
				Iteration:  iteration,
				ToolCallID: tc.ID,
				ToolName:   tc.Function.Name,
				Arguments:  argsJSON,
			}

			if path, ok := repeatedMarkdownFullRead(tc.Function.Name, argsJSON, refusedMarkdownFullReads); ok {
				blockMsg := repeatedMarkdownFullReadBlockMessage(path)
				log.Printf("agent: %s", blockMsg)
				markdownRecovery.Require(path)
				record.Result = &tools.Result{
					IsError: true,
					Content: blockMsg,
				}
				record.Error = nil
				response.ToolCalls = append(response.ToolCalls, record)
				messages = append(messages, llm.Message{
					Role:       "tool",
					Content:    blockMsg,
					ToolCallID: tc.ID,
				})
				a.emitProgress(ProgressEvent{
					Type:            EventAgentActivity,
					ActivityMessage: fmt.Sprintf("Warning: ⚠️ %s repeated Markdown full read blocked", tc.Function.Name),
				})
				continue
			}

			if verificationSucceeded && isExploratoryTool(tc.Function.Name) {
				final := a.localizedResponse(userInput,
					"指定された検証は成功済みです。検証成功後の追加探索を停止し、ここで完了します。",
					"The requested verification has already passed. Stopping extra exploration after successful verification and completing here.",
				)
				log.Printf("agent: stopping exploratory tool %q after successful verification", tc.Function.Name)
				response.FinalContent = final
				response.Structured = &StructuredResponse{
					Summary:         final,
					Confidence:      ConfidenceHigh,
					RequestedAction: ActionContinue,
				}
				response.Messages = messages
				return response, nil
			}

			if count := successfulExplorationCalls[callHash(tc.Function.Name, argsJSON)]; shouldBlockRepeatedSuccessfulExploration(tc.Function.Name, count) {
				blockMsg := repeatedSuccessfulExplorationBlockMessage(tc.Function.Name, argsJSON, count)
				log.Printf("agent: %s", blockMsg)
				record.Result = &tools.Result{
					IsError: true,
					Content: blockMsg,
				}
				record.Error = nil
				response.ToolCalls = append(response.ToolCalls, record)
				messages = append(messages, llm.Message{
					Role:       "tool",
					Content:    blockMsg,
					ToolCallID: tc.ID,
				})
				a.emitProgress(ProgressEvent{
					Type:            EventAgentActivity,
					ActivityMessage: fmt.Sprintf("Warning: ⚠️ %s duplicate blocked", tc.Function.Name),
				})
				continue
			}

			if count := failedExplorationCalls[callHash(tc.Function.Name, argsJSON)]; shouldBlockRepeatedFailedExploration(tc.Function.Name, count) {
				blockMsg := repeatedFailedExplorationBlockMessage(tc.Function.Name, argsJSON, count)
				log.Printf("agent: %s", blockMsg)
				record.Result = &tools.Result{
					IsError: true,
					Content: blockMsg,
				}
				record.Error = nil
				response.ToolCalls = append(response.ToolCalls, record)
				messages = append(messages, llm.Message{
					Role:       "tool",
					Content:    blockMsg,
					ToolCallID: tc.ID,
				})
				a.emitProgress(ProgressEvent{
					Type:            EventAgentActivity,
					ActivityMessage: fmt.Sprintf("Warning: ⚠️ %s duplicate failure blocked", tc.Function.Name),
				})
				continue
			}

			// ウォッチドッグ: ループ検出
			if !skipIdenticalToolCallWatchdog(tc.Function.Name) {
				if signal := a.watchdog.RecordToolCall(tc.Function.Name, argsJSON); signal != nil {
					log.Printf("agent: watchdog stop: %s - %s", signal.Reason, signal.Detail)
					return a.escalate(ctx, messages, response, signal)
				}
			}

			if tools.ContainsOmittedToolArgument(tc.Function.Arguments) {
				blockMsg := omittedToolArgumentBlockMessage(tc.Function.Name, tc.Function.Arguments)
				log.Printf("agent: %s", blockMsg)
				if tc.Function.Name != "write_file" {
					structuralRecovery.Require("an omitted tool argument placeholder was rejected")
				}
				messages = scrubToolCallArguments(messages, tc.ID, omittedToolArgumentScrubReason(tc.Function.Name))
				record.Result = &tools.Result{
					IsError: true,
					Content: blockMsg,
				}
				record.Error = nil
				response.ToolCalls = append(response.ToolCalls, record)
				messages = append(messages, llm.Message{
					Role:       "tool",
					Content:    blockMsg,
					ToolCallID: tc.ID,
				})
				a.emitProgress(ProgressEvent{
					Type:            EventAgentActivity,
					ActivityMessage: fmt.Sprintf("Warning: ⚠️ %s blocked", tc.Function.Name),
				})
				if signal := a.watchdog.RecordToolFailure(tc.Function.Name, argsJSON, blockMsg); signal != nil {
					log.Printf("agent: watchdog stop: %s - %s", signal.Reason, signal.Detail)
					return a.escalate(ctx, messages, response, signal)
				}
				if tc.Function.Name != "write_file" {
					if signal := recordSemanticSafetyFailure(semanticSafetyFailures, "omitted_tool_argument", tc.Function.Name, blockMsg); signal != nil {
						log.Printf("agent: watchdog stop: %s - %s", signal.Reason, signal.Detail)
						return a.escalate(ctx, messages, response, signal)
					}
				}
				continue
			}

			// ツール取得
			tool, exists := a.tools.Get(tc.Function.Name)
			toolIsMutating := exists && tool.IsMutating()

			if structuralRecovery.Required && toolIsMutating {
				blockMsg := structuralRecoveryMutatingToolBlock(tc.Function.Name, structuralRecovery.Reason)
				log.Printf("agent: %s", blockMsg)
				messages = scrubToolCallArguments(messages, tc.ID, "mutating tool blocked until structural read confirms current file state")
				record.Result = &tools.Result{
					IsError: true,
					Content: blockMsg,
				}
				record.Error = nil
				response.ToolCalls = append(response.ToolCalls, record)
				messages = append(messages, llm.Message{
					Role:       "tool",
					Content:    blockMsg,
					ToolCallID: tc.ID,
				})
				a.emitProgress(ProgressEvent{
					Type:            EventAgentActivity,
					ActivityMessage: fmt.Sprintf("Warning: ⚠️ %s blocked", tc.Function.Name),
				})
				if signal := a.watchdog.RecordToolFailure(tc.Function.Name, argsJSON, blockMsg); signal != nil {
					log.Printf("agent: watchdog stop: %s - %s", signal.Reason, signal.Detail)
					return a.escalate(ctx, messages, response, signal)
				}
				if signal := recordSemanticSafetyFailure(semanticSafetyFailures, "structural_read_required", tc.Function.Name, blockMsg); signal != nil {
					log.Printf("agent: watchdog stop: %s - %s", signal.Reason, signal.Detail)
					return a.escalate(ctx, messages, response, signal)
				}
				continue
			}

			if exists && tc.Function.Name == "run_command" && !opts.AutoConfirmRunCommand {
				var runArgs struct {
					Command string `json:"command"`
					Dir     string `json:"dir,omitempty"`
				}
				if err := json.Unmarshal(argsJSON, &runArgs); err == nil {
					if runTool, ok := tool.(*tools.RunCommandTool); ok {
						action := runTool.Config().ClassifyCommand(runArgs.Command)
						if action == tools.ActionConfirm {
							// TUI に確認要求を通知
							a.emitProgress(ProgressEvent{
								Type:           EventRunCommandConfirmNeeded,
								PendingCommand: runArgs.Command,
								PendingDir:     runArgs.Dir,
							})
						}
					}
				}
			}

			// プランモード時の書き込み系ツールブロック
			if a.planMode && toolIsMutating {
				log.Printf("agent: BLOCKED write tool %q in plan mode", tc.Function.Name)
				blockMsg := fmt.Sprintf(
					"Tool %q is disabled in PLAN mode. "+
						"Switch to EDIT mode (Shift+Tab) to make file modifications. "+
						"In PLAN mode, only investigation and proposals are allowed.",
					tc.Function.Name,
				)
				record.Result = &tools.Result{
					IsError: true,
					Content: blockMsg,
				}
				record.Error = nil // execErr ではなく明示的なブロック
				response.ToolCalls = append(response.ToolCalls, record)

				messages = append(messages, llm.Message{
					Role:       "tool",
					Content:    blockMsg,
					ToolCallID: tc.ID,
				})
				if signal := a.watchdog.RecordToolFailure(tc.Function.Name, argsJSON, blockMsg); signal != nil {
					log.Printf("agent: watchdog stop: %s - %s", signal.Reason, signal.Detail)
					return a.escalate(ctx, messages, response, signal)
				}
				continue // 次のツール呼び出しへ
			}

			// 書き込み系ツールのチェックとpre-commit
			isMutating := false
			if toolIsMutating && a.shadow != nil {
				isMutating = true
				shadowCtx, shadowCancel := context.WithTimeout(ctx, ShadowOperationTimeout)
				preHash, err := a.shadow.CommitPre(shadowCtx, tc.Function.Name)
				shadowCancel()
				if err != nil {
					blockMsg := fmt.Sprintf("Tool %q blocked: shadow snapshot failed before mutating tool execution: %v", tc.Function.Name, err)
					log.Printf("agent: %s", blockMsg)
					record.Result = &tools.Result{
						IsError: true,
						Content: blockMsg,
					}
					record.Error = nil
					response.ToolCalls = append(response.ToolCalls, record)

					messages = append(messages, llm.Message{
						Role:       "tool",
						Content:    blockMsg,
						ToolCallID: tc.ID,
					})
					a.emitProgress(ProgressEvent{
						Type:            EventAgentActivity,
						ActivityMessage: fmt.Sprintf("Warning: ⚠️ %s blocked", tc.Function.Name),
					})
					if signal := a.watchdog.RecordToolFailure(tc.Function.Name, argsJSON, blockMsg); signal != nil {
						log.Printf("agent: watchdog stop: %s - %s", signal.Reason, signal.Detail)
						return a.escalate(ctx, messages, response, signal)
					}
					continue
				}
				record.PreCommit = preHash
				log.Printf("agent: pre-commit %s for %s", preHash, tc.Function.Name)
			}

			start := time.Now()
			result, execErr := a.tools.Execute(ctx, tc.Function.Name, argsJSON)
			record.DurationMs = time.Since(start).Milliseconds()

			record.Result = result
			record.Error = execErr

			// 書き込み系ツールのpost-commit
			if isMutating && a.shadow != nil {
				shadowCtx, shadowCancel := context.WithTimeout(ctx, ShadowOperationTimeout)
				postHash, err := a.shadow.CommitPost(shadowCtx, tc.Function.Name)
				shadowCancel()
				if err != nil {
					log.Printf("agent: post-commit failed: %v", err)
				} else {
					record.PostCommit = postHash
					log.Printf("agent: post-commit %s for %s", postHash, tc.Function.Name)
				}
			}

			response.ToolCalls = append(response.ToolCalls, record)

			var resultContent string
			if execErr != nil {
				resultContent = fmt.Sprintf("Error: %v", execErr)
				log.Printf("agent: tool %q error: %v", tc.Function.Name, execErr)
				a.emitProgress(ProgressEvent{
					Type:            EventAgentActivity,
					ActivityMessage: fmt.Sprintf("Warning: ⚠️ %s failed", tc.Function.Name),
				})
			} else if result.IsError {
				resultContent = result.Content
				log.Printf("agent: tool %q returned error: %s", tc.Function.Name, result.Content)
				if isCheckerUnavailableResult(result) {
					unavailableTools[tc.Function.Name] = true
					resultContent += "\n\nThis checker is unavailable in the current environment and will be hidden for the rest of this run. Continue with other available checks or explain the environment blocker if verification was required."
					result.Content = resultContent
					log.Printf("agent: checker tool %q marked unavailable for this run", tc.Function.Name)
				}
				a.emitProgress(ProgressEvent{
					Type:            EventAgentActivity,
					ActivityMessage: fmt.Sprintf("Warning: ⚠️ %s returned error", tc.Function.Name),
				})
			} else {
				resultContent = result.Content
				log.Printf("agent: tool %q success, %d bytes", tc.Function.Name, len(result.Content))
				a.emitProgress(ProgressEvent{
					Type:            EventAgentActivity,
					ActivityMessage: fmt.Sprintf("Success: ✓ %s (%d bytes)", tc.Function.Name, len(result.Content)),
				})

				// 差分（Diff）を取得して結果に注入
				// run_command は副作用が多く Diff が巨大になりがちなため、明示的な書き込みツールのみに限定する。
				// write_file は新規文書作成などで巨大 diff になりやすく、内容はモデル自身が生成済みなので返さない。
				// edit_with_pattern は一意置換の成否が本体結果で分かるため、diff が必要なら get_diff_summary に寄せる。
				if a.shadow != nil && record.PreCommit != "" && record.PostCommit != "" && tc.Function.Name != "run_command" && tc.Function.Name != "write_file" && tc.Function.Name != "edit_with_pattern" {
					// 最大行数に制限してコンテキスト溢れを防ぐ
					shadowCtx, shadowCancel := context.WithTimeout(ctx, ShadowOperationTimeout)
					diff, err := a.shadow.Diff(shadowCtx, record.PreCommit, record.PostCommit, DefaultDiffMaxLines)
					shadowCancel()
					if err == nil && diff != "" {
						resultContent = fmt.Sprintf("%s\n\n```diff\n%s\n```", resultContent, diff)
					}
				}
			}

			messages = append(messages, llm.Message{
				Role:       "tool",
				Content:    resultContent,
				ToolCallID: tc.ID,
			})

			// ツール実行後の推定トークン数でTUIを更新（リアルタイム性向上）
			a.emitProgress(ProgressEvent{
				Type:         EventTokenUpdate,
				PromptTokens: a.estimateTokenCountWithTools(prepareMessagesForLLMRequest(messages)),
				Iteration:    iteration + 1,
			})

			if execErr != nil || (result != nil && result.IsError) {
				if isRepeatedExplorationGuardTool(tc.Function.Name) {
					failedExplorationCalls[callHash(tc.Function.Name, argsJSON)]++
				}
				if path, ok := markdownFullReadRefusal(resultContent, tc.Function.Name, argsJSON); ok {
					if path != "" {
						refusedMarkdownFullReads[path] = true
					}
					markdownRecovery.Require(path)
					log.Printf("agent: markdown read recovery required for %s", path)
				}
				if path, ok := editPatternNotFound(resultContent, tc.Function.Name, argsJSON); ok {
					structuralRecovery.Require(fmt.Sprintf("edit_with_pattern could not find the requested text in %s", path))
					log.Printf("agent: structural read recovery required after edit_with_pattern not-found for %s", path)
				}
				if signal := a.watchdog.RecordToolFailure(tc.Function.Name, argsJSON, resultContent); signal != nil {
					log.Printf("agent: watchdog stop: %s - %s", signal.Reason, signal.Detail)
					return a.escalate(ctx, messages, response, signal)
				}
				if safetyKind := semanticSafetyFailureKind(resultContent); safetyKind != "" {
					if signal := recordSemanticSafetyFailure(semanticSafetyFailures, safetyKind, tc.Function.Name, resultContent); signal != nil {
						log.Printf("agent: watchdog stop: %s - %s", signal.Reason, signal.Detail)
						return a.escalate(ctx, messages, response, signal)
					}
				}
			} else {
				if isRepeatedExplorationGuardTool(tc.Function.Name) {
					successfulExplorationCalls[callHash(tc.Function.Name, argsJSON)]++
				}
				if markdownRecovery.Required && isMarkdownRecoveryToolCall(tc.Function.Name, argsJSON, markdownRecovery.Path) {
					log.Printf("agent: markdown read recovery satisfied by %s", tc.Function.Name)
					markdownRecovery.Clear()
				}
				if structuralRecovery.Required && isStructuralReadToolCall(tc.Function.Name, argsJSON) {
					log.Printf("agent: structural read recovery satisfied by %s", tc.Function.Name)
					structuralRecovery.ClearAfterStructuralRead()
				} else if structuralRecovery.VerifyAfterNextEdit && toolIsMutating {
					structuralRecovery.RequirePostEditVerification()
					log.Printf("agent: structural post-edit verification required after %s", tc.Function.Name)
				}
				if tc.Function.Name == "run_tests" {
					verificationSucceeded = true
				}
			}
		}

		response.Messages = messages
	}

	// 最大イテレーション到達
	signal := &StopSignal{
		Reason: StopReasonLoopDetected,
		Detail: fmt.Sprintf("reached maximum %d iterations", maxIterations),
	}
	response.MaxIterationsReached = true
	return a.escalate(ctx, messages, response, signal)
}

func normalizeMaxIterations(maxIterations int) int {
	if maxIterations <= 0 {
		return MaxIterations
	}
	return maxIterations
}

func taskRequiresSavedArtifact(input string) bool {
	normalized := strings.ToLower(strings.TrimSpace(input))
	if normalized == "" {
		return false
	}
	hasWriteIntent := strings.Contains(normalized, "保存") ||
		strings.Contains(normalized, "書き出") ||
		strings.Contains(normalized, "作成") ||
		strings.Contains(normalized, "出力") ||
		strings.Contains(normalized, "save") ||
		strings.Contains(normalized, "write") ||
		strings.Contains(normalized, "create")
	if !hasWriteIntent {
		return false
	}
	hasArtifact := strings.Contains(normalized, "計画書") ||
		strings.Contains(normalized, "プラン") ||
		strings.Contains(normalized, "ドキュメント") ||
		strings.Contains(normalized, "document") ||
		strings.Contains(normalized, "plan") ||
		strings.Contains(normalized, "file") ||
		strings.Contains(normalized, "ファイル") ||
		strings.Contains(normalized, ".md") ||
		strings.Contains(normalized, "/")
	if !hasArtifact {
		return false
	}
	return strings.Contains(normalized, "配下") ||
		strings.Contains(normalized, "path") ||
		strings.Contains(normalized, "パス") ||
		strings.Contains(normalized, ".md") ||
		strings.Contains(normalized, "/")
}

func hasSuccessfulFileMutation(records []ToolCallRecord) bool {
	for _, record := range records {
		switch record.ToolName {
		case "write_file", "edit_file", "edit_with_pattern":
		default:
			continue
		}
		if record.Error != nil {
			continue
		}
		if record.Result != nil && !record.Result.IsError {
			return true
		}
	}
	return false
}

func hasSuccessfulMutationOrVerification(records []ToolCallRecord) bool {
	for _, record := range records {
		if record.Error != nil || record.Result == nil || record.Result.IsError {
			continue
		}
		switch record.ToolName {
		case "write_file", "edit_file", "edit_with_pattern",
			"check_python_syntax", "check_go_package", "check_javascript_syntax", "check_typescript",
			"run_tests":
			return true
		}
	}
	return false
}

func shouldBlockIntentOnlyFinal(userInput, content string, records []ToolCallRecord) bool {
	if hasSuccessfulMutationOrVerification(records) {
		return false
	}
	if !userRequestsImplementation(userInput) {
		return false
	}
	if inferActionFromText(content) == ActionAskUser {
		return false
	}
	return isIntentOnlyFinalResponse(content)
}

func userRequestsImplementation(input string) bool {
	normalized := strings.ToLower(strings.TrimSpace(input))
	if normalized == "" {
		return false
	}
	for _, marker := range []string{
		"実装", "修正", "変更", "追加", "削除", "置換", "更新", "適用", "直して", "なおして",
		"implement", "fix", "change", "modify", "edit", "add", "remove", "delete", "update", "apply",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func isIntentOnlyFinalResponse(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	runes := []rune(trimmed)
	if len(runes) > 180 {
		return false
	}
	lower := strings.ToLower(trimmed)
	intentMarkers := []string{
		"実装します", "修正します", "変更します", "追加します", "更新します", "対応します",
		"確認します", "調べます", "着手します", "進めます", "行います",
		"i will implement", "i'll implement", "i will fix", "i'll fix", "i will update",
		"i'll update", "i will check", "i'll check", "let me check", "let me implement",
		"let me fix", "now let me", "i'll proceed",
	}
	for _, marker := range intentMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func intentOnlyFinalRecoveryPrompt() string {
	return "You stopped after only declaring intent. Do not provide the final answer yet.\n" +
		"Continue the user's implementation request now by calling the next necessary tool.\n" +
		"If a previous lookup failed, use another available inspection tool such as find_symbol, search_text, get_file_outline, or a narrow read_file range to locate the current target.\n" +
		"After making the required change, run the appropriate syntax check or verification tool, update any requested status file, and only then finish with a concise result."
}

func isVMaxRunOptions(opts RunOptions) bool {
	return opts.MaxIterations >= VMaxIterations || (opts.AutoConfirmRunCommand && opts.PreflightShrink)
}

type structuralReadRecovery struct {
	Required             bool
	Reason               string
	PostEditVerification bool
	VerifyAfterNextEdit  bool
}

type markdownReadRecovery struct {
	Required bool
	Path     string
}

func (r *markdownReadRecovery) Require(path string) {
	r.Required = true
	r.Path = path
}

func (r *markdownReadRecovery) Clear() {
	r.Required = false
	r.Path = ""
}

func (r *structuralReadRecovery) Require(reason string) {
	r.Required = true
	r.Reason = reason
	r.PostEditVerification = false
	r.VerifyAfterNextEdit = false
}

func (r *structuralReadRecovery) RequirePostEditVerification() {
	r.Required = true
	r.Reason = "a follow-up edit was applied after structural recovery and needs structural verification"
	r.PostEditVerification = true
	r.VerifyAfterNextEdit = false
}

func (r *structuralReadRecovery) ClearAfterStructuralRead() {
	verifyAfterNextEdit := r.Required && !r.PostEditVerification
	r.Required = false
	r.Reason = ""
	r.PostEditVerification = false
	r.VerifyAfterNextEdit = verifyAfterNextEdit
}

func structuralRecoveryFinalPrompt(reason string) string {
	return "Do not finish yet. " + structuralRecoveryInstruction(reason)
}

func markdownRecoveryFinalPrompt(path string) string {
	return "Do not finish yet. " + markdownRecoveryInstruction(path)
}

func structuralRecoveryToolChoicePrompt(reason string) string {
	return "Recovery step required before continuing. " + structuralRecoveryInstruction(reason) +
		" The only available tools in this step are structural read tools. Choose one structural read for the current target now; do not attempt to edit, write, test, or finish."
}

func markdownRecoveryToolChoicePrompt(path string) string {
	return "Recovery step required before continuing. " + markdownRecoveryInstruction(path) +
		" The only available tools in this step are Markdown-focused read tools. Choose get_markdown_outline or read_markdown_section for the same Markdown file now; do not call read_file(path) without an explicit narrow range, edit, test, or finish."
}

func structuralRecoveryMutatingToolBlock(toolName, reason string) string {
	return fmt.Sprintf("Tool %q blocked: %s", toolName, structuralRecoveryInstruction(reason))
}

func markdownRecoveryInstruction(path string) string {
	path = strings.TrimSpace(path)
	target := "the Markdown file"
	if path != "" {
		target = fmt.Sprintf("%q", path)
	}
	return fmt.Sprintf(
		"A previous full Markdown read_file call was refused for %s. Do not repeat read_file with only path. First inspect the Markdown structure with get_markdown_outline, or read a specific heading with read_markdown_section. After that focused Markdown read succeeds, continue from the current file state.",
		target,
	)
}

func structuralRecoveryInstruction(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "a previous safety guard discarded or omitted edit context"
	}
	return fmt.Sprintf(
		"%s. Do not infer current file state from omitted previews or prior intent. "+
			"Before any further edit or final report, perform a structural read of the current target: prefer read_symbol, get_file_outline, or get_symbol_outline for supported code files; otherwise use read_file with a narrow line range. After that read succeeds, regenerate the next edit from the current source.",
		reason,
	)
}

func isStructuralReadToolCall(toolName string, argsJSON []byte) bool {
	switch toolName {
	case "read_symbol", "get_file_outline", "get_symbol_outline", "get_markdown_outline", "read_markdown_section", "get_json_outline", "read_json_path", "get_file_imports":
		return true
	case "read_file":
		var args struct {
			StartLine int `json:"start_line"`
			EndLine   int `json:"end_line"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return false
		}
		return args.StartLine > 0 && (args.EndLine == 0 || (args.EndLine >= args.StartLine && args.EndLine-args.StartLine <= 200))
	default:
		return false
	}
}

func structuralRecoveryToolDefinitions(defs []llm.ToolDefinition) []llm.ToolDefinition {
	filtered := make([]llm.ToolDefinition, 0, len(defs))
	for _, def := range defs {
		if isStructuralRecoveryToolName(def.Function.Name) {
			filtered = append(filtered, def)
		}
	}
	if len(filtered) == 0 {
		return defs
	}
	return filtered
}

func markdownRecoveryToolDefinitions(defs []llm.ToolDefinition) []llm.ToolDefinition {
	filtered := make([]llm.ToolDefinition, 0, len(defs))
	for _, def := range defs {
		if isMarkdownRecoveryToolName(def.Function.Name) {
			filtered = append(filtered, def)
		}
	}
	if len(filtered) == 0 {
		return defs
	}
	return filtered
}

func isStructuralRecoveryToolName(name string) bool {
	switch name {
	case "read_symbol", "get_file_outline", "get_symbol_outline", "get_markdown_outline", "read_markdown_section", "get_json_outline", "read_json_path", "get_file_imports", "read_file":
		return true
	default:
		return false
	}
}

func isMarkdownRecoveryToolName(name string) bool {
	switch name {
	case "get_markdown_outline", "read_markdown_section":
		return true
	default:
		return false
	}
}

func markdownFullReadRefusal(resultContent string, toolName string, argsJSON []byte) (string, bool) {
	if toolName != "read_file" || !strings.Contains(resultContent, "Refusing full Markdown read") {
		return "", false
	}
	var args struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", true
	}
	if args.StartLine > 0 || args.EndLine > 0 {
		return args.Path, false
	}
	return args.Path, true
}

func repeatedMarkdownFullRead(toolName string, argsJSON []byte, refused map[string]bool) (string, bool) {
	if toolName != "read_file" || len(refused) == 0 {
		return "", false
	}
	var args struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", false
	}
	if strings.TrimSpace(args.Path) == "" || args.StartLine > 0 || args.EndLine > 0 {
		return "", false
	}
	if !refused[args.Path] {
		return "", false
	}
	return args.Path, true
}

func repeatedMarkdownFullReadBlockMessage(path string) string {
	return fmt.Sprintf(
		"Tool \"read_file\" blocked: a full Markdown read for %q was already refused in this run. "+
			"Do not repeat read_file with only path. Use get_markdown_outline(path=%q), "+
			"read_markdown_section(path=%q, heading=\"EXACT_HEADING\"), "+
			"read_markdown_section(path=%q, start_line=START_LINE, end_line=END_LINE), "+
			"or read_file(path=%q, start_line=START_LINE, end_line=END_LINE).",
		path, path, path, path, path,
	)
}

func editPatternNotFound(resultContent string, toolName string, argsJSON []byte) (string, bool) {
	if toolName != "edit_with_pattern" || !strings.Contains(resultContent, "find_text not found") {
		return "", false
	}
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", true
	}
	return args.Path, true
}

func isMarkdownRecoveryToolCall(toolName string, argsJSON []byte, recoveryPath string) bool {
	var args struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return false
	}
	if recoveryPath != "" && args.Path != "" && args.Path != recoveryPath {
		return false
	}
	switch toolName {
	case "get_markdown_outline", "read_markdown_section":
		return true
	case "read_file":
		return args.StartLine > 0 && args.EndLine >= args.StartLine
	default:
		return false
	}
}

func filterUnavailableToolDefinitions(defs []llm.ToolDefinition, unavailable map[string]bool) []llm.ToolDefinition {
	if len(unavailable) == 0 {
		return defs
	}
	filtered := make([]llm.ToolDefinition, 0, len(defs))
	for _, def := range defs {
		if unavailable[def.Function.Name] {
			continue
		}
		filtered = append(filtered, def)
	}
	return filtered
}

func isCheckerUnavailableResult(result *tools.Result) bool {
	if result == nil || result.Metadata == nil {
		return false
	}
	value, _ := result.Metadata["checker_unavailable"].(bool)
	return value
}

func semanticSafetyFailureKind(resultContent string) string {
	switch {
	case strings.Contains(resultContent, tools.OmittedToolArgumentError()) ||
		strings.Contains(resultContent, tools.OmittedToolArgumentMarker):
		return "omitted_tool_argument"
	case strings.Contains(resultContent, "serialized list of code lines"):
		return "serialized_code_line_list"
	default:
		return ""
	}
}

func recordSemanticSafetyFailure(counts map[string]int, kind string, toolName string, detail string) *StopSignal {
	if kind == "" {
		return nil
	}
	key := toolName + ":" + kind
	counts[key]++
	if counts[key] < maxSemanticSafetyFailuresPerRun {
		return nil
	}
	return &StopSignal{
		Reason: StopReasonLoopDetected,
		Detail: fmt.Sprintf(
			"tool %q hit safety guard %q %d times in this run. Stop retrying the same omitted or unsafe payload; use a structural read of the current target, then regenerate real tool arguments from current source. Last error: %s",
			toolName,
			kind,
			counts[key],
			truncateForStopDetail(detail, 300),
		),
	}
}

func omittedToolArgumentBlockMessage(toolName string, args map[string]interface{}) string {
	if toolName != "write_file" {
		return fmt.Sprintf("Tool %q blocked: %s", toolName, tools.OmittedToolArgumentError())
	}
	path, _ := args["path"].(string)
	if path == "" {
		return fmt.Sprintf("Tool %q blocked: omitted-content placeholder is not executable file content. Regenerate the full content before calling write_file again.", toolName)
	}
	return fmt.Sprintf("Tool %q blocked: omitted-content placeholder is not executable file content for %s. Regenerate the full content before calling write_file again; if you need the current file state, read %s with read_file first.", toolName, path, path)
}

func omittedToolArgumentScrubReason(toolName string) string {
	if toolName == "write_file" {
		return "write_file content placeholder was rejected; regenerate the full file content or read the current path before writing again"
	}
	return "omitted tool argument payload was discarded; use a structural read before regenerating a small real edit"
}

func normalizeRawArgsToolCalls(toolCalls []llm.ToolCall) []llm.ToolCall {
	if len(toolCalls) == 0 {
		return toolCalls
	}
	out := make([]llm.ToolCall, len(toolCalls))
	copy(out, toolCalls)
	changed := false
	for i, tc := range out {
		normalized, ok := normalizeRawArgs(tc.Function.Arguments)
		if !ok {
			continue
		}
		tc.Function.Arguments = normalized
		out[i] = tc
		changed = true
	}
	if !changed {
		return toolCalls
	}
	return out
}

func normalizeRawArgs(args map[string]interface{}) (map[string]interface{}, bool) {
	if len(args) != 1 {
		return nil, false
	}
	raw, ok := args["raw_args"].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil, false
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil || len(parsed) == 0 {
		return nil, false
	}
	if _, nested := parsed["raw_args"]; nested && len(parsed) == 1 {
		return nil, false
	}
	return parsed, true
}

func malformedRawArgsBlockMessage(tc llm.ToolCall) string {
	if len(tc.Function.Arguments) != 1 {
		return ""
	}
	raw, ok := tc.Function.Arguments["raw_args"].(string)
	if !ok {
		return ""
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Sprintf("Tool %q blocked: tool arguments were malformed or empty. Regenerate a valid structured tool call with the required fields; do not reuse raw_args.", tc.Function.Name)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &parsed); err == nil && len(parsed) > 0 {
		return ""
	}
	return fmt.Sprintf("Tool %q blocked: tool arguments were malformed or truncated before execution. Regenerate a valid structured tool call with the required fields; do not reuse raw_args. For write_file, include explicit path, content, and mode when writing an existing file.", tc.Function.Name)
}

func normalizeInlineToolCallMarkup(msg llm.Message) llm.Message {
	if len(msg.ToolCalls) > 0 || !strings.Contains(msg.Content, "<tool_call>") {
		return msg
	}
	toolCalls, content := parseInlineToolCallMarkup(msg.Content)
	if len(toolCalls) == 0 {
		return msg
	}
	msg.Content = strings.TrimSpace(content)
	msg.ToolCalls = toolCalls
	return msg
}

func parseInlineToolCallMarkup(content string) ([]llm.ToolCall, string) {
	blockRe := regexp.MustCompile(`(?s)<tool_call>\s*<function=([A-Za-z_][A-Za-z0-9_]*)>\s*(.*?)</tool_call>`)
	paramRe := regexp.MustCompile(`<parameter=([A-Za-z_][A-Za-z0-9_]*)>`)
	matches := blockRe.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return nil, content
	}

	toolCalls := make([]llm.ToolCall, 0, len(matches))
	var cleaned strings.Builder
	last := 0
	for i, match := range matches {
		cleaned.WriteString(content[last:match[0]])
		last = match[1]

		name := content[match[2]:match[3]]
		body := content[match[4]:match[5]]
		args := map[string]interface{}{}
		params := paramRe.FindAllStringSubmatchIndex(body, -1)
		for j, param := range params {
			key := strings.TrimSpace(body[param[2]:param[3]])
			valueStart := param[1]
			valueEnd := len(body)
			if j+1 < len(params) {
				valueEnd = params[j+1][0]
			}
			value := strings.TrimSpace(body[valueStart:valueEnd])
			if key == "" {
				continue
			}
			args[key] = parseInlineToolCallValue(value)
		}
		if name == "" || len(args) == 0 {
			continue
		}
		toolCalls = append(toolCalls, llm.ToolCall{
			ID: fmt.Sprintf("inline_tool_call_%d", i+1),
			Function: llm.FunctionCall{
				Name:      name,
				Arguments: args,
			},
		})
	}
	cleaned.WriteString(content[last:])
	return toolCalls, cleaned.String()
}

func parseInlineToolCallValue(value string) interface{} {
	switch strings.ToLower(value) {
	case "true":
		return true
	case "false":
		return false
	}
	if n, err := strconv.Atoi(value); err == nil {
		return n
	}
	return value
}

func scrubToolCallArguments(messages []llm.Message, toolCallID string, reason string) []llm.Message {
	if toolCallID == "" {
		return messages
	}
	out := make([]llm.Message, len(messages))
	copy(out, messages)
	for i := len(out) - 1; i >= 0; i-- {
		msg := out[i]
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		toolCalls := make([]llm.ToolCall, len(msg.ToolCalls))
		copy(toolCalls, msg.ToolCalls)
		for j, tc := range toolCalls {
			if tc.ID != toolCallID {
				continue
			}
			tc.Function.Arguments = scrubbedToolArguments(tc.Function.Name, tc.Function.Arguments, reason)
			toolCalls[j] = tc
			msg.ToolCalls = toolCalls
			out[i] = msg
			return out
		}
	}
	return messages
}

func scrubLargeToolCallArgumentsForHistory(msg llm.Message) llm.Message {
	if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
		return msg
	}
	toolCalls := make([]llm.ToolCall, len(msg.ToolCalls))
	copy(toolCalls, msg.ToolCalls)
	changed := false
	for i, tc := range toolCalls {
		if !hasLargeHistoryArgument(tc.Function.Name, tc.Function.Arguments) &&
			!tools.ContainsOmittedToolArgument(tc.Function.Arguments) {
			continue
		}
		tc.Function.Arguments = scrubbedToolArguments(tc.Function.Name, tc.Function.Arguments, "large or unsafe tool payload was discarded before saving conversation history")
		toolCalls[i] = tc
		changed = true
	}
	if !changed {
		return msg
	}
	msg.ToolCalls = toolCalls
	return msg
}

func hasLargeHistoryArgument(toolName string, args map[string]interface{}) bool {
	for _, field := range compactibleArgumentFields(toolName) {
		value, ok := args[field]
		if !ok {
			continue
		}
		if argumentValueLength(value) > toolArgumentCompactionChars {
			return true
		}
	}
	return false
}

func argumentValueLength(value interface{}) int {
	text, ok := argumentValueText(value)
	if !ok {
		return 0
	}
	return len(text)
}

func argumentValueText(value interface{}) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case []string:
		return strings.Join(v, "\n"), true
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return "", false
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, "\n"), true
	default:
		return "", false
	}
}

func scrubbedToolArguments(toolName string, args map[string]interface{}, reason string) map[string]interface{} {
	out := map[string]interface{}{
		"_discarded_tool_arguments": reason,
		"_instruction":              "Do not reuse this historical tool payload. Use a structural read of the current target (read_symbol/get_file_outline/get_symbol_outline, or narrow read_file fallback) before creating a semantic edit from current source.",
		"_scrubbed_tool_arguments":  scrubbedToolArgumentMetadata(toolName, args, reason),
	}
	if path, ok := args["path"]; ok {
		out["path"] = path
	}
	switch toolName {
	case "edit_file":
		if startLine, ok := args["start_line"]; ok {
			out["start_line"] = startLine
		}
		if endLine, ok := args["end_line"]; ok {
			out["end_line"] = endLine
		}
		out["new_lines"] = []interface{}{"[discarded large or unsafe edit payload; do not reuse; perform a structural read and create a semantic edit from current source]"}
	case "edit_with_pattern":
		out["find_text"] = "[discarded unsafe find_text payload; do not reuse]"
		out["replace_with"] = "[discarded large or unsafe replacement payload; do not reuse; perform a structural read and create a semantic edit from current source]"
	case "write_file":
		out["_instruction"] = "Do not reuse this historical write_file payload. The raw content was omitted from conversation history; read the target path if you need current file state, or regenerate the full content before writing again."
		out["content"] = writeFileContentReference(args, reason)
	}
	return out
}

func writeFileContentReference(args map[string]interface{}, reason string) string {
	content, _ := argumentValueText(args["content"])
	sum := sha256.Sum256([]byte(content))
	lines := 0
	if content != "" {
		lines = strings.Count(content, "\n") + 1
	}
	path, _ := args["path"].(string)
	mode, _ := args["mode"].(string)
	if mode == "" {
		mode = "write"
	}

	var b strings.Builder
	b.WriteString(tools.OmittedToolArgumentMarker)
	b.WriteString("\nwrite_file content omitted from conversation history; this is a reference record, not executable file content.")
	if path != "" {
		b.WriteString("\nPath: ")
		b.WriteString(path)
	}
	b.WriteString("\nMode: ")
	b.WriteString(mode)
	b.WriteString(fmt.Sprintf("\nOriginal chars: %d\nOriginal lines: %d\nOriginal estimated tokens: %d\nSHA256: %s",
		len(content),
		lines,
		tokenizer.EstimateTokens(content),
		hex.EncodeToString(sum[:]),
	))
	if reason != "" {
		b.WriteString("\nReason: ")
		b.WriteString(reason)
	}
	if path != "" {
		b.WriteString("\nTo inspect current content after a successful write_file, call read_file with this path. To write again, regenerate the full content; do not copy this placeholder.")
	} else {
		b.WriteString("\nTo write again, regenerate the full content; do not copy this placeholder.")
	}
	return b.String()
}

func scrubbedToolArgumentMetadata(toolName string, args map[string]interface{}, reason string) []map[string]interface{} {
	fields := compactibleArgumentFields(toolName)
	if len(fields) == 0 {
		return nil
	}
	metadata := make([]map[string]interface{}, 0, len(fields))
	for _, field := range fields {
		value, ok := args[field]
		if !ok {
			continue
		}
		text, ok := argumentValueText(value)
		if !ok {
			continue
		}
		sum := sha256.Sum256([]byte(text))
		metadata = append(metadata, map[string]interface{}{
			"field":          field,
			"reason":         reason,
			"original_chars": len(text),
			"original_lines": strings.Count(text, "\n") + 1,
			"sha256":         hex.EncodeToString(sum[:]),
		})
	}
	return metadata
}

func truncateForStopDetail(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "..."
}

func (a *Agent) setRunCommandAutoConfirm(enabled bool) func() {
	tool, exists := a.tools.Get("run_command")
	if !exists {
		return func() {}
	}
	runTool, ok := tool.(*tools.RunCommandTool)
	if !ok {
		return func() {}
	}
	previous := runTool.AutoConfirm()
	runTool.SetAutoConfirm(enabled)
	return func() {
		runTool.SetAutoConfirm(previous)
	}
}

func (a *Agent) toolDefinitions() []llm.ToolDefinition {
	defs := a.tools.Definitions()

	// ツールを優先順位に従ってソート
	// find_symbol, get_file_outline を先頭に、search_text を最後に配置する
	sort.Slice(defs, func(i, j int) bool {
		priority := map[string]int{
			"find_symbol":             100,
			"find_dependents":         95,
			"get_file_outline":        90,
			"get_symbol_outline":      89,
			"get_json_outline":        88,
			"read_json_path":          87,
			"get_markdown_outline":    86,
			"read_markdown_section":   84,
			"get_file_imports":        85,
			"get_diff_summary":        82,
			"read_file":               80,
			"check_python_syntax":     76,
			"check_go_package":        76,
			"check_javascript_syntax": 76,
			"check_typescript":        76,
			"write_file":              70,
			"edit_file":               60,
			"list_files":              50,
			"run_command":             40,
			"search_text":             0, // 最低優先度（フォールバック）
		}
		pI := priority[defs[i].Function.Name]
		pJ := priority[defs[j].Function.Name]
		if pI != pJ {
			return pI > pJ
		}
		return defs[i].Function.Name < defs[j].Function.Name
	})

	result := make([]llm.ToolDefinition, 0, len(defs))
	for _, d := range defs {
		if !a.toolAllowedByProfile(d.Function.Name) {
			continue
		}
		// プランモード時は書き込み系ツールを除外
		if a.planMode {
			tool, exists := a.tools.Get(d.Function.Name)
			if exists && tool.IsMutating() {
				continue // 除外
			}
		}

		result = append(result, llm.ToolDefinition{
			Type: d.Type,
			Function: struct {
				Name        string                 `json:"name"`
				Description string                 `json:"description"`
				Parameters  map[string]interface{} `json:"parameters"`
			}{
				Name:        d.Function.Name,
				Description: compactToolDescription(d.Function.Name, d.Function.Description),
				Parameters:  compactToolParameters(d.Function.Name, d.Function.Parameters),
			},
		})
	}
	return result
}

func compactToolDescription(name, fallback string) string {
	descriptions := map[string]string{
		"find_symbol":             "Find indexed code symbols by name. Use before search_text when looking for functions, methods, classes, types, consts, or vars.",
		"get_file_outline":        "Return a code file outline with symbols, signatures, line numbers, and docs. Use before reading large code files.",
		"get_symbol_outline":      "Return child symbols for one large class/type/function without reading its body.",
		"read_symbol":             "Read one symbol by AST boundary. Large symbols default to summary; full mode is only for small symbols.",
		"get_markdown_outline":    "Inspect .md headings with line ranges and estimated tokens. Use before read_file for Markdown.",
		"read_markdown_section":   "Read one Markdown section by heading or line range. Use instead of read_file for .md sections.",
		"get_json_outline":        "Return JSON structure and size without loading the whole file.",
		"read_json_path":          "Read a focused JSON value using JSONPath.",
		"get_file_imports":        "Return indexed imports for one Python file.",
		"find_dependents":         "Find Python files importing a module using the import index.",
		"get_callers":             "Find indexed callers of a Go or Python function/method.",
		"get_call_graph":          "Return a Mermaid call graph for a Go or Python function/method.",
		"get_diff_summary":        "Summarize recent shadow-git edits without reading full files.",
		"read_file":               "Read a file with line numbers and short line hashes. Do not use without a range for .md files; use Markdown tools or ranges.",
		"search_text":             "Fallback full-text regex search for strings, comments, configs, or non-indexed files.",
		"list_files":              "List files/directories, optionally recursively.",
		"write_file":              "Create, append, or overwrite a file. Prefer edit tools for existing files.",
		"edit_file":               "Replace a specific 1-indexed line range in a file; pass expected line hashes from read_file when available.",
		"edit_with_pattern":       "Replace one unique text pattern in a file. Preferred for precise edits.",
		"check_python_syntax":     "Check Python syntax for one .py file using py_compile. Use after Python edits before run_tests.",
		"check_go_package":        "Check a Go package quickly with go test -run '^$'. Use after Go edits before run_tests.",
		"check_javascript_syntax": "Check JavaScript syntax for one file using node --check. Use after JS edits before run_tests.",
		"check_typescript":        "Check TypeScript with tsc --noEmit --pretty false. Use after TS/TSX edits before run_tests.",
		"run_tests":               "Run project tests for Go, Python, JS/TS, or Rust.",
		"run_command":             "Run a workspace shell command; unsafe commands may require confirmation.",
		"fetch_docs":              "Fetch a web page and return extracted Markdown.",
	}
	if description, ok := descriptions[name]; ok {
		return description
	}
	return fallback
}

func compactToolParameters(toolName string, parameters map[string]interface{}) map[string]interface{} {
	if parameters == nil {
		return nil
	}
	out := cloneMap(parameters)
	props, ok := out["properties"].(map[string]interface{})
	if !ok {
		return out
	}
	for name, value := range props {
		prop, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		prop = cloneMap(prop)
		if desc := compactParameterDescription(toolName, name); desc != "" {
			prop["description"] = desc
		} else {
			delete(prop, "description")
		}
		props[name] = prop
	}
	out["properties"] = props
	return out
}

func compactParameterDescription(toolName, name string) string {
	byName := map[string]string{
		"path":                "Path relative to workspace.",
		"pattern":             "Regex/text pattern.",
		"name":                "Symbol/function name.",
		"symbol_name":         "Exact symbol name.",
		"heading":             "Markdown heading title.",
		"jsonpath":            "JSONPath expression.",
		"start_line":          "1-indexed start line.",
		"end_line":            "Inclusive end line.",
		"max_lines":           "Maximum lines.",
		"max_results":         "Maximum results.",
		"limit":               "Maximum results.",
		"receiver":            "Exact receiver/class.",
		"name_filter":         "Case-insensitive name filter.",
		"file_path":           "Path substring filter.",
		"type":                "Symbol/type filter.",
		"fallback_only":       "Only fallback symbols.",
		"include_methods":     "Include methods.",
		"full":                "Return full body only for small symbols.",
		"content":             "File content.",
		"mode":                "write mode: overwrite or append.",
		"find_text":           "Unique text to replace.",
		"replace_with":        "Replacement text.",
		"expected_start_hash": "Optional h: hash for start_line.",
		"expected_end_hash":   "Optional h: hash for end_line.",
		"new_lines":           "Replacement lines.",
		"command":             "Shell command.",
		"dir":                 "Working directory.",
		"url":                 "URL to fetch.",
		"max_depth":           "Maximum outline depth.",
		"language":            "Language override.",
	}
	if desc, ok := byName[name]; ok {
		return desc
	}
	switch toolName + "." + name {
	case "find_dependents.module":
		return "Module name."
	case "find_dependents.exact":
		return "Exact module only."
	case "find_dependents.import_kind":
		return "import or from_import."
	case "find_dependents.imported_name":
		return "Imported symbol name."
	case "find_dependents.alias":
		return "Import alias."
	case "find_dependents.scope":
		return "Import scope."
	case "find_dependents.include_relative":
		return "Include relative imports."
	case "find_dependents.wildcard_only":
		return "Only wildcard imports."
	case "list_files.recursive":
		return "List recursively."
	case "list_files.show_hidden":
		return "Include hidden files."
	case "list_files.glob":
		return "Glob filter."
	case "search_text.ignore_case":
		return "Case-insensitive."
	case "search_text.file_type":
		return "File type filter."
	case "get_diff_summary.from":
		return "Start shadow commit."
	case "get_diff_summary.to":
		return "End shadow commit."
	default:
		return ""
	}
}

func cloneMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		if nested, ok := v.(map[string]interface{}); ok {
			out[k] = cloneMap(nested)
			continue
		}
		out[k] = v
	}
	return out
}

func normalizeToolProfile(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "", ToolProfileDefault, "full":
		return ToolProfileDefault
	case ToolProfileSmall:
		return ToolProfileSmall
	default:
		return ToolProfileDefault
	}
}

func NormalizeToolProfile(profile string) string {
	return normalizeToolProfile(profile)
}

func (a *Agent) toolAllowedByProfile(name string) bool {
	if a.ToolProfile() != ToolProfileSmall {
		return true
	}
	return smallToolAllowlist[name]
}

func (a *Agent) limitToolCallsPerIteration(toolCalls []llm.ToolCall) []llm.ToolCall {
	if len(toolCalls) == 0 {
		return toolCalls
	}

	limited := toolCalls
	if len(limited) > MaxToolCallsPerIteration {
		log.Printf("agent: limiting tool calls from %d to %d (total limit)",
			len(limited), MaxToolCallsPerIteration)
		limited = limited[:MaxToolCallsPerIteration]
	}

	result := make([]llm.ToolCall, 0, len(limited))
	heavyReadTotal := 0
	readOnlyTotal := 0
	mutatingTotal := 0
	heavyReadKept := 0
	readOnlyKept := 0
	mutatingKept := 0

	for _, tc := range limited {
		if isHeavyReadToolCall(tc) {
			heavyReadTotal++
			if heavyReadKept >= MaxHeavyReadToolCallsPerIteration {
				continue
			}
			heavyReadKept++
			result = append(result, tc)
			continue
		}

		if a.isMutatingToolCall(tc) {
			mutatingTotal++
			if mutatingKept >= MaxMutatingToolCallsPerIteration {
				continue
			}
			mutatingKept++
			result = append(result, tc)
			continue
		}

		readOnlyTotal++
		if readOnlyKept >= MaxReadOnlyToolCallsPerIteration {
			continue
		}
		readOnlyKept++
		result = append(result, tc)
	}

	if readOnlyTotal > MaxReadOnlyToolCallsPerIteration {
		log.Printf("agent: limiting read-only tool calls from %d to %d",
			readOnlyTotal, MaxReadOnlyToolCallsPerIteration)
	}
	if heavyReadTotal > MaxHeavyReadToolCallsPerIteration {
		log.Printf("agent: limiting heavy read tool calls from %d to %d",
			heavyReadTotal, MaxHeavyReadToolCallsPerIteration)
	}
	if mutatingTotal > MaxMutatingToolCallsPerIteration {
		log.Printf("agent: limiting mutating tool calls from %d to %d",
			mutatingTotal, MaxMutatingToolCallsPerIteration)
	}

	return result
}

func isHeavyReadToolCall(tc llm.ToolCall) bool {
	if tc.Function.Name == "read_symbol" {
		return true
	}
	if tc.Function.Name != "read_file" {
		return false
	}
	startLine := intArg(tc.Function.Arguments, "start_line")
	endLine := intArg(tc.Function.Arguments, "end_line")
	if startLine <= 0 || endLine <= 0 {
		return true
	}
	return endLine-startLine+1 > 80
}

func (a *Agent) isMutatingToolCall(tc llm.ToolCall) bool {
	tool, exists := a.tools.Get(tc.Function.Name)
	if !exists {
		return true
	}
	return tool.IsMutating()
}

func (a *Agent) logEmptyResponse(resp *llm.ChatResponse) {
	if resp == nil {
		log.Printf("agent: empty response: response=nil")
		return
	}
	log.Printf(
		"agent: empty response: finish_reason=%q completion_tokens=%d content_chars=%d tool_calls=%d had_stream_partial=%t",
		resp.FinishReason,
		resp.CompletionTokens,
		len([]rune(resp.Message.Content)),
		len(resp.Message.ToolCalls),
		resp.HadPartial,
	)
}

func emptyResponseRecoveryPrompt() string {
	return "Your previous response was empty. Continue the task now.\n" +
		"Either call the next necessary tool, provide the final answer from the context already gathered, or explain the blocker.\n" +
		"Do not return an empty response.\n" +
		"Do not make edits unless the user explicitly requested implementation.\n" +
		"If you are waiting for user confirmation, say so explicitly and end with a question mark.\n" +
		"If no confirmation is needed, do not stop after a declaration of intent; continue with the next tool call or final answer.\n" +
		"If this is an investigation or debug-context request and enough context has been gathered, answer with the likely cause and next verification step."
}

type responseMetadata struct {
	FinishReason       string `json:"finish_reason,omitempty"`
	CompletionTokens   int    `json:"completion_tokens"`
	ContentBytes       int    `json:"content_bytes"`
	ContentChars       int    `json:"content_chars"`
	ToolCallCount      int    `json:"tool_call_count"`
	HadStreamPartial   bool   `json:"had_stream_partial"`
	EmptyResponse      bool   `json:"empty_response"`
	TotalDuration      int64  `json:"total_duration,omitempty"`
	LoadDuration       int64  `json:"load_duration,omitempty"`
	PromptEvalDuration int64  `json:"prompt_eval_duration,omitempty"`
	EvalDuration       int64  `json:"eval_duration,omitempty"`
}

// escalate はウォッチドッグ停止時に最終要約を取得してレスポンスを返す
func (a *Agent) escalate(ctx context.Context, messages []llm.Message, response *Response, signal *StopSignal) (*Response, error) {
	log.Printf("agent: escalating to user: %s - %s", signal.Reason, signal.Detail)

	// 停止理由に応じたプロンプト
	prompt := fmt.Sprintf(
		"I need to stop here. Reason: %s (%s). "+
			"Based on the information gathered so far, briefly explain progress, likely blocker or cause, and what remains to be done. "+
			"Do not call any more tools.",
		signal.Reason, signal.Detail,
	)

	escalateMessages := make([]llm.Message, len(messages))
	copy(escalateMessages, messages)
	escalateMessages = append(escalateMessages, llm.Message{
		Role:    "user",
		Content: prompt,
	})
	requestMessages := prepareMessagesForLLMRequest(escalateMessages)

	localEstimate := a.estimateTokenCount(requestMessages)
	a.emitProgress(ProgressEvent{
		Type:         EventTokenUpdate,
		PromptTokens: localEstimate,
		Iteration:    -1,
	})

	finalResp, err := a.llmClient.Chat(ctx, llm.ChatRequest{
		Messages: requestMessages,
		Tools:    nil,
		Stream:   true,
		StreamFunc: func(partial string) {
			a.emitProgress(ProgressEvent{
				Type:           EventPartialResponse,
				PartialContent: partial,
			})
		},
	})
	if err != nil {
		response.WatchdogStop = signal
		return response, fmt.Errorf("escalation summary failed: %w", err)
	}

	// escalate の LLM呼び出しも記録（iteration は -1 で特殊マーク）
	a.recordExchange(ExchangeIterationEscalate, requestMessages, nil, nil, finalResp)

	promptTokens := finalResp.PromptTokens
	if promptTokens == 0 {
		promptTokens = localEstimate
	}

	// 進捗通知（escalate も最新のトークン数を反映）
	a.emitProgress(ProgressEvent{
		Type:             EventTokenUpdate,
		PromptTokens:     promptTokens,
		CompletionTokens: finalResp.CompletionTokens,
		Iteration:        -1,
	})

	content := strings.TrimSpace(finalResp.Message.Content)
	if content == "" {
		content = fmt.Sprintf("Stopped by watchdog: %s (%s)", signal.Reason, signal.Detail)
	}
	finalMessage := llm.Message{
		Role:    "assistant",
		Content: content,
	}
	response.Structured = &StructuredResponse{
		Summary:         summarizeForMetadata(content, 100),
		Confidence:      ConfidenceMedium,
		RequestedAction: inferActionFromText(content),
	}
	response.FinalContent = content
	response.Messages = append(messages, finalMessage)
	response.WatchdogStop = signal
	return response, nil
}

// recordExchange はLLM呼び出しのリクエスト/レスポンスを記録する
func (a *Agent) recordExchange(iteration int, messages []llm.Message, tools []llm.ToolDefinition, format interface{}, resp *llm.ChatResponse) {
	if a.repo == nil || a.currentTurnID == 0 {
		return // 記録先が設定されていない場合はスキップ
	}

	// messages を JSON にシリアライズ
	messagesJSON, err := json.Marshal(messages)
	if err != nil {
		log.Printf("agent: failed to marshal messages for exchange record: %v", err)
		return
	}

	// tools を JSON にシリアライズ
	var toolsJSON string
	if len(tools) > 0 {
		b, err := json.Marshal(tools)
		if err != nil {
			log.Printf("agent: failed to marshal tools for exchange record: %v", err)
		} else {
			toolsJSON = string(b)
		}
	}

	// format を JSON にシリアライズ
	var formatJSON string
	if format != nil {
		b, err := json.Marshal(format)
		if err != nil {
			log.Printf("agent: failed to marshal format for exchange record: %v", err)
		} else {
			formatJSON = string(b)
		}
	}

	// response の tool_calls を JSON にシリアライズ
	var toolCallsJSON string
	if len(resp.Message.ToolCalls) > 0 {
		b, err := json.Marshal(resp.Message.ToolCalls)
		if err != nil {
			log.Printf("agent: failed to marshal response tool_calls: %v", err)
		} else {
			toolCallsJSON = string(b)
		}
	}

	meta := responseMetadata{
		FinishReason:       resp.FinishReason,
		CompletionTokens:   resp.CompletionTokens,
		ContentBytes:       len(resp.Message.Content),
		ContentChars:       len([]rune(resp.Message.Content)),
		ToolCallCount:      len(resp.Message.ToolCalls),
		HadStreamPartial:   resp.HadPartial,
		EmptyResponse:      strings.TrimSpace(resp.Message.Content) == "" && len(resp.Message.ToolCalls) == 0,
		TotalDuration:      resp.TotalDuration,
		LoadDuration:       resp.LoadDuration,
		PromptEvalDuration: resp.PromptEvalDuration,
		EvalDuration:       resp.EvalDuration,
	}
	metadataJSON := ""
	if b, err := json.Marshal(meta); err != nil {
		log.Printf("agent: failed to marshal response metadata: %v", err)
	} else {
		metadataJSON = string(b)
	}

	record := repository.LLMExchangeRecord{
		TurnID:            a.currentTurnID,
		Iteration:         iteration,
		RequestMessages:   string(messagesJSON),
		RequestTools:      toolsJSON,
		RequestFormat:     formatJSON,
		ResponseContent:   resp.Message.Content,
		ResponseToolCalls: toolCallsJSON,
		ResponseMetadata:  metadataJSON,
		PromptTokens:      resp.PromptTokens,
		CompletionTokens:  resp.CompletionTokens,
	}

	if _, err := a.repo.LLMExchanges.Create(record); err != nil {
		log.Printf("agent: failed to record llm_exchange: %v", err)
		// 記録失敗はエージェント動作に影響させない
	}
}

type toolResultMessageInfo struct {
	MessageIndex int
	ToolName     string
	ToolCallID   string
	Arguments    map[string]interface{}
	Ordinal      int
}

func prepareMessagesForLLMRequest(messages []llm.Message) []llm.Message {
	prepared := compactToolResultMessages(messages)
	prepared = compactToolCallArguments(prepared)
	prepared = compactEmptyResponseRecoveryPrompts(prepared)
	prepared = dropEmptyAssistantMessages(prepared)
	prepared = mergeSystemMessagesForLLMRequest(prepared)
	return prepared
}

func cloneMessageSlice(messages []llm.Message) []llm.Message {
	out := make([]llm.Message, len(messages))
	copy(out, messages)
	return out
}

func mergeSystemMessagesForLLMRequest(messages []llm.Message) []llm.Message {
	if len(messages) == 0 {
		return messages
	}

	var systemParts []string
	nonSystem := make([]llm.Message, 0, len(messages))
	systemCount := 0
	for _, msg := range messages {
		if msg.Role != "system" {
			nonSystem = append(nonSystem, msg)
			continue
		}
		systemCount++
		if content := strings.TrimSpace(msg.Content); content != "" {
			systemParts = append(systemParts, content)
		}
	}
	if systemCount == 0 {
		return messages
	}
	if systemCount == 1 && messages[0].Role == "system" {
		return messages
	}

	out := make([]llm.Message, 0, len(nonSystem)+1)
	if len(systemParts) > 0 {
		out = append(out, llm.Message{
			Role:    "system",
			Content: strings.Join(systemParts, "\n\n"),
		})
	}
	out = append(out, nonSystem...)
	return out
}

func dropEmptyAssistantMessages(messages []llm.Message) []llm.Message {
	out := make([]llm.Message, 0, len(messages))
	dropped := false
	for _, msg := range messages {
		if msg.Role == "assistant" && strings.TrimSpace(msg.Content) == "" && len(msg.ToolCalls) == 0 {
			dropped = true
			continue
		}
		out = append(out, msg)
	}
	if !dropped {
		return messages
	}
	return out
}

func compactEmptyResponseRecoveryPrompts(messages []llm.Message) []llm.Message {
	lastRecovery := -1
	count := 0
	for i, msg := range messages {
		if isEmptyResponseRecoveryMessage(msg) {
			lastRecovery = i
			count++
		}
	}
	if count <= 1 {
		return messages
	}

	out := make([]llm.Message, 0, len(messages)-count+1)
	for i, msg := range messages {
		if isEmptyResponseRecoveryMessage(msg) && i != lastRecovery {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func isEmptyResponseRecoveryMessage(msg llm.Message) bool {
	if msg.Role != "user" {
		return false
	}
	content := strings.TrimSpace(msg.Content)
	return strings.HasPrefix(content, "Your previous response was empty. Continue the task now.") &&
		strings.Contains(content, "Do not return an empty response.")
}

func compactToolCallArguments(messages []llm.Message) []llm.Message {
	out := make([]llm.Message, len(messages))
	copy(out, messages)
	changed := false

	for i, msg := range out {
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		toolCalls := make([]llm.ToolCall, len(msg.ToolCalls))
		copy(toolCalls, msg.ToolCalls)
		messageChanged := false
		for j, tc := range toolCalls {
			compactedArgs, compacted := compactArgumentsForToolCall(tc.Function.Name, tc.Function.Arguments)
			if !compacted {
				continue
			}
			tc.Function.Arguments = compactedArgs
			toolCalls[j] = tc
			messageChanged = true
		}
		if messageChanged {
			msg.ToolCalls = toolCalls
			out[i] = msg
			changed = true
		}
	}

	if !changed {
		return messages
	}
	return out
}

func compactArgumentsForToolCall(toolName string, args map[string]interface{}) (map[string]interface{}, bool) {
	if len(args) == 0 {
		return args, false
	}
	fields := compactibleArgumentFields(toolName)
	if len(fields) == 0 {
		return args, false
	}

	out := cloneMap(args)
	changed := false
	for _, field := range fields {
		value, ok := out[field]
		if !ok {
			continue
		}
		if toolName == "write_file" && field == "content" && argumentValueLength(value) > toolArgumentCompactionChars {
			out[field] = writeFileContentReference(out, "large write_file content was omitted before LLM resend")
			changed = true
			continue
		}
		compacted, didCompact := compactArgumentValue(toolName, field, value)
		if !didCompact {
			continue
		}
		out[field] = compacted
		changed = true
	}
	if !changed {
		return args, false
	}
	return out, true
}

func compactibleArgumentFields(toolName string) []string {
	switch toolName {
	case "write_file":
		return []string{"content"}
	case "edit_file":
		return []string{"new_lines"}
	case "edit_with_pattern":
		return []string{"find_text", "replace_with"}
	default:
		return nil
	}
}

func compactArgumentValue(toolName, field string, value interface{}) (interface{}, bool) {
	switch v := value.(type) {
	case string:
		if len(v) <= toolArgumentCompactionChars {
			return value, false
		}
		return compactArgumentString(toolName, field, v), true
	case []string:
		joined := strings.Join(v, "\n")
		if len(joined) <= toolArgumentCompactionChars {
			return value, false
		}
		return compactArgumentString(toolName, field, joined), true
	case []interface{}:
		parts := make([]string, 0, len(v))
		allStrings := true
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				allStrings = false
				break
			}
			parts = append(parts, s)
		}
		if !allStrings {
			return value, false
		}
		joined := strings.Join(parts, "\n")
		if len(joined) <= toolArgumentCompactionChars {
			return value, false
		}
		return compactArgumentString(toolName, field, joined), true
	default:
		return value, false
	}
}

func compactArgumentString(toolName, field, value string) string {
	if toolName == "write_file" && field == "content" {
		return writeFileContentReference(map[string]interface{}{
			"content": value,
		}, "large write_file content was omitted before LLM resend")
	}
	return fmt.Sprintf(
		tools.OmittedToolArgumentMarker+"\nThis is an internal context-compaction placeholder, not executable file content. Do not copy it into future tool calls or infer current file state from this placeholder. Use a structural read of the current target before creating a new %s argument.\nTool: %s\nField: %s\nOriginal chars: %d\nOriginal estimated tokens: %d",
		field,
		toolName,
		field,
		len(value),
		tokenizer.EstimateTokens(value),
	)
}

func compactToolResultMessages(messages []llm.Message) []llm.Message {
	infos := collectToolResultMessageInfo(messages)
	if len(infos) == 0 {
		return messages
	}

	totalsByTool := map[string]int{}
	for _, info := range infos {
		totalsByTool[info.ToolName]++
	}

	out := make([]llm.Message, len(messages))
	copy(out, messages)
	compacted := 0
	for _, info := range infos {
		msg := out[info.MessageIndex]
		policy := toolResultCompactionPolicyFor(info.ToolName)
		forceCompact := shouldForceCompactToolResult(msg.Content)
		if !forceCompact && info.Ordinal > totalsByTool[info.ToolName]-policy.KeepRecent {
			continue
		}
		if !shouldCompactToolResult(msg.Content, policy) {
			continue
		}
		out[info.MessageIndex].Content = compactToolResultContent(info.ToolName, info.ToolCallID, info.Arguments, msg.Content)
		compacted++
	}

	if compacted == 0 {
		return compactToolResultsToTotalBudget(messages, infos)
	}
	return compactToolResultsToTotalBudget(out, infos)
}

func compactToolResultsToTotalBudget(messages []llm.Message, infos []toolResultMessageInfo) []llm.Message {
	totalTokens := 0
	rawIndexes := make([]int, 0, len(infos))
	for _, info := range infos {
		if info.MessageIndex < 0 || info.MessageIndex >= len(messages) {
			continue
		}
		content := messages[info.MessageIndex].Content
		if strings.HasPrefix(content, "[tool result omitted to save context]") {
			continue
		}
		totalTokens += tokenizer.EstimateTokens(content)
		rawIndexes = append(rawIndexes, info.MessageIndex)
	}
	if totalTokens <= toolResultTotalBudgetTokens || len(rawIndexes) <= toolResultMinRawRecent {
		return messages
	}

	out := make([]llm.Message, len(messages))
	copy(out, messages)
	rawRemaining := len(rawIndexes)
	changed := false
	for _, info := range infos {
		if totalTokens <= toolResultTotalBudgetTokens || rawRemaining <= toolResultMinRawRecent {
			break
		}
		if info.MessageIndex < 0 || info.MessageIndex >= len(out) {
			continue
		}
		content := out[info.MessageIndex].Content
		if strings.HasPrefix(content, "[tool result omitted to save context]") {
			continue
		}
		beforeTokens := tokenizer.EstimateTokens(content)
		out[info.MessageIndex].Content = compactToolResultContent(info.ToolName, info.ToolCallID, info.Arguments, content)
		afterTokens := tokenizer.EstimateTokens(out[info.MessageIndex].Content)
		totalTokens -= beforeTokens - afterTokens
		rawRemaining--
		changed = true
	}
	if !changed {
		return messages
	}
	return out
}

func collectToolResultMessageInfo(messages []llm.Message) []toolResultMessageInfo {
	toolCallByID := map[string]llm.ToolCall{}
	pendingToolCalls := []llm.ToolCall{}
	ordinalsByTool := map[string]int{}
	infos := []toolResultMessageInfo{}

	for i, msg := range messages {
		switch msg.Role {
		case "assistant":
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					toolCallByID[tc.ID] = tc
				}
				pendingToolCalls = append(pendingToolCalls, tc)
			}
		case "tool":
			tc, ok := toolCallByID[msg.ToolCallID]
			if !ok && len(pendingToolCalls) > 0 {
				tc = pendingToolCalls[0]
				pendingToolCalls = pendingToolCalls[1:]
			}
			toolName := tc.Function.Name
			if toolName == "" {
				toolName = "(unknown)"
			}
			ordinalsByTool[toolName]++
			infos = append(infos, toolResultMessageInfo{
				MessageIndex: i,
				ToolName:     toolName,
				ToolCallID:   msg.ToolCallID,
				Arguments:    tc.Function.Arguments,
				Ordinal:      ordinalsByTool[toolName],
			})
		}
	}
	return infos
}

func toolResultCompactionPolicyFor(toolName string) toolResultCompactionPolicy {
	switch toolName {
	case "search_text":
		return toolResultCompactionPolicy{KeepRecent: 8, MinChars: 8000, MinTokens: 2500}
	case "get_file_outline", "get_symbol_outline", "list_files":
		return toolResultCompactionPolicy{KeepRecent: 6, MinChars: 8000, MinTokens: 2500}
	case "read_file", "read_symbol":
		return toolResultCompactionPolicy{KeepRecent: 4, MinChars: 4000, MinTokens: 1200}
	default:
		return toolResultCompactionPolicy{KeepRecent: 4, MinChars: 4000, MinTokens: 1200}
	}
}

func shouldCompactToolResult(content string, policy toolResultCompactionPolicy) bool {
	if strings.HasPrefix(content, "[tool result omitted to save context]") {
		return false
	}
	if len(content) <= policy.MinChars && tokenizer.EstimateTokens(content) <= policy.MinTokens {
		return false
	}
	return true
}

func shouldForceCompactToolResult(content string) bool {
	if strings.HasPrefix(content, "[tool result omitted to save context]") {
		return false
	}
	return len(content) > toolResultHardMaxChars || tokenizer.EstimateTokens(content) > toolResultHardMaxTokens
}

func compactToolResultContent(toolName, toolCallID string, args map[string]interface{}, content string) string {
	if toolName == "" {
		toolName = "(unknown)"
	}
	switch toolName {
	case "search_text":
		return compactSearchTextResult(toolName, toolCallID, args, content)
	case "read_file":
		return compactReadFileResult(toolName, toolCallID, args, content)
	case "read_symbol":
		return compactReadSymbolResult(toolName, toolCallID, args, content)
	case "get_file_outline", "get_symbol_outline":
		return compactFileOutlineResult(toolName, toolCallID, args, content)
	default:
		return compactGenericToolResult(toolName, toolCallID, content)
	}
}

func compactGenericToolResult(toolName, toolCallID, content string) string {
	preview := strings.TrimSpace(content)
	preview = strings.ReplaceAll(preview, "\r\n", "\n")
	preview = strings.Join(strings.Fields(preview), " ")
	previewRunes := []rune(preview)
	if len(previewRunes) > toolResultCompactionPreview {
		preview = string(previewRunes[:toolResultCompactionPreview]) + "..."
	}
	return fmt.Sprintf(
		"[tool result omitted to save context]\nTool: %s\nTool call ID: %s\nOriginal chars: %d\nOriginal estimated tokens: %d\nPreview: %s\nUse a focused read/range/outline tool if the omitted details are needed again.",
		toolName,
		toolCallID,
		len(content),
		tokenizer.EstimateTokens(content),
		preview,
	)
}

func compactSearchTextResult(toolName, toolCallID string, args map[string]interface{}, content string) string {
	pattern := stringArg(args, "pattern")
	matches := extractSearchTextMatchLines(content)
	representative := representativeSearchMatches(matches)
	var sb strings.Builder
	writeToolResultOmitHeader(&sb, toolName, toolCallID, content)
	if pattern != "" {
		sb.WriteString(fmt.Sprintf("Pattern: %s\n", pattern))
	}
	if len(matches) > 0 {
		sb.WriteString(fmt.Sprintf("Estimated match lines: %d\n", len(matches)))
		sb.WriteString("Representative match lines:\n")
		for _, line := range representative {
			sb.WriteString("- ")
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString("Preview: ")
		sb.WriteString(compactTextPreview(content))
		sb.WriteString("\n")
	}
	sb.WriteString("Use search_text with a narrower path/pattern if full results are needed again.")
	return sb.String()
}

func fallbackHistorySummary(messages []llm.Message) string {
	var sb strings.Builder
	sb.WriteString("LLM summarization returned empty output. Deterministic fallback summary:\n")
	limit := 20
	start := 0
	if len(messages) > limit {
		start = len(messages) - limit
		sb.WriteString(fmt.Sprintf("- Preserved the most recent %d of %d older messages.\n", limit, len(messages)))
	} else {
		sb.WriteString(fmt.Sprintf("- Preserved %d older messages.\n", len(messages)))
	}
	for i, msg := range messages[start:] {
		originalIndex := start + i + 1
		item := summarizeMessageForFallback(msg)
		if item == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("- Message %d (%s): %s\n", originalIndex, msg.Role, item))
	}
	return strings.TrimSpace(sb.String())
}

func summarizeMessageForFallback(msg llm.Message) string {
	if len(msg.ToolCalls) > 0 {
		names := make([]string, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			names = append(names, tc.Function.Name)
		}
		return fmt.Sprintf("tool calls: %s", strings.Join(names, ", "))
	}
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return ""
	}
	content = strings.Join(strings.Fields(content), " ")
	runes := []rune(content)
	if len(runes) > 220 {
		content = string(runes[:220]) + "..."
	}
	return content
}

func compactReadFileResult(toolName, toolCallID string, args map[string]interface{}, content string) string {
	path := stringArg(args, "path")
	startLine := intArg(args, "start_line")
	endLine := intArg(args, "end_line")
	if (startLine == 0 || endLine == 0) && (path == "" || startLine == 0 || endLine == 0) {
		fallbackPath, fallbackStart, fallbackEnd := parseReadFileHeader(content)
		if path == "" {
			path = fallbackPath
		}
		if startLine == 0 {
			startLine = fallbackStart
		}
		if endLine == 0 {
			endLine = fallbackEnd
		}
	}

	var sb strings.Builder
	writeToolResultOmitHeader(&sb, toolName, toolCallID, content)
	if path != "" {
		sb.WriteString(fmt.Sprintf("Path: %s\n", path))
	}
	if startLine > 0 || endLine > 0 {
		sb.WriteString(fmt.Sprintf("Lines: %d-%d\n", startLine, endLine))
	}
	observation := compressedCodeObservation(content)
	if len(observation.Symbols) > 0 || len(observation.Calls) > 0 || len(observation.Branches) > 0 {
		sb.WriteString("Compressed code observation:\n")
		if len(observation.Symbols) > 0 {
			sb.WriteString("- Symbols found:\n")
			for _, line := range observation.Symbols {
				sb.WriteString("  - ")
				sb.WriteString(line)
				sb.WriteString("\n")
			}
		}
		if len(observation.Calls) > 0 {
			sb.WriteString("- Important calls:\n")
			for _, line := range observation.Calls {
				sb.WriteString("  - ")
				sb.WriteString(line)
				sb.WriteString("\n")
			}
		}
		if len(observation.Branches) > 0 {
			sb.WriteString("- Important branches/side effects:\n")
			for _, line := range observation.Branches {
				sb.WriteString("  - ")
				sb.WriteString(line)
				sb.WriteString("\n")
			}
		}
	} else {
		sb.WriteString("Compressed code observation: no obvious symbols/calls detected in omitted content.\n")
	}
	sb.WriteString("Preview: ")
	sb.WriteString(compactTextPreview(content))
	sb.WriteString("\n")
	if path != "" && startLine > 0 && endLine > 0 {
		sb.WriteString(fmt.Sprintf("Suggested next reads: prefer a narrower range than %d-%d if omitted details are needed again.", startLine, endLine))
	} else if path != "" {
		sb.WriteString(fmt.Sprintf("Suggested next reads: use read_file(path=%q, start_line=START_LINE, end_line=END_LINE) only for a justified narrow range.", path))
	} else {
		sb.WriteString("Suggested next reads: use a narrower read_file range if omitted details are needed again.")
	}
	return sb.String()
}

func compactReadSymbolResult(toolName, toolCallID string, args map[string]interface{}, content string) string {
	path := stringArg(args, "path")
	symbolName := stringArg(args, "symbol_name")
	receiver := stringArg(args, "receiver")

	var sb strings.Builder
	writeToolResultOmitHeader(&sb, toolName, toolCallID, content)
	if path != "" {
		sb.WriteString(fmt.Sprintf("Path: %s\n", path))
	}
	if symbolName != "" {
		sb.WriteString(fmt.Sprintf("Symbol: %s\n", symbolName))
	} else if line := extractLineWithPrefix(content, "Symbol:"); line != "" {
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	if receiver != "" {
		sb.WriteString(fmt.Sprintf("Receiver: %s\n", receiver))
	}
	for _, prefix := range []string{"Type:", "Mode:", "Language:"} {
		if line := extractLineWithPrefix(content, prefix); line != "" {
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}

	observation := compressedCodeObservation(content)
	if len(observation.Symbols) > 0 || len(observation.Calls) > 0 || len(observation.Branches) > 0 {
		sb.WriteString("Compressed symbol observation:\n")
		if len(observation.Symbols) > 0 {
			sb.WriteString("- Nested symbols/signatures:\n")
			for _, line := range observation.Symbols {
				sb.WriteString("  - ")
				sb.WriteString(line)
				sb.WriteString("\n")
			}
		}
		if len(observation.Calls) > 0 {
			sb.WriteString("- Important calls:\n")
			for _, line := range observation.Calls {
				sb.WriteString("  - ")
				sb.WriteString(line)
				sb.WriteString("\n")
			}
		}
		if len(observation.Branches) > 0 {
			sb.WriteString("- Important branches/side effects:\n")
			for _, line := range observation.Branches {
				sb.WriteString("  - ")
				sb.WriteString(line)
				sb.WriteString("\n")
			}
		}
	} else {
		sb.WriteString("Compressed symbol observation: no obvious nested symbols/calls detected in omitted content.\n")
	}
	sb.WriteString("Preview: ")
	sb.WriteString(compactTextPreview(content))
	sb.WriteString("\n")
	if path != "" && symbolName != "" {
		sb.WriteString(fmt.Sprintf("Suggested next reads: use read_symbol(path=%q, symbol_name=%q) for summary, get_symbol_outline for children, or a justified narrow read_file range.", path, symbolName))
	} else {
		sb.WriteString("Suggested next reads: use read_symbol summary, get_symbol_outline for children, or a justified narrow read_file range.")
	}
	return sb.String()
}

func compactFileOutlineResult(toolName, toolCallID string, args map[string]interface{}, content string) string {
	path := stringArg(args, "path")
	var sb strings.Builder
	writeToolResultOmitHeader(&sb, toolName, toolCallID, content)
	if path != "" {
		sb.WriteString(fmt.Sprintf("Path: %s\n", path))
	}
	if filters := formatOutlineArgumentFilters(args); filters != "" {
		sb.WriteString(fmt.Sprintf("Filters: %s\n", filters))
	} else if headerFilters := extractLineWithPrefix(content, "Filters:"); headerFilters != "" {
		sb.WriteString(headerFilters)
		sb.WriteString("\n")
	}
	if langLine := extractLineWithPrefix(content, "Language:"); langLine != "" {
		sb.WriteString(langLine)
		sb.WriteString("\n")
	}
	sb.WriteString("Compressed outline observation: ")
	if symbolLine := extractLineWithPrefix(content, "Language:"); symbolLine != "" {
		sb.WriteString(symbolLine)
	} else {
		sb.WriteString("large outline omitted")
	}
	sb.WriteString("\nPreview: ")
	sb.WriteString(compactTextPreview(content))
	sb.WriteString("\n")
	sb.WriteString("Suggested next reads: use get_file_outline with narrower receiver/name_filter/type or get_symbol_outline, then read_symbol for specific symbols.")
	return sb.String()
}

type codeObservation struct {
	Symbols  []string
	Calls    []string
	Branches []string
}

func compressedCodeObservation(content string) codeObservation {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	obs := codeObservation{
		Symbols:  make([]string, 0, 8),
		Calls:    make([]string, 0, 8),
		Branches: make([]string, 0, 8),
	}
	symbolRe := regexp.MustCompile(`^\s*\d+\s*\|\s*((func\s+|type\s+|class\s+|def\s+|async\s+def\s+|interface\s+|struct\s+).*)$`)
	callRe := regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_\.]*)\s*\(`)
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "File:") || strings.HasPrefix(line, "----") {
			continue
		}
		if len(obs.Symbols) < 8 {
			if m := symbolRe.FindStringSubmatch(raw); len(m) > 1 {
				obs.Symbols = append(obs.Symbols, compactObservationLine(raw))
				continue
			}
		}
		if len(obs.Branches) < 8 && looksLikeBranchOrSideEffect(raw) {
			obs.Branches = append(obs.Branches, compactObservationLine(raw))
		}
		if len(obs.Calls) < 8 {
			for _, m := range callRe.FindAllStringSubmatch(raw, -1) {
				if len(m) < 2 || ignoredCallName(m[1]) {
					continue
				}
				obs.Calls = append(obs.Calls, compactObservationLine(raw))
				break
			}
		}
		if len(obs.Symbols) >= 8 && len(obs.Calls) >= 8 && len(obs.Branches) >= 8 {
			break
		}
	}
	return obs
}

func compactObservationLine(line string) string {
	line = strings.TrimSpace(line)
	line = strings.Join(strings.Fields(line), " ")
	runes := []rune(line)
	if len(runes) > 180 {
		return string(runes[:180]) + "..."
	}
	return line
}

func looksLikeBranchOrSideEffect(line string) bool {
	text := strings.TrimSpace(line)
	keywords := []string{
		" if ", " if(", " for ", " for(", " while ", " while(", " switch ", " case ",
		" return ", " raise ", " throw ", " except ", " catch ",
		" json.load", "json.loads", "open(", ".write(", ".save(", "os.", "subprocess.",
	}
	padded := " " + text + " "
	for _, keyword := range keywords {
		if strings.Contains(padded, keyword) {
			return true
		}
	}
	return false
}

func ignoredCallName(name string) bool {
	switch name {
	case "if", "for", "while", "switch", "return", "func", "def", "class", "len", "range", "str", "int", "float", "print":
		return true
	default:
		return false
	}
}

func writeToolResultOmitHeader(sb *strings.Builder, toolName, toolCallID, content string) {
	sb.WriteString("[tool result omitted to save context]\n")
	sb.WriteString(fmt.Sprintf("Tool: %s\n", toolName))
	sb.WriteString(fmt.Sprintf("Tool call ID: %s\n", toolCallID))
	sb.WriteString(fmt.Sprintf("Original chars: %d\n", len(content)))
	sb.WriteString(fmt.Sprintf("Original estimated tokens: %d\n", tokenizer.EstimateTokens(content)))
}

func compactTextPreview(content string) string {
	preview := strings.TrimSpace(content)
	preview = strings.ReplaceAll(preview, "\r\n", "\n")
	preview = strings.Join(strings.Fields(preview), " ")
	previewRunes := []rune(preview)
	if len(previewRunes) > toolResultCompactionPreview {
		return string(previewRunes[:toolResultCompactionPreview]) + "..."
	}
	return preview
}

func extractSearchTextMatchLines(content string) []string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	matches := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Found matches for pattern") {
			continue
		}
		if strings.HasPrefix(line, "No matches found") {
			return nil
		}
		matches = append(matches, line)
	}
	return matches
}

func representativeSearchMatches(matches []string) []string {
	if len(matches) <= 10 {
		return matches
	}
	byPath := make([]string, 0, 3)
	seenPath := map[string]bool{}
	for _, line := range matches {
		path := searchMatchPath(line)
		if path == "" || seenPath[path] {
			continue
		}
		seenPath[path] = true
		byPath = append(byPath, line)
		if len(byPath) == 3 {
			return byPath
		}
	}
	return []string{matches[0], matches[len(matches)/2], matches[len(matches)-1]}
}

func searchMatchPath(line string) string {
	if idx := strings.Index(line, ":"); idx > 0 {
		return strings.TrimSpace(line[:idx])
	}
	return ""
}

var readFileRangeHeaderPattern = regexp.MustCompile(`^File:\s+(.+?)\s+\(lines\s+([0-9]+)-([0-9]+)\)`)

func parseReadFileHeader(content string) (string, int, int) {
	firstLine := content
	if idx := strings.Index(firstLine, "\n"); idx >= 0 {
		firstLine = firstLine[:idx]
	}
	match := readFileRangeHeaderPattern.FindStringSubmatch(strings.TrimSpace(firstLine))
	if match == nil {
		return "", 0, 0
	}
	startLine, _ := strconv.Atoi(match[2])
	endLine, _ := strconv.Atoi(match[3])
	return match[1], startLine, endLine
}

func formatOutlineArgumentFilters(args map[string]interface{}) string {
	parts := make([]string, 0, 5)
	for _, key := range []string{"name_filter", "type", "receiver"} {
		if value := stringArg(args, key); value != "" {
			parts = append(parts, fmt.Sprintf("%s=%q", key, value))
		}
	}
	if boolArg(args, "fallback_only") {
		parts = append(parts, "fallback_only=true")
	}
	if includeMethods, ok := args["include_methods"].(bool); ok && !includeMethods {
		parts = append(parts, "include_methods=false")
	}
	return strings.Join(parts, ", ")
}

func extractLineWithPrefix(content, prefix string) string {
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func stringArg(args map[string]interface{}, key string) string {
	if args == nil {
		return ""
	}
	value, ok := args[key]
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

func intArg(args map[string]interface{}, key string) int {
	if args == nil {
		return 0
	}
	switch v := args[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	default:
		return 0
	}
}

func boolArg(args map[string]interface{}, key string) bool {
	if args == nil {
		return false
	}
	value, _ := args[key].(bool)
	return value
}

func (a *Agent) estimateTokenCount(messages []llm.Message) int {
	total := 0
	for _, m := range messages {
		total += tokenizer.EstimateTokens(m.Content)
	}
	return total
}

// estimateToolTokens は現在のツール定義の推定トークン数を返す
func (a *Agent) estimateToolTokens() int {
	return a.estimateToolTokensRaw()
}

func (a *Agent) estimateToolTokensRaw() int {
	defs := a.toolDefinitions()
	if len(defs) == 0 {
		return 0
	}
	data, _ := json.Marshal(defs)
	return tokenizer.EstimateTokens(string(data))
}

// estimateTokenCountWithTools はメッセージとツール定義を合わせた推定トークン数を返す
func (a *Agent) estimateTokenCountWithTools(messages []llm.Message) int {
	return a.applyTokenCalibration(a.estimateTokenCountWithToolsRaw(messages))
}

// EstimateContextTokens returns the calibrated prompt token estimate for a
// future agent call using the current tool profile.
func (a *Agent) EstimateContextTokens(messages []llm.Message) int {
	return a.estimateTokenCountWithTools(messages)
}

func (a *Agent) estimateTokenCountWithToolsRaw(messages []llm.Message) int {
	return a.estimateTokenCount(messages) + a.estimateToolTokensRaw()
}

func (a *Agent) applyTokenCalibration(heuristicEstimate int) int {
	if heuristicEstimate <= 0 {
		return 0
	}
	calibration := a.tokenCalibration
	if calibration <= 0 {
		calibration = 1.0
	}
	estimate := int(math.Ceil(float64(heuristicEstimate)*calibration - 1e-9))
	if estimate < 1 {
		return 1
	}
	return estimate
}

func (a *Agent) updateTokenCalibration(heuristicEstimate int, actualTokens int) {
	if heuristicEstimate <= 0 || actualTokens <= 0 {
		return
	}
	observation := float64(actualTokens) / float64(heuristicEstimate)
	if observation <= 0 {
		return
	}
	if a.tokenCalibration <= 0 {
		a.tokenCalibration = 1.0
	}
	a.tokenCalibration = 0.8*a.tokenCalibration + 0.2*observation
}

func isExploratoryTool(name string) bool {
	switch name {
	case "read_file", "read_symbol", "find_symbol", "get_file_outline", "get_symbol_outline", "search_text", "list_files", "get_callers", "get_call_graph", "get_file_imports", "find_dependents", "get_diff_summary", "get_markdown_outline", "read_markdown_section":
		return true
	default:
		return false
	}
}

func isRepeatedExplorationGuardTool(name string) bool {
	switch name {
	case "search_text", "read_symbol", "find_symbol", "get_file_outline", "get_symbol_outline", "get_markdown_outline", "read_markdown_section", "list_files":
		return true
	default:
		return false
	}
}

func shouldBlockRepeatedSuccessfulExploration(toolName string, previousSuccesses int) bool {
	return previousSuccesses >= 2 && isRepeatedExplorationGuardTool(toolName)
}

func shouldBlockRepeatedFailedExploration(toolName string, previousFailures int) bool {
	return previousFailures >= 1 && isRepeatedExplorationGuardTool(toolName)
}

func skipIdenticalToolCallWatchdog(toolName string) bool {
	return toolName == "read_file"
}

func repeatedSuccessfulExplorationBlockMessage(toolName string, argsJSON []byte, previousSuccesses int) string {
	return fmt.Sprintf(
		"Tool %q blocked: the same exploratory call already succeeded %d times with identical arguments (%s). Do not repeat it. Use the previous result already in context, switch to a structural tool such as find_symbol/get_file_outline/read_symbol, or call %q again only with a narrower path or pattern.",
		toolName,
		previousSuccesses,
		string(argsJSON),
		toolName,
	)
}

func repeatedFailedExplorationBlockMessage(toolName string, argsJSON []byte, previousFailures int) string {
	return fmt.Sprintf(
		"Tool %q blocked: the same exploratory call already failed %d time(s) with identical arguments (%s). Do not repeat it. Treat the previous error as evidence that the target is unavailable or misspelled; switch to another lookup such as find_symbol, get_file_outline with a receiver/name_filter, search_text, or adjust the symbol/path.",
		toolName,
		previousFailures,
		string(argsJSON),
	)
}

// emitProgress は progress channel にイベントを送信する
// channel が未設定、またはバッファがフルの場合は何もしない（ノンブロッキング）
func (a *Agent) emitProgress(ev ProgressEvent) {
	if a.progressCh == nil {
		return
	}
	select {
	case a.progressCh <- ev:
		// 送信成功
	default:
		// バッファフル: イベント取りこぼし（致命的でない）
		// ログには出さない（高頻度発火の場合にログ汚染を避ける）
	}
}
