# タスク指示書: docs/modelfile_guide.md の作成

## 概要

Virgil の運用上の重要な知見を `docs/modelfile_guide.md` として記録する。
特に Modelfile の設定ミスによる XML 構文エラー問題と、モデル選定ガイドラインを文書化する。

## 背景

ドッグフーディング中に発覚した重要な運用知見:

1. **`num_predict` の明示指定が XML 構文エラーを引き起こす** — Modelfile に `PARAMETER num_predict 8192` を書くと、qwen3.6:35b-a3b 等で生成途中の出力が切れて XML/JSON が壊れる
2. **dense モデル vs a3b の使い分け** — dense (qwen3.5:27b 等) はエージェント用途で安定、a3b は速いがコンテキスト膨張時に不安定
3. **環境変数のチューニング** — モデルやコンテキスト長に応じた `VIRGIL_WATCHDOG_CONTEXT_LIMIT`、`VIRGIL_AGENT_TIMEOUT_MINUTES` の推奨値

これらは README に書くには詳細すぎるが、運用者には不可欠な情報なので `docs/` 配下に分離する。

## 対象ファイル

| ファイル | 操作 |
|---|---|
| `docs/modelfile_guide.md` | **新規作成** |

---

## 作成するファイル: `docs/modelfile_guide.md`

以下の内容を `read_file` 不要で**そのまま作成**してください:

```markdown
# Virgil Modelfile & Model Selection Guide

このドキュメントは Virgil を実用的に運用するための Modelfile 設定とモデル選定ガイドです。

## 推奨モデル

### 安定運用向け（推奨）

| モデル | 特徴 | 用途 |
|---|---|---|
| `qwen3.5:27b` (dense) | 安定、出力品質が高い | エージェントタスク全般 |
| `qwen3.6:27b` (dense) | qwen3.5 の後継 | エージェントタスク全般 |
| `qwen3-coder:30b` | コーディング特化 | コード生成・編集 |

### 実験的サポート

| モデル | 特徴 | 注意 |
|---|---|---|
| `qwen3.6:35b-a3b` (MoE) | 速度が dense より速い | 長コンテキスト・複雑なツール呼び出しで不安定になる場合あり |
| `qwen3.5:4b` | 軽量 | 開発機での動作確認用、本番運用には非推奨 |

## Modelfile 設定

### 重要: 設定してはいけないパラメータ

#### `PARAMETER num_predict <N>` を明示してはならない

```
# ❌ これは設定しないこと
PARAMETER num_predict 8192
```

**理由:** `num_predict` を明示すると、ツール呼び出しの XML 出力が途中で切れて構文エラーになる:

```
ollama error: XML syntax error on line 4: element <function> closed by </parameter>
```

この症状は特に以下の条件で発生しやすい:
- 長いコンテキスト（10K トークン以上）
- 複雑なツール呼び出し引数
- MoE モデル（a3b 系）

**正しい対応:** `num_predict` を Modelfile から削除する。Ollama がモデルのデフォルト（実用上は十分大きい値）を使うようになり、出力が途中で切れなくなる。

### 推奨 Modelfile テンプレート

#### qwen3.5:27b（dense）向け

```
FROM qwen3.5:27b

# コンテキストウィンドウを 128k に設定
PARAMETER num_ctx 131072

# 温度（低めで安定した出力）
PARAMETER temperature 0.3

# Top-K, Top-P（モデルのデフォルトに任せても可）
PARAMETER top_k 40
PARAMETER top_p 0.9

# システムプロンプトは Virgil 側で動的に設定するため、ここでは空
```

#### qwen3.6:35b-a3b（実験的）向け

```
FROM qwen3.6:35b-a3b-q8_0

# コンテキストウィンドウを 128k に設定
PARAMETER num_ctx 131072

# 温度（a3b はやや高めの方が安定する場合あり）
PARAMETER temperature 0.4

PARAMETER top_k 40
PARAMETER top_p 0.9

# num_predict は絶対に指定しない
```

### Modelfile の適用

```bash
# Modelfile を作成
cat > Modelfile << 'EOF'
FROM qwen3.5:27b
PARAMETER num_ctx 131072
PARAMETER temperature 0.3
EOF

# カスタムモデルとして登録
ollama create qwen3.5-virgil -f Modelfile

# 確認
ollama list
```

## 環境変数の推奨値

Virgil の挙動はモデルとコンテキスト長に応じて環境変数で調整する。

### dense モデル + 128k コンテキスト（推奨設定）

```bash
# .env
OLLAMA_MODEL=qwen3.5-virgil:latest
OLLAMA_HOST=http://127.0.0.1:11434

# ウォッチドッグ: 128k コンテキストの 75% 程度
VIRGIL_WATCHDOG_CONTEXT_LIMIT=100000

# エージェント全体のタイムアウト
VIRGIL_AGENT_TIMEOUT_MINUTES=20

