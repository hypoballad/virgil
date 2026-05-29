package tools

import (
	"strings"
	"testing"

	"github.com/hypoballad/virgil/internal/repository"
)

func TestFormatCallersResult_NoMatches(t *testing.T) {
	output := FormatCallersResult("Foo", nil, 30)
	if !strings.Contains(output, "No callers found") {
		t.Errorf("expected 'No callers found' in output, got:\n%s", output)
	}
}

func TestFormatCallersResult_WithMatches(t *testing.T) {
	records := []repository.CallRecord{
		{
			CallerFile:     "internal/agent/agent.go",
			CallerName:     "Run",
			CallerReceiver: "*Agent",
			CalleeName:     "emitProgress",
			CallLine:       295,
		},
		{
			CallerFile:     "internal/agent/agent.go",
			CallerName:     "escalate",
			CallerReceiver: "*Agent",
			CalleeName:     "emitProgress",
			CallLine:       620,
		},
	}

	output := FormatCallersResult("emitProgress", records, 30)

	expectedSubstrings := []string{
		"Callers of `emitProgress`",
		"Found 2 caller",
		"agent.go",
		"295",
		"620",
		"Run",
		"escalate",
		"*Agent",
		"Next steps:",
	}

	for _, expected := range expectedSubstrings {
		if !strings.Contains(output, expected) {
			t.Errorf("expected output to contain %q, got:\n%s", expected, output)
		}
	}
}

func TestFormatCallersResult_GlobalCaller(t *testing.T) {
	records := []repository.CallRecord{
		{
			CallerFile: "main.go",
			CallerName: "<global>",
			CalleeName: "initSetup",
			CallLine:   10,
		},
	}

	output := FormatCallersResult("initSetup", records, 30)
	if !strings.Contains(output, "global init") {
		t.Errorf("expected '<global>' to be rendered as readable, got:\n%s", output)
	}
}
