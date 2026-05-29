package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleTaskBreakdown = `# Example Tasks

## Task RPT-001: Add loader skeleton

Status: todo
Depends on: RPT-000

### Objective

Add the loader method skeleton.

### References

- Source implementation: src/legacy/report.py, ReportLoader.load

### Edit Targets

- src/reporting/report.py

### Edit Steps

1. Inspect the insertion point.
2. Insert a skeleton.

### Completion Criteria

- Syntax remains valid.

## Task RPT-002: Validate runtime path

Status: done-pending-user-test

### Objective

Validate the runtime path.
`

func TestParseTaskBreakdown(t *testing.T) {
	tasks := parseTaskBreakdown(sampleTaskBreakdown)
	if len(tasks) != 2 {
		t.Fatalf("len(tasks) = %d, want 2", len(tasks))
	}
	if tasks[0].ID != "RPT-001" || tasks[0].Title != "Add loader skeleton" {
		t.Fatalf("first task = %#v", tasks[0])
	}
	if tasks[0].Status != "todo" {
		t.Fatalf("first status = %q", tasks[0].Status)
	}
	if !strings.Contains(tasks[0].Block, "### Edit Steps") {
		t.Fatalf("first block missing edit steps:\n%s", tasks[0].Block)
	}
	if tasks[1].Status != "done-pending-user-test" {
		t.Fatalf("second status = %q", tasks[1].Status)
	}
}

func TestFormatTaskList(t *testing.T) {
	tasks := parseTaskBreakdown(sampleTaskBreakdown)
	got := formatTaskList("/tmp/tasks.md", tasks)
	for _, want := range []string{
		"Tasks: /tmp/tasks.md",
		"[ ] RPT-001 Add loader skeleton",
		"[test] RPT-002 Validate runtime path (done-pending-user-test)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted task list missing %q:\n%s", want, got)
		}
	}
}

func TestBuildDoTaskPromptContainsSafetyConstraints(t *testing.T) {
	task := parseTaskBreakdown(sampleTaskBreakdown)[0]
	prompt := buildDoTaskPrompt("/tmp/tasks.md", task, []string{"dependency RPT-000 status is blocked, not done"})
	for _, want := range []string{
		"Task ID: RPT-001",
		"Execute only this task",
		"Follow the task's Edit Steps one at a time",
		"Prefer edits under 40 inserted lines",
		"do not write concrete implementation code",
		"heading skeleton",
		"Do not update the task document automatically in this P1 flow",
		"Dependency warning",
		"### Edit Steps",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestDependencyWarnings(t *testing.T) {
	tasks := parseTaskBreakdown(sampleTaskBreakdown)
	warnings := dependencyWarnings(tasks[0], tasks)
	if len(warnings) != 1 {
		t.Fatalf("len(warnings) = %d, want 1: %#v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "RPT-000") {
		t.Fatalf("warning should mention missing dependency: %#v", warnings)
	}
}

func TestLoadTaskBreakdownRejectsOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "tasks.md")
	if err := os.WriteFile(outside, []byte(sampleTaskBreakdown), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadTaskBreakdown(root, outside); err == nil {
		t.Fatal("expected outside-workspace path to be rejected")
	}
}

func TestResolveTaskDocPathTrimsDuplicatedWorkspaceBasename(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "train")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveTaskDocPath(root, "train/.virgil/tasks/report_tasks.md")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".virgil", "tasks", "report_tasks.md")
	if got != want {
		t.Fatalf("resolved path = %q, want %q", got, want)
	}
}

func TestDoSlashCommandBuildsTaskRequest(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "tasks.md")
	if err := os.WriteFile(path, []byte(sampleTaskBreakdown), 0o644); err != nil {
		t.Fatal(err)
	}
	m := testModel()
	m.workspaceRoot = root

	_, cmd := m.handleSlashCommand("/do RPT-001 tasks.md")
	if cmd == nil {
		t.Fatal("expected /do command")
	}
	msg, ok := cmd().(taskRequestMsg)
	if !ok {
		t.Fatalf("expected taskRequestMsg, got %#v", cmd())
	}
	if msg.display != "/do RPT-001 tasks.md" {
		t.Fatalf("display = %q", msg.display)
	}
	if !strings.Contains(msg.description, "Task ID: RPT-001") {
		t.Fatalf("description missing task block:\n%s", msg.description)
	}
}

func TestDoSlashCommandBuildsFlowTaskRequest(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "tasks.md")
	if err := os.WriteFile(path, []byte(sampleTaskBreakdown), 0o644); err != nil {
		t.Fatal(err)
	}
	m := testModel()
	m.workspaceRoot = root

	_, cmd := m.handleSlashCommand("/do RPT-001 tasks.md --flow")
	if cmd == nil {
		t.Fatal("expected /do --flow command")
	}
	msg, ok := cmd().(taskRequestMsg)
	if !ok {
		t.Fatalf("expected taskRequestMsg, got %#v", cmd())
	}
	if !msg.flow {
		t.Fatal("flow flag should be set")
	}
	if msg.display != "/do RPT-001 tasks.md --flow" {
		t.Fatalf("display = %q", msg.display)
	}
	if !strings.Contains(msg.description, "Flow mode") {
		t.Fatalf("description missing flow instruction:\n%s", msg.description)
	}
}

