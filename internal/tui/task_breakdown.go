package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type breakdownTask struct {
	ID        string
	Title     string
	Status    string
	DependsOn []string
	StartLine int
	EndLine   int
	Block     string
}

type breakdownCommand struct {
	Source string
	Output string
}

const (
	taskBreakdownTemplatePath = ".virgil/task_breakdown_template.md"
	taskBreakdownTasksDir     = ".virgil/tasks"
)

const defaultTaskBreakdownTemplate = `# <Project Or Migration Name> Tasks

Source: <source document, issue, or high-level request>
Generated: <YYYY-MM-DD>

## Rules

- Execute one task at a time.
- Do not implement tasks not explicitly selected.
- Prefer minimal edits.
- Follow each task's Edit Steps one at a time.
- Prefer edits under 40 inserted lines.
- User runs full runtime tests unless the task says otherwise.

## Task <AREA-001>: <short task title>

Status: todo
Priority: P1
Scope: <one method, helper, file section, or investigation>
Depends on: <optional task id or none>
Manual test: pending
Edit budget: prefer <= 40 inserted lines per edit

### Objective

<One or two sentences describing the desired behavior or investigation result.>

### References

- Source implementation: <path>, <symbol or line range>
- Existing pattern: <path>, <symbol or line range>
- Related helper: <path>, <symbol or line range>

### Edit Targets

- <path>

### Edit Steps

1. Inspect the current target state around the intended insertion point or changed symbol.
2. Insert or adjust a small skeleton.
3. Fill one branch, mode, or helper.
4. Run the narrowest useful validation.
5. Update only this task's status/manual-test line when appropriate.

### Completion Criteria

- <observable implementation-side condition>
- <existing behavior that must not change>
- <validation command or syntax check passes>

### Out of Scope

- <nearby task that must not be implemented here>
- <refactor that should not be included>

### Manual Test Notes

- <what the user will test manually, or "none">
`

var (
	taskHeadingPattern = regexp.MustCompile(`^##+\s+Task\s+([^\s:]+)\s*:\s*(.+?)\s*$`)
	taskStatusPattern  = regexp.MustCompile(`^Status:\s*(.+?)\s*$`)
	taskDependsPattern = regexp.MustCompile(`^Depends on:\s*(.+?)\s*$`)
)

func ensureTaskBreakdownTemplate(workspaceRoot string) (string, error) {
	path, err := resolveTaskDocPath(workspaceRoot, taskBreakdownTemplatePath)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(defaultTaskBreakdownTemplate), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func defaultBreakdownOutputPath(source string) string {
	base := strings.TrimSpace(source)
	if base == "" {
		base = "task_breakdown"
	} else {
		base = filepath.Base(filepath.Clean(base))
		base = strings.TrimSuffix(base, filepath.Ext(base))
	}
	slug := sanitizeBreakdownSlug(base)
	if slug == "" {
		slug = "task_breakdown"
	}
	return filepath.ToSlash(filepath.Join(taskBreakdownTasksDir, slug+"_tasks.md"))
}

func sanitizeBreakdownSlug(value string) string {
	var b strings.Builder
	lastSep := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastSep = false
		case r == '-' || r == '_':
			if b.Len() > 0 && !lastSep {
				b.WriteRune(r)
				lastSep = true
			}
		default:
			if b.Len() > 0 && !lastSep {
				b.WriteByte('_')
				lastSep = true
			}
		}
	}
	slug := strings.Trim(b.String(), "_-")
	if len(slug) > 80 {
		slug = strings.Trim(slug[:80], "_-")
	}
	return slug
}

func ensureBreakdownOutputPath(workspaceRoot, pathArg string) (string, error) {
	path, err := resolveTaskDocPath(workspaceRoot, pathArg)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, nil
}

func loadTaskBreakdown(workspaceRoot, pathArg string) (string, []breakdownTask, error) {
	if strings.TrimSpace(pathArg) == "" {
		return "", nil, fmt.Errorf("task document path is required")
	}
	path, err := resolveTaskDocPath(workspaceRoot, pathArg)
	if err != nil {
		return "", nil, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	tasks := parseTaskBreakdown(string(content))
	if len(tasks) == 0 {
		return path, nil, fmt.Errorf("no task blocks found; expected headings like '## Task AREA-001: title'")
	}
	return path, tasks, nil
}

func resolveTaskDocPath(workspaceRoot, pathArg string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(pathArg))
	if clean == "." || clean == "" {
		return "", fmt.Errorf("task document path is required")
	}
	absRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(clean) {
		clean = trimWorkspaceBasePrefix(absRoot, clean)
		clean = filepath.Join(absRoot, clean)
	}
	absPath, err := filepath.Abs(clean)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("task document must be inside the workspace: %s", pathArg)
	}
	return absPath, nil
}

