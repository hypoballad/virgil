# Tree-sitter 活用によるコンテキスト節約戦略 (Tree-sitter Strategy)

## 1. 目的と背景 (Objective & Background)
ローカルLLM（コンテキスト窓 32k-128k 程度）を使用したコーディングエージェントにおいて、最大の問題は「コードの理解に消費されるトークン量」です。
従来の `read_file` による全文読み込みや `grep` 的な文字列検索では、ファイル構造を理解するために不要な実装詳細（関数本体など）までコンテキストに流し込んでしまい、重要な思考用スペースを圧迫していました。

Tree-sitter を導入することで、**「ファイルの内容（テキスト）」ではなく「ファイルの構造（AST）」**を扱うようにシフトし、トークン消費を劇的に削減します。

## 2. 技術選定: Pure Go による CGO 回避 (Technology Selection)
Virgil はクロスコンパイルと配布の容易さを重視しています。

*   **採用ライブラリ**: `odvcencio/gotreesitter`
*   **理由**: 
    *   **CGO 不用**: 完全な Go 再実装であるため、`CGO_ENABLED=0` でビルド可能。
    *   **ポータビリティ**: Mac/Linux/Windows 向けのバイナリ配布が容易。
    *   **多言語対応**: Go, Python, TypeScript 等の主要言語のパーサーが内包されている。

## 3. アーキテクチャ (Architecture)

### 3.1 バックグラウンド・インデックス化
Virgil 起動時に、バックグラウンドの Goroutine でワークスペースのスキャンを開始します。

*   **差分更新**: ファイルの最終更新日時 (`mtime`) をチェックし、更新があったファイルのみを再パースします。
*   **SQLite への保存**: 抽出したシンボル（関数名、構造体名、メソッド名、開始/終了行）を SQLite の `codebase_symbols` テーブルに保存します。
*   **高速検索**: LLM からの要求時は、Tree-sitter を直接叩くのではなく、SQLite に対して高速な SQL クエリ（または FTS5 全文検索）を発行します。

### 3.2 既存ツールとの連携
Tree-sitter は「場所の特定」を担当し、`read_file` は「内容の取得」を担当する役割分担を明確にします。

1.  `find_symbol` 等で関数の行番号範囲を取得。
2.  `read_file(path, start_line, end_line)` で、その関数の本体だけをピンポイントに読み込む。

### 3.3 Python fallback symbol extraction
Python ファイルでは、Tree-sitter が `ERROR` root になったり、後続の top-level class/function を AST として復元できないケースがあります。
その場合でも最低限の探索を維持するため、Virgil は Python に限り行ベースの fallback symbol extraction を常時実行します。

*   **対象**: インデント 0 の `class`, `def`, `async def` と、top-level class 直下の `def`, `async def`
*   **merge 方針**: Tree-sitter/Tagger 由来を優先し、同名シンボルが AST 側にない場合のみ fallback として追加
*   **表示**: fallback 由来のシンボルは `(via fallback)` と表示
*   **EndLine 推定**: 次の top-level fallback シンボル直前まで。最後のシンボルはファイル末尾まで

既知の限界:

*   decorator の内容は抽出しません
*   複数行 class/function 定義は開始行だけを signature として記録します
*   メソッドは top-level class 直下のみ対象です。ネストした関数や動的に追加されたメソッドは対象外です
*   docstring や特殊な文字列内の `class` / `def` を完全に避けられるわけではありません
*   fallback の `EndLine` はヒューリスティックであり、AST 由来ほど正確ではありません

### 3.4 Docstring / コメントのインデックス化
Virgil はシンボル抽出時に、シンボルに紐づく説明文も `codebase_symbols.doc` に保存します。

*   **Python**: class / function / method 直下の docstring を優先して抽出
*   **Go / JavaScript / TypeScript / Rust**: シンボル直前の連続した `//` コメントを抽出
*   **Python コメント**: docstring が無い場合は、シンボル直前の連続した `#` コメントを抽出
*   **表示**: `find_symbol`, `get_file_outline`, `read_symbol` の出力に説明文を含める

既知の限界:

*   ブロックコメント (`/* ... */`) はまだ対象外です
*   Python の属性や変数代入に続くコメントは対象外です
*   docstring は静的な先頭リテラルのみ対象で、動的に生成される説明文は対象外です

## 4. ローカルLLM向けツール設計 (API Design)
ローカルLLM（7B-14B）は複雑なクエリ言語の生成が苦手なため、API は「意図ベース」で極限まで単純化します。

| ツール名 | パラメータ | 動作 |
|---|---|---|
| `get_file_outline` | `path`, `name_filter`, `type`, `receiver`, `fallback_only`, `include_methods` | ファイル内のシンボルのシグネチャ・行番号・Doc (docstring / 直前コメント) を返す。必要に応じて名前・種類・receiver・fallback 由来・method 有無で絞り込める。 |
| `find_symbol` | `name`, `type`, `receiver`, `file_path`, `fallback_only`, `limit` | プロジェクト全体から名前でシンボルを検索し、シグネチャ・行番号・Doc (docstring / 直前コメント) を返す。common method 名は type / receiver / file_path / fallback_only で絞り込める。Doc 本文の全文検索ではない。 |
| `read_symbol` | `path`, `symbol_name`, `full` | 指定シンボルを AST / fallback 境界で読む。デフォルトでは 50 行超の大きなシンボルはシグネチャ・Doc・メソッド一覧の summary を返し、全文が必要な場合のみ `full=true` を指定する。 |
| `get_file_imports` | `path` | Python ファイルが import しているモジュール・シンボルを返す。 |
| `find_dependents` | `module`, `exact_module`, `import_kind`, `imported_name`, `alias`, `file_path`, `scope`, `include_relative`, `wildcard_only`, `max_results` | 指定 Python モジュールを import しているファイルを逆引きする。alias / from-import 名 / import scope / wildcard / file path で絞り込める。 |

※ LLM に生の S式（Tree-sitter クエリ）を書かせることはせず、内部の Go コードが AST を走査して結果を組み立てます。

## 5. ステアリング戦略 (Steering Strategy)
LLM が「とりあえず全文読む」癖を抑え、Tree-sitter ツールを優先的に使うように誘導します。

*   **プロンプトによる制約**: 「巨大なファイルをいきなり読むのは禁止。まずはアウトラインを取得せよ」とシステムプロンプトで指示。
*   **ツール説明の工夫**: 「このツールを使うとトークンを節約でき、あなたの思考をより正確にできます」といったメリットを明記。
*   **ガードレール**: `read_file` で巨大なファイルが指定された場合、Virgil 側でエラーを返し、アウトライン取得ツールの使用を促す。

## 6. 将来の拡張性
*   **コード変更の影響分析**: 関数を変更した際、AST を辿ってその関数を呼び出している箇所（Call Graph）を正確に特定し、修正が必要な範囲を提案する。
*   **構文ベースの修正 (`edit_file`)**: 正規表現マッチングではなく、AST のノード単位での正確なコード置換。
