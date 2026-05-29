package debugctx

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	CurrentSchemaVersion = 1
	MaxPromptLocals      = 30
	MaxPromptStackFrames = 12
	MaxPromptCodeLines   = 15
	MaxPromptValueRunes  = 240
)

type Context struct {
	SchemaVersion       int                  `json:"schema_version"`
	Source              string               `json:"source"`
	ExportedAt          string               `json:"exported_at"`
	WorkspaceRoot       string               `json:"workspace_root"`
	Language            string               `json:"language"`
	Event               string               `json:"event"`
	Stopped             Stopped              `json:"stopped"`
	Exception           Exception            `json:"exception"`
	ExceptionCandidates []ExceptionCandidate `json:"exception_candidates"`
	CurrentFrame        Frame                `json:"current_frame"`
	Stack               []Frame              `json:"stack"`
	UserFocus           UserFocus            `json:"user_focus"`
	Limits              Limits               `json:"limits"`

	Warnings []string `json:"-"`
}

type Stopped struct {
	Reason     string `json:"reason"`
	ThreadID   any    `json:"thread_id"`
	ThreadName string `json:"thread_name"`
}

type Exception struct {
	Type            string `json:"type"`
	Message         string `json:"message"`
	TracebackSource string `json:"traceback_source"`
}

type ExceptionCandidate struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Value      string `json:"value"`
	Source     string `json:"source"`
	Confidence string `json:"confidence"`
}

type Frame struct {
	File          string      `json:"file"`
	AbsoluteFile  string      `json:"absolute_file"`
	Line          int         `json:"line"`
	Function      string      `json:"function"`
	FileMtimeUnix int64       `json:"file_mtime_unix"`
	FileSHA256    string      `json:"file_sha256"`
	CodeContext   CodeContext `json:"code_context"`
	Locals        []Variable  `json:"locals"`
}

type CodeContext struct {
	StartLine   int        `json:"start_line"`
	CurrentLine int        `json:"current_line"`
	Lines       []CodeLine `json:"lines"`
}

type CodeLine struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

type Variable struct {
	Name               string `json:"name"`
	Type               string `json:"type"`
	Value              string `json:"value"`
	VariablesReference int    `json:"variablesReference"`
	ChildrenOmitted    bool   `json:"children_omitted"`
}

type UserFocus struct {
	ActiveFile     string `json:"active_file"`
	ActiveLine     int    `json:"active_line"`
	SelectionStart int    `json:"selection_start"`
	SelectionEnd   int    `json:"selection_end"`
}

type Limits struct {
	MaxLocals           int `json:"max_locals"`
	MaxDepth            int `json:"max_depth"`
	MaxStringChars      int `json:"max_string_chars"`
	MaxCollectionItems  int `json:"max_collection_items"`
	MaxStackFrames      int `json:"max_stack_frames"`
	MaxCodeContextLines int `json:"max_code_context_lines"`
}

func Load(path string, workspaceRoot string) (*Context, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var ctx Context
	if err := json.Unmarshal(data, &ctx); err != nil {
		return nil, fmt.Errorf("parse debug context: %w", err)
	}
	ctx.normalize(workspaceRoot)
	ctx.checkStale(workspaceRoot)
	return &ctx, nil
}

