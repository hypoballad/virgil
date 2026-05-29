package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// RunCommandConfig は run_command の設定
type RunCommandConfig struct {
	// AutoAllow に該当するコマンドは確認なしで実行
	// 各要素はコマンドのプレフィックスマッチ
	AutoAllow []string

	// Deny に該当するコマンドは完全にブロック
	Deny []string

	// DefaultAction は AutoAllow / Deny のいずれにも該当しない場合の動作
	// "auto": 確認なしで実行
	// "confirm": ユーザー確認を求める（推奨）
	// "deny": 拒否
	DefaultAction string

	// AllowOutsideWorkspace が true なら、ワークスペース外でも実行可能
	AllowOutsideWorkspace bool

	// Timeout はコマンド実行のタイムアウト
	Timeout time.Duration

	// MaxOutputBytes は出力の最大サイズ
	MaxOutputBytes int

	// WorkspaceRoot はワークスペースのルートパス
	WorkspaceRoot string
}

// DefaultRunCommandConfig はデフォルト設定を返す
func DefaultRunCommandConfig() RunCommandConfig {
	return RunCommandConfig{
		AutoAllow: []string{
			// Go 関連
			"go test", "go build", "go vet", "go fmt", "go mod tidy",
			"gofmt", "gofumpt", "goimports",
			"golangci-lint",
			// Git の読み取り系
			"git status", "git diff", "git log", "git show", "git branch",
			"git ls-files", "git remote -v",
			// ファイルシステム読み取り
			"ls", "pwd", "cat", "head", "tail", "wc", "find . -name",
			// その他
			"echo",
		},
		Deny: []string{
			`\brm\s+-rf\b`,
			`\bsudo\b`,
			`\bgit\s+push\b`,
			`\bgit\s+reset\s+--hard\b`,
			`\bgit\s+clean\s+-fdx\b`,
			`\bcurl\b`,
			`\bwget\b`,
			`:(){:|:&};:`, // fork bomb
		},
		DefaultAction:         "confirm",
		AllowOutsideWorkspace: false,
		Timeout:               30 * time.Second,
		MaxOutputBytes:        100 * 1024, // 100KB
	}
}

// CommandAction はコマンドに対する判定結果
type CommandAction string

const (
	ActionAuto    CommandAction = "auto"
	ActionConfirm CommandAction = "confirm"
	ActionDeny    CommandAction = "deny"
)

// ClassifyCommand はコマンドの実行可否を判定する
func (c RunCommandConfig) ClassifyCommand(cmd string) CommandAction {
	cmd = strings.TrimSpace(cmd)

	// 1. Deny に該当するか（正規表現）
	for _, pattern := range c.Deny {
		if matched, _ := regexp.MatchString(pattern, cmd); matched {
			return ActionDeny
		}
	}

	// 2. AutoAllow に該当するか
	for _, prefix := range c.AutoAllow {
		if strings.HasPrefix(cmd, prefix) {
			// シェル演算子が含まれている場合は安全のため確認を求める
			if strings.ContainsAny(cmd, ";&|$><`") {
				return ActionConfirm
			}
			return ActionAuto
		}
	}

	// 3. デフォルト動作
	switch c.DefaultAction {
	case "auto":
		return ActionAuto
	case "deny":
		return ActionDeny
	default:
		return ActionConfirm
	}
}

// RunCommandTool は run_command ツールの実装
type RunCommandTool struct {
	config      RunCommandConfig
	autoConfirm bool

	// 確認モード用: nil でなければ確認待ち状態
	// Agent 側から SetPendingConfirmation で設定し、ユーザー応答後に Continue を呼ぶ
	pendingConfirmation *PendingCommand
}

// PendingCommand は確認待ち状態のコマンド
type PendingCommand struct {
	Command   string
	Dir       string
	Confirmed bool          // ユーザーが承認したか
	Feedback  string        // 拒否時にユーザーが入力した代替指示
	ResultCh  chan struct{} // ユーザー応答後の通知用
}

func NewRunCommandTool(config RunCommandConfig) *RunCommandTool {
	return &RunCommandTool{
		config: config,
	}
}

func (t *RunCommandTool) Name() string {
	return "run_command"
}

func (t *RunCommandTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        "run_command",
			Description: "Execute a shell command in the workspace. Read-only commands like 'go test', 'git status' run automatically. Other commands may require user confirmation. Has a 30s timeout and 100KB output limit.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{
						"type":        "string",
						"description": "Shell command to execute. Examples: 'go test ./...', 'git status', 'ls -la'",
					},
					"dir": map[string]interface{}{
						"type":        "string",
						"description": "Optional working directory (relative to workspace root). Default: workspace root.",
					},
				},
				"required": []string{"command"},
			},
		},
	}
}

// IsMutating: run_command は基本的に書き込み系として扱う
// プランモードでブロックされるため、調査時は read_file/search_text を使う
func (t *RunCommandTool) IsMutating() bool {
	return true
}

type runCommandArgs struct {
	Command string `json:"command"`
	Dir     string `json:"dir,omitempty"`
}

