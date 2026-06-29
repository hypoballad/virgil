package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	markdownSectionDefaultMaxLines = 200
	markdownSectionMaxLines        = 400
	markdownSectionMaxChars        = 20000
)

var markdownHeadingPattern = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*\s*$`)

type markdownToolBase struct {
	AllowedRoot string
}

type markdownHeading struct {
	Level     int
	Title     string
	StartLine int
	EndLine   int
	Chars     int
}

type getMarkdownOutlineArgs struct {
	Path     string `json:"path"`
	MaxDepth int    `json:"max_depth,omitempty"`
}

type readMarkdownSectionArgs struct {
	Path      string `json:"path"`
	Heading   string `json:"heading,omitempty"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	MaxLines  int    `json:"max_lines,omitempty"`
}

type GetMarkdownOutlineTool struct {
	markdownToolBase
}

type ReadMarkdownSectionTool struct {
	markdownToolBase
}

func NewGetMarkdownOutlineTool(allowedRoot string) *GetMarkdownOutlineTool {
	return &GetMarkdownOutlineTool{markdownToolBase{AllowedRoot: allowedRoot}}
}

func NewReadMarkdownSectionTool(allowedRoot string) *ReadMarkdownSectionTool {
	return &ReadMarkdownSectionTool{markdownToolBase{AllowedRoot: allowedRoot}}
}

func (t *GetMarkdownOutlineTool) Name() string {
	return "get_markdown_outline"
}

func (t *GetMarkdownOutlineTool) IsMutating() bool {
	return false
}

func (t *GetMarkdownOutlineTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        t.Name(),
			Description: "Inspect a Markdown file without loading the full content. Returns heading hierarchy with line ranges and estimated tokens. Use this before read_file for long .md documents.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the Markdown file, relative to workspace root",
					},
					"max_depth": map[string]interface{}{
						"type":        "integer",
						"description": "Optional maximum heading depth to include, for example 2 for # and ## headings.",
					},
				},
				"required": []string{"path"},
			},
		},
	}
}

func (t *GetMarkdownOutlineTool) Execute(ctx context.Context, rawArgs json.RawMessage) (*Result, error) {
	var args getMarkdownOutlineArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	path, err := t.resolveMarkdownPath(args.Path)
	if err != nil {
		return ErrorResult(err.Error()), nil
	}

	lines, headings, err := readMarkdownHeadings(path)
	if err != nil {
		return ErrorResult(err.Error()), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Markdown outline: %s\n", args.Path))
	sb.WriteString(fmt.Sprintf("Lines: %d | Headings: %d\n\n", len(lines), len(headings)))
	if len(headings) == 0 {
		sb.WriteString("(no headings found)\n")
		return SuccessResult(sb.String()), nil
	}

	sb.WriteString("| Lines | Level | Title | Est. tokens |\n")
	sb.WriteString("|---|---:|---|---:|\n")
	for _, h := range headings {
		if args.MaxDepth > 0 && h.Level > args.MaxDepth {
			continue
		}
		indent := strings.Repeat("  ", h.Level-1)
		sb.WriteString(fmt.Sprintf("| %d-%d | %d | %s%s | ~%d |\n",
			h.StartLine, h.EndLine, h.Level, indent, h.Title, approximateTokens(h.Chars)))
	}
	sb.WriteString("\nNext step: use read_markdown_section with a heading or line range.\n")
	return SuccessResult(sb.String()), nil
}

func (t *ReadMarkdownSectionTool) Name() string {
	return "read_markdown_section"
}

func (t *ReadMarkdownSectionTool) IsMutating() bool {
	return false
}

func (t *ReadMarkdownSectionTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        t.Name(),
			Description: "Read one focused section from a Markdown file by heading or line range. Use after get_markdown_outline instead of reading long Markdown files in full.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the Markdown file, relative to workspace root",
					},
					"heading": map[string]interface{}{
						"type":        "string",
						"description": "Optional exact heading title to read. Case-insensitive.",
					},
					"start_line": map[string]interface{}{
						"type":        "integer",
						"description": "Optional 1-indexed start line. Used when heading is omitted.",
					},
					"end_line": map[string]interface{}{
						"type":        "integer",
						"description": "Optional inclusive end line. Used when heading is omitted.",
					},
					"max_lines": map[string]interface{}{
						"type":        "integer",
						"description": "Optional maximum lines to return. Defaults to 200 and is capped at 400.",
					},
				},
				"required": []string{"path"},
			},
		},
	}
}