func (c *Context) normalize(workspaceRoot string) {
	if c.WorkspaceRoot == "" {
		c.WorkspaceRoot = workspaceRoot
	}
	if c.SchemaVersion == 0 {
		c.SchemaVersion = CurrentSchemaVersion
	}
	if c.SchemaVersion != CurrentSchemaVersion {
		c.Warnings = append(c.Warnings, fmt.Sprintf("unknown schema_version %d; using best-effort parsing", c.SchemaVersion))
	}
	if c.Stopped.Reason == "" {
		c.Stopped.Reason = "unknown"
	}
	if c.CurrentFrame.File == "" && len(c.Stack) > 0 {
		c.CurrentFrame.File = c.Stack[0].File
		c.CurrentFrame.AbsoluteFile = c.Stack[0].AbsoluteFile
		c.CurrentFrame.Line = c.Stack[0].Line
		c.CurrentFrame.Function = c.Stack[0].Function
	}
	c.CurrentFrame.File = trimWorkspaceBasePrefix(workspaceRoot, c.CurrentFrame.File)
	for i := range c.Stack {
		c.Stack[i].File = trimWorkspaceBasePrefix(workspaceRoot, c.Stack[i].File)
	}
	if c.UserFocus.ActiveFile != "" {
		c.UserFocus.ActiveFile = trimWorkspaceBasePrefix(workspaceRoot, c.UserFocus.ActiveFile)
	}
	for i := range c.CurrentFrame.Locals {
		c.CurrentFrame.Locals[i].Value = truncateRunes(strings.TrimSpace(c.CurrentFrame.Locals[i].Value), MaxPromptValueRunes)
	}
	for i := range c.ExceptionCandidates {
		c.ExceptionCandidates[i].Value = truncateRunes(strings.TrimSpace(c.ExceptionCandidates[i].Value), MaxPromptValueRunes)
	}
	if len(c.ExceptionCandidates) == 0 {
		c.ExceptionCandidates = collectExceptionCandidates(c.CurrentFrame.Locals)
	}
	if c.Exception.Type == "" && c.Exception.Message == "" && len(c.ExceptionCandidates) > 0 {
		candidate := c.ExceptionCandidates[0]
		c.Exception = Exception{
			Type:            firstNonEmpty(candidate.Type, candidate.Name),
			Message:         candidate.Value,
			TracebackSource: "locals:" + candidate.Name,
		}
	}
}

func trimWorkspaceBasePrefix(workspaceRoot, file string) string {
	file = filepath.ToSlash(strings.TrimSpace(file))
	if file == "" || filepath.IsAbs(file) {
		return file
	}
	rootBase := filepath.Base(filepath.Clean(workspaceRoot))
	if rootBase == "" || rootBase == "." || rootBase == string(filepath.Separator) {
		return file
	}
	prefix := rootBase + "/"
	if strings.HasPrefix(file, prefix) {
		return strings.TrimPrefix(file, prefix)
	}
	return file
}

func (c *Context) checkStale(workspaceRoot string) {
	if c.CurrentFrame.FileMtimeUnix <= 0 {
		return
	}
	path := c.CurrentFrame.AbsoluteFile
	if path == "" && c.CurrentFrame.File != "" {
		root := c.WorkspaceRoot
		if root == "" {
			root = workspaceRoot
		}
		if root != "" {
			path = filepath.Join(root, filepath.FromSlash(c.CurrentFrame.File))
		}
	}
	if path == "" {
		return
	}
	stat, err := os.Stat(path)
	if err != nil {
		return
	}
	current := stat.ModTime().Unix()
	if current != c.CurrentFrame.FileMtimeUnix {
		c.Warnings = append(c.Warnings, fmt.Sprintf("current file mtime differs from exported debug context (%s)", time.Unix(c.CurrentFrame.FileMtimeUnix, 0).Format(time.RFC3339)))
	}
}

func (c Context) ActiveLabel() string {
	file := c.CurrentFrame.File
	if file == "" {
		file = filepath.Base(c.CurrentFrame.AbsoluteFile)
	}
	if file == "" {
		return "debug context"
	}
	if c.CurrentFrame.Line > 0 {
		return fmt.Sprintf("%s:%d", file, c.CurrentFrame.Line)
	}
	return file
}

