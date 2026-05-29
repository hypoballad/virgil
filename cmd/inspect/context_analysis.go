package main

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/hypoballad/virgil/internal/llm"
	"github.com/hypoballad/virgil/internal/repository"
	"github.com/hypoballad/virgil/internal/tokenizer"
)

type contextBreakdownItem struct {
	Label  string `json:"label"`
	Tokens int    `json:"tokens"`
	Bytes  int    `json:"bytes"`
	Count  int    `json:"count"`
}

type toolBreakdownItem struct {
	ToolName                 string `json:"tool_name"`
	Tokens                   int    `json:"tokens"`
	Bytes                    int    `json:"bytes"`
	Count                    int    `json:"count"`
	CompactedCount           int    `json:"compacted_count,omitempty"`
	CompactionOriginalTokens int    `json:"compaction_original_tokens,omitempty"`
	CompactionSavedTokens    int    `json:"compaction_saved_tokens,omitempty"`
}

type contextAnalysis struct {
	SessionID                string                 `json:"session_id"`
	ExchangeID               int64                  `json:"exchange_id"`
	TurnID                   int64                  `json:"turn_id"`
	Iteration                int                    `json:"iteration"`
	PromptTokens             int                    `json:"prompt_tokens"`
	CompletionTokens         int                    `json:"completion_tokens"`
	EstimatedTokens          int                    `json:"estimated_tokens"`
	ToolDefinitionTokens     int                    `json:"tool_definition_tokens"`
	ToolDefinitionBytes      int                    `json:"tool_definition_bytes"`
	MessageCount             int                    `json:"message_count"`
	ResponseContentBytes     int                    `json:"response_content_bytes"`
	ResponseContentChars     int                    `json:"response_content_chars"`
	ResponseToolCallCount    int                    `json:"response_tool_call_count"`
	ResponseMetadata         interface{}            `json:"response_metadata"`
	Breakdown                []contextBreakdownItem `json:"breakdown"`
	ToolResultBreakdown      []toolBreakdownItem    `json:"tool_result_breakdown"`
	ToolArgBreakdown         []toolBreakdownItem    `json:"tool_arg_breakdown"`
	RawContext               interface{}            `json:"raw_context"`
	RedactedContext          interface{}            `json:"redacted_context"`
	SanitizedContext         interface{}            `json:"sanitized_context"`
	RedactionCount           int                    `json:"redaction_count"`
	CompactedToolResults     int                    `json:"compacted_tool_results"`
	CompactionOriginalTokens int                    `json:"compaction_original_tokens"`
	CompactionSavedTokens    int                    `json:"compaction_saved_tokens"`
}

type contextAccumulator struct {
	tokens int
	bytes  int
	count  int
}

type toolCompactionAccumulator struct {
	count          int
	originalTokens int
	savedTokens    int
}

