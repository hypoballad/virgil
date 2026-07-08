# スラッシュコマンドリファレンス (Slash Command Reference)

日付: 2026-05-16

真実のソース (Source of Truth):

- コマンドディスパッチ: `internal/tui/update.go` 内の `handleSlashCommand`
- ヘルプテキスト: `internal/tui/update.go` 内の `slashCommandHelp`
- TUIメッセージタイプ: `internal/tui/messages.go`

## 概要 (Overview)

スラッシュコマンドはTUIの入力フィールドに入力され、通常のLLMへのリクエスト送信前に処理されます。ディスパッチ前に最初のフィールド（コマンド名）が小文字に変換されるため、コマンド名は大文字小文字を区別しません。引数は一般的に `strings.Fields` でパースされますが、`/task` や `/btw` などのフリーテキストコマンドは除外され、コマンドプレフィックス以降の残りのテキストがそのまま保持されます。

## コマンド一覧 (Commands)

### `/rewind`

引数:

- なし

挙動:

- 最近のシャドウGitコミット履歴を表示します。
- `m.shadow.LogRecent(ctx, 20)` を呼び出します。
- 後で `/rewind <N>` に渡すことができる番号付きのエントリを表示します。

関連ファイル:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/shadow/shadow.go`

使用方法:

```text
/rewind
```

注意点:

- シャドウGitが初期化されている必要があります。
- シャドウGitが利用できない場合は、`Shadow git is not initialized.`（シャドウGitが初期化されていません）と出力されます。

### `/rewind <N>`

引数:

- `N`: 最近のシャドウコミットリストにおける1から始まるインデックス

挙動:

- 最近のシャドウ履歴から、指定された番号のコミットを解決します。
- 現在のワークスペースから対象のコミットへの差分（diff）サマリーを計算します。
- 保留中のリワインド確認状態を作成します。
- `/confirm` が実行されるまで、ファイルは変更されません。

関連ファイル:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/shadow/shadow.go`

使用方法:

```text
/rewind 3
```

注意点:

- 無効なインデックスを指定すると、`invalid index: N (range: 1-M)` のようなエラーが発生します。
- 保留中の確認状態は5分後に期限切れとなります。

### `/rewind <hash>`

引数:

- `hash`: シャドウコミットハッシュのプレフィックス

挙動:

- 指定されたプレフィックスで始まるハッシュを持つ最近のシャドウコミットを解決します。
- 差分サマリーを計算します。
- 保留中のリワインド確認状態を作成します。
- `/confirm` が実行されるまで、ファイルは変更されません。

関連ファイル:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/shadow/shadow.go`

使用方法:

```text
/rewind abc1234
```

注意点:

- 指定されたプレフィックスに一致する最近のコミットが見つからない場合は、`commit not found: <hash>` を返します。

### `/confirm`

引数:

- なし

挙動:

- 保留中の `/rewind` 操作を確認し、実行します。
- リワインドを実行する前に、ツール名 `before-rewind` で安全のためのシャドウコミットの作成を試みます。
- ワークスペースのファイルを保留中の対象コミットの状態に復元します。

関連ファイル:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/shadow/shadow.go`

使用方法:

```text
/confirm
```

注意点:

- 保留中のリワインド操作がない場合は、`No pending rewind operation.` と表示されます。
- 保留中のリワインドが5分以上経過している場合は、期限切れとなります。

### `/clear`

引数:

- なし

挙動:

- 現在のセッションをステータス `cleared` で終了します。
- 新しいセッションを作成します。
- メモリ上のTUI状態をリセットします：
  - セッションID (session ID)
  - ターン数 (turn number)
  - 現在のターンID (current turn ID)
  - 表示されているメッセージ (visible messages)
  - LLM履歴 (LLM history)
  - 最後に呼び出されたツール (last tool calls)
  - トークン数 (token count)
  - 現在のエラー (current error)

