package main

import "testing"

func TestParseStartupArgsDangerousVMax(t *testing.T) {
	args, vmax := parseStartupArgs([]string{"--dangerous-vmax", "small"})
	if !vmax {
		t.Fatal("dangerous vmax should be enabled")
	}
	if len(args) != 1 || args[0] != "small" {
		t.Fatalf("args = %#v", args)
	}
}

func TestParseStartupArgsNoVMax(t *testing.T) {
	args, vmax := parseStartupArgs([]string{"default"})
	if vmax {
		t.Fatal("dangerous vmax should be disabled")
	}
	if len(args) != 1 || args[0] != "default" {
		t.Fatalf("args = %#v", args)
	}
}
