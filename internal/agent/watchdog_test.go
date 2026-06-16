package agent

import (
	"strings"
	"testing"
)

func TestWatchdog_LoopDetection(t *testing.T) {
	config := DefaultWatchdogConfig()
	config.MaxRepeatCalls = 3
	w := NewWatchdog(config)

	tool := "read_file"
	args := []byte(`{"path": "main.go"}`)

	// 1回目
	if signal := w.RecordToolCall(tool, args); signal != nil {
		t.Errorf("unexpected signal on 1st call: %v", signal)
	}

	// 2回目
	if signal := w.RecordToolCall(tool, args); signal != nil {
		t.Errorf("unexpected signal on 2nd call: %v", signal)
	}

	// 3回目 -> 停止
	signal := w.RecordToolCall(tool, args)
	if signal == nil {
		t.Fatal("expected signal on 3rd identical call, got nil")
	}
	if signal.Reason != StopReasonLoopDetected {
		t.Errorf("expected reason %s, got %s", StopReasonLoopDetected, signal.Reason)
	}
}

func TestWatchdog_NoFalsePositive(t *testing.T) {
	config := DefaultWatchdogConfig()
	config.MaxRepeatCalls = 3
	w := NewWatchdog(config)

	tool := "read_file"

	// 1回目
	w.RecordToolCall(tool, []byte(`{"path": "a.go"}`))
	// 2回目 (違う引数)
	w.RecordToolCall(tool, []byte(`{"path": "b.go"}`))
	// 3回目 (最初の引数と同じだが連続していない)
	signal := w.RecordToolCall(tool, []byte(`{"path": "a.go"}`))

	if signal != nil {
		t.Errorf("unexpected signal on non-consecutive identical call: %v", signal)
	}
}

func TestWatchdog_RepeatedToolFailureDetection(t *testing.T) {
	config := DefaultWatchdogConfig()
	config.MaxRepeatFailures = 2
	w := NewWatchdog(config)

	tool := "get_file_outline"
	args := []byte(`{"path":"missing.go"}`)
	errMsg := "failed to stat file"

	if signal := w.RecordToolFailure(tool, args, errMsg); signal != nil {
		t.Errorf("unexpected signal on 1st failure: %v", signal)
	}

	signal := w.RecordToolFailure(tool, []byte(`{ "path" : "missing.go" }`), errMsg)
	if signal == nil {
		t.Fatal("expected signal on 2nd identical failure, got nil")
	}
	if signal.Reason != StopReasonLoopDetected {
		t.Errorf("expected reason %s, got %s", StopReasonLoopDetected, signal.Reason)
	}
	if signal.Detail == "" {
		t.Fatal("expected detail to be populated")
	}
}

func TestWatchdog_RepeatedToolFailureNoFalsePositive(t *testing.T) {
	config := DefaultWatchdogConfig()
	config.MaxRepeatFailures = 2
	w := NewWatchdog(config)

	w.RecordToolFailure("read_file", []byte(`{"path":"a.go"}`), "missing")
	signal := w.RecordToolFailure("read_file", []byte(`{"path":"a.go"}`), "permission denied")
	if signal != nil {
		t.Fatalf("unexpected signal for different failure: %v", signal)
	}
}

func TestWatchdog_EmptyResponse(t *testing.T) {
	config := DefaultWatchdogConfig()
	config.MaxEmptyResponses = 2
	w := NewWatchdog(config)

	// 1回目
	if signal := w.RecordEmptyResponse(); signal != nil {
		t.Errorf("unexpected signal on 1st empty response: %v", signal)
	}

	// 途中で正常レスポンス -> リセット
	w.ResetEmptyCount()

	// 再び1回目
	if signal := w.RecordEmptyResponse(); signal != nil {
		t.Errorf("unexpected signal on 1st empty response after reset: %v", signal)
	}

	// 2回目 -> 停止
	signal := w.RecordEmptyResponse()
	if signal == nil {
		t.Fatal("expected signal on 2nd empty response, got nil")
	}
	if signal.Reason != StopReasonEmptyResponse {
		t.Errorf("expected reason %s, got %s", StopReasonEmptyResponse, signal.Reason)
	}
}

func TestWatchdog_ContextLimit(t *testing.T) {
	config := DefaultWatchdogConfig()
	config.ContextTokenLimit = 1000
	w := NewWatchdog(config)

	// 閾値以下
	if signal := w.CheckContextSize(500); signal != nil {
		t.Errorf("unexpected signal for context size 500: %v", signal)
	}

	// 閾値ちょうど (OKとする)
	if signal := w.CheckContextSize(1000); signal != nil {
		t.Errorf("unexpected signal for context size 1000: %v", signal)
	}

	// 閾値超過
	signal := w.CheckContextSize(1001)
	if signal == nil {
		t.Fatal("expected signal for context size 1001, got nil")
	}
	if signal.Reason != StopReasonContextLimit {
		t.Errorf("expected reason %s, got %s", StopReasonContextLimit, signal.Reason)
	}
	if !strings.Contains(signal.Detail, "context overflow risk") {
		t.Fatalf("detail should mention context overflow risk, got %q", signal.Detail)
	}
}
