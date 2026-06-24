package main

import (
	"path/filepath"
	"strings"
	"testing"

	dbpkg "github.com/hypoballad/virgil/internal/db"
	"github.com/hypoballad/virgil/internal/repository"
)

func TestParseStartupArgsDangerousVMax(t *testing.T) {
	args, vmax, fullPower, resumeTarget, forceNewSession := parseStartupArgs([]string{"--dangerous-vmax", "small"})
	if !vmax {
		t.Fatal("dangerous vmax should be enabled")
	}
	if fullPower {
		t.Fatal("fullpower should be disabled")
	}
	if len(args) != 1 || args[0] != "small" {
		t.Fatalf("args = %#v", args)
	}
	if resumeTarget != "" {
		t.Fatalf("resumeTarget = %q, want empty", resumeTarget)
	}
	if forceNewSession {
		t.Fatal("forceNewSession should be disabled")
	}
}

func TestParseStartupArgsNoVMax(t *testing.T) {
	args, vmax, fullPower, resumeTarget, forceNewSession := parseStartupArgs([]string{"default"})
	if vmax {
		t.Fatal("dangerous vmax should be disabled")
	}
	if fullPower {
		t.Fatal("fullpower should be disabled")
	}
	if len(args) != 1 || args[0] != "default" {
		t.Fatalf("args = %#v", args)
	}
	if resumeTarget != "" {
		t.Fatalf("resumeTarget = %q, want empty", resumeTarget)
	}
	if forceNewSession {
		t.Fatal("forceNewSession should be disabled")
	}
}

func TestParseStartupArgsFullPower(t *testing.T) {
	args, vmax, fullPower, resumeTarget, forceNewSession := parseStartupArgs([]string{"fullpower", "small", "--dangerous-vmax"})
	if !vmax {
		t.Fatal("dangerous vmax should be enabled")
	}
	if !fullPower {
		t.Fatal("fullpower should be enabled")
	}
	if len(args) != 1 || args[0] != "small" {
		t.Fatalf("args = %#v", args)
	}
	if resumeTarget != "" {
		t.Fatalf("resumeTarget = %q, want empty", resumeTarget)
	}
	if forceNewSession {
		t.Fatal("forceNewSession should be disabled")
	}
}

func TestParseStartupArgsResume(t *testing.T) {
	args, vmax, fullPower, resumeTarget, forceNewSession := parseStartupArgs([]string{"--resume", "latest", "small", "--dangerous-vmax"})
	if !vmax {
		t.Fatal("dangerous vmax should be enabled")
	}
	if fullPower {
		t.Fatal("fullpower should be disabled")
	}
	if resumeTarget != "latest" {
		t.Fatalf("resumeTarget = %q, want latest", resumeTarget)
	}
	if forceNewSession {
		t.Fatal("forceNewSession should be disabled")
	}
	if len(args) != 1 || args[0] != "small" {
		t.Fatalf("args = %#v", args)
	}
}

func TestParseStartupArgsResumeKeepsProfileArg(t *testing.T) {
	args, _, _, resumeTarget, forceNewSession := parseStartupArgs([]string{"--resume", "small"})
	if resumeTarget != "latest" {
		t.Fatalf("resumeTarget = %q, want latest", resumeTarget)
	}
	if forceNewSession {
		t.Fatal("forceNewSession should be disabled")
	}
	if len(args) != 1 || args[0] != "small" {
		t.Fatalf("args = %#v", args)
	}
}

func TestParseStartupArgsResumeEqualsAndNewSession(t *testing.T) {
	args, _, _, resumeTarget, forceNewSession := parseStartupArgs([]string{"--resume=abc123", "--new-session", "default"})
	if resumeTarget != "" {
		t.Fatalf("resumeTarget = %q, want empty when --new-session is present", resumeTarget)
	}
	if !forceNewSession {
		t.Fatal("forceNewSession should be enabled")
	}
	if len(args) != 1 || args[0] != "default" {
		t.Fatalf("args = %#v", args)
	}
}