func analyzeExchangeContext(sessionID string, exchange *repository.LLMExchangeRecord) (*contextAnalysis, error) {
	var messages []llm.Message
	if err := json.Unmarshal([]byte(exchange.RequestMessages), &messages); err != nil {
		return nil, err
	}

	toolNameByID := map[string]string{}
	pendingToolNames := []string{}
	categories := map[string]*contextAccumulator{}
	toolResults := map[string]*contextAccumulator{}
	toolArgs := map[string]*contextAccumulator{}
	toolCompactions := map[string]*toolCompactionAccumulator{}
	compactedToolResults := 0
	compactionOriginalTokens := 0
	compactionSavedTokens := 0

	addCategory := func(label, content string) {
		if strings.TrimSpace(content) == "" {
			return
		}
		addAccumulator(categories, label, content)
	}
	addTool := func(items map[string]*contextAccumulator, name, content string) {
		if strings.TrimSpace(content) == "" {
			return
		}
		if name == "" {
			name = "(unknown)"
		}
		addAccumulator(items, name, content)
	}

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			addCategory("system messages", msg.Content)
		case "user":
			addCategory("user messages", msg.Content)
		case "assistant":
			addCategory("LLM responses in context", msg.Content)
			for _, tc := range msg.ToolCalls {
				name := tc.Function.Name
				if tc.ID != "" {
					toolNameByID[tc.ID] = name
				}
				pendingToolNames = append(pendingToolNames, name)
				argsJSON, _ := json.Marshal(tc.Function.Arguments)
				addCategory("tool call arguments", string(argsJSON))
				addTool(toolArgs, name, string(argsJSON))
			}
		case "tool":
			toolName := toolNameByID[msg.ToolCallID]
			if toolName == "" && len(pendingToolNames) > 0 {
				toolName = pendingToolNames[0]
				pendingToolNames = pendingToolNames[1:]
			}
			if originalTokens, ok := compactedToolOriginalTokens(msg.Content); ok {
				compactedToolResults++
				compactionOriginalTokens += originalTokens
				saved := originalTokens - tokenizer.EstimateTokens(msg.Content)
				if saved > 0 {
					compactionSavedTokens += saved
				}
				addToolCompaction(toolCompactions, toolName, originalTokens, saved)
			}
			addCategory("tool results", msg.Content)
			addTool(toolResults, toolName, msg.Content)
		default:
			addCategory(msg.Role+" messages", msg.Content)
		}
	}

	if strings.TrimSpace(exchange.RequestTools) != "" {
		addCategory("tool definitions", exchange.RequestTools)
	}
	if strings.TrimSpace(exchange.RequestFormat) != "" {
		addCategory("response format", exchange.RequestFormat)
	}

	rawContext := map[string]interface{}{
		"request_messages":  json.RawMessage(exchange.RequestMessages),
		"request_tools":     jsonOrNull(exchange.RequestTools),
		"request_format":    jsonOrNull(exchange.RequestFormat),
		"response_metadata": jsonOrNull(exchange.ResponseMetadata),
	}
	redactedContext, redactionCount := redactValue(rawContext)
	sanitizedContext := sanitizeContextForCopy(redactedContext)
	responseToolCallCount := countResponseToolCalls(exchange.ResponseToolCalls)
	responseMetadata := jsonOrNull(exchange.ResponseMetadata)

	return &contextAnalysis{
		SessionID:                sessionID,
		ExchangeID:               exchange.ID,
		TurnID:                   exchange.TurnID,
		Iteration:                exchange.Iteration,
		PromptTokens:             exchange.PromptTokens,
		CompletionTokens:         exchange.CompletionTokens,
		EstimatedTokens:          sumAccumulators(categories),
		ToolDefinitionTokens:     tokenizer.EstimateTokens(exchange.RequestTools),
		ToolDefinitionBytes:      len(exchange.RequestTools),
		MessageCount:             len(messages),
		ResponseContentBytes:     len(exchange.ResponseContent),
		ResponseContentChars:     len([]rune(exchange.ResponseContent)),
		ResponseToolCallCount:    responseToolCallCount,
		ResponseMetadata:         responseMetadata,
		Breakdown:                sortedContextBreakdown(categories),
		ToolResultBreakdown:      sortedToolBreakdownWithCompaction(toolResults, toolCompactions),
		ToolArgBreakdown:         sortedToolBreakdown(toolArgs),
		RawContext:               rawContext,
		RedactedContext:          redactedContext,
		SanitizedContext:         sanitizedContext,
		RedactionCount:           redactionCount,
		CompactedToolResults:     compactedToolResults,
		CompactionOriginalTokens: compactionOriginalTokens,
		CompactionSavedTokens:    compactionSavedTokens,
	}, nil
}

func countResponseToolCalls(raw string) int {
	if strings.TrimSpace(raw) == "" {
		return 0
	}
	var calls []interface{}
	if err := json.Unmarshal([]byte(raw), &calls); err == nil {
		return len(calls)
	}
	return 0
}

func addAccumulator(items map[string]*contextAccumulator, key, content string) {
	item := items[key]
	if item == nil {
		item = &contextAccumulator{}
		items[key] = item
	}
	item.tokens += tokenizer.EstimateTokens(content)
	item.bytes += len(content)
	item.count++
}

func sumAccumulators(items map[string]*contextAccumulator) int {
	total := 0
	for _, item := range items {
		total += item.tokens
	}
	return total
}