関連ファイル:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/repository/session.go`

使用方法:

```text
/clear
```

注意点:

- リポジトリ/セッションストアが利用可能である必要があります。
- `/debug-context` によってロードされたアクティブなデバッグコンテキストもクリアされます。

### `/debug-context`

引数:

- なし、またはコマンドの後に続くフリーテキストの質問

挙動:

- 現在のワークスペースから debug context JSON をロードします。
- 既定の探索順は `.vscode/debug-context.json`、次に `.virgil/debug-context.json` です。ワークスペースルートから開始し、親ディレクトリ側も探索します。
- `VIRGIL_DEBUG_CONTEXT_PATH` でパスを上書きできます。相対パスはワークスペースルートから解決されます。
- VS Code拡張機能のデバッグコンテキストスキーマ（バージョン1）をパースします。
- アクティブなTUI状態として保存します。
- 現在のフレーム、停止理由、ローカル変数数、スタック数、および警告を含む簡潔なサマリーを表示します。
- コマンドの後にテキストが続く場合、デバッグコンテキストを添付した状態で、そのテキストを通常のチャットリクエストとして即座に送信します。
- 以降の通常のチャットメッセージおよび `/task` リクエストに、アクティブなデバッグコンテキストを添付します。
- 管理用コマンド（`/help`、`/clear`、`/debug-context`、`/debug-context clear` など）には添付しません。
- 添付後も、アクティブなデバッグコンテキストはクリアされません。

関連ファイル:

- `internal/tui/update.go`
- `internal/debugctx/debugctx.go`
- `vscode-extension/src/extension.js`

使用方法:

```text
/debug-context
/debug-context この停止位置で何が起きているか見てください
```

注意点:

- VS Code拡張機能は、`.vscode/debug-context.json.tmp` を介してアトミックに書き込みを行った後にリネームします。
- 古いコンテキストの検出には、利用可能な場合に `current_frame.file_mtime_unix` を使用します。不一致がある場合は警告として報告されますが、コンテキストはロードされます。
- `file_sha256` は許容されますが、MVPでは使用されません。
- 未知の将来の `schema_version` の値はベストエフォートでパースされ、警告として報告されます。

### `/debug-context clear`

引数:

- なし

挙動:

- 通常の会話履歴をクリアすることなく、アクティブなデバッグコンテキストのみをクリアします。

使用方法:

```text
/debug-context clear
```

### `/vmax`

引数:

- なし

挙動:

- Virgilが `--dangerous-vmax` フラグ付きで起動された場合のみ利用可能です。
- 次に実行される通常のチャットまたは `/task` に対して、ワンショットのVMAXモードを準備（有効化）します。
- `VMAX ready!` と出力します。
- 次の実行では、最大60回のイテレーションが許可され、`run_command` の確認が自動的に承認されます。
- その回の実行が終了、エラー終了、ウォッチドッグ停止、またはイテレーション制限に達した際、VMAXは自動的に無効化されます。

関連ファイル:

- `cmd/virgil/main.go`
- `internal/tui/update.go`
- `internal/agent/agent.go`
- `internal/tools/run_command.go`

使用方法:

```text
/vmax
```

注意点:

- `/vmax` は `/btw` や `/continue` には適用されません。
- 破壊的コマンドの拒否ルール、ウォッチドッグ、保護パスのチェック、シャドウGitスナップショット、および省略引数ガードは有効なままです。
- `--dangerous-vmax` を指定せずに起動した場合、`/vmax` は無効である旨のガイダンスを出力し、有効化されません。

### `/task <task>`

引数:

- `task`: タスクの説明テキスト

挙動:

- `m.agent.RunTask` を使用して、タスクのTODOリストを作成します。
- TODOリストの作成中にはツールを渡しません。
- 生成されたTODOリストを表示し、確認待ち状態になります。
- Enterキーを押すと、`m.agent.RunTaskTodos` を通じてTODOを実行します。
- Escキーを押すと、保留中のタスク計画をキャンセルします。

関連ファイル:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/tui/view.go`
- `internal/agent/task.go`

