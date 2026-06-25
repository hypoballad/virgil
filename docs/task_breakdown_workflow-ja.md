# タスク分割ワークフロー (Task Breakdown Workflow)

このドキュメントでは、大規模なコーディング作業を、中断・再開可能な小さなVirgilタスクに分解するための手動のP0ワークフローについて説明します。

ゴールは、各実行を1つの明示的なタスクに集中させ、コード変更については一度に1つの小さな編集ステップのみを行うことで、ローカルモデルでも大規模な移行作業を行えるようにすることです。

## 使用するタイミング

通常の `/task` 要求では範囲が広すぎる場合に、このワークフローを使用します。例えば以下のようなケースです：

- 大規模なクラスまたはモジュールの移行
- 1つのサブシステムを新しいランタイムへ移植する
- 複数のメソッドにまたがるバグの修正
- コンテキスト縮小やエージェントの中断の後に作業を続行する
- 大規模なコードベースでVMAXのドッグフーディングを行う

単純な1ファイルの修正には使用しないでください。小さな変更には、依然として直接 `/task` を使用する方が適しています。

## コマンドフロー

Virgilは現在、以下のP1/P2コマンドフローをサポートしています：

```text
/breakdown docs/source_plan.md
/tasks .virgil/tasks/source_plan_tasks.md
/do AREA-001 .virgil/tasks/source_plan_tasks.md
/task-status AREA-001 done-pending-user-test .virgil/tasks/source_plan_tasks.md
```

`/do` は意図的に1つの明示的なタスクIDを実行します。`/do next` は初期のコマンドフローには含まれていません。

## 手動フロー

プロンプト変更のドッグフーディング中や、コマンドが使用できない場合でも、手動フローは引き続き有用です。

1. `.virgil/task_breakdown_template.md` からタスク分割ドキュメントを作成または更新します。
2. そのドキュメントから正確に1つのタスクを実行するようVirgilに指示します。
3. プロンプトに選択したタスクブロックを含めます。
4. ユーザー実行テストが期待されているかどうかをVirgilに伝えます。
5. 実装後、手動でタスクステータスを更新するか、またはステータス行のみを更新するようVirgilに指示します。

`/breakdown` は `.virgil/task_breakdown_template.md` がなければ自動生成します。対象ワークスペースに `docs/task_breakdown_template.md` を事前準備する必要はありません。`--output` を省略した場合、タスクドキュメントは `.virgil/tasks/<source-or-request>_tasks.md` に書き出されます。

重要な移行については、タスクドキュメントを `docs/<feature>_tasks.md` のようなプロジェクトから見える場所に配置してください。ローカルの実験については、`.virgil/tasks/<slug>.md` で問題ありません。

## 分割プロンプト (Breakdown Prompt)

Virgilに手動でタスクドキュメントの作成を依頼する場合は、以下のプロンプトを使用します。`/breakdown` コマンドもこれと同じ種類のプロンプトを構築します。

```text
 Create a Virgil task breakdown document for the following work.

 Source material:
 <ファイルパス、ソースドキュメント、または高レベルのタスクを貼り付ける>

 Rules:
 - Markdown形式のみで出力すること。
 - .virgil/task_breakdown_template.md のタスクスキーマを使用すること。
 - 代替テンプレートを探すために docs/ や .virgil/ を探索しないこと。
 - ソース資料が Markdown ファイルの場合は、まず get_markdown_outline で構成を確認し、関連セクションだけを読むこと。
 - 機能レベルの作業を小さなタスクに分割すること。
 - 1つのタスクにつき、1つのメソッド、1つのヘルパー、1つのローダー/セーバーのパス、または1つの実行時エラーに収めることを推奨。
 - すべてのコードタスクに「編集ステップ (Edit Steps)」を含めること。
 - 編集ステップは、独立してレビューできる小さな意味単位の編集にすること。
 - すべてのタスクについて、References（参照先）、Edit Targets（編集対象）、Completion Criteria（完了基準）、および Out of Scope（スコープ外）を含めること。
 - タスクの実行に他のタスクの事前完了が必要な場合は、Depends on（依存先）を含めること。
 - 新規タスクには Status: todo を使用すること。
 - ユーザーが実行時テストを走らせる場合は、Manual test: pending を含めること。
 - 提示されたソース資料に元から含まれている場合を除き、企業固有の名前を含めないこと。

 Output:
 - ユーザーが別パスを指定していない限り、タスク分割 Markdown を .virgil/tasks/<source-or-request>_tasks.md に書き出すこと。
 - write_file はその出力先パスに対してのみ使用すること。
```