func sortedContextBreakdown(items map[string]*contextAccumulator) []contextBreakdownItem {
	out := make([]contextBreakdownItem, 0, len(items))
	for label, item := range items {
		out = append(out, contextBreakdownItem{
			Label:  label,
			Tokens: item.tokens,
			Bytes:  item.bytes,
			Count:  item.count,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tokens != out[j].Tokens {
			return out[i].Tokens > out[j].Tokens
		}
		return out[i].Label < out[j].Label
	})
	return out
}

func sortedToolBreakdown(items map[string]*contextAccumulator) []toolBreakdownItem {
	return sortedToolBreakdownWithCompaction(items, nil)
}

func sortedToolBreakdownWithCompaction(items map[string]*contextAccumulator, compactions map[string]*toolCompactionAccumulator) []toolBreakdownItem {
	out := make([]toolBreakdownItem, 0, len(items))
	for toolName, item := range items {
		row := toolBreakdownItem{
			ToolName: toolName,
			Tokens:   item.tokens,
			Bytes:    item.bytes,
			Count:    item.count,
		}
		if compaction := compactions[toolName]; compaction != nil {
			row.CompactedCount = compaction.count
			row.CompactionOriginalTokens = compaction.originalTokens
			row.CompactionSavedTokens = compaction.savedTokens
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tokens != out[j].Tokens {
			return out[i].Tokens > out[j].Tokens
		}
		return out[i].ToolName < out[j].ToolName
	})
	return out
}

func addToolCompaction(items map[string]*toolCompactionAccumulator, toolName string, originalTokens, savedTokens int) {
	if toolName == "" {
		toolName = "(unknown)"
	}
	item := items[toolName]
	if item == nil {
		item = &toolCompactionAccumulator{}
		items[toolName] = item
	}
	item.count++
	item.originalTokens += originalTokens
	if savedTokens > 0 {
		item.savedTokens += savedTokens
	}
}

var secretRedactors = []*regexp.Regexp{
	regexp.MustCompile(`(?is)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/=-]{16,}`),
	regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|refresh[_-]?token|secret|password|passwd|pwd|authorization)\s*[:=]\s*["']?[^"'\s,}]+`),
	regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`),
	regexp.MustCompile(`\b[A-Fa-f0-9]{40,}\b`),
}

const sanitizedContentMaxLen = 200

var pathRedactor = regexp.MustCompile(`(?:` +
	`(?:/[^\s\[\]{}<>|*"]+)+` +
	`|(?:\.\./[^\s\[\]{}<>|*"]+)+` +
	`|(?:\.[^/\s\[\]{}<>|*"]+/[^\s\[\]{}<>|*"]*)` +
	`|(?:[a-zA-Z][\w.\-]+/(?:[^\s\[\]{}<>|*"]*/)*[^\s\[\]{}<>|*"]+)` +
	`)`)

var pythonFilenameRedactor = regexp.MustCompile(`\b[a-zA-Z_][A-Za-z0-9_.-]*\.py\b`)

var compactedToolOriginalTokensPattern = regexp.MustCompile(`(?m)^Original estimated tokens:\s*([0-9]+)\s*$`)

func compactedToolOriginalTokens(content string) (int, bool) {
	if !strings.HasPrefix(content, "[tool result omitted to save context]") {
		return 0, false
	}
	match := compactedToolOriginalTokensPattern.FindStringSubmatch(content)
	if match == nil {
		return 0, true
	}
	tokens, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, true
	}
	return tokens, true
}

func redactValue(value interface{}) (interface{}, int) {
	switch v := value.(type) {
	case json.RawMessage:
		var decoded interface{}
		if err := json.Unmarshal(v, &decoded); err == nil {
			return redactValue(decoded)
		}
		return redactString(string(v))
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		total := 0
		for key, item := range v {
			redacted, count := redactValue(item)
			out[key] = redacted
			total += count
		}
		return out, total
	case []interface{}:
		out := make([]interface{}, len(v))
		total := 0
		for i, item := range v {
			redacted, count := redactValue(item)
			out[i] = redacted
			total += count
		}
		return out, total
	case string:
		return redactString(v)
	default:
		return value, 0
	}
}

func redactString(input string) (string, int) {
	out := input
	count := 0
	for _, re := range secretRedactors {
		matches := re.FindAllString(out, -1)
		if len(matches) == 0 {
			continue
		}
		count += len(matches)
		out = re.ReplaceAllString(out, "[REDACTED]")
	}
	return out, count
}

func sanitizeContextForCopy(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, item := range v {
			if key == "request_messages" {
				out[key] = sanitizeRequestMessages(item)
				continue
			}
			out[key] = sanitizeGenericValueForCopy(item)
		}
		return out
	default:
		return value
	}
}

func sanitizeGenericValueForCopy(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, item := range v {
			out[key] = sanitizeGenericValueForCopy(item)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = sanitizeGenericValueForCopy(item)
		}
		return out
	case string:
		return sanitizePathInText(v)
	default:
		return value
	}
}

func sanitizeRequestMessages(value interface{}) interface{} {
	switch v := value.(type) {
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = sanitizeMessageForCopy(item)
		}
		return out
	default:
		return value
	}
}