使用方法:

```text
/task add logging to agent.go
```

注意点:

- タスクが指定されていない場合は、`⚠️ /task requires a description. Example: /task add tests for tokenizer` と表示されます。
- Shift+Tab を使用した Plan/Edit モードの切り替えは引き続き利用可能ですが、`/task` 自体はいずれのモードも必須としません。

### `/tasks <path>`

引数:

- `path`: ワークスペース相対パスで指定するタスク分割Markdownドキュメントへのパス

挙動:

- タスク分割ドキュメントを読み込みます。
- タスクID、タイトル、およびステータスの一覧を表示します。
- LLMは呼び出しません。
- ファイルの変更は行いません。

関連ファイル:

- `internal/tui/task_breakdown.go`
- `internal/tui/update.go`

使用方法:

```text
/tasks docs/reporting_migration_tasks.md
```

注意点:

- タスクの見出しは `## Task AREA-001: title` の形式である必要があります。
- ステータスは、各タスクブロック内の最初の `Status: <value>` 行から読み取られます。

### `/do <task-id> <path>`

引数:

- `task-id`: タスク分割ドキュメントに記載されている厳密なタスクID
- `path`: ワークスペース相対パスで指定するタスク分割Markdownドキュメントへのパス

挙動:

- タスク分割ドキュメントから、指定されたタスクブロックのみを読み込みます。
- 制約された「単一タスク実行プロンプト」を構築します。
- そのプロンプトを使用して、既存の `/task` 実行パスを実行します。
- P1段階では、タスクドキュメントのステータスを自動更新しません。
- すでに `done` または `skipped` にマークされているタスクの実行は拒否します。

関連ファイル:

- `internal/tui/task_breakdown.go`
- `internal/tui/update.go`
- `internal/agent/task.go`

使用方法:

```text
/do RPT-AN-03 docs/reporting_migration_tasks.md
```

注意点:

- P1では意図的に明示的なタスクIDを要求し、`/do next` は実装されていません。
- 最終レポートにおいて、`blocked`、`done-pending-user-test`、または `done` への移行を推奨（提示）します。

### `/task-status <task-id> <status> <path>`

引数:

- `task-id`: タスク分割ドキュメントに記載されている厳密なタスクID
- `status`: `todo`, `doing`, `done-pending-user-test`, `done`, `blocked`, `skipped` のいずれか
- `path`: ワークスペース相対パスで指定するタスク分割Markdownドキュメントへのパス

挙動:

- タスク分割ドキュメントを読み込みます。
- 指定されたタスクのブロックを見つけます。
- そのタスクブロック内の最初の `Status: <value>` 行のみを置換します。
- LLMは呼び出しません。

関連ファイル:

- `internal/tui/task_breakdown.go`
- `internal/tui/update.go`

使用方法:

```text
/task-status RPT-AN-03 done-pending-user-test docs/reporting_migration_tasks.md
```

### `/breakdown <source> [--output <path>]`

引数:

- `source`: ソースドキュメントへのパス、またはフリーテキストによるタスクの説明
- `--output <path>`: （オプション）生成されたタスクドキュメントのワークスペース相対の出力先パス

挙動:

- `.virgil/task_breakdown_template.md` が存在することを確認し、なければ自動生成してから、その固定スキーマでタスク分割プロンプトを構築します。
- 代替テンプレートを探すために `docs/` や `.virgil/` を探索しないようエージェントに指示します。
- 小規模なコードタスクに対する `Edit Steps`（編集ステップ）の出力を要求します。
- `--output` が指定されている場合、そのMarkdownファイルのみを書き出すようエージェントに要求します。
- `--output` が指定されていない場合、`.virgil/tasks/<source-or-request>_tasks.md` に書き出します。
- エージェントに書き込みを依頼する前に出力先ディレクトリを作成します。
- 実装ファイルは意図的には編集しません。

関連ファイル:

- `internal/tui/task_breakdown.go`
- `internal/tui/update.go`