func TestInitializeSessionResumeLatestUsesWorkspaceSession(t *testing.T) {
	database, err := dbpkg.New(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repo := repository.New(database)

	workspace := filepath.Join(t.TempDir(), "workspace")
	otherWorkspace := filepath.Join(t.TempDir(), "other")
	other, err := repo.Sessions.Create("model", otherWorkspace, "other")
	if err != nil {
		t.Fatal(err)
	}
	target, err := repo.Sessions.Create("model", workspace, "target")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SqlDB.Exec("UPDATE sessions SET started_at = ? WHERE id = ?", 200, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SqlDB.Exec("UPDATE sessions SET started_at = ? WHERE id = ?", 100, target.ID); err != nil {
		t.Fatal(err)
	}

	turn1, err := repo.Turns.Create(target.ID, 1, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Turns.UpdateTurnResponse(turn1.ID, "hi", "stop", 10, 2, 100); err != nil {
		t.Fatal(err)
	}
	turn2, err := repo.Turns.Create(target.ID, 2, "continue")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Turns.UpdateTurnResponse(turn2.ID, "done", "stop", 11, 3, 120); err != nil {
		t.Fatal(err)
	}

	session, history, turnNumber, resumed, err := initializeSession(repo, "model", workspace, "latest", false)
	if err != nil {
		t.Fatalf("initializeSession error = %v", err)
	}
	if !resumed {
		t.Fatal("resumed = false, want true")
	}
	if session.ID != target.ID {
		t.Fatalf("session ID = %s, want %s", session.ID, target.ID)
	}
	if turnNumber != 2 {
		t.Fatalf("turnNumber = %d, want 2", turnNumber)
	}
	got := make([]string, 0, len(history))
	for _, msg := range history {
		got = append(got, msg.Role+":"+msg.Content)
	}
	want := []string{"user:hello", "assistant:hi", "user:continue", "assistant:done"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("history = %#v, want %#v", got, want)
	}
}

func TestLoadOpenAIParametersFromEnv(t *testing.T) {
	t.Setenv("OPENAI_TEMPERATURE", "0.2")
	t.Setenv("OPENAI_TOP_P", "0.9")
	t.Setenv("OPENAI_MAX_TOKENS", "2048")
	t.Setenv("OPENAI_PRESENCE_PENALTY", "0.1")
	t.Setenv("OPENAI_FREQUENCY_PENALTY", "0.3")
	t.Setenv("OPENAI_STREAM", "false")

	params, err := loadOpenAIParametersFromEnv()
	if err != nil {
		t.Fatalf("loadOpenAIParametersFromEnv() error = %v", err)
	}
	if params.Temperature == nil || *params.Temperature != 0.2 {
		t.Fatalf("Temperature = %v, want 0.2", params.Temperature)
	}
	if params.TopP == nil || *params.TopP != 0.9 {
		t.Fatalf("TopP = %v, want 0.9", params.TopP)
	}
	if params.MaxTokens == nil || *params.MaxTokens != 2048 {
		t.Fatalf("MaxTokens = %v, want 2048", params.MaxTokens)
	}
	if params.PresencePenalty == nil || *params.PresencePenalty != 0.1 {
		t.Fatalf("PresencePenalty = %v, want 0.1", params.PresencePenalty)
	}
	if params.FrequencyPenalty == nil || *params.FrequencyPenalty != 0.3 {
		t.Fatalf("FrequencyPenalty = %v, want 0.3", params.FrequencyPenalty)
	}
	if !params.DisableStream {
		t.Fatal("DisableStream = false, want true")
	}
}

func TestLoadOpenAIParametersFromEnvInvalid(t *testing.T) {
	t.Setenv("OPENAI_TEMPERATURE", "warm")

	_, err := loadOpenAIParametersFromEnv()
	if err == nil {
		t.Fatal("loadOpenAIParametersFromEnv() succeeded, want error")
	}
	if !strings.Contains(err.Error(), "OPENAI_TEMPERATURE") {
		t.Fatalf("error = %v, want OPENAI_TEMPERATURE context", err)
	}
}

func TestLoadOpenAIParametersFromEnvInvalidStream(t *testing.T) {
	t.Setenv("OPENAI_STREAM", "maybe")

	_, err := loadOpenAIParametersFromEnv()
	if err == nil {
		t.Fatal("loadOpenAIParametersFromEnv() succeeded, want error")
	}
	if !strings.Contains(err.Error(), "OPENAI_STREAM") {
		t.Fatalf("error = %v, want OPENAI_STREAM context", err)
	}
}
