package tui

import (
	"time"

	"github.com/hypoballad/virgil/internal/agent"
	"github.com/hypoballad/virgil/internal/llm"
	"github.com/hypoballad/virgil/internal/shadow"
)

type quitConfirmTimeoutMsg struct{}

type llmResponseMsg struct {
	response         string
	err              error
	duration         time.Duration
	promptTokens     int
	completionTokens int
}

type agentResponseMsg struct {
	response *agent.Response
	err      error
}

// rewindRequestMsg は /rewind コマンドが入力された時のメッセージ
type rewindRequestMsg struct {
	target string // 番号 or hash prefix、空なら一覧表示
}

// rewindConfirmMsg は /confirm が入力された時のメッセージ
type rewindConfirmMsg struct{}

// clearSessionRequestMsg は /clear が入力された時のメッセージ
type clearSessionRequestMsg struct{}

type reindexRequestMsg struct {
	force bool
}

type taskRequestMsg struct {
	description string
	display     string
}

type agentTaskResponseMsg struct {
	description string
	response    *agent.Response
	err         error
}

// rewindCompleteMsg は rewind 実行完了時のメッセージ
type rewindCompleteMsg struct {
	success    bool
	targetHash string
	err        error
}

// historyDisplayMsg は履歴表示用のメッセージ
type historyDisplayMsg struct {
	content string
}

// rewindPreparedMsg は rewind 準備完了（確認待ち）のメッセージ
type rewindPreparedMsg struct {
	hash    string
	message string
	diff    *shadow.DiffSummary
}

// progressMsg は Agent からの進捗通知を TUI のメッセージとしてラップする
// agent.ProgressEvent をそのまま含む
type progressMsg struct {
	event agent.ProgressEvent
}

// btwRequestMsg は /btw コマンドが入力された時のメッセージ
type btwRequestMsg struct {
	question string
}

// agentBtwResponseMsg は RunBtw の結果を TUI に返すメッセージ
type agentBtwResponseMsg struct {
	question string
	response *agent.Response
	err      error
}

// shrinkCompleteMsg は /shrink による履歴圧縮の結果を返す
type shrinkCompleteMsg struct {
	summary            string
	newHistory         []llm.Message
	summarizedMessages int
	keptMessages       int
	saved              bool
	auto               bool
	beforeTokens       int
	beforePercent      int
	err                error
}

// runCommandConfirmRequestMsg は run_command が確認待ちになった時のメッセージ
type runCommandConfirmRequestMsg struct {
	command string
	dir     string
}

// runCommandConfirmMsg はユーザーが /confirm-run と入力した時のメッセージ
type runCommandConfirmMsg struct {
	approved bool
	feedback string
}