使用方法:

```text
/breakdown docs/reporting_migration.md --output docs/reporting_migration_tasks.md
/breakdown migrate the reporting service
```

注意点:

- 出力先パスはワークスペースの内部である必要があります。
- P2段階では、スペースを含む出力先パスはサポートされていません。

### `/breakdown-last [--output <path>]`

チャット履歴内の最後の空でない assistant 応答から、タスク分割ドキュメントを生成します。

`--output` を省略した場合、assistant 応答の最初の有意な行から出力先パスを生成します。

```text
.virgil/tasks/<assistant-response-slug>_tasks.md
```

同名ファイルが既に存在する場合は `_2` のような連番を付けます。

Plan mode が有効な場合でも、このコマンドはタスクドキュメント生成の実行に限って一時的に書き込みを許可します。

### `/copy-last`

最後の空でない assistant 応答を raw Markdown としてクリップボードにコピーします。端末表示用の padding や折り返し由来の空白は含めません。
- デフォルト出力先は Virgil のローカル状態です。プロジェクトから見える場所に残す場合は `--output docs/<name>_tasks.md` を指定してください。

### `/reindex`

引数:

- なし

挙動:

- 更新時間（mtime）に基づいた差分方式で、バックグラウンドでのシンロットインデックスのスキャンを開始します。
- `m.indexer.StartFullScan(context.Background())` を呼び出します。
- インデクサーのステータス更新の表示を開始します。

関連ファイル:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/symbols/indexer.go`

使用方法:

```text
/reindex
```

注意点:

- インデクサーが利用できない場合は、`⚠️ Symbol indexer is not available.` と表示されます。

### `/reindex --force`

引数:

- `--force` または `-f`

挙動:

- mtimeキャッシュを無視して、バックグラウンドでのシンボルインデックスのフルスキャンを開始します。
- `m.indexer.StartFullScanWithForce(context.Background(), true)` を呼び出します。

関連ファイル:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/symbols/indexer.go`

使用方法:

```text
/reindex --force
/reindex -f
```

注意点:

- ヘルプテキストには `--force` が記載されていますが、実装上は短縮形の `-f` もサポートされています。

### `/callers <name>`

引数:

- `name`: 関数またはメソッドの名前

挙動:

- 指定された関数/メソッド名を呼び出している（呼び出し元の）関数を検索します。
- `m.callRepo.FindIncoming(name, 30)` を呼び出します。
- `tools.FormatCallersResult` で結果をフォーマットします。

関連ファイル:

- `internal/tui/update.go`
- `internal/repository/calls.go`
- `internal/tools/get_callers.go`

使用方法:

```text
/callers Execute
```

注意点:

- コールグラフのストレージが利用できない場合は、`⚠️ Call graph is not available.` と表示されます。
- 名前が指定されていない場合は、`Usage: /callers <function_name>` と表示されます。
- TUIコマンドからの呼び出し上限は30回に固定されています。

### `/callgraph <name> [depth]`

引数:

- `name`: 関数またはメソッドの名前
- `depth`: （オプション）正の整数

挙動:

- 指定された関数から始まるコールグラフのレポートを構築します。
- depthのデフォルト値は `3` です。
- オプションのdepthが正の整数としてパース可能な場合、その値を使用します。
- `tools.BuildCallGraphReport` を使用して出力をフォーマットします。

関連ファイル:

- `internal/tui/update.go`
- `internal/repository/calls.go`
- `internal/tools/get_call_graph.go`

使用方法:

```text
/callgraph Execute
/callgraph Execute 4
```

注意点:

- コールグラフのストレージが利用できない場合は、`⚠️ Call graph is not available.` と表示されます。
- 名前が指定されていない場合は、`Usage: /callgraph <function_name> [depth]` と表示されます。
- 無効な値や正でないdepth値が指定された場合は無視され、デフォルトの `3` に設定されます。
- `BuildCallGraphReport` は内部でdepthを正規化するため、最終的な最大深度は `tools` パッケージによって制御されます。