func (c Context) Summary() string {
	var sb strings.Builder
	sb.WriteString("Debug context loaded")
	if label := c.ActiveLabel(); label != "" {
		sb.WriteString(": ")
		sb.WriteString(label)
	}
	if c.CurrentFrame.Function != "" {
		sb.WriteString(" in ")
		sb.WriteString(c.CurrentFrame.Function)
	}
	if c.Stopped.Reason != "" {
		sb.WriteString(fmt.Sprintf(" (reason: %s)", c.Stopped.Reason))
	}
	if c.Exception.Type != "" || c.Exception.Message != "" {
		sb.WriteString(fmt.Sprintf("\nException: %s %s", c.Exception.Type, c.Exception.Message))
	}
	if len(c.ExceptionCandidates) > 0 {
		sb.WriteString(fmt.Sprintf("\nException candidates: %d", len(c.ExceptionCandidates)))
	}
	if len(c.CurrentFrame.Locals) > 0 {
		sb.WriteString(fmt.Sprintf("\nLocals: %d", len(c.CurrentFrame.Locals)))
	}
	if len(c.Stack) > 0 {
		sb.WriteString(fmt.Sprintf(" | Stack frames: %d", len(c.Stack)))
	}
	if len(c.Warnings) > 0 {
		sb.WriteString("\nWarnings:")
		for _, warning := range c.Warnings {
			sb.WriteString("\n- ")
			sb.WriteString(warning)
		}
	}
	return sb.String()
}

func (c Context) FormatForPrompt() string {
	var sb strings.Builder
	sb.WriteString("<debug_context>\n")
	writeField(&sb, "source", c.Source)
	writeField(&sb, "exported_at", c.ExportedAt)
	writeField(&sb, "language", c.Language)
	writeField(&sb, "event", c.Event)
	writeField(&sb, "stopped_reason", c.Stopped.Reason)
	if c.Exception.Type != "" || c.Exception.Message != "" {
		sb.WriteString("exception:\n")
		writeIndentedField(&sb, "type", c.Exception.Type)
		writeIndentedField(&sb, "message", c.Exception.Message)
		writeIndentedField(&sb, "traceback_source", c.Exception.TracebackSource)
	}
	if len(c.ExceptionCandidates) > 0 {
		sb.WriteString("exception_candidates:\n")
		for _, candidate := range c.ExceptionCandidates {
			sb.WriteString(fmt.Sprintf("- name: %s\n", candidate.Name))
			writeIndentedField(&sb, "type", candidate.Type)
			writeIndentedField(&sb, "value", candidate.Value)
			writeIndentedField(&sb, "source", candidate.Source)
			writeIndentedField(&sb, "confidence", candidate.Confidence)
		}
	}
	sb.WriteString("current_frame:\n")
	writeIndentedField(&sb, "file", c.CurrentFrame.File)
	writeIndentedField(&sb, "line", fmt.Sprintf("%d", c.CurrentFrame.Line))
	writeIndentedField(&sb, "function", c.CurrentFrame.Function)
	if len(c.CurrentFrame.CodeContext.Lines) > 0 {
		sb.WriteString("code_context:\n")
		for _, line := range limitedCodeLines(c.CurrentFrame.CodeContext.Lines) {
			prefix := "  "
			if line.Line == c.CurrentFrame.Line || line.Line == c.CurrentFrame.CodeContext.CurrentLine {
				prefix = "> "
			}
			sb.WriteString(fmt.Sprintf("%s%d | %s\n", prefix, line.Line, line.Text))
		}
	}
	if len(c.CurrentFrame.Locals) > 0 {
		sb.WriteString("locals:\n")
		for _, local := range limitedLocals(c.CurrentFrame.Locals) {
			typePart := ""
			if local.Type != "" {
				typePart = " (" + local.Type + ")"
			}
			omitted := ""
			if local.ChildrenOmitted {
				omitted = " [children omitted]"
			}
			sb.WriteString(fmt.Sprintf("- %s%s = %s%s\n", local.Name, typePart, local.Value, omitted))
		}
		if len(c.CurrentFrame.Locals) > MaxPromptLocals {
			sb.WriteString(fmt.Sprintf("- ... %d more locals omitted\n", len(c.CurrentFrame.Locals)-MaxPromptLocals))
		}
	}
	if len(c.Stack) > 0 {
		sb.WriteString("stack:\n")
		for _, frame := range limitedStack(c.Stack) {
			location := frame.File
			if location == "" {
				location = filepath.Base(frame.AbsoluteFile)
			}
			sb.WriteString(fmt.Sprintf("- %s:%d %s\n", location, frame.Line, frame.Function))
		}
		if len(c.Stack) > MaxPromptStackFrames {
			sb.WriteString(fmt.Sprintf("- ... %d more frames omitted\n", len(c.Stack)-MaxPromptStackFrames))
		}
	}
	if len(c.Warnings) > 0 {
		sb.WriteString("warnings:\n")
		for _, warning := range c.Warnings {
			sb.WriteString("- ")
			sb.WriteString(warning)
			sb.WriteString("\n")
		}
	}
	sb.WriteString("</debug_context>")
	return sb.String()
}