func (t *ReadMarkdownSectionTool) Execute(ctx context.Context, rawArgs json.RawMessage) (*Result, error) {
	var args readMarkdownSectionArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	path, err := t.resolveMarkdownPath(args.Path)
	if err != nil {
		return ErrorResult(err.Error()), nil
	}

	lines, headings, err := readMarkdownHeadings(path)
	if err != nil {
		return ErrorResult(err.Error()), nil
	}
	if len(lines) == 0 {
		return SuccessResult(fmt.Sprintf("Markdown section: %s\n(empty file)\n", args.Path)), nil
	}

	startLine, endLine, label, err := markdownSectionRange(args, headings, len(lines))
	if err != nil {
		return ErrorResult(err.Error()), nil
	}

	maxLines := args.MaxLines
	if maxLines <= 0 {
		maxLines = markdownSectionDefaultMaxLines
	}
	if maxLines > markdownSectionMaxLines {
		maxLines = markdownSectionMaxLines
	}
	if endLine-startLine+1 > maxLines {
		endLine = startLine + maxLines - 1
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Markdown section: %s", args.Path))
	if label != "" {
		sb.WriteString(fmt.Sprintf(" | %s", label))
	}
	sb.WriteString(fmt.Sprintf(" | lines %d-%d\n", startLine, endLine))
	sb.WriteString(strings.Repeat("-", 40))
	sb.WriteString("\n")

	truncatedByChars := false
	for lineNum := startLine; lineNum <= endLine; lineNum++ {
		next := fmt.Sprintf("%4d | %s\n", lineNum, lines[lineNum-1])
		if sb.Len()+len(next) > markdownSectionMaxChars {
			truncatedByChars = true
			break
		}
		sb.WriteString(next)
	}
	if truncatedByChars {
		sb.WriteString("... [truncated by character limit]\n")
	} else if endLine < markdownSectionOriginalEnd(args, headings, len(lines)) {
		sb.WriteString("... [truncated by max_lines]\n")
	}

	result := SuccessResult(sb.String())
	result.Metadata = map[string]interface{}{
		"path":       args.Path,
		"start_line": startLine,
		"end_line":   endLine,
		"mode":       "section",
	}
	return result, nil
}

func (t markdownToolBase) resolveMarkdownPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".md" && ext != ".markdown" {
		return "", fmt.Errorf("not a Markdown file: %s", path)
	}

	var abs string
	var err error
	if filepath.IsAbs(path) {
		abs = filepath.Clean(path)
	} else {
		abs, err = filepath.Abs(filepath.Join(t.AllowedRoot, path))
		if err != nil {
			return "", fmt.Errorf("failed to resolve path: %v", err)
		}
	}
	if t.AllowedRoot != "" {
		root, err := filepath.Abs(t.AllowedRoot)
		if err != nil {
			return "", fmt.Errorf("failed to resolve workspace root: %v", err)
		}
		relPath, err := filepath.Rel(root, abs)
		if err != nil || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("path outside allowed root: %s", path)
		}
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%s", formatPathError(t.AllowedRoot, path, abs))
		}
		return "", fmt.Errorf("failed to stat file: %v", err)
	}
	return abs, nil
}

func readMarkdownHeadings(path string) ([]string, []markdownHeading, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open error: %v", err)
	}
	defer f.Close()

	lines := make([]string, 0)
	headings := make([]markdownHeading, 0)
	inFence := false

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		lines = append(lines, line)
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if match := markdownHeadingPattern.FindStringSubmatch(line); match != nil {
			headings = append(headings, markdownHeading{
				Level:     len(match[1]),
				Title:     strings.TrimSpace(match[2]),
				StartLine: len(lines),
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("scan error: %v", err)
	}

	for i := range headings {
		endLine := len(lines)
		for j := i + 1; j < len(headings); j++ {
			if headings[j].Level <= headings[i].Level {
				endLine = headings[j].StartLine - 1
				break
			}
		}
		headings[i].EndLine = endLine
		for lineNum := headings[i].StartLine; lineNum <= endLine; lineNum++ {
			headings[i].Chars += len(lines[lineNum-1]) + 1
		}
	}
	return lines, headings, nil
}

func markdownSectionRange(args readMarkdownSectionArgs, headings []markdownHeading, lineCount int) (int, int, string, error) {
	if strings.TrimSpace(args.Heading) != "" {
		for _, h := range headings {
			if strings.EqualFold(h.Title, strings.TrimSpace(args.Heading)) {
				return h.StartLine, h.EndLine, fmt.Sprintf("heading %q", h.Title), nil
			}
		}
		return 0, 0, "", fmt.Errorf("heading %q not found\n%s", args.Heading, formatAvailableMarkdownHeadings(headings, 12))
	}

	startLine := args.StartLine
	endLine := args.EndLine
	if startLine <= 0 {
		startLine = 1
	}
	if endLine <= 0 || endLine > lineCount {
		endLine = lineCount
	}
	if startLine > lineCount {
		return 0, 0, "", fmt.Errorf("start_line %d is beyond file length %d", startLine, lineCount)
	}
	if endLine < startLine {
		return 0, 0, "", fmt.Errorf("end_line %d is before start_line %d", endLine, startLine)
	}
	return startLine, endLine, "", nil
}

func formatAvailableMarkdownHeadings(headings []markdownHeading, limit int) string {
	if len(headings) == 0 {
		return "Available headings: none. Use read_markdown_section with start_line/end_line."
	}
	if limit <= 0 || limit > len(headings) {
		limit = len(headings)
	}
	var sb strings.Builder
	sb.WriteString("Available headings:\n")
	for i := 0; i < limit; i++ {
		h := headings[i]
		sb.WriteString(fmt.Sprintf("- %s (lines %d-%d)\n", h.Title, h.StartLine, h.EndLine))
	}
	if len(headings) > limit {
		sb.WriteString(fmt.Sprintf("- ... %d more headings omitted; use get_markdown_outline for the full list.\n", len(headings)-limit))
	}
	sb.WriteString("Use one of these exact headings or read_markdown_section with start_line/end_line.")
	return strings.TrimRight(sb.String(), "\n")
}

func markdownSectionOriginalEnd(args readMarkdownSectionArgs, headings []markdownHeading, lineCount int) int {
	startLine, endLine, _, err := markdownSectionRange(args, headings, lineCount)
	if err != nil || startLine == 0 {
		return endLine
	}
	return endLine
}