### `/shrink`

引数:

- なし

挙動:

- 過去の会話履歴を圧縮して要約に変換します。
- システムメッセージを保持し、古い本文メッセージを要約し、最近のメッセージを保持します。
- `m.agent.SummarizeHistory` を使用します。
- 現在のターンIDが存在する場合は、`m.repo.Turns.UpdateTurnSummary` で生成された要約を保存します。
- `m.history` を圧縮された履歴で置き換えます。
- 通常の `/task` またはチャットの応答後、コンテキストの使用率が50%に達するか、履歴が20メッセージを超えた場合に自動的にトリガーされます。
- 自動圧縮が実行される前に、1回限りの「30% コンテキスト使用」通知を表示します。

関連ファイル:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/agent/agent.go`
- `internal/repository/turn.go`

使用方法:

```text
/shrink
```

注意点:

- 圧縮するのに十分な履歴（最近の保持ウィンドウを超える古いメッセージ）が必要です。
- 圧縮するものがない場合は、`⚠️ Nothing to shrink yet. Continue for a few more turns, then run /shrink.` と表示されます。
- 自動圧縮の実行は可視化されています：Virgilは圧縮前のコンテキスト使用率と圧縮後のコンテキスト使用率を含む、開始通知と完了通知を表示します。
- 別の圧縮処理が進行中である場合、および前回の自動圧縮から6メッセージの間は、自動圧縮が抑制されます。

### `/confirm-run`

引数:

- なし

挙動:

- `run_command` ツールによって要求された、保留中のシェルコマンドの実行を承認します。
- `m.agent.NotifyRunCommandConfirmationWithFeedback(true, "")` を呼び出します。
- 保留中のコマンド実行状態をクリアします。

関連ファイル:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/agent/agent.go`
- `internal/tools/run_command.go`

使用方法:

```text
/confirm-run
```

注意点:

- 保留中のコマンドがない場合は、`No pending command to confirm.` と表示されます。

### `/reject-run`

引数:

- なし

挙動:

- `run_command` ツールによって要求された、保留中のシェルコマンドの実行を拒否します。
- `m.agent.NotifyRunCommandConfirmationWithFeedback(false, "")` を呼び出します。
- 保留中のコマンド実行状態をクリアします。

関連ファイル:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/agent/agent.go`
- `internal/tools/run_command.go`

使用方法:

```text
/reject-run
```

注意点:

- 保留中のコマンドがない場合は、`No pending command to reject.` と表示されます。
- コマンドが保留されている間、スラッシュコマンドではなく通常のテキストを入力すると、コマンドが拒否され、入力した指示テキストが `run_command` ツールの実行結果の一部としてエージェントに返されます。

例：

```text
pytest ではなく python -m unittest で確認してください
```

### `/btw <question>`

引数:

- `question`: 任意の質問テキスト

挙動:

- 独立した「ついで（by-the-way）」の質問を `m.agent.RunBtw` を通じて実行します。
- 現在のコンテキストを使用しますが、通常の会話履歴の一部として結果を記録しません。
- 特別なBTWメッセージのスタイルで応答を描画します。

関連ファイル:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/agent/agent.go`

使用方法:

```text
/btw What does this function do?
```

注意点:

- 質問が空の場合は、`⚠️ /btw requires a question. Example: /btw What does this function do?` と表示されます。
- キャンセルされた場合は、`⚠️ By-the-way request cancelled by user.` と表示されます。

### `/help`

引数:

- なし

挙動:

- `slashCommandHelp()` の出力を表示します。

関連ファイル:

- `internal/tui/update.go`

使用方法:

```text
/help
```

### `/unstuck`

引数:

- なし

挙動:

- local LLM が長考に入った、またはキャンセルされた試行から抜け出すための回復ターンを開始します。
- 直前の隠れた推論、途中出力、同じ長い分析経路を続けないよう指示します。
- エージェントには、1つの focused tool call を行うか、最大5個の簡潔な箇条書きで回答するよう求めます。
- 会話履歴に含まれる active task の制約は保持します。

