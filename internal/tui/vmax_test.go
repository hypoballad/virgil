package tui

import (
	"testing"

	"github.com/hypoballad/virgil/internal/agent"
)

func TestVMaxConsumeRunOptionsOneShot(t *testing.T) {
	m := Model{vmaxAvailable: true, vmaxArmed: true, contextLimit: 80000}

	opts := m.consumeVMaxRunOptions()
	if opts.MaxIterations != agent.VMaxIterations {
		t.Fatalf("MaxIterations = %d, want %d", opts.MaxIterations, agent.VMaxIterations)
	}
	if !opts.AutoConfirmRunCommand {
		t.Fatal("AutoConfirmRunCommand should be true")
	}
	if !opts.PreflightShrink {
		t.Fatal("PreflightShrink should be true")
	}
	if opts.PreflightShrinkPercent != 45 {
		t.Fatalf("PreflightShrinkPercent = %d, want 45", opts.PreflightShrinkPercent)
	}
	if opts.ContextLimitTokens != 80000 {
		t.Fatalf("ContextLimitTokens = %d, want 80000", opts.ContextLimitTokens)
	}
	if opts.PreflightShrinkCooldownIterations != 5 {
		t.Fatalf("PreflightShrinkCooldownIterations = %d, want 5", opts.PreflightShrinkCooldownIterations)
	}
	if m.vmaxArmed {
		t.Fatal("vmaxArmed should be consumed")
	}
	if !m.vmaxActive {
		t.Fatal("vmaxActive should be true")
	}

	second := m.consumeVMaxRunOptions()
	if second.MaxIterations != 0 || second.AutoConfirmRunCommand {
		t.Fatalf("second consume should be normal options, got %#v", second)
	}
}

func TestVMaxUnavailableDoesNotArmOptions(t *testing.T) {
	m := Model{vmaxAvailable: false, vmaxArmed: true}
	opts := m.consumeVMaxRunOptions()
	if opts.MaxIterations != 0 || opts.AutoConfirmRunCommand {
		t.Fatalf("unavailable vmax should not return options, got %#v", opts)
	}
}
