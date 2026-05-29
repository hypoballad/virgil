package tools

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/hypoballad/virgil/internal/db"
	"github.com/hypoballad/virgil/internal/repository"
	"github.com/hypoballad/virgil/internal/symbols"
)

func TestMermaidID_AlphanumericPassthrough(t *testing.T) {
	if got := mermaidID("Foo123"); got != "Foo123" {
		t.Errorf("got %q, want Foo123", got)
	}
}

func TestMermaidID_SpecialCharsReplaced(t *testing.T) {
	if got := mermaidID("*Agent.Run"); got != "_Agent_Run" {
		t.Errorf("got %q, want _Agent_Run", got)
	}
}

func TestMermaidID_Empty(t *testing.T) {
	if got := mermaidID(""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestCallNode_Display(t *testing.T) {
	tests := []struct {
		node callNode
		want string
	}{
		{callNode{Name: "Run"}, "Run"},
		{callNode{Name: "Run", Receiver: "*Agent"}, "*Agent.Run"},
	}
	for _, tt := range tests {
		if got := tt.node.Display(); got != tt.want {
			t.Errorf("Display() = %q, want %q", got, tt.want)
		}
	}
}

func TestNormalizeCallGraphDepth(t *testing.T) {
	tests := []struct {
		depth int
		want  int
	}{
		{0, callGraphDefaultDepth},
		{-1, callGraphDefaultDepth},
		{2, 2},
		{99, callGraphMaxDepth},
	}
	for _, tt := range tests {
		if got := normalizeCallGraphDepth(tt.depth); got != tt.want {
			t.Errorf("normalizeCallGraphDepth(%d) = %d, want %d", tt.depth, got, tt.want)
		}
	}
}

func TestBuildCallGraphReport_WithRepository(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatalf("db.New failed: %v", err)
	}
	defer database.Close()

	calls := repository.NewCallRepository(database)
	graph := &symbols.FileCallGraph{
		FilePath: "internal/agent/agent.go",
		Language: "go",
		Calls: []symbols.CallEdge{
			{CallerName: "Run", CalleeName: "zeta", CallLine: 10},
			{CallerName: "Run", CalleeName: "alpha", CallLine: 11},
			{CallerName: "alpha", CalleeName: "leaf", CallLine: 20},
		},
	}
	if err := calls.UpsertFile(graph.FilePath, graph); err != nil {
		t.Fatalf("UpsertFile failed: %v", err)
	}

	output := BuildCallGraphReport(calls, "Run", 2)
	for _, expected := range []string{
		"Call Graph from `Run`",
		"```mermaid",
		"graph TD",
		"Run[\"Run\"] --> alpha[\"alpha\"]",
		"Run[\"Run\"] --> zeta[\"zeta\"]",
		"alpha[\"alpha\"] --> leaf[\"leaf\"]",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected output to contain %q, got:\n%s", expected, output)
		}
	}

	alphaIdx := strings.Index(output, "Run[\"Run\"] --> alpha[\"alpha\"]")
	zetaIdx := strings.Index(output, "Run[\"Run\"] --> zeta[\"zeta\"]")
	if alphaIdx < 0 || zetaIdx < 0 || alphaIdx > zetaIdx {
		t.Fatalf("expected deterministic sorted edges, got:\n%s", output)
	}
}
