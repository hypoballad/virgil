package agent

import (
	"strings"
	"testing"
)

func TestParseStructuredResponse_ValidJSON(t *testing.T) {
	input := `{"summary":"auth.py修正完了","confidence":"high","requested_action":"continue"}`
	resp := ParseStructuredResponse(input)
	if resp.Summary != "auth.py修正完了" {
		t.Errorf("Summary = %q, want %q", resp.Summary, "auth.py修正完了")
	}
	if resp.Confidence != ConfidenceHigh {
		t.Errorf("Confidence = %q, want %q", resp.Confidence, ConfidenceHigh)
	}
	if resp.RequestedAction != ActionContinue {
		t.Errorf("RequestedAction = %q, want %q", resp.RequestedAction, ActionContinue)
	}
}

func TestParseStructuredResponse_MarkdownWrapped(t *testing.T) {
	input := "Here is my response:\n```json\n{\"summary\":\"done\",\"confidence\":\"high\",\"requested_action\":\"continue\"}\n```"
	resp := ParseStructuredResponse(input)
	if resp.Summary != "done" {
		t.Errorf("Summary = %q, want %q", resp.Summary, "done")
	}
	if resp.PrefixText != "Here is my response:" {
		t.Errorf("PrefixText = %q, want %q", resp.PrefixText, "Here is my response:")
	}
}

func TestParseStructuredResponse_MixedText(t *testing.T) {
	input := `I have completed the task. {"summary":"task done","confidence":"medium","requested_action":"continue"} That's all.`
	resp := ParseStructuredResponse(input)
	if resp.Summary != "task done" {
		t.Errorf("Summary = %q, want %q", resp.Summary, "task done")
	}
}

func TestParseStructuredResponse_Freetext(t *testing.T) {
	input := "I fixed the bug in auth.py. The login function now validates tokens correctly."
	resp := ParseStructuredResponse(input)
	if resp.Summary != input {
		t.Errorf("Summary should be original text on fallback")
	}
	if resp.Confidence != ConfidenceMedium {
		t.Errorf("Confidence should default to medium on fallback")
	}
	if resp.RequestedAction != ActionContinue {
		t.Errorf("RequestedAction should default to continue on fallback")
	}
}

func TestParseStructuredResponse_EmptySummary(t *testing.T) {
	input := `{"confidence":"high","requested_action":"continue"}`
	resp := ParseStructuredResponse(input)
	if resp.Summary != input {
		t.Errorf("Should fallback when summary is empty")
	}
}

func TestParseStructuredResponse_AskUser(t *testing.T) {
	input := `{"summary":"テストが通らない","confidence":"low","requested_action":"ask_user","blocker":"assertion error","question_to_user":"期待される戻り値は？"}`
	resp := ParseStructuredResponse(input)
	if resp.RequestedAction != ActionAskUser {
		t.Errorf("RequestedAction = %q, want %q", resp.RequestedAction, ActionAskUser)
	}
	if resp.Blocker != "assertion error" {
		t.Errorf("Blocker = %q, want %q", resp.Blocker, "assertion error")
	}
	if resp.QuestionToUser != "期待される戻り値は？" {
		t.Errorf("QuestionToUser = %q, want %q", resp.QuestionToUser, "期待される戻り値は？")
	}
}

func TestParseStructuredResponse_UnknownValues(t *testing.T) {
	input := `{"summary":"done","confidence":"very_high","requested_action":"maybe"}`
	resp := ParseStructuredResponse(input)
	if resp.Confidence != ConfidenceMedium {
		t.Errorf("Unknown confidence should normalize to medium, got %q", resp.Confidence)
	}
	if resp.RequestedAction != ActionContinue {
		t.Errorf("Unknown action should normalize to continue, got %q", resp.RequestedAction)
	}
}

// probeのTest5で確認されたOllamaの実際の出力パターン
func TestParseStructuredResponse_ProbeTest5Format(t *testing.T) {
	input := `{"confidence":"medium","requested_action":"ask_user","summary":"I don't have access to the auth.py file yet.","question_to_user":"Could you please share the contents of auth.py?"}`
	resp := ParseStructuredResponse(input)
	if resp.Confidence != ConfidenceMedium {
		t.Errorf("Confidence = %q, want medium", resp.Confidence)
	}
	if resp.RequestedAction != ActionAskUser {
		t.Errorf("RequestedAction = %q, want ask_user", resp.RequestedAction)
	}
	if resp.QuestionToUser == "" {
		t.Error("QuestionToUser should not be empty")
	}
}

func TestParseStructuredResponse_TrailingJSON(t *testing.T) {
	// LLMが詳細テキスト + 末尾JSONで応答するパターン
	input := `ウォッチドッグの仕組みを調査しました。

## 実装の概要

ウォッチドッグは以下の 3 つの機能を実装しています。

**総評**：ウォッチドッグの実装はテストと実装が一致しており、正しく機能しています。
{"summary":"ウォッチドッグは正しく機能しています","confidence":"high","requested_action":"continue"}`

	resp := ParseStructuredResponse(input)

	if resp.Summary != "ウォッチドッグは正しく機能しています" {
		t.Errorf("Summary = %q, want parsed JSON summary", resp.Summary)
	}
	if resp.Confidence != ConfidenceHigh {
		t.Errorf("Confidence = %q, want high", resp.Confidence)
	}
	if resp.PrefixText == "" {
		t.Error("PrefixText should contain the detailed analysis text")
	}
	if !strings.Contains(resp.PrefixText, "ウォッチドッグの仕組み") {
		t.Errorf("PrefixText should contain the analysis, got %q", resp.PrefixText[:50])
	}
}

func TestParseStructuredResponse_TrailingJSON_WithCodeBlocks(t *testing.T) {
	// コードブロック内に { が含まれるケース（先ほどの実際の問題パターン）
	input := "分析結果:\n```go\nif x == \"\" && len(items) == 0 {\n    return nil\n}\n```\n以上です。\n" +
		`{"summary":"分析完了","confidence":"high","requested_action":"continue"}`

	resp := ParseStructuredResponse(input)

	if resp.Summary != "分析完了" {
		t.Errorf("Summary = %q, want '分析完了'", resp.Summary)
	}
	if resp.PrefixText == "" {
		t.Error("PrefixText should not be empty")
	}
	if !strings.Contains(resp.PrefixText, "if x == \"\"") {
		t.Errorf("PrefixText should contain code block, got %q", resp.PrefixText)
	}
}

func TestParseStructuredResponse_PureJSON_NoPrefixText(t *testing.T) {
	// JSONのみの応答ではPrefixTextは空
	input := `{"summary":"完了","confidence":"high","requested_action":"continue"}`
	resp := ParseStructuredResponse(input)

	if resp.PrefixText != "" {
		t.Errorf("PrefixText should be empty for pure JSON, got %q", resp.PrefixText)
	}
	if resp.Summary != "完了" {
		t.Errorf("Summary = %q, want '完了'", resp.Summary)
	}
}