// PendingConfirmation は確認待ち中のコマンドを返す
// nil なら確認待ちなし
func (t *RunCommandTool) PendingConfirmation() *PendingCommand {
	return t.pendingConfirmation
}

// SetConfirmationResult はユーザーの確認結果をセットする
// pending.Confirmed = true なら実行を続行、false ならブロック
func (t *RunCommandTool) SetConfirmationResult(confirmed bool) {
	t.SetConfirmationResultWithFeedback(confirmed, "")
}

// SetConfirmationResultWithFeedback はユーザーの確認結果と任意の拒否理由をセットする。
func (t *RunCommandTool) SetConfirmationResultWithFeedback(confirmed bool, feedback string) {
	if t.pendingConfirmation == nil {
		return
	}
	t.pendingConfirmation.Confirmed = confirmed
	t.pendingConfirmation.Feedback = strings.TrimSpace(feedback)
	close(t.pendingConfirmation.ResultCh)
}

// Config returns the current configuration.
func (t *RunCommandTool) Config() RunCommandConfig {
	return t.config
}

func (t *RunCommandTool) AutoConfirm() bool {
	return t.autoConfirm
}

func (t *RunCommandTool) SetAutoConfirm(enabled bool) {
	t.autoConfirm = enabled
}

func (t *RunCommandTool) Execute(ctx context.Context, argsJSON json.RawMessage) (*Result, error) {
	var args runCommandArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	if strings.TrimSpace(args.Command) == "" {
		return ErrorResult("command is empty"), nil
	}

	// 作業ディレクトリの解決
	workDir := t.config.WorkspaceRoot
	if args.Dir != "" {
		if filepath.IsAbs(args.Dir) {
			if !t.config.AllowOutsideWorkspace {
				return ErrorResult(fmt.Sprintf(
					"absolute path not allowed: %s. Set VIRGIL_RUN_ALLOW_OUTSIDE_WORKSPACE=true to permit.",
					args.Dir)), nil
			}
			workDir = args.Dir
		} else {
			workDir = filepath.Join(t.config.WorkspaceRoot, args.Dir)
		}
	}

	// コマンドの分類
	action := t.config.ClassifyCommand(args.Command)

	switch action {
	case ActionDeny:
		return ErrorResult(fmt.Sprintf(
			"command blocked by VIRGIL_RUN_DENY: %q",
			args.Command)), nil

	case ActionConfirm:
		if t.autoConfirm {
			break
		}
		// 確認待ち状態にして、ユーザーの応答を待つ
		t.pendingConfirmation = &PendingCommand{
			Command:  args.Command,
			Dir:      workDir,
			ResultCh: make(chan struct{}),
		}

		// ユーザー応答 or タイムアウトを待つ
		select {
		case <-t.pendingConfirmation.ResultCh:
			confirmed := t.pendingConfirmation.Confirmed
			feedback := t.pendingConfirmation.Feedback
			t.pendingConfirmation = nil
			if !confirmed {
				if feedback != "" {
					return ErrorResult(fmt.Sprintf(
						"command rejected by user: %q\nUser instruction: %s",
						args.Command, feedback)), nil
				}
				return ErrorResult(fmt.Sprintf(
					"command rejected by user: %q",
					args.Command)), nil
			}
		case <-ctx.Done():
			t.pendingConfirmation = nil
			return ErrorResult("command confirmation timed out"), nil
		}
		// 承認されたので続行

	case ActionAuto:
		// そのまま実行
	}

	// コマンド実行
	return t.executeCommand(ctx, args.Command, workDir)
}

func (t *RunCommandTool) executeCommand(ctx context.Context, command, workDir string) (*Result, error) {
	execCtx, cancel := context.WithTimeout(ctx, t.config.Timeout)
	defer cancel()

	// sh -c で実行（パイプやリダイレクトをサポート）
	cmd := exec.CommandContext(execCtx, "sh", "-c", command)
	cmd.Dir = workDir
	// 環境変数は親プロセス（Virgil）のものを継承（os.Environ()がデフォルト）

	output, err := cmd.CombinedOutput()

	// タイムアウト判定
	if execCtx.Err() == context.DeadlineExceeded {
		return ErrorResult(fmt.Sprintf(
			"command timed out after %v: %q",
			t.config.Timeout, command)), nil
	}

	// 出力サイズ制限
	truncated := false
	if len(output) > t.config.MaxOutputBytes {
		output = output[:t.config.MaxOutputBytes]
		truncated = true
	}

	// 結果の組み立て
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("$ %s\n", command))
	if workDir != "" {
		sb.WriteString(fmt.Sprintf("(in %s)\n", workDir))
	}
	sb.WriteString(string(output))
	if truncated {
		sb.WriteString(fmt.Sprintf("\n[output truncated at %d bytes]", t.config.MaxOutputBytes))
	}

	if err != nil {
		// コマンドが非ゼロ終了
		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		sb.WriteString(fmt.Sprintf("\n[exit code: %d]", exitCode))
		return &Result{
			IsError: true,
			Content: sb.String(),
		}, nil
	}

	sb.WriteString("\n[exit code: 0]")
	return &Result{
		IsError: false,
		Content: sb.String(),
	}, nil
}
