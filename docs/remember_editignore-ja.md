# remember と editignore の使い方

`remember` と `editignore` は目的が違います。

- `remember`: セッション中にエージェントへ覚えさせる会話上のメモ
- `editignore`: 書き込み系ツールに編集させたくないパスの ignorelist

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
- 編集禁止の強制には使いません。編集ガードには `editignore` を使います。

## editignore

`editignore` は、`write_file`、`edit_file`、`edit_with_pattern` などの書き込み系ツールに対する ignorelist です。

既定ファイルは次です。

```text
.virgil/editignore
```

このファイルが存在する場合、Virgil 起動時に自動で読み込まれます。

例:

```text
# 1行1パス
src/interface/
src/common.py
src/train.py
```

カンマ区切りでも書けます。

```text
src/interface/, src/common.py, src/train.py
```

`edit-ignore:` 付きでも読み込めます。

```text
edit-ignore: src/interface/, src/common.py, src/train.py
```

現在の ignorelist を表示します。

```text
/editignore
```

セッション中にファイルを編集した後、再読み込みします。

```text
/editignore --reload
```

別ファイルを指定して再読み込みします。

```text
/editignore --reload policy/editignore
```

`.env` で既定ファイルを変更できます。

```env
VIRGIL_EDITIGNORE_FILE=/home/user/.virgil/editignore
```

`.env` に直接カンマ区切りで ignorelist を書くこともできます。

```env
VIRGIL_EDIT_DENYLIST=src/interface/,src/common.py,src/train.py
```

## base の使い方

`base:` を書くと、それ以降の相対パスを base 基準で解決します。

```text
base: src

interface/
common.py
train.py
```

これは次と同じ意味です。

```text
src/interface/
src/common.py
src/train.py
```

`base:` は絶対パスでも書けます。

```text
base: /home/user/project/src

interface/
common.py
train.py
```

絶対パスの `base:` は workspace root 配下にある場合だけ有効です。workspace root 外を指す `base:` は無効として扱われます。

## ls から作る例

`src` 配下をまず全部 ignore にして、編集したいものだけ削除する運用ができます。

```bash
mkdir -p .virgil
{
  echo "base: src"
  echo
  ls -1 src
} > .virgil/editignore
```

その後、編集を許可したい項目を `.virgil/editignore` から削除します。

例として次の3つを編集可能にしたい場合:

```text
MAE_testcase/
AE_pytorch.py
MAE_pytorch.py
```

これらの行を `.virgil/editignore` から削除します。

セッション中に反映します。

```text
/editignore --reload
```

## パスの書き方

ディレクトリは末尾に `/` を付けます。

```text
src/interface/
```

ファイルはそのまま書きます。

```text
src/common.py
```

相対パスは workspace root 基準です。ただし `base:` がある場合、その後の相対パスは base 基準です。

## ブロックされるもの

`editignore` が設定されている場合、ignorelist に一致するパスへの書き込み系ツールは実行前にブロックされます。

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

読み取りは許可されるため、ignorelist 内のファイルを調査することはできます。ただし、編集や新規作成はできません。

## 新規作成

新規作成も `editignore` の対象です。

例えば:

```text
src/interface/
```

が ignorelist にある場合、次はブロックされます。

```text
src/interface/new_file.py
```

ignorelist に一致しない場所への新規作成は許可されます。

## 推奨構成

短期的な会話ルールは `/remember` に入れます。

```text
/remember 最終回答は日本語で返してください。
```

編集禁止範囲は `.virgil/editignore` に入れます。

```text
base: src

interface/
common.py
train.py
```

ファイルを変更したら、セッション中に再読み込みします。

```text
/editignore --reload
```
