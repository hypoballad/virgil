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

func TestRunOptionsWithPreflightDefaultsEnablesNormalPreflight(t *testing.T) {
	m := Model{contextLimit: 76000}
	opts := m.runOptionsWithPreflightDefaults(agent.RunOptions{})

	if !opts.PreflightShrink {
		t.Fatal("normal run should use preflight shrink")
	}
	if opts.ContextLimitTokens != 76000 {
		t.Fatalf("ContextLimitTokens = %d, want 76000", opts.ContextLimitTokens)
	}
	if opts.PreflightShrinkPercent != 45 {
		t.Fatalf("PreflightShrinkPercent = %d, want 45", opts.PreflightShrinkPercent)
	}
	if opts.PreflightShrinkCooldownIterations != 5 {
		t.Fatalf("PreflightShrinkCooldownIterations = %d, want 5", opts.PreflightShrinkCooldownIterations)
	}
}

func TestRunOptionsWithPreflightDefaultsPreservesExplicitValues(t *testing.T) {
	m := Model{contextLimit: 76000}
	opts := m.runOptionsWithPreflightDefaults(agent.RunOptions{
		MaxIterations:                     agent.VMaxIterations,
		AutoConfirmRunCommand:             true,
		PreflightShrink:                   true,
		ContextLimitTokens:                80000,
		PreflightShrinkPercent:            40,
		PreflightShrinkCooldownIterations: 3,
	})

	if opts.ContextLimitTokens != 80000 || opts.PreflightShrinkPercent != 40 || opts.PreflightShrinkCooldownIterations != 3 {
		t.Fatalf("explicit preflight values should be preserved, got %#v", opts)
	}
	if opts.MaxIterations != agent.VMaxIterations || !opts.AutoConfirmRunCommand {
		t.Fatalf("non-preflight options changed: %#v", opts)
	}
}

func TestVMaxUnavailableDoesNotArmOptions(t *testing.T) {
	m := Model{vmaxAvailable: false, vmaxArmed: true}
	opts := m.consumeVMaxRunOptions()
	if opts.MaxIterations != 0 || opts.AutoConfirmRunCommand {
		t.Fatalf("unavailable vmax should not return options, got %#v", opts)
	}
}

func TestVMaxAutoOffClearsStaleArmedState(t *testing.T) {
	m := Model{vmaxArmed: true, currentRunMaxIterations: agent.VMaxIterations}

	cmd := m.vmaxAutoOffCommand()
	if cmd != nil {
		t.Fatal("stale armed state should be cleared silently")
	}
	if m.vmaxArmed || m.vmaxActive {
		t.Fatalf("vmax state should be cleared, armed=%v active=%v", m.vmaxArmed, m.vmaxActive)
	}
	if m.currentRunMaxIterations != agent.MaxIterations {
		t.Fatalf("currentRunMaxIterations = %d, want %d", m.currentRunMaxIterations, agent.MaxIterations)
	}
}

func TestVMaxAutoOffClearsActiveState(t *testing.T) {
	m := Model{vmaxActive: true, currentRunMaxIterations: agent.VMaxIterations}

	cmd := m.vmaxAutoOffCommand()
	if cmd == nil {
		t.Fatal("active state should return an auto-off message command")
	}
	if m.vmaxArmed || m.vmaxActive {
		t.Fatalf("vmax state should be cleared, armed=%v active=%v", m.vmaxArmed, m.vmaxActive)
	}
	if m.currentRunMaxIterations != agent.MaxIterations {
		t.Fatalf("currentRunMaxIterations = %d, want %d", m.currentRunMaxIterations, agent.MaxIterations)
	}
}
