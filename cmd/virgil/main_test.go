package main

import (
	"strings"
	"testing"
)

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
