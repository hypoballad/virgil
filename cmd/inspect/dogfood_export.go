package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hypoballad/virgil/internal/repository"
)

const dogfoodDebugTailByteLimit = 1024 * 1024

type dogfoodExportOptions struct {
	Session        string
	Exchange       string
	OutDir         string
	LogPath        string
	LogLines       int
	AllowPatterns  string
	DenyPatterns   string
	FailOnFindings bool
	now            func() time.Time
}

type dogfoodExportResult struct {
	OutputDir string
	Scan      secretScanReport
}

type dogfoodContextSummary struct {
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
	RedactionCount           int                    `json:"redaction_count"`
	CompactedToolResults     int                    `json:"compacted_tool_results"`
	CompactionOriginalTokens int                    `json:"compaction_original_tokens"`
	CompactionSavedTokens    int                    `json:"compaction_saved_tokens"`
}

func exportDogfoodReport(repo *repository.Repository, opts dogfoodExportOptions) (*dogfoodExportResult, error) {
	if opts.Session == "" {
		opts.Session = "latest"
	}
	if opts.Exchange == "" {
		opts.Exchange = "latest"
	}
	if opts.OutDir == "" {
		opts.OutDir = "work/dogfood"
	}
	if opts.LogLines <= 0 {
		opts.LogLines = 300
	}
	if opts.now == nil {
		opts.now = time.Now
	}

	sessionID, err := resolveDogfoodSession(repo, opts.Session)
	if err != nil {
		return nil, err
	}
	exchange, err := resolveDogfoodExchange(repo, sessionID, opts.Exchange)
	if err != nil {
		return nil, err
	}

	analysis, err := analyzeExchangeContext(sessionID, exchange)
	if err != nil {
		return nil, err
	}

	outputDir := filepath.Join(opts.OutDir, opts.now().UTC().Format("2006-01-02-150405"))
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, err
	}

	debugTail, err := readDogfoodDebugTail(opts.LogPath, opts.LogLines)
	if err != nil {
		debugTail = ""
	}
	debugTail = sanitizeDogfoodText(debugTail)
	errorSnippet := extractDogfoodErrorSnippet(debugTail)

	summary := sanitizeDogfoodContextSummary(dogfoodContextSummary{
		SessionID:                analysis.SessionID,
		ExchangeID:               analysis.ExchangeID,
		TurnID:                   analysis.TurnID,
		Iteration:                analysis.Iteration,
		PromptTokens:             analysis.PromptTokens,
		CompletionTokens:         analysis.CompletionTokens,
		EstimatedTokens:          analysis.EstimatedTokens,
		ToolDefinitionTokens:     analysis.ToolDefinitionTokens,
		ToolDefinitionBytes:      analysis.ToolDefinitionBytes,
		MessageCount:             analysis.MessageCount,
		ResponseContentBytes:     analysis.ResponseContentBytes,
		ResponseContentChars:     analysis.ResponseContentChars,
		ResponseToolCallCount:    analysis.ResponseToolCallCount,
		ResponseMetadata:         sanitizeDogfoodValue(analysis.ResponseMetadata),
		Breakdown:                analysis.Breakdown,
		ToolResultBreakdown:      analysis.ToolResultBreakdown,
		ToolArgBreakdown:         analysis.ToolArgBreakdown,
		RedactionCount:           analysis.RedactionCount,
		CompactedToolResults:     analysis.CompactedToolResults,
		CompactionOriginalTokens: analysis.CompactionOriginalTokens,
		CompactionSavedTokens:    analysis.CompactionSavedTokens,
	})

	if err := writePrettyJSON(filepath.Join(outputDir, "sanitized_context.json"), sanitizeDogfoodValue(analysis.SanitizedContext)); err != nil {
		return nil, err
	}
	if err := writePrettyJSON(filepath.Join(outputDir, "context_summary.json"), summary); err != nil {
		return nil, err
	}
	if debugTail != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "debug_tail.log"), []byte(debugTail), 0600); err != nil {
			return nil, err
		}
	}

	initialScan := scanFilesForSecrets(outputDir, []string{"sanitized_context.json", "context_summary.json", "debug_tail.log"}, nil, nil)
	report := sanitizeDogfoodText(renderDogfoodReport(summary, initialScan, errorSnippet, debugTail != ""))
	issueBody := sanitizeDogfoodText(renderDogfoodIssueBody(summary, initialScan, errorSnippet))
	readme := renderDogfoodReadme()

	if err := os.WriteFile(filepath.Join(outputDir, "report.md"), []byte(report), 0600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(outputDir, "issue_body.md"), []byte(issueBody), 0600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(outputDir, "README.md"), []byte(readme), 0600); err != nil {
		return nil, err
	}

	allow, err := loadRegexFile(opts.AllowPatterns)
	if err != nil {
		return nil, err
	}
	deny, err := loadRegexFile(opts.DenyPatterns)
	if err != nil {
		return nil, err
	}
	scan := scanFilesForSecrets(outputDir, []string{
		"sanitized_context.json",
		"context_summary.json",
		"debug_tail.log",
		"report.md",
		"issue_body.md",
	}, allow, deny)
	if err := writePrettyJSON(filepath.Join(outputDir, "scan_report.json"), scan); err != nil {
		return nil, err
	}

	return &dogfoodExportResult{OutputDir: outputDir, Scan: scan}, nil
}

