package main

import "testing"

func TestParseStartupArgsDangerousVMax(t *testing.T) {
	args, vmax, fullPower := parseStartupArgs([]string{"--dangerous-vmax", "small"})
	if !vmax {
		t.Fatal("dangerous vmax should be enabled")
	}
	if fullPower {
		t.Fatal("fullpower should be disabled")
	}
	if len(args) != 1 || args[0] != "small" {
		t.Fatalf("args = %#v", args)
	}
}

func TestParseStartupArgsNoVMax(t *testing.T) {
	args, vmax, fullPower := parseStartupArgs([]string{"default"})
	if vmax {
		t.Fatal("dangerous vmax should be disabled")
	}
	if fullPower {
		t.Fatal("fullpower should be disabled")
	}
	if len(args) != 1 || args[0] != "default" {
		t.Fatalf("args = %#v", args)
	}
}

func TestParseStartupArgsFullPower(t *testing.T) {
	args, vmax, fullPower := parseStartupArgs([]string{"fullpower", "small", "--dangerous-vmax"})
	if !vmax {
		t.Fatal("dangerous vmax should be enabled")
	}
	if !fullPower {
		t.Fatal("fullpower should be enabled")
	}
	if len(args) != 1 || args[0] != "small" {
		t.Fatalf("args = %#v", args)
	}
}