func trimWorkspaceBasePrefix(absRoot, pathArg string) string {
	rootBase := filepath.Base(absRoot)
	if rootBase == "" || rootBase == "." || rootBase == string(filepath.Separator) {
		return pathArg
	}
	prefix := rootBase + string(filepath.Separator)
	if strings.HasPrefix(pathArg, prefix) {
		return strings.TrimPrefix(pathArg, prefix)
	}
	return pathArg
}

func parseTaskBreakdown(content string) []breakdownTask {
	lines := strings.Split(content, "\n")
	var tasks []breakdownTask
	for i, line := range lines {
		match := taskHeadingPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		if len(tasks) > 0 {
			prev := &tasks[len(tasks)-1]
			prev.EndLine = i
			prev.Block = strings.Join(lines[prev.StartLine-1:i], "\n")
		}
		tasks = append(tasks, breakdownTask{
			ID:        match[1],
			Title:     match[2],
			Status:    "todo",
			StartLine: i + 1,
		})
	}
	if len(tasks) == 0 {
		return nil
	}
	last := &tasks[len(tasks)-1]
	last.EndLine = len(lines)
	last.Block = strings.Join(lines[last.StartLine-1:], "\n")

	for i := range tasks {
		for _, line := range strings.Split(tasks[i].Block, "\n") {
			match := taskStatusPattern.FindStringSubmatch(line)
			if match != nil {
				tasks[i].Status = strings.TrimSpace(match[1])
				continue
			}
			match = taskDependsPattern.FindStringSubmatch(line)
			if match != nil {
				tasks[i].DependsOn = parseDependsOn(match[1])
			}
		}
	}
	return tasks
}

func parseDependsOn(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "none") || value == "-" {
		return nil
	}
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	var deps []string
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			deps = append(deps, field)
		}
	}
	return deps
}

func formatTaskList(path string, tasks []breakdownTask) string {
	var b strings.Builder
	b.WriteString("Tasks: ")
	b.WriteString(path)
	b.WriteString("\n\n")
	for _, task := range tasks {
		fmt.Fprintf(&b, "%s %s %s", taskListMarker(task.Status), task.ID, task.Title)
		if task.Status != "" && task.Status != "todo" {
			fmt.Fprintf(&b, " (%s)", task.Status)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func taskListMarker(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "done":
		return "[x]"
	case "doing":
		return "[~]"
	case "blocked":
		return "[!]"
	case "skipped":
		return "[-]"
	case "done-pending-user-test":
		return "[test]"
	default:
		return "[ ]"
	}
}

func findBreakdownTask(tasks []breakdownTask, id string) (breakdownTask, bool) {
	for _, task := range tasks {
		if task.ID == id {
			return task, true
		}
	}
	return breakdownTask{}, false
}

func buildDoTaskPrompt(path string, task breakdownTask, dependencyWarnings []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `Execute exactly one task from this Virgil task breakdown document.

Task document: %s
Task ID: %s
Task title: %s

Hard constraints:
- Execute only this task.
- Do not start any other task ID.
- If this task creates or updates a plan, design document, or migration policy, do not write concrete implementation code unless the task explicitly asks for code.
- For plan, design document, or migration policy tasks, focus on phases, affected files/modules, risks, decisions, validation strategy, migration order, and open questions.
- For long Markdown documents, create or preserve a heading skeleton first, then append one bounded section at a time.
- First inspect the current edit target around the intended insertion point or changed symbol.
- Read only the References listed in the task unless blocked.
- Follow the task's Edit Steps one at a time.
- Do not make a single full-method or full-class edit when the task can be split.
- Prefer edits under 40 inserted lines.
- If a large edit is rejected, immediately regenerate a smaller fresh edit; do not retry the rejected payload.
- If previous partial changes are present, repair only obvious damage needed for this task.
- Do not update the task document automatically in this P1 flow.
- In the final report, suggest the next status: blocked, done-pending-user-test, or done.
- If implementation is done but user testing remains, suggest done-pending-user-test instead of done.
- Do not update unrelated task statuses.
`, path, task.ID, task.Title)
	if len(dependencyWarnings) > 0 {
		b.WriteString("\nDependency warning:\n")
		for _, warning := range dependencyWarnings {
			b.WriteString("- ")
			b.WriteString(warning)
			b.WriteString("\n")
		}
		b.WriteString("Proceed only if the task can still be handled safely; otherwise report blocked.\n")
	}
	b.WriteString("\nTask block:\n")
	b.WriteString(task.Block)
	return b.String()
}

func dependencyWarnings(task breakdownTask, tasks []breakdownTask) []string {
	if len(task.DependsOn) == 0 {
		return nil
	}
	byID := make(map[string]breakdownTask, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	var warnings []string
	for _, dep := range task.DependsOn {
		depTask, ok := byID[dep]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("dependency %s is not present in the task document", dep))
			continue
		}
		if strings.ToLower(strings.TrimSpace(depTask.Status)) != "done" {
			warnings = append(warnings, fmt.Sprintf("dependency %s status is %s, not done", dep, depTask.Status))
		}
	}
	return warnings
}