func TestParseBreakdownCommand(t *testing.T) {
	cmd, err := parseBreakdownCommand("/Breakdown docs/source.md --output docs/tasks.md")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Source != "docs/source.md" || cmd.Output != "docs/tasks.md" {
		t.Fatalf("parsed command = %#v", cmd)
	}

	cmd, err = parseBreakdownCommand("/breakdown migrate the reporting service")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Source != "migrate the reporting service" || cmd.Output != "" {
		t.Fatalf("parsed free-text command = %#v", cmd)
	}
}

func TestBuildBreakdownPromptWithOutput(t *testing.T) {
	prompt := buildBreakdownPrompt(breakdownCommand{
		Source: "docs/source.md",
		Output: "docs/tasks.md",
	})
	for _, want := range []string{
		"Create a Virgil task breakdown document",
		"Use the task schema from .virgil/task_breakdown_template.md",
		"do not search docs/ or .virgil/ for other task templates",
		"If the source material is a Markdown file",
		"## Task <AREA-001>: <short task title>",
		"For every code task, include Edit Steps",
		"do not include concrete implementation code",
		"bounded section-append tasks",
		"Write the task breakdown Markdown to this workspace-relative path: docs/tasks.md",
		"Do not edit implementation files",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildBreakdownPromptWithoutOutputWritesDefaultTaskPath(t *testing.T) {
	prompt := buildBreakdownPrompt(breakdownCommand{
		Source: "docs/source_plan.md",
	})
	for _, want := range []string{
		"Write the task breakdown Markdown to this workspace-relative path: .virgil/tasks/source_plan_tasks.md",
		"Use write_file only for that output path",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "Return the complete task breakdown Markdown in the response") {
		t.Fatalf("prompt should write to default output path:\n%s", prompt)
	}
}

func TestDefaultBreakdownOutputPath(t *testing.T) {
	tests := map[string]string{
		"train/src/plan/AE_pytorch_bug_investigation_report.md": ".virgil/tasks/AE_pytorch_bug_investigation_report_tasks.md",
		"migrate the reporting service":                         ".virgil/tasks/migrate_the_reporting_service_tasks.md",
		"!!!":                                                   ".virgil/tasks/task_breakdown_tasks.md",
	}
	for input, want := range tests {
		if got := defaultBreakdownOutputPath(input); got != want {
			t.Fatalf("defaultBreakdownOutputPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestEnsureBreakdownOutputPathCreatesParentDirectory(t *testing.T) {
	root := t.TempDir()
	path, err := ensureBreakdownOutputPath(root, ".virgil/tasks/source_tasks.md")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".virgil", "tasks", "source_tasks.md")
	if path != want {
		t.Fatalf("output path = %q, want %q", path, want)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || !info.IsDir() {
		t.Fatalf("output directory was not created: info=%v err=%v", info, err)
	}
}

func TestEnsureTaskBreakdownTemplateCreatesFixedTemplate(t *testing.T) {
	root := t.TempDir()
	path, err := ensureTaskBreakdownTemplate(root)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(root, ".virgil", "task_breakdown_template.md")
	if path != wantPath {
		t.Fatalf("template path = %q, want %q", path, wantPath)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "## Task <AREA-001>: <short task title>") {
		t.Fatalf("template missing task schema:\n%s", string(data))
	}
}

func TestEnsureTaskBreakdownTemplateDoesNotOverwriteExistingTemplate(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".virgil", "task_breakdown_template.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("custom template"), 0o644); err != nil {
		t.Fatal(err)
	}
	gotPath, err := ensureTaskBreakdownTemplate(root)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != path {
		t.Fatalf("template path = %q, want %q", gotPath, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "custom template" {
		t.Fatalf("existing template was overwritten:\n%s", string(data))
	}
}

func TestUpdateTaskStatusUpdatesOnlySelectedTask(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "tasks.md")
	if err := os.WriteFile(path, []byte(sampleTaskBreakdown), 0o644); err != nil {
		t.Fatal(err)
	}

	updatedPath, old, err := updateTaskStatus(root, "tasks.md", "RPT-001", "doing")
	if err != nil {
		t.Fatal(err)
	}
	if updatedPath != path {
		t.Fatalf("updated path = %q, want %q", updatedPath, path)
	}
	if old != "todo" {
		t.Fatalf("old status = %q, want todo", old)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "Status: doing") {
		t.Fatalf("updated content missing new status:\n%s", got)
	}
	if !strings.Contains(got, "Status: done-pending-user-test") {
		t.Fatalf("second task status should remain unchanged:\n%s", got)
	}
}

func TestTaskStatusSlashCommand(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "tasks.md")
	if err := os.WriteFile(path, []byte(sampleTaskBreakdown), 0o644); err != nil {
		t.Fatal(err)
	}
	m := testModel()
	m.workspaceRoot = root

	_, cmd := m.handleSlashCommand("/task-status RPT-001 blocked tasks.md")
	if cmd == nil {
		t.Fatal("expected /task-status command")
	}
	_ = cmd()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Status: blocked") {
		t.Fatalf("status not updated:\n%s", string(data))
	}
}