func WithPrompt(ctx *Context, prompt string) string {
	if ctx == nil {
		return prompt
	}
	return ctx.FormatForPrompt() + "\n\nUser request:\n" + prompt
}

func limitedLocals(locals []Variable) []Variable {
	if len(locals) <= MaxPromptLocals {
		return locals
	}
	return locals[:MaxPromptLocals]
}

func limitedStack(stack []Frame) []Frame {
	if len(stack) <= MaxPromptStackFrames {
		return stack
	}
	return stack[:MaxPromptStackFrames]
}

func limitedCodeLines(lines []CodeLine) []CodeLine {
	if len(lines) <= MaxPromptCodeLines {
		return lines
	}
	return lines[:MaxPromptCodeLines]
}

func collectExceptionCandidates(locals []Variable) []ExceptionCandidate {
	candidates := make([]ExceptionCandidate, 0, 3)
	for _, local := range locals {
		candidate, ok := exceptionCandidateFromLocal(local)
		if ok {
			candidates = append(candidates, candidate)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return confidenceRank(candidates[i].Confidence) > confidenceRank(candidates[j].Confidence)
	})
	if len(candidates) > 5 {
		return candidates[:5]
	}
	return candidates
}

func exceptionCandidateFromLocal(local Variable) (ExceptionCandidate, bool) {
	nameHit := isExceptionName(local.Name)
	typeHit := isExceptionType(local.Type)
	valueHit := isExceptionValue(local.Value)
	if !nameHit && !typeHit && !valueHit {
		return ExceptionCandidate{}, false
	}
	confidence := "low"
	if typeHit && (nameHit || valueHit) {
		confidence = "high"
	} else if typeHit || (nameHit && valueHit) {
		confidence = "medium"
	}
	return ExceptionCandidate{
		Name:       local.Name,
		Type:       local.Type,
		Value:      local.Value,
		Source:     "locals",
		Confidence: confidence,
	}, true
}

func isExceptionName(name string) bool {
	switch strings.ToLower(name) {
	case "e", "err", "error", "exc", "exception":
		return true
	default:
		return false
	}
}

func isExceptionType(value string) bool {
	return value == "BaseException" ||
		value == "Exception" ||
		strings.HasSuffix(value, "Error") ||
		strings.HasSuffix(value, "Exception")
}

func isExceptionValue(value string) bool {
	return strings.Contains(value, "Error(") || strings.Contains(value, "Exception(")
}

func confidenceRank(confidence string) int {
	switch confidence {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func writeField(sb *strings.Builder, key string, value string) {
	if value == "" {
		return
	}
	sb.WriteString(key)
	sb.WriteString(": ")
	sb.WriteString(value)
	sb.WriteString("\n")
}

func writeIndentedField(sb *strings.Builder, key string, value string) {
	if value == "" {
		return
	}
	sb.WriteString("  ")
	sb.WriteString(key)
	sb.WriteString(": ")
	sb.WriteString(value)
	sb.WriteString("\n")
}

func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "..."
}