使用方法:

```text
/unstuck
```

関連ファイル:

- `internal/tui/update.go`

### `/history`

引数:

- なし: 直近の入力履歴を新しい順に表示します
- `<number>`: 番号で指定した履歴を、送信せず入力欄に復元します

挙動:

- `/history` は入力履歴を番号付きで表示します。
- `/history <number>` は選択した履歴を入力欄に復元します。
- 復元した入力は自動送信されません。必要に応じて編集してから `Alt+Enter` または `Ctrl+D` で送信します。
- スラッシュコマンドも入力履歴に含まれます。

使用方法:

```text
/history
/history 2
```

関連ファイル:

- `internal/tui/update.go`

### `/remember [note]`

引数:

- なし: 登録済みのセッションメモリを表示します
- `note`: 現在のセッション中に保持したい自由記述のメモ

挙動:

- `/remember <note>` はメモをセッションメモリに登録します。
- 登録されたメモは、以後の通常チャット、`/task`、継続実行、`/btw` のエージェント呼び出しに system message として注入されます。
- コマンド自体は LLM を呼び出さず、ユーザーターンも追加しません。
- セッションメモリはプロセス内のみで保持され、`/clear` で消去されます。

使用方法:

```text
/remember 最終報告は必ず日本語で返してください。
/remember
```

関連ファイル:

- `internal/tui/update.go`

### `/forget <number|all>`

引数:

- `<number>`: 1始まりのセッションメモリ番号
- `all`: 登録済みのセッションメモリをすべて消去します

挙動:

- 指定したメモを1件削除するか、すべてのセッションメモリを消去します。
- LLM は呼び出さず、ユーザーターンも追加しません。

使用方法:

```text
/forget 1
/forget all
```

関連ファイル:

- `internal/tui/update.go`

### `/last`

引数:

- なし

挙動:

- 直前の入力を、送信せず入力欄に復元します。
- 最新の入力履歴を復元するショートカットです。

使用方法:

```text
/last
```

関連ファイル:

- `internal/tui/update.go`

## 未知のコマンド (Unknown Commands)

認識されないスラッシュコマンドはすべて以下を返します：

```text
Unknown command: <cmd>. Type /help for available commands.
```

## ヘルプに記載されているキーボードショートカット (Keyboard Shortcuts Listed By Help)

ヘルプ出力には以下も記載されています：

- `Enter`: 改行の挿入
- `Alt+Enter`: メッセージ送信
- `Ctrl+D`: メッセージ送信
- `Alt+PageUp/PageDown` または `Alt+Up/Down`: 入力履歴の移動
- `Shift+Tab`: Plan（設計）/ Edit（開発）モードの切り替え
- `Ctrl+C (2回)`: Virgilの終了

これらはキーバインドであり、スラッシュコマンドではありません。

## ヘルプの整合性チェック (Help Consistency Check)

`slashCommandHelp()` は、現在 `handleSlashCommand` によって処理されるすべてのコマンドをリストしています：

- `/rewind`
- `/confirm`
- `/clear`
- `/continue`
- `/unstuck`
- `/abort`
- `/debug-context`
- `/vmax`
- `/task`
- `/reindex`
- `/callers`
- `/callgraph`
- `/shrink`
- `/history`
- `/last`
- `/remember`
- `/forget`
- `/confirm-run`
- `/reject-run`
- `/btw`
- `/help`

また、実装上は以下もサポートされています：

- `/reindex -f`

ヘルプテキストには `/reindex --force` は記載されていますが、短縮形の `-f` エイリアスについては言及されていません。

実装されているコマンドで、ヘルプ出力に完全に欠落しているものはありません。

## 推奨される修正案 (Suggested Fixes)

1. ヘルプテキストに短縮形のリインデックスエイリアスを追加する：

