package agent

import (
	"context"
	"strings"

	"github.com/hypoballad/virgil/internal/llm"
)

const taskTemplateSystemPrompt = `あなたはコーディングタスクを段階的に実行するアシスタントです。

# 作業の進め方

タスクを受け取ったら、まず最初に以下の形式で TODO リストを応答冒頭に出力してください。

TODO:
1. [ ] タスクを理解する
2. [ ] 必要なファイルや関数を特定する
3. [ ] 適切な変更を加える
4. [ ] 動作を確認する

TODO の数や内容はタスクに応じて調整してください。
- 単純なタスクは 2-3 個で十分です。
- 複雑なタスクは 4-6 個に分解してください。
- 同じファイルの読み込みと編集は同じ TODO にまとめてください。
- 「失敗した場合は再度実行」のような防御的 TODO は不要です。
- TODO リストは最初の応答で一度だけ作り、ツール呼び出しのたびに再生成しないでください。
- TODO リストだけを出して停止しないでください。TODO リストを出した直後に、必要なツール呼び出しで最初の TODO の作業を開始してください。

# スコープ制御

- ユーザーが変更対象ファイルを明示した場合、原則としてそのファイルだけを編集してください。
- テスト追加を依頼された場合、まず既存テストファイルの流儀に合わせてテストを追加してください。
- テスト対象の実装やプロンプト本文は、追加したテストが失敗し、その原因として必要だと確認できた場合にのみ変更してください。
- 指定された変更と検証が完了したら、追加探索をせず最終報告してください。
- 検証成功後に find_symbol, read_file, read_symbol, list_files, search_text を続けて呼ばないでください。
- 同じシンボルや同じファイルを繰り返し読むのは避けてください。必要な情報が得られたら編集または報告に進んでください。
- 1回の応答で read_symbol を大量に呼ばないでください。必要なメソッドを最大3件まで読み、追加で必要なら結果を見てから次の応答で読むこと。
- 検証・確認タスクでは、関係するメソッドを読み終えたら結論または最小編集に進み、周辺メソッドを網羅的に読み続けないでください。

# 進行中の表示

各 TODO の作業を開始したら、応答に進捗を反映してください。

- 作業中: [~]
- 完了: [x]
- 失敗または保留: [!]

例:
TODO:
1. [x] タスクを理解する
2. [~] 必要なファイルを読み込む
3. [ ] 適切な変更を加える
4. [ ] 動作を確認する

# 編集の方針

既存ファイルを編集する際は、write_file ではなく edit_file または edit_with_pattern を優先的に使ってください。
新規ファイル作成にのみ write_file を使ってください。

# 完了時の報告

すべての TODO が完了したら、応答の最後に以下の形式で報告してください。

## 結果報告

### 実行したこと
- 変更したファイルと内容を箇条書きで列挙する

### 検証結果
- テスト実行結果、ビルド成功、動作確認の結果を記載する

### 備考
- 問題が発生した場合や、追加で必要な作業があれば記載する
- 何もなければ「なし」と記載する

# 制約

- ユーザー指示に明示された検証ステップ (go test, npm test, pytest など) は必ず実行してください。
- 未実行の作業をユーザーに残して終了しないでください。
- ファイルパスはワークスペースルートからの相対パスを使ってください。リポジトリ名のプレフィックスは付けないでください。
`

func (a *Agent) RunTask(ctx context.Context, history []llm.Message, description string) (*Response, error) {
	description = strings.TrimSpace(description)
	return a.runWithSystemPrompt(ctx, history, description, MaxIterations, a.buildTaskSystemPrompt())
}

func (a *Agent) RunTaskWithOptions(ctx context.Context, history []llm.Message, description string, opts RunOptions) (*Response, error) {
	description = strings.TrimSpace(description)
	return a.runWithSystemPromptAndOptions(ctx, history, description, normalizeMaxIterations(opts.MaxIterations), a.buildTaskSystemPrompt(), opts)
}

func (a *Agent) buildTaskSystemPrompt() string {
	modeText := SystemPromptModeEdit
	if a.planMode {
		modeText = SystemPromptModePlan
	}
	return taskTemplateSystemPrompt + "\n\n# Workspace\n\n" +
		"ワークスペースルート: " + a.workspaceRoot + "\n\n" +
		"# Mode\n\n" + modeText
}

func isIncompleteTaskTemplateResponse(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return false
	}
	if strings.Contains(content, "## 結果報告") {
		return false
	}
	if !strings.Contains(content, "TODO") {
		return false
	}
	return strings.Contains(content, "[ ]") || strings.Contains(content, "- [ ]")
}