# TODO 実行のタイムアウト（複数 TODO の実行を見込んで長めに）
VIRGIL_RUN_TIMEOUT_MINUTES=60
```

### a3b モデル使用時（実験的）

```bash
# a3b は長コンテキストで不安定になりやすいため、制限を厳しめに
OLLAMA_MODEL=qwen3.6-35b-a3b-virgil:latest
VIRGIL_WATCHDOG_CONTEXT_LIMIT=50000

# 応答時間が遅くなる傾向があるため、タイムアウトは長めに
VIRGIL_AGENT_TIMEOUT_MINUTES=20
VIRGIL_RUN_TIMEOUT_MINUTES=60
```

### 軽量モデル（qwen3.5:4b など、開発機向け）

```bash
OLLAMA_MODEL=qwen3.5:4b
VIRGIL_WATCHDOG_CONTEXT_LIMIT=12000

# 小型モデルは早く応答するが品質は劣る
VIRGIL_AGENT_TIMEOUT_MINUTES=10
VIRGIL_RUN_TIMEOUT_MINUTES=30
```

## モデル選定ガイド

シナリオ別の推奨モデル:

| シナリオ | 推奨モデル |
|---|---|
| エージェントとしての通常運用 | **dense 27b** |
| 大規模なコード変更タスク | **dense 27b** |
| 長いセッション（複数 TODO 実行） | **dense 27b** |
| シンプルな対話・短い質問 | a3b（速度メリット） |
| 開発機でのテスト | qwen3.5:4b |

## トラブルシューティング

### 症状: `XML syntax error on line N: element <function> closed by </parameter>`

**原因:** Modelfile で `num_predict` が指定されている

**対処:**
1. `ollama show <model> --modelfile` で Modelfile の内容を確認
2. `PARAMETER num_predict ...` の行を削除
3. `ollama create <model> -f Modelfile` で再作成

### 症状: メタデータ取得で長時間ハング

**原因:** 古いバージョンの Virgil（メタデータ取得タイムアウト未実装）または a3b モデルの応答遅延

**対処:**
1. Virgil を最新版に更新（30秒タイムアウトが組み込み済み）
2. それでも遅い場合は dense モデルに切り替え

### 症状: 1 iteration で大量のツール呼び出しが発生してコンテキスト枯渇

**原因:** モデルが並列で多数のツール呼び出しを生成し、コード側の `MaxToolCallsPerIteration` 制限を超過

**対処:**
- 現在の制限は全体 15 ツール / iteration、重い読み取り 3 ツール / iteration、通常の読み取り専用 10 ツール / iteration、書き込み・実行系 2 ツール / iteration（agent.go の定数で調整可能）
- 重い読み取りは現在 `read_symbol(full=true)` のみが対象
- dense モデルに切り替えると並列度が下がる傾向

### 症状: 応答が途中で切れる

**原因 1:** Modelfile に `num_predict` が指定されている → 上記参照

**原因 2:** `VIRGIL_AGENT_TIMEOUT_MINUTES` が短すぎる
- 大きなタスクや a3b モデルでは 20分が目安

### 症状: ウォッチドッグが頻繁に発動する

**原因:** `VIRGIL_WATCHDOG_CONTEXT_LIMIT` がコンテキスト長に対して小さすぎる

**対処:**
- コンテキストの 75% 程度を目安に設定
- 128k なら 100000、16k なら 12000

## モデル切り替えの実践

実行中のモデル状況を確認:

```bash
# 現在のモデルを確認
ollama list

# モデルの詳細（Modelfile 含む）を確認
ollama show <model_name> --modelfile

# モデルを削除して再作成
ollama rm <model_name>
ollama create <model_name> -f Modelfile
```

`.env` を切り替えて Virgil を再起動するだけで、別のモデルでの動作を試せる:

```bash
# dense モデルに切り替え
OLLAMA_MODEL=qwen3.5-virgil:latest ./virgil

# a3b モデルに切り替え
OLLAMA_MODEL=qwen3.6-35b-a3b-virgil:latest ./virgil
```

## 将来の改善余地

- **モデル自動選定**: タスクの種類に応じて dense / a3b を自動切り替え
- **`num_predict` の動的設定**: ChatRequest 側で適切な値を渡す（Modelfile に依存しない）
- **ハイブリッド運用**: チャットタスクは a3b、編集タスクは dense
```

---

## 完了条件

- [ ] `docs/modelfile_guide.md` が新規作成されている
- [ ] 上記の内容が正確に記述されている
- [ ] Markdown のフォーマット（見出し、コードブロック、表）が正しく表示される

## 注意事項

- このファイルは**ドキュメントのみ**で、コード変更はない
- 既存の `docs/probe_report.md` と同じディレクトリに配置
- README.md からのリンク追加はスコープ外（必要なら別タスク）
- 内容は本指示書の Markdown ブロックから**そのままコピー**すること。Gemini が独自に書き換えないように
- 将来モデル設定や運用知見が変わった場合は、このドキュメントを更新する運用とする