func parseBreakdownCommand(input string) (breakdownCommand, error) {
	input = strings.TrimSpace(input)
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return breakdownCommand{}, fmt.Errorf("source document or task description is required")
	}
	body := strings.TrimSpace(input[len(parts[0]):])
	if body == "" {
		return breakdownCommand{}, fmt.Errorf("source document or task description is required")
	}
	const outputFlag = " --output "
	if idx := strings.LastIndex(body, outputFlag); idx >= 0 {
		source := strings.TrimSpace(body[:idx])
		output := strings.TrimSpace(body[idx+len(outputFlag):])
		if source == "" || output == "" {
			return breakdownCommand{}, fmt.Errorf("usage: /breakdown <source> --output <task_document.md>")
		}
		if strings.Contains(output, " ") {
			return breakdownCommand{}, fmt.Errorf("output path must not contain spaces")
		}
		return breakdownCommand{Source: source, Output: output}, nil
	}
	return breakdownCommand{Source: body}, nil
}

func buildBreakdownPrompt(cmd breakdownCommand) string {
	output := strings.TrimSpace(cmd.Output)
	if output == "" {
		output = defaultBreakdownOutputPath(cmd.Source)
	}

	var b strings.Builder
	b.WriteString(`Create a Virgil task breakdown document for the following work.

Source material:
`)
	b.WriteString(cmd.Source)
	b.WriteString(`

Rules:
- Output Markdown only.
- Use the task schema from .virgil/task_breakdown_template.md.
- The canonical schema is embedded below; do not search docs/ or .virgil/ for other task templates.
- If the source material is a Markdown file, inspect it with get_markdown_outline first, then read only relevant sections.
- Do not call read_file on a Markdown source without a narrow line range.
- Split feature-level work into small tasks.
- Prefer one method, one helper, one loader/saver path, or one runtime error per task.
- For every code task, include Edit Steps.
- Edit Steps must be small enough that each edit can be done in under 40 inserted lines.
- Include References, Edit Targets, Completion Criteria, and Out of Scope for every task.
- Include Depends on when a task requires another task to be done first.
- Use Status: todo for new tasks.
- If the user will run runtime tests, include Manual test: pending.
- Do not include company-specific names unless they are already present in the requested source material.
- For plan, design document, or migration policy work, do not include concrete implementation code unless the source explicitly asks for code.
- For long Markdown deliverables, split work into skeleton creation and bounded section-append tasks.
- Do not edit implementation files.

Task schema:
`)
	b.WriteString(defaultTaskBreakdownTemplate)
	b.WriteString(`
`)
	b.WriteString(`
Output:
- Write the task breakdown Markdown to this workspace-relative path: `)
	b.WriteString(output)
	b.WriteString(`
- Use write_file only for that output path.
- Do not modify any other file.
- In the final report, mention the output path and the number of tasks created.
`)
	return b.String()
}

func updateTaskStatus(workspaceRoot, pathArg, taskID, status string) (string, string, error) {
	status = strings.TrimSpace(status)
	if !isValidTaskStatus(status) {
		return "", "", fmt.Errorf("invalid status %q; expected todo, doing, done-pending-user-test, done, blocked, or skipped", status)
	}
	path, err := resolveTaskDocPath(workspaceRoot, pathArg)
	if err != nil {
		return "", "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	content := string(data)
	tasks := parseTaskBreakdown(content)
	task, ok := findBreakdownTask(tasks, taskID)
	if !ok {
		return "", "", fmt.Errorf("task %s not found in %s", taskID, path)
	}
	lines := strings.Split(content, "\n")
	start := task.StartLine - 1
	end := task.EndLine
	for i := start; i < end && i < len(lines); i++ {
		match := taskStatusPattern.FindStringSubmatch(lines[i])
		if match == nil {
			continue
		}
		old := strings.TrimSpace(match[1])
		lines[i] = "Status: " + status
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			return "", "", err
		}
		return path, old, nil
	}
	return "", "", fmt.Errorf("task %s has no Status line", taskID)
}

func isValidTaskStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "todo", "doing", "done-pending-user-test", "done", "blocked", "skipped":
		return true
	default:
		return false
	}
}
