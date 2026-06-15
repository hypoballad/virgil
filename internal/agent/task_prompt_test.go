package agent

import (
	"strings"
	"testing"
)

func TestTaskTemplateSystemPromptContainsKeyInstructions(t *testing.T) {
	keywords := []string{
		"TODO リスト",
		"結果報告",
		"edit_file",
		"ワークスペースルート",
		"スコープ制御",
		"追加探索をせず最終報告",
		"検証成功後",
		"具体的な実装コードを書かない",
		"見出しスケルトン",
		"章ごとに追記",
		"ユーザー確認待ち",
		"宣言文だけで停止しない",
	}
	for _, kw := range keywords {
		if !strings.Contains(taskTemplateSystemPrompt, kw) {
			t.Errorf("taskTemplateSystemPrompt should contain %q", kw)
		}
	}
}

func TestSystemPromptClarificationMustBeExplicitQuestion(t *testing.T) {
	required := []string{
		"waiting for user confirmation",
		"end with a question mark",
		"Do not stop after a declarative sentence",
		"If no confirmation is needed",
	}

	for _, want := range required {
		if !strings.Contains(SystemPromptDefault, want) {
			t.Fatalf("SystemPromptDefault is missing confirmation clarity guidance %q", want)
		}
	}
}

func TestSystemPromptEditModeVerificationInvariants(t *testing.T) {
	required := []string{
		"Completion requirements",
		"you MUST run it before finishing",
		"After modifying files, run the narrowest relevant test or build command",
		"When tests fail, use the Failure summary from run_tests",
		"When tests pass, stop calling exploratory tools",
		"Do not tell the user to run a required verification command themselves",
	}

	for _, want := range required {
		if !strings.Contains(SystemPromptModeEdit, want) {
			t.Fatalf("SystemPromptModeEdit is missing required invariant %q", want)
		}
	}
}

func TestSystemPromptMentionsIndexedDocsInTreeSitterTools(t *testing.T) {
	required := []string{
		"find_symbol",
		"get_file_outline",
		"read_symbol",
		"indexed docstring/leading-comment",
		"Use search_text only when searching arbitrary doc/comment text",
	}

	for _, want := range required {
		if !strings.Contains(SystemPromptDefault, want) {
			t.Fatalf("SystemPromptDefault is missing doc/comment tool guidance %q", want)
		}
	}
}

func TestSystemPromptMentionsWorkspaceSpecsConvention(t *testing.T) {
	required := []string{
		"Workspace Specifications",
		".virgil/SPECS/",
		"list_files on \".virgil/SPECS/\"",
		"get_markdown_outline before reading a spec",
		"read_markdown_section to read only relevant sections",
		"Do not create or edit specs unless the user explicitly asks",
	}

	for _, want := range required {
		if !strings.Contains(SystemPromptDefault, want) {
			t.Fatalf("SystemPromptDefault is missing workspace specs guidance %q", want)
		}
	}
}

func TestSystemPromptMentionsPlanningDocumentConstraints(t *testing.T) {
	required := []string{
		"PLANNING / DESIGN DOCUMENT tasks",
		"Do not include concrete implementation code",
		"first create a heading skeleton",
		"one bounded section at a time",
	}

	for _, want := range required {
		if !strings.Contains(SystemPromptDefault, want) {
			t.Fatalf("SystemPromptDefault is missing planning document guidance %q", want)
		}
	}
}

func TestIsIncompleteTaskTemplateResponseVariations(t *testing.T) {
	tests := []struct {
		content string
		want    bool
	}{
		{"", false},
		{"  ## 結果報告  ", false},
		{"TODO リスト\\n- [ ] Do something", true},
		{"Some random text with TODO", false},
		{"TODO リスト\\n[ ] Do something", true},
		{"TODO リスト\\nDo something", false},
	}
	for _, tt := range tests {
		if got := isIncompleteTaskTemplateResponse(tt.content); got != tt.want {
			t.Fatalf("isIncompleteTaskTemplateResponse(%q) = %v; want %v", tt.content, got, tt.want)
		}
	}
}