func resolveDogfoodSession(repo *repository.Repository, requested string) (string, error) {
	if requested != "" && requested != "latest" {
		if _, err := repo.Sessions.Get(requested); err != nil {
			return "", fmt.Errorf("get session %q: %w", requested, err)
		}
		return requested, nil
	}
	sessions, err := repo.Sessions.ListRecent(1)
	if err != nil {
		return "", err
	}
	if len(sessions) == 0 {
		return "", fmt.Errorf("no sessions found")
	}
	return sessions[0].ID, nil
}

func resolveDogfoodExchange(repo *repository.Repository, sessionID, requested string) (*repository.LLMExchangeRecord, error) {
	if requested != "" && requested != "latest" {
		id, err := strconv.ParseInt(requested, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid exchange id %q", requested)
		}
		return repo.LLMExchanges.GetByID(id)
	}
	exchanges, err := repo.LLMExchanges.ListBySession(sessionID)
	if err != nil {
		return nil, err
	}
	if len(exchanges) == 0 {
		return nil, fmt.Errorf("no LLM exchanges recorded for session %s", sessionID)
	}
	return exchanges[len(exchanges)-1], nil
}

func writePrettyJSON(path string, value interface{}) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0600)
}

func sanitizeDogfoodContextSummary(summary dogfoodContextSummary) dogfoodContextSummary {
	for i := range summary.Breakdown {
		summary.Breakdown[i].Label = sanitizeDogfoodText(summary.Breakdown[i].Label)
	}
	for i := range summary.ToolResultBreakdown {
		summary.ToolResultBreakdown[i].ToolName = sanitizeDogfoodText(summary.ToolResultBreakdown[i].ToolName)
	}
	for i := range summary.ToolArgBreakdown {
		summary.ToolArgBreakdown[i].ToolName = sanitizeDogfoodText(summary.ToolArgBreakdown[i].ToolName)
	}
	return summary
}

func sanitizeDogfoodValue(value interface{}) interface{} {
	switch v := value.(type) {
	case json.RawMessage:
		var decoded interface{}
		if err := json.Unmarshal(v, &decoded); err == nil {
			return sanitizeDogfoodValue(decoded)
		}
		return sanitizeDogfoodText(string(v))
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, item := range v {
			out[key] = sanitizeDogfoodValue(item)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = sanitizeDogfoodValue(item)
		}
		return out
	case string:
		return sanitizeDogfoodText(v)
	default:
		return value
	}
}

func sanitizeDogfoodText(text string) string {
	return maskSensitiveText(sanitizePathInText(text))
}

func readDogfoodDebugTail(path string, lines int) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) > dogfoodDebugTailByteLimit {
		data = data[len(data)-dogfoodDebugTailByteLimit:]
		if idx := bytes.IndexByte(data, '\n'); idx >= 0 && idx+1 < len(data) {
			data = data[idx+1:]
		}
	}
	all := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n"), nil
}

