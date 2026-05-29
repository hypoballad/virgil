package shadow

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	// シャドウ git リポジトリのパス（プロジェクトルート相対）
	ShadowDirName = ".virgil/shadow"

	// メッセージプレフィックス
	PreCommitPrefix  = "pre-tool"
	PostCommitPrefix = "post-tool"
)

// ShadowRepo はシャドウ git リポジトリを管理します。
// workspaceRoot: 対象とするプロジェクトのルートディレクトリ
// shadowDir: .agent/shadow のフルパス
type ShadowRepo struct {
	workspaceRoot string // プロジェクトのワーキングツリールート
	shadowDir     string // .agent/shadow のフルパス
}

// DiffSummary はファイル変更のサマリーを表します。
type DiffSummary struct {
	AddedFiles    []string // rewind で削除されるファイル（現在追加されている）
	DeletedFiles  []string // rewind で復活するファイル（現在削除されている）
	ModifiedFiles []string // rewind で変更されるファイル
}

// New は ShadowRepo を作成します。
// workspaceRoot は対象とするプロジェクトのルートディレクトリ
//
// 戻り値: ShadowRepo 参照、error - 作成に失敗した場合
func New(workspaceRoot string) (*ShadowRepo, error) {
	abs, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}

	// 実際の git dir は .git サブディレクトリ
	shadowDir := filepath.Join(abs, ShadowDirName, ".git")

	return &ShadowRepo{
		workspaceRoot: abs,
		shadowDir:     shadowDir,
	}, nil
}

// Init はシャドウ git リポジトリを初期化します。
// workspaceRoot: 対象とするプロジェクトのルートディレクトリ
//
// 既に存在する場合は何もしません。
// 戻り値: error - 初期化に失敗した場合
func (r *ShadowRepo) Init(ctx context.Context) error {
	// .git ディレクトリが存在するかチェック
	if _, err := os.Stat(r.shadowDir); err == nil {
		return nil // 既に初期化済み
	}

	// 親ディレクトリ（.agent/shadow）を作成
	repoRoot := filepath.Dir(r.shadowDir)
	if err := os.MkdirAll(repoRoot, 0755); err != nil {
		return fmt.Errorf("create shadow repo root: %w", err)
	}

	// git init (normal repo, but in a custom dir)
	cmd := exec.CommandContext(ctx, "git", "init", repoRoot)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git init failed: %w (output: %s)", err, string(output))
	}

	// 初期 user 設定（ローカル）
	if err := r.runGit(ctx, "config", "user.name", "virgil-shadow"); err != nil {
		return fmt.Errorf("set user.name: %w", err)
	}
	if err := r.runGit(ctx, "config", "user.email", "virgil@localhost"); err != nil {
		return fmt.Errorf("set user.email: %w", err)
	}

	// 初期コミット（空の tree で）
	if _, err := r.commitAll(ctx, "initial state"); err != nil {
		return fmt.Errorf("initial commit: %w", err)
	}

	return nil
}

// CommitPre はツール実行前のスナップショットを作成します。
// toolName: トールの名前
//
// 戻り値: string - コミット hash, error - コミットに失敗した場合
func (r *ShadowRepo) CommitPre(ctx context.Context, toolName string) (string, error) {
	msg := fmt.Sprintf("%s: %s", PreCommitPrefix, toolName)
	hash, err := r.commitAll(ctx, msg)
	if err != nil {
		return "", err
	}
	return hash, nil
}

// CommitPost はツール実行後のスナップショットを作成します。
// toolName: トールの名前
//
// 戻り値: string - コミット hash, error - コミットに失敗した場合
func (r *ShadowRepo) CommitPost(ctx context.Context, toolName string) (string, error) {
	msg := fmt.Sprintf("%s: %s", PostCommitPrefix, toolName)
	hash, err := r.commitAll(ctx, msg)
	if err != nil {
		return "", err
	}
	return hash, nil
}

// commitAll は現在のワーキングツリー全体をコミットします。
// message: コミットメッセージ
//
// 変更がない場合でも HEAD コミットを返します（noop）。
// 戻り値: string - コミット hash, error - コミットに失敗した場合
func (r *ShadowRepo) commitAll(ctx context.Context, message string) (string, error) {
	// .virgil ディレクトリを無視するように設定
	excludePath := filepath.Join(r.shadowDir, "info/exclude")
	ensureExcluded(excludePath, ".virgil/")

	// git add .
	if err := r.runGit(ctx, "add", "-A", "."); err != nil {
		return "", fmt.Errorf("git add: %w", err)
	}

	// 変更があるか確認
	// --porcelain を使って変更の有無を確認
	out, err := r.runGitOutput(ctx, "status", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("git status: %w", err)
	}

	if strings.TrimSpace(out) == "" {
		// 変更なし: 現在の HEAD を返す
		// 初回コミット前などで HEAD がない場合は空文字を返す可能性があるが、
		// その場合は commit を進めるべき（Init で空コミットを作るため）
		head, err := r.HeadCommit(ctx)
		if err == nil && head != "" {
			return head, nil
		}
	}

	// git commit
	if err := r.runGit(ctx, "commit", "-m", message, "--allow-empty"); err != nil {
		return "", fmt.Errorf("git commit: %w", err)
	}

	// commit hash を取得
	return r.HeadCommit(ctx)
}

// ensureExcluded は指定したパターンが exclude ファイルに含まれていることを保証します。
// path: exclude ファイルのパス
// pattern: 追加するパターン
func ensureExcluded(path, pattern string) {
	content, err := os.ReadFile(path)
	if err != nil {
		// 存在しない場合は作成
		_ = os.MkdirAll(filepath.Dir(path), 0755)
		_ = os.WriteFile(path, []byte(pattern+"\n"), 0644)
		return
	}

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == strings.TrimSpace(pattern) {
			return // 既に存在する
		}
	}

	// 追加
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(pattern + "\n")
}