func sanitizeMessageForCopy(value interface{}) interface{} {
	msg, ok := value.(map[string]interface{})
	if !ok {
		return value
	}

	out := make(map[string]interface{}, len(msg))
	for key, item := range msg {
		switch key {
		case "content":
			if s, ok := item.(string); ok {
				out[key] = truncateSanitizedContent(sanitizePathInText(s))
			} else {
				out[key] = item
			}
		case "tool_calls":
			out[key] = sanitizeToolCallsForCopy(item)
		default:
			out[key] = item
		}
	}
	return out
}

func sanitizeToolCallsForCopy(value interface{}) interface{} {
	calls, ok := value.([]interface{})
	if !ok {
		return value
	}

	out := make([]interface{}, len(calls))
	for i, call := range calls {
		callMap, ok := call.(map[string]interface{})
		if !ok {
			out[i] = call
			continue
		}
		copied := make(map[string]interface{}, len(callMap))
		for key, item := range callMap {
			if key == "function" {
				copied[key] = sanitizeToolFunctionForCopy(item)
			} else {
				copied[key] = item
			}
		}
		out[i] = copied
	}
	return out
}

func sanitizeToolFunctionForCopy(value interface{}) interface{} {
	fn, ok := value.(map[string]interface{})
	if !ok {
		return value
	}

	out := make(map[string]interface{}, len(fn))
	for key, item := range fn {
		if key == "arguments" {
			out[key] = sanitizeToolArgumentsForCopy(item)
			continue
		}
		out[key] = item
	}
	return out
}

func sanitizeToolArgumentsForCopy(value interface{}) interface{} {
	switch args := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(args))
		for key, item := range args {
			switch key {
			case "path":
				out[key] = "[sanitized path]"
			case "symbol_name":
				out[key] = "[sanitized symbol]"
			case "content":
				if s, ok := item.(string); ok {
					out[key] = truncateSanitizedContent(sanitizePathInText(s))
				} else {
					out[key] = item
				}
			default:
				out[key] = item
			}
		}
		return out
	case string:
		var parsed interface{}
		if err := json.Unmarshal([]byte(args), &parsed); err != nil {
			return args
		}
		sanitized := sanitizeToolArgumentsForCopy(parsed)
		data, err := json.Marshal(sanitized)
		if err != nil {
			return args
		}
		return string(data)
	default:
		return value
	}
}

func sanitizePathInText(text string) string {
	text = pathRedactor.ReplaceAllString(text, "[sanitized path]")
	text = pythonFilenameRedactor.ReplaceAllString(text, "[sanitized filename]")
	return text
}

func truncateSanitizedContent(content string) string {
	runes := []rune(content)
	if len(runes) <= sanitizedContentMaxLen {
		return content
	}
	cutCount := len(runes) - sanitizedContentMaxLen
	return string(runes[:sanitizedContentMaxLen]) + "[cut " + strconv.Itoa(cutCount) + "]"
}