```text
  /reindex -f      Force reindex (ignore mtime cache)
```

あるいは、現在の行を以下のように変更する：

```text
  /reindex --force, -f  Force reindex (ignore mtime cache)
```

2. `/callers` がTUIコマンドからの上限として30回に固定されている旨を記載することを検討する。

3. `/callgraph` の無効なdepth値が無視され、デフォルトの3に設定される旨を記載することを検討する。

## JSONツールに関する注意点 (JSON Tool Notes)

巨大な `.json` ファイルは、`read_file` ではなくJSON専用ツールを使って検査する必要があります。

- `get_json_outline`: ファイルサイズ、推定トークン数、および `max_depth` までのキー/型構造を返します。
- `read_json_path`: JSONPath（`$`、`.key`、`[index]`、`[*]`、`[start:end]`）を使用して、特定のJSON値を返します。

代表的なフロー:

```text
get_json_outline(path="config/large.json", max_depth=2)
read_json_path(path="config/large.json", jsonpath="$.users[0]")
```

これにより、数メガバイトのJSONドキュメントをLLMコンテキストに読み込むことを防ぎます。

## Markdownツールに関する注意点 (Markdown Tool Notes)

巨大な `.md` ファイルは、`read_file` を使用する前に見出しごとに検査する必要があります。

- `get_markdown_outline`: 見出しの階層構造、行範囲、および推定トークン数を返します。
- `read_markdown_section`: 見出しまたは行範囲で指定された特定のセクションを返します。

代表的なフロー:

```text
get_markdown_outline(path="docs/large_plan.md", max_depth=2)
read_markdown_section(path="docs/large_plan.md", heading="Implementation Plan")
```

これにより、必要なのが1つのセクションだけである場合に、長い設計ドキュメントや仕様書全体をLLMコンテキストに読み込むことを防ぎます。

## インスペクターCLIに関する注意点 (Inspector CLI Notes)

`cmd/inspect` は、ブラウザベースのインスペクター（Inspector）サーバーとして実行できるほか、ワンショットのドッグフーディングエクスポートコマンドとしても実行できます。

### ドッグフードのエクスポート (Dogfood Export)

目的:

- Virgilのドッグフーディング失敗事例を共有するための、ローカルでレビュー可能なパッケージを作成します。
- インスペクターのコンテキストサニタイズ（秘匿化）を再利用します。
- GitHubのIssueを作成したり外部にデータを送信したりすることなく、Issueの本文テンプレートを生成します。

使用方法:

```text
go run ./cmd/inspect --db .virgil/virgil.db --export-dogfood --session latest --out work/dogfood
```

選択されたデータベースの隣に `.virgil/debug.log` が存在する場合、それは自動的にサニタイズされたログ末尾（tail）として含まれます。ログを明示的に指定する場合：

```text
go run ./cmd/inspect --db .virgil/virgil.db --export-dogfood --log .virgil/debug.log
```

任意の企業固有スキャンパターン：

```text
go run ./cmd/inspect --db .virgil/virgil.db \
  --export-dogfood \
  --deny-patterns ~/.virgil/company-deny-patterns.txt \
  --allow-patterns ~/.virgil/dogfood-allow-patterns.txt \
  --out work/dogfood
```

生成されるファイル:

- `report.md`: 人間による確認用の概要とチェックリスト
- `issue_body.md`: 手動でのIssue作成や共有に適したMarkdownテンプレート
- `sanitized_context.json`: サニタイズされたインスペクターのコンテキストデータ
- `context_summary.json`: 生のコンテキストを含まない、トークンとツールの分析データ
- `debug_tail.log`: （利用可能な場合）サニタイズされたデバッグログの末尾
- `scan_report.json`: 機密情報スキャンの検出結果

重要:

- 共有する前に生成されたファイルをレビューしてください。
- 生のコンテキスト（raw context）はエクスポートされません。
- このコマンドは、`gh` コマンド、GitHub API、またはその他の外部サービスを呼び出しません。