func extractDogfoodErrorSnippet(logText string) string {
	if strings.TrimSpace(logText) == "" {
		return "No debug log was included."
	}
	keywords := []string{
		"agent error:",
		"llm chat failed",
		"ollama error",
		"panic:",
		"fatal",
		"XML syntax error",
		"context deadline exceeded",
		"maximum context",
		"context overflow",
		"context length",
		"context shift",
		"num_ctx",
	}
	lines := strings.Split(logText, "\n")
	selected := map[int]bool{}
	for i, line := range lines {
		lower := strings.ToLower(line)
		for _, keyword := range keywords {
			if strings.Contains(lower, strings.ToLower(keyword)) {
				for j := i - 2; j <= i+2; j++ {
					if j >= 0 && j < len(lines) {
						selected[j] = true
					}
				}
			}
		}
	}
	if len(selected) == 0 {
		return "No obvious error lines found in debug tail."
	}
	idxs := make([]int, 0, len(selected))
	for idx := range selected {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)
	out := make([]string, 0, len(idxs))
	prev := -2
	for _, idx := range idxs {
		if idx > prev+1 && len(out) > 0 {
			out = append(out, "...")
		}
		out = append(out, lines[idx])
		prev = idx
	}
	return strings.Join(out, "\n")
}

func renderDogfoodReport(summary dogfoodContextSummary, scan secretScanReport, errorSnippet string, hasDebugLog bool) string {
	var sb strings.Builder
	sb.WriteString("# Virgil Dogfood Export\n\n")
	sb.WriteString("## Summary\n")
	sb.WriteString(fmt.Sprintf("- Session: `%s`\n", summary.SessionID))
	sb.WriteString(fmt.Sprintf("- Exchange: `%d`\n", summary.ExchangeID))
	sb.WriteString(fmt.Sprintf("- Turn: `%d`\n", summary.TurnID))
	sb.WriteString(fmt.Sprintf("- Iteration: `%d`\n", summary.Iteration))
	sb.WriteString(fmt.Sprintf("- Prompt tokens: `%d`\n", summary.PromptTokens))
	sb.WriteString(fmt.Sprintf("- Completion tokens: `%d`\n", summary.CompletionTokens))
	sb.WriteString(fmt.Sprintf("- Estimated tokens: `%d`\n", summary.EstimatedTokens))
	sb.WriteString(fmt.Sprintf("- Message count: `%d`\n", summary.MessageCount))
	sb.WriteString(fmt.Sprintf("- Response content: `%d chars / %d bytes`\n", summary.ResponseContentChars, summary.ResponseContentBytes))
	sb.WriteString(fmt.Sprintf("- Response tool calls: `%d`\n", summary.ResponseToolCallCount))
	if summary.ResponseMetadata != nil {
		if b, err := json.Marshal(summary.ResponseMetadata); err == nil && string(b) != "null" {
			sb.WriteString(fmt.Sprintf("- Response metadata: `%s`\n", string(b)))
		}
	}
	sb.WriteString(fmt.Sprintf("- Redactions: `%d`\n", summary.RedactionCount))
	sb.WriteString(fmt.Sprintf("- Debug log included: `%t`\n\n", hasDebugLog))

	sb.WriteString("## Error / Last Activity\n\n```text\n")
	sb.WriteString(errorSnippet)
	sb.WriteString("\n```\n\n")

	sb.WriteString("## Context Breakdown\n\n")
	sb.WriteString("| Category | Tokens | Bytes | Count |\n| --- | ---: | ---: | ---: |\n")
	for _, row := range summary.Breakdown {
		sb.WriteString(fmt.Sprintf("| %s | %d | %d | %d |\n", row.Label, row.Tokens, row.Bytes, row.Count))
	}
	sb.WriteString("\n## Tool Result Breakdown\n\n")
	sb.WriteString("| Tool | Tokens | Bytes | Count | Compacted | Saved tokens |\n| --- | ---: | ---: | ---: | ---: | ---: |\n")
	for _, row := range summary.ToolResultBreakdown {
		sb.WriteString(fmt.Sprintf("| %s | %d | %d | %d | %d | %d |\n", row.ToolName, row.Tokens, row.Bytes, row.Count, row.CompactedCount, row.CompactionSavedTokens))
	}
	sb.WriteString("\n## Tool Argument Breakdown\n\n")
	sb.WriteString("| Tool | Tokens | Bytes | Count |\n| --- | ---: | ---: | ---: |\n")
	for _, row := range summary.ToolArgBreakdown {
		sb.WriteString(fmt.Sprintf("| %s | %d | %d | %d |\n", row.ToolName, row.Tokens, row.Bytes, row.Count))
	}

	sb.WriteString("\n## Secret Scan\n\n")
	sb.WriteString(fmt.Sprintf("- Findings: `%d`\n", scan.FindingCount))
	sb.WriteString("- Review required before sharing.\n\n")
	sb.WriteString("## Files\n\n")
	sb.WriteString("- `sanitized_context.json`\n- `context_summary.json`\n- `debug_tail.log`\n- `issue_body.md`\n- `scan_report.json`\n\n")
	sb.WriteString("## Review Checklist\n\n")
	sb.WriteString("- [ ] 社内固有のパス・URL・ドメインが残っていない\n")
	sb.WriteString("- [ ] 顧客名・個人名・メールアドレスが残っていない\n")
	sb.WriteString("- [ ] token / key / cookie が残っていない\n")
	sb.WriteString("- [ ] 必要なエラー行と context は残っている\n")
	return sb.String()
}

