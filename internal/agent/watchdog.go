package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// StopReason はウォッチドッグが停止を判断した理由
type StopReason string

const (
	StopReasonLoopDetected  StopReason = "loop_detected"
	StopReasonEmptyResponse StopReason = "empty_response"
	StopReasonContextLimit  StopReason = "context_limit"
)

// StopSignal はウォッチドッグからの停止指示
type StopSignal struct {
	Reason StopReason
	Detail string // 人間が読める説明（例: "read_file called 3 times with same args"）
}

// WatchdogConfig はウォッチドッグの設定
type WatchdogConfig struct {
	MaxRepeatCalls    int // 同一ツール+同一引数の連続呼び出し上限（デフォルト: 3）
	MaxRepeatFailures int // 同一ツール+同一引数+同一エラーの連続失敗上限（デフォルト: 2）
	MaxEmptyResponses int // 空レスポンス連続上限（デフォルト: 2）
	ContextTokenLimit int // コンテキストトークン上限（デフォルト: 12000）
}

// DefaultWatchdogConfig はデフォルト設定を返す
// ContextTokenLimit は qwen3.5:4b (num_ctx 16384) 向けの値。
// 本番環境 (qwen3.5:27b, num_ctx 131072) では環境変数
// VIRGIL_WATCHDOG_CONTEXT_LIMIT で上書きすること。
func DefaultWatchdogConfig() WatchdogConfig {
	return WatchdogConfig{
		MaxRepeatCalls:    3,
		MaxRepeatFailures: 2,
		MaxEmptyResponses: 2,
		ContextTokenLimit: 12000,
	}
}

// Watchdog はエージェントループの暴走を検出する
type Watchdog struct {
	config        WatchdogConfig
	callHashes    []string // 直近のツール呼び出しハッシュ履歴
	failureHashes []string // 直近の失敗したツール結果ハッシュ履歴
	emptyCount    int      // 空レスポンスの連続カウント
}

// NewWatchdog は新しいWatchdogを生成する
func NewWatchdog(config WatchdogConfig) *Watchdog {
	return &Watchdog{
		config:        config,
		callHashes:    make([]string, 0),
		failureHashes: make([]string, 0),
	}
}

// callHash はツール名+引数からハッシュを生成する
func callHash(toolName string, args []byte) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%s", toolName, normalizeJSON(args))))
	return fmt.Sprintf("%x", h[:8]) // 先頭16文字で十分
}

func failureHash(toolName string, args []byte, failure string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s", toolName, normalizeJSON(args), failure)))
	return fmt.Sprintf("%x", h[:8]) // 先頭16文字で十分
}

func normalizeJSON(data []byte) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err == nil {
		return buf.String()
	}
	return string(data)
}

// RecordToolCall はツール呼び出しを記録し、ループを検出する
// ループ検出した場合は StopSignal を返す
func (w *Watchdog) RecordToolCall(toolName string, args []byte) *StopSignal {
	hash := callHash(toolName, args)
	w.callHashes = append(w.callHashes, hash)

	// 直近N件が全て同一かチェック
	n := w.config.MaxRepeatCalls
	if len(w.callHashes) >= n {
		tail := w.callHashes[len(w.callHashes)-n:]
		allSame := true
		for _, h := range tail {
			if h != tail[0] {
				allSame = false
				break
			}
		}
		if allSame {
			return &StopSignal{
				Reason: StopReasonLoopDetected,
				Detail: fmt.Sprintf("tool %q called %d times with identical arguments", toolName, n),
			}
		}
	}
	return nil
}

// RecordToolFailure は失敗したツール結果を記録し、同一失敗の繰り返しを検出する。
func (w *Watchdog) RecordToolFailure(toolName string, args []byte, failure string) *StopSignal {
	hash := failureHash(toolName, args, failure)
	w.failureHashes = append(w.failureHashes, hash)

	n := w.config.MaxRepeatFailures
	if n <= 0 {
		return nil
	}
	if len(w.failureHashes) >= n {
		tail := w.failureHashes[len(w.failureHashes)-n:]
		allSame := true
		for _, h := range tail {
			if h != tail[0] {
				allSame = false
				break
			}
		}
		if allSame {
			return &StopSignal{
				Reason: StopReasonLoopDetected,
				Detail: fmt.Sprintf("tool %q failed %d times with identical arguments and error: %s", toolName, n, failure),
			}
		}
	}
	return nil
}

// RecordEmptyResponse は空レスポンスを記録する
// 連続上限に達した場合は StopSignal を返す
func (w *Watchdog) RecordEmptyResponse() *StopSignal {
	w.emptyCount++
	if w.emptyCount >= w.config.MaxEmptyResponses {
		return &StopSignal{
			Reason: StopReasonEmptyResponse,
			Detail: fmt.Sprintf("%d consecutive empty responses from LLM", w.emptyCount),
		}
	}
	return nil
}

// ResetEmptyCount は空レスポンスカウントをリセットする
// 正常なレスポンスを受信したら呼ぶ
func (w *Watchdog) ResetEmptyCount() {
	w.emptyCount = 0
}

// CheckContextSize はコンテキストのトークン数をチェックする
// 超過した場合は StopSignal を返す
func (w *Watchdog) CheckContextSize(tokenCount int) *StopSignal {
	if tokenCount > w.config.ContextTokenLimit {
		return &StopSignal{
			Reason: StopReasonContextLimit,
			Detail: fmt.Sprintf("context overflow risk: context size %d tokens exceeds limit %d", tokenCount, w.config.ContextTokenLimit),
		}
	}
	return nil
}
