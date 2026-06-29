package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetMarkdownOutlineTool(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "plan.md"), strings.Join([]string{
		"# Plan",
		"",
		"intro",
		"## Phase 1",
		"details",
		"```",
		"# Not A Heading",
		"```",
		"### Step A",
		"more details",
		"## Phase 2",
		"later",
	}, "\n"))

	tool := NewGetMarkdownOutlineTool(root)
	result, err := tool.Execute(context.Background(), mustJSONArgs(t, map[string]interface{}{
		"path":      "plan.md",
		"max_depth": 2,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	for _, want := range []string{"Markdown outline: plan.md", "1-12", "Plan", "4-10", "Phase 1", "11-12", "Phase 2"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("outline missing %q:\n%s", want, result.Content)
		}
	}
	if strings.Contains(result.Content, "Not A Heading") || strings.Contains(result.Content, "Step A") {
		t.Fatalf("outline should skip fenced headings and max_depth headings:\n%s", result.Content)
	}
}

func TestReadMarkdownSectionToolByHeading(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "plan.md"), strings.Join([]string{
		"# Plan",
		"intro",
		"## Phase 1",
		"details",
		"## Phase 2",
		"later",
	}, "\n"))

	tool := NewReadMarkdownSectionTool(root)
	result, err := tool.Execute(context.Background(), mustJSONArgs(t, map[string]interface{}{
		"path":    "plan.md",
		"heading": "phase 1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "   3 | ## Phase 1") || !strings.Contains(result.Content, "   4 | details") {
		t.Fatalf("section missing expected lines:\n%s", result.Content)
	}
	if strings.Contains(result.Content, "Phase 2") {
		t.Fatalf("section should stop before next heading:\n%s", result.Content)
	}
}

func TestReadMarkdownSectionToolHeadingNotFoundListsAvailableHeadings(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "plan.md"), strings.Join([]string{
		"# Plan",
		"intro",
		"## Phase 1",
		"details",
		"## Phase 2",
		"later",
	}, "\n"))

	tool := NewReadMarkdownSectionTool(root)
	result, err := tool.Execute(context.Background(), mustJSONArgs(t, map[string]interface{}{
		"path":    "plan.md",
		"heading": "Missing",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("expected error result, got: %s", result.Content)
	}
	for _, want := range []string{
		`heading "Missing" not found`,
		"Available headings:",
		"Plan (lines 1-6)",
		"Phase 1 (lines 3-4)",
		"Use one of these exact headings",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("error missing %q:\n%s", want, result.Content)
		}
	}
}

func TestReadMarkdownSectionToolTruncatesMaxLines(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "notes.md"), strings.Join([]string{
		"# Notes",
		"one",
		"two",
		"three",
	}, "\n"))

	tool := NewReadMarkdownSectionTool(root)
	result, err := tool.Execute(context.Background(), mustJSONArgs(t, map[string]interface{}{
		"path":       "notes.md",
		"start_line": 1,
		"end_line":   4,
		"max_lines":  2,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "... [truncated by max_lines]") {
		t.Fatalf("expected max_lines truncation, got:\n%s", result.Content)
	}
}
