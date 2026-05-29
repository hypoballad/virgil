package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hypoballad/virgil/internal/repository"
)

func TestFindSymbol_EmptyName(t *testing.T) {
	tool := &FindSymbolTool{symbols: nil}

	args, _ := json.Marshal(map[string]string{
		"name": "",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if !result.IsError {
		t.Error("expected IsError=true for empty name")
	}
}

func TestFormatFindSymbolResults_NoMatches(t *testing.T) {
	output := formatFindSymbolResults("Foo", findSymbolFilters{}, nil, 20)

	if !strings.Contains(output, "No matching symbols found") {
		t.Errorf("expected 'No matching symbols found' in output, got:\n%s", output)
	}
}

func TestFormatFindSymbolResults_WithMatches(t *testing.T) {
	records := []repository.SymbolRecord{
		{
			FilePath:  "internal/agent/agent.go",
			Name:      "Run",
			Type:      "method",
			Receiver:  "*Agent",
			Signature: "func (a *Agent) Run(ctx context.Context) error",
			Doc:       "Run starts the agent loop.",
			StartLine: 286,
		},
		{
			FilePath:   "internal/agent/agent.go",
			Name:       "RunPlan",
			Type:       "method",
			Receiver:   "*Agent",
			Signature:  "func (a *Agent) RunPlan(ctx context.Context) error",
			StartLine:  647,
			IsFallback: true,
		},
	}

	output := formatFindSymbolResults("Run", findSymbolFilters{
		Type:       "method",
		Receiver:   "*Agent",
		HasFilters: true,
	}, records, 20)

	expectedSubstrings := []string{
		"Symbol search: \"Run\"",
		`type="method"`,
		`receiver="*Agent"`,
		"Found 2 match",
		"agent.go",
		"286",
		"647",
		"*Agent",
		"method (via fallback)",
		"Run starts the agent loop.",
		"Next steps:",
	}

	for _, expected := range expectedSubstrings {
		if !strings.Contains(output, expected) {
			t.Errorf("expected output to contain %q, got:\n%s", expected, output)
		}
	}
}

func TestFindSymbolFilters(t *testing.T) {
	filters := newFindSymbolFilters(findSymbolArgs{
		Type:         "Method",
		Receiver:     "myAE",
		FilePath:     "train/src/AE.py",
		FallbackOnly: true,
	})
	if !filters.HasFilters {
		t.Fatal("expected HasFilters=true")
	}
	if filters.Type != "method" {
		t.Fatalf("Type = %q, want method", filters.Type)
	}
	formatted := formatFindSymbolFilters(filters)
	expected := []string{
		`type="method"`,
		`receiver="myAE"`,
		`file_path="train/src/AE.py"`,
		"fallback_only=true",
	}
	for _, want := range expected {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatFindSymbolFilters() missing %q in %q", want, formatted)
		}
	}
}