// HeadCommit は現在の HEAD コミット hash を返します。
//
// 戻り値: string - コミット hash, error - 取得に失敗した場合
func (r *ShadowRepo) HeadCommit(ctx context.Context) (string, error) {
	out, err := r.runGitOutput(ctx, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// Rewind は指定した commit にワーキングツリーを巻き戻します。
// commit: 巻き戻す対象の commit hash
//
// 注意：これは破壊的操作です。ユーザーの確認を取った後に呼ぶこと。
// 戻り値: error - 巻き戻しに失敗した場合
func (r *ShadowRepo) Rewind(ctx context.Context, commit string) error {
	if err := r.runGit(ctx, "reset", "--hard", commit); err != nil {
		return fmt.Errorf("git reset: %w", err)
	}
	return nil
}

// LogRecent は直近のコミットログを取得します。
// limit: 取得するコミット数
//
// 戻り値: []CommitInfo - コミット情報スライス, error - 取得に失敗した場合
func (r *ShadowRepo) LogRecent(ctx context.Context, limit int) ([]CommitInfo, error) {
	format := "%H|%s|%ai"
	out, err := r.runGitOutput(ctx, "log",
		fmt.Sprintf("--max-count=%d", limit),
		fmt.Sprintf("--pretty=format:%s", format),
	)
	if err != nil {
		return nil, err
	}

	var commits []CommitInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 {
			continue
		}
		commits = append(commits, CommitInfo{
			Hash:    parts[0],
			Message: parts[1],
			Date:    parts[2],
		})
	}
	return commits, nil
}

// DiffFromCurrent は現在のワーキングツリーと指定 commit の差分を返します。
// targetCommit: 比較する target commit hash
//
// rewind で何が削除/追加/変更されるかを事前に把握するために使う。
// 戻り値: *DiffSummary - ファイル変更サマリー, error - 差分取得に失敗した場合
func (r *ShadowRepo) DiffFromCurrent(ctx context.Context, targetCommit string) (*DiffSummary, error) {
	out, err := r.runGitOutput(ctx, "diff", "--name-status", "HEAD", targetCommit)
	if err != nil {
		return nil, fmt.Errorf("git diff failed: %w", err)
	}

	summary := &DiffSummary{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		status := parts[0]
		file := parts[1]

		switch status {
		case "D":
			// HEAD にあるが targetCommit に無い → rewind で削除される（＝現在追加されている）
			summary.AddedFiles = append(summary.AddedFiles, file)
		case "A":
			// HEAD に無いが targetCommit にある → rewind で復活する（＝現在削除されている）
			summary.DeletedFiles = append(summary.DeletedFiles, file)
		case "M":
			// 両方にあるが内容が異なる → rewind で変更される
			summary.ModifiedFiles = append(summary.ModifiedFiles, file)
		}
	}
	return summary, nil
}

// Diff は指定した2つのコミット間の統合差分を返します。
// maxLines: 取得する最大行数。0 の場合は無制限。
func (r *ShadowRepo) Diff(ctx context.Context, fromCommit, toCommit string, maxLines int) (string, error) {
	// 統合差分 (unified diff) を取得
	// --no-color で色コードを排除、--unified=3 で前後3行のみに限定
	out, err := r.runGitOutput(ctx, "diff", "--no-color", "--unified=3", fromCommit, toCommit)
	if err != nil {
		return "", fmt.Errorf("git diff failed: %w", err)
	}

	if maxLines <= 0 {
		return out, nil
	}

	// strings.Split は末尾が \n の場合、最後に空文字列を作るため、
	// TrimSpace または行数判定に注意が必要。
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) <= maxLines {
		return out, nil
	}

	// hunk ヘッダー（@@ ... @@）の直前で切断しないよう調整
	// なるべく意味のある塊で終わるようにする
	cutoff := maxLines
	for i := cutoff; i > 0 && i > cutoff-10; i-- {
		// 次の行が @@ で始まるなら、そこで切るのが美しい
		if i < len(lines) && strings.HasPrefix(lines[i], "@@") {
			cutoff = i
			break
		}
	}

	truncated := strings.Join(lines[:cutoff], "\n")
	return fmt.Sprintf("%s\n\n[Diff truncated. %d more lines omitted.]", truncated, len(lines)-cutoff), nil
}

// CommitInfo はコミット情報を表します。
type CommitInfo struct {
	Hash    string // コミット hash
	Message string // コミットメッセージ
	Date    string // コミット日時
}

// runGit は git コマンドを実行します（出力は捨てる）。
// args: git コマンドの引数
//
// 戻り値: error - コマンド実行に失敗した場合
func (r *ShadowRepo) runGit(ctx context.Context, args ...string) error {
	fullArgs := append([]string{
		"-C", r.workspaceRoot,
		"--git-dir=" + r.shadowDir,
		"--work-tree=" + r.workspaceRoot,
	}, args...)

	cmd := exec.CommandContext(ctx, "git", fullArgs...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w (output: %s)",
			strings.Join(args, " "), err, string(output))
	}
	return nil
}

// runGitOutput は git コマンドを実行して標準出力を返します。
// args: git コマンドの引数
//
// 戻り値: string - コマンドの標準出力, error - コマンド実行に失敗した場合
func (r *ShadowRepo) runGitOutput(ctx context.Context, args ...string) (string, error) {
	fullArgs := append([]string{
		"-C", r.workspaceRoot,
		"--git-dir=" + r.shadowDir,
		"--work-tree=" + r.workspaceRoot,
	}, args...)

	cmd := exec.CommandContext(ctx, "git", fullArgs...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
