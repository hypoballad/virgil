package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRunCommandRejectsWithUserFeedback(t *testing.T) {
	cfg := DefaultRunCommandConfig()
	cfg.WorkspaceRoot = t.TempDir()
	cfg.DefaultAction = "confirm"
	cfg.AutoAllow = nil
	cfg.Timeout = time.Second

	tool := NewRunCommandTool(cfg)
	args, err := json.Marshal(runCommandArgs{Command: "python script.py"})
	if err != nil {
		t.Fatal(err)
	}

	resultCh := make(chan *Result, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := tool.Execute(context.Background(), args)
		resultCh <- result
		errCh <- err
	}()

	deadline := time.After(time.Second)
	for tool.PendingConfirmation() == nil {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for pending confirmation")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	tool.SetConfirmationResultWithFeedback(false, "use python -m unittest instead")

	result := <-resultCh
	if err := <-errCh; err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("result = %#v, want error result", result)
	}
	for _, want := range []string{
		`command rejected by user: "python script.py"`,
		"User instruction: use python -m unittest instead",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("result missing %q:\n%s", want, result.Content)
		}
	}
}