## 単一タスク実行プロンプト (Execute-One-Task Prompt)

1つのタスクを手動で実行するには、以下のプロンプトを使用します。`/do` コマンドもこれと同じ種類のプロンプトを構築します。

```text
 Execute exactly one task from this task breakdown document.

 Task ID: <TASK-ID>
 Task title: <TASK-TITLE>

 Hard constraints:
 - Execute only this task.
 - Do not start any other task ID.
 - 編集を行う前に、挿入ポイント付近または変更対象シンボル周辺の現在の編集対象ファイルを検査すること。
 - ブロックされていない限り、タスクにリストされているReferences（参照先）のみを読み取ること。
 - タスクのEdit Steps（編集ステップ）を1つずつ順に実行すること。
 - タスクを分割できる場合、メソッド全体やクラス全体を一度に編集しないこと。
 - 独立してレビューできる小さな意味単位の編集を優先すること。
 - 巨大な編集が拒否された場合は、直ちに小さく調整した新規の編集を再生成すること。拒否されたペイロードをそのまま再試行しないこと。
 - 以前の部分的な変更が残っている場合は、このタスクに必要な明らかな破損のみを修復すること。
 - 実装は完了したもののユーザーによるテストが残っている場合は、doneの代わりにdone-pending-user-testを報告すること。
 - 関連のない他のタスクのステータスを更新しないこと。

 Task block:
 <タスクブロックを正確に1つ貼り付ける>
```

## ステータスの規約 (Status Convention)

以下の値を使用します：

```text
todo
doing
done-pending-user-test
done
blocked
skipped
```

推奨される状態遷移：

- `todo -> doing`: 作業中の任意のステータス。
- `doing -> done-pending-user-test`: 実装側の基準は満たされたが、ユーザーによる検証がまだ必要な状態。
- `done-pending-user-test -> done`: ユーザーが検証の合格を確認した状態。
- `doing -> blocked`: 必要な情報が欠落している、検証が失敗した、またはユーザー入力が必要になった状態。

単に編集が適用されたという理由だけで `done` とマークしてはなりません。

## 必須のタスクフィールド (Required Task Fields)

すべてのタスクに以下を含める必要があります：

- 見出しにおけるタスクIDおよびタイトル
- `Status`
- `Objective`
- `References`
- `Edit Targets`
- `Completion Criteria`

強く推奨されるフィールド：

- `Depends on`
- `Manual test`
- `Edit budget`
- `Edit Steps`
- `Out of Scope`
- `Manual Test Notes`

## 適切なタスクの境界 (Good Task Boundaries)

適切なタスクの境界：

- 1つのメソッド
- 1つのヘルパー関数
- 1つのローダー/セーバー of パス
- 1つの実行時エラーの修正
- 1つのデバッグコンテキストの調査
- 1つのドキュメントステータスの更新

不適切なタスクの境界：

- クラス全体の移行
- 協調して動作する複数のクラスをまとめて処理する
- 広範な機能セクション
- 無関係なリファクタリング

## 適切な編集ステップ (Good Edit Steps)

適切な編集ステップの例：

- 現在の挿入ポイントを検査する
- 小さなスケルトンを挿入する
- 1つの分岐またはモードを実装する
- 1つのヘルパーを追加する
- 構文を検証する
- 特定のステータス行/手動テスト行のみを更新する

不適切な編集ステップの例：

- 関係のない実装箇所を1回の編集に混ぜる
- 実装とステータス更新を同時に行う
- 省略または削除されたツール引数を再利用する

## VMAXに関するメモ (VMAX Notes)

この手動ワークフローでもVMAXを使用できますが、VMAX実行ごとに1つの明示的なタスクのみを対象とするようにしてください。

大きな編集ペイロードは許可されます。ツールが省略済み、削除済み、または不正なペイロードを拒否した場合は、同じペイロードを再試行せず、現在のソースから有効な引数を再生成してください。

## コマンドサマリー (Command Summary)

- `/breakdown <source> [--output <path>]`: ソース資料からタスクドキュメントを生成する。
- `/tasks <path>`: タスクドキュメントからタスクIDとステータスを一覧表示する。
- `/do <task-id> <path>`: 「単一タスク実行プロンプト」を使用して、正確に1つのタスクを実行する。
- `/task-status <task-id> <status> <path>`: 特定のステータス行を更新する。
