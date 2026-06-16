package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hypoballad/virgil/internal/db"
	"github.com/hypoballad/virgil/internal/llm"
	"github.com/hypoballad/virgil/internal/repository"
)

func TestDogfoodExportCreatesExpectedFiles(t *testing.T) {
	repo, closeFn := newDogfoodTestRepo(t)
	defer closeFn()
	fakeOpenAIKey := "sk-" + "1234567890abcdef1234567890abcdef"
	sessionID := createDogfoodExchange(t, repo, "API_KEY="+fakeOpenAIKey)

	logPath := filepath.Join(t.TempDir(), "debug.log")
	if err := os.WriteFile(logPath, []byte("debug\nagent error: llm chat failed: ollama error\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "dogfood")
	result, err := exportDogfoodReport(repo, dogfoodExportOptions{
		Session:  sessionID,
		Exchange: "latest",
		OutDir:   out,
		LogPath:  logPath,
		LogLines: 20,
		now:      func() time.Time { return time.Date(2026, 5, 22, 1, 2, 3, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("exportDogfoodReport error = %v", err)
	}

	for _, name := range []string{"report.md", "issue_body.md", "sanitized_context.json", "context_summary.json", "debug_tail.log", "scan_report.json", "README.md"} {
		if _, err := os.Stat(filepath.Join(result.OutputDir, name)); err != nil {
			t.Fatalf("expected %s: %v", name, err)
		}
	}
	if !strings.HasSuffix(result.OutputDir, filepath.Join("dogfood", "2026-05-22-010203")) {
		t.Fatalf("output dir = %q", result.OutputDir)
	}
}

func TestDogfoodExportDoesNotWriteRawContext(t *testing.T) {
	repo, closeFn := newDogfoodTestRepo(t)
	defer closeFn()
	sessionID := createDogfoodExchange(t, repo, "Authorization: Bearer abcdefghijklmnopqrstuvwxyz in train/src/AE_pytorch.py")

	logPath := filepath.Join(t.TempDir(), "debug.log")
	if err := os.WriteFile(logPath, []byte("contact admin@example.internal at 10.1.2.3 while reading train/src/AE.py\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := exportDogfoodReport(repo, dogfoodExportOptions{
		Session: sessionID,
		OutDir:  filepath.Join(t.TempDir(), "dogfood"),
		LogPath: logPath,
		now:     func() time.Time { return time.Date(2026, 5, 22, 1, 2, 3, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("exportDogfoodReport error = %v", err)
	}

	for _, name := range []string{"sanitized_context.json", "context_summary.json", "debug_tail.log", "report.md", "issue_body.md"} {
		data, err := os.ReadFile(filepath.Join(result.OutputDir, name))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, forbidden := range []string{"Bearer abcdefghijklmnopqrstuvwxyz", "admin@example.internal", "10.1.2.3", "train/src/AE.py", "train/src/AE_pytorch.py"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s contains raw sensitive value %q:\n%s", name, forbidden, text)
			}
		}
	}
}

func TestSecretScanDetectsCommonSecrets(t *testing.T) {
	fakeGitHubToken := "ghp_" + "abcdefghijklmnopqrstuvwxyz123456"
	fakeOpenAIKey := "sk-" + "1234567890abcdef1234567890abcdef"
	text := strings.Join([]string{
		"token=" + fakeGitHubToken,
		"OPENAI_API_KEY=" + fakeOpenAIKey,
		"email admin@example.com",
		"host service.example.internal",
		"ip 192.168.1.10",
	}, "\n")

	findings := scanTextForSecrets("sample.log", text, nil, nil)
	if len(findings) < 5 {
		t.Fatalf("findings = %#v, want at least 5", findings)
	}
}

func TestSecretScanAllowPatternsSuppressFindings(t *testing.T) {
	allow := []*regexp.Regexp{regexp.MustCompile(`email`)}
	findings := scanTextForSecrets("sample.log", "admin@example.com", allow, nil)
	if len(findings) != 0 {
		t.Fatalf("findings = %#v, want none", findings)
	}
}

func TestSecretScanDenyPatternsAddFindings(t *testing.T) {
	deny := []*regexp.Regexp{regexp.MustCompile(`ProjectSecretName`)}
	findings := scanTextForSecrets("sample.log", "ProjectSecretName", nil, deny)
	if len(findings) != 1 || findings[0].Detector != "custom-deny" {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestIssueBodyIncludesErrorSnippet(t *testing.T) {
	body := renderDogfoodIssueBody(dogfoodContextSummary{PromptTokens: 10}, secretScanReport{}, "llm chat failed: XML syntax error")
	if !strings.Contains(body, "llm chat failed") || !strings.Contains(body, "XML syntax error") {
		t.Fatalf("issue body missing error snippet:\n%s", body)
	}
}

func TestDogfoodErrorSnippetIncludesContextOverflow(t *testing.T) {
	snippet := extractDogfoodErrorSnippet("debug\nagent: watchdog stop: context_limit - context overflow risk: context size 70000 tokens exceeds limit 65536\nnext")
	if !strings.Contains(snippet, "context overflow risk") {
		t.Fatalf("snippet missing context overflow line:\n%s", snippet)
	}
}

func newDogfoodTestRepo(t *testing.T) (*repository.Repository, func()) {
	t.Helper()
	database, err := db.New(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatalf("db.New error = %v", err)
	}
	return repository.New(database), func() { _ = database.Close() }
}

func createDogfoodExchange(t *testing.T, repo *repository.Repository, toolContent string) string {
	t.Helper()
	session, err := repo.Sessions.Create("model", "/tmp/workspace", "dogfood")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	turn, err := repo.Turns.Create(session.ID, 1, "please inspect")
	if err != nil {
		t.Fatalf("create turn: %v", err)
	}
	messages := []llm.Message{
		{Role: "user", Content: "please inspect /tmp/workspace/internal/agent/agent.go"},
		{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{{
				ID: "call_1",
				Function: llm.FunctionCall{
					Name:      "read_file",
					Arguments: map[string]interface{}{"path": "/tmp/workspace/internal/agent/agent.go"},
				},
			}},
		},
		{Role: "tool", ToolCallID: "call_1", Content: toolContent},
	}
	rawMessages, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.LLMExchanges.Create(repository.LLMExchangeRecord{
		TurnID:          turn.ID,
		Iteration:       1,
		RequestMessages: string(rawMessages),
		RequestTools:    `[{"function":{"name":"read_file"}}]`,
		PromptTokens:    100,
	})
	if err != nil {
		t.Fatalf("create exchange: %v", err)
	}
	return session.ID
}
