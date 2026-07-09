# remember と editallow の使い方

`remember` と `editallow` は目的が違います。

- `remember`: セッション中にエージェントへ覚えさせる会話上のメモ
- `editallow`: 書き込み系ツールが編集できるパスを制限する強制ガード

## remember

`remember` は、以後の通常チャット、`/task`、継続実行、`/btw` に system message として注入されるセッションメモリです。

メモを追加します。

```text
/remember 最終回答は必ず日本語で返してください。
/remember 既存のユーザー変更は revert しないでください。
```

登録内容を表示します。

```text
/remember
```

指定番号のメモを削除します。

```text
/forget 1
```

すべて削除します。

```text
/forget all
```

注意:

- `remember` はプロセス内のセッションメモリです。
- `/clear` で消えます。
- ファイルからの読み込み機能はありません。
- 書き込み禁止や編集許可範囲の強制には使いません。編集ガードには `editallow` を使います。

## editallow

`editallow` は、`write_file`、`edit_file`、`edit_with_pattern` などの書き込み系ツールに対する編集許可リストです。

既定ファイルは次です。

```text
.virgil/editallow
```

このファイルが存在する場合、Virgil 起動時に自動で読み込まれます。

例:

```text
# 1行1パス
src/MAE_testcase/
src/AE_pytorch.py
src/MAE_pytorch.py
```

カンマ区切りでも書けます。

```text
src/MAE_testcase/, src/AE_pytorch.py, src/MAE_pytorch.py
```

`edit-allow:` 付きでも読み込めます。

```text
edit-allow: src/MAE_testcase/, src/AE_pytorch.py, src/MAE_pytorch.py
```

現在の許可リストを表示します。

```text
/editallow
```

セッション中にファイルを編集した後、再読み込みします。

```text
/editallow --reload
```

別ファイルを指定して再読み込みします。

```text
/editallow --reload policy/editallow
```

`.env` で既定ファイルを変更できます。

```env
VIRGIL_EDITALLOW_FILE=/home/user/.virgil/editallow
```

`.env` に直接カンマ区切りで許可リストを書くこともできます。

```env
VIRGIL_EDIT_ALLOWLIST=src/MAE_testcase/,src/AE_pytorch.py,src/MAE_pytorch.py
```

## パスの書き方

ディレクトリは末尾に `/` を付けます。

```text
src/MAE_testcase/
```

ファイルはそのまま書きます。

```text
src/AE_pytorch.py
```

相対パスは workspace root 基準です。

## ブロックされるもの

`editallow` が設定されている場合、許可リスト外のパスに対する書き込み系ツールは実行前にブロックされます。

対象:

- `write_file`
- `edit_file`
- `edit_with_pattern`
- その他 `IsMutating()` が true の path 付きツール

対象外:

- `read_file`
- `read_symbol`
- `find_symbol`
- `search_text`
- その他の読み取り専用ツール

読み取りは許可されるため、許可リスト外のファイルを調査することはできます。ただし、編集はできません。

## 推奨構成

短期的な会話ルールは `/remember` に入れます。

```text
/remember 最終回答は日本語で返してください。
```

編集範囲は `.virgil/editallow` に入れます。

```text
src/MAE_testcase/
src/AE_pytorch.py
src/MAE_pytorch.py
```

ファイルを変更したら、セッション中に再読み込みします。

```text
/editallow --reload
```