func renderDogfoodIssueBody(summary dogfoodContextSummary, scan secretScanReport, errorSnippet string) string {
	var sb strings.Builder
	sb.WriteString("> This report was generated by `inspect --export-dogfood`.\n")
	sb.WriteString("> It contains sanitized context only. Please review before sharing.\n\n")
	sb.WriteString("# Dogfood Report\n\n")
	sb.WriteString("## What Happened\n\n<!-- 手で1-3行補足 -->\n\n")
	sb.WriteString("## Error\n\n```text\n")
	sb.WriteString(errorSnippet)
	sb.WriteString("\n```\n\n")
	sb.WriteString("## Context Metrics\n\n")
	sb.WriteString(fmt.Sprintf("- Prompt tokens: `%d`\n", summary.PromptTokens))
	sb.WriteString(fmt.Sprintf("- Completion tokens: `%d`\n", summary.CompletionTokens))
	sb.WriteString(fmt.Sprintf("- Estimated tokens: `%d`\n", summary.EstimatedTokens))
	sb.WriteString(fmt.Sprintf("- Message count: `%d`\n", summary.MessageCount))
	sb.WriteString(fmt.Sprintf("- Response content: `%d chars / %d bytes`\n", summary.ResponseContentChars, summary.ResponseContentBytes))
	sb.WriteString(fmt.Sprintf("- Response tool calls: `%d`\n", summary.ResponseToolCallCount))
	if summary.ResponseMetadata != nil {
		if b, err := json.Marshal(summary.ResponseMetadata); err == nil && string(b) != "null" {
			sb.WriteString(fmt.Sprintf("- Response metadata: `%s`\n", string(b)))
		}
	}
	sb.WriteString("- Largest context categories:\n")
	for _, row := range firstContextRows(summary.Breakdown, 5) {
		sb.WriteString(fmt.Sprintf("  - %s: %d tokens\n", row.Label, row.Tokens))
	}
	sb.WriteString("- Largest tool results:\n")
	for _, row := range firstToolRows(summary.ToolResultBreakdown, 5) {
		sb.WriteString(fmt.Sprintf("  - %s: %d tokens\n", row.ToolName, row.Tokens))
	}
	sb.WriteString("\n## Sanitized Context\n\nSee `sanitized_context.json`.\n\n")
	sb.WriteString("## Secret Scan\n\n")
	sb.WriteString(fmt.Sprintf("- Findings: `%d`\n", scan.FindingCount))
	sb.WriteString("- Review required: yes\n\n")
	sb.WriteString("## Notes for Codex\n\n<!-- 期待する分析観点を書く -->\n")
	return sb.String()
}

func renderDogfoodReadme() string {
	return "# Dogfood Export\n\n" +
		"Review `report.md` and `issue_body.md` before sharing.\n\n" +
		"`sanitized_context.json` is the sanitized context payload. Raw context is not exported.\n"
}

func firstContextRows(rows []contextBreakdownItem, n int) []contextBreakdownItem {
	if len(rows) < n {
		return rows
	}
	return rows[:n]
}

func firstToolRows(rows []toolBreakdownItem, n int) []toolBreakdownItem {
	if len(rows) < n {
		return rows
	}
	return rows[:n]
}
