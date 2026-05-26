<!-- SPDX-License-Identifier: MIT -->
<!-- Adapted from cc-sdd (https://github.com/gotalab/cc-sdd), MIT License, Copyright (c) gotalab -->

# tasks.md 生成ルール

Architect が出力する `tasks.md` は、Developer が迷わず実装を進められる粒度と、
トレーサビリティを持つアノテーションを持たせます。

## 基本フォーマット

### 単純タスクのみの場合

```markdown
- [ ] 1. <タスクの要約>
  - <詳細項目（必要な場合のみ）>
  - _Requirements: 1.1, 2.3_
```

### 親タスクと子タスクの構造を取る場合

```markdown
- [ ] 1. <親タスクの要約>
- [ ] 1.1 <子タスクの記述> (P)
  - <詳細項目 1>
  - <詳細項目 2>
  - _Requirements: 1.1, 1.2_
  - _Boundary: UserService, AuthController_
  - _Depends: 2.1_
```

## Checkbox 形式の必須化

`tasks.md` の **すべての実装タスク行**は、行頭が `- [ ]`（未完了）または `- [ ]*`（deferrable
印、後述）の checkbox 形式で開始することを **必須** とします。これは Developer の resume
機能（`IMPL_RESUME_PROGRESS_TRACKING=true`、Issue #67 / #112 以降の既定）が `- [ ]` → `- [x]`
の markdown checkbox 編集を進捗の **正本** として読む前提を確実に成立させるためです。

- **親タスク行・子タスク行のいずれにも checkbox を付与すること**
  （例: `- [ ] 1. ...` / `- [ ] 1.1 ...` のように親も子もリスト項目 + checkbox で書く）
- **markdown header のみ**（例: `## T-01: タスク名` / `### Task 1` / `#### 1.1 子タスク`）で
  タスクを表現することは **禁止**。タスク行は必ずリスト項目 (`- [ ]`) で書くこと
- 詳細項目（`_Requirements:_` 等のアノテーション行や説明箇条書き）は checkbox を持たない
  通常のリスト項目で構わない（タスクそのものを表現する行のみが checkbox 必須）
- 判定パターン（POSIX 互換 ERE）: `^- \[[ x]\]\*? [0-9]+(\.[0-9]+)*\.? ` — 行頭が `- [ ]`
  / `- [ ]*` / `- [x]` / `- [x]*` のいずれかで、続けて numeric 階層 ID（`1` / `1.1` /
  `2.1.3` 等）+ 半角スペースで始まる行をタスク行と認識する（最上位タスクは ID 末尾の
  `.` あり [`- [ ] 1. <名前>`]、子タスクは末尾の `.` なし [`- [ ] 1.1 <名前>`] が既存表記）

> **Mechanical Check との対応**: 上記必須化は Architect の自己レビュー時に
> [`design-review-gate.md`](./design-review-gate.md) の **tasks.md checkbox enforcement check**
> Mechanical Check が機械的に検証します（checkbox 不在のタスク行を 1 件でも検出した場合は
> 違反として報告し、Architect が `- [ ] <numeric ID>. <タスク名>` 形式に修正してから確定する）。

## アノテーション

| キー | 必須? | 用途 |
|---|---|---|
| `_Requirements:_` | **必須** | 対応する requirement ID を列挙（numeric のみ、例: `1.1, 2.3`）。説明や括弧書きは付けない |
| `_Boundary:_` | 並列可タスク `(P)` でのみ必須 | 担当するコンポーネント名を列挙（design.md の Components 名と一致） |
| `_Depends:_` | 非自明な cross-boundary 依存のみ | 先行するタスク ID を列挙。自明な順序依存は省略 |

## 並列マーカー `(P)`

- **並列実行可能**なタスクのみ末尾に ` (P)` を付ける
- 並列実行できないタスク（順序依存のあるタスク）には付けない（デフォルト=直列）
- `(P)` を付けるなら `_Boundary:_` を必須とする（並列時の競合境界を明示するため）

## ID 規則

- **numeric 階層 ID** のみ使用: `1`, `1.1`, `1.2`, `2`, `2.1` ...
- `T-01` や `FR-01` 形式の英字 ID は使わない（requirements.md の numeric ID と揃えるため）

## Optional なテストタスク

deferrable なテスト追加タスクは checkbox を `- [ ]*`（アスタリスク付き）と記述し、詳細項目で
対応する AC を説明します。**`- [ ]*` も checkbox 形式の一種**として扱われ、上記
「Checkbox 形式の必須化」節および Mechanical Check の判定で違反として報告されません:

```markdown
- [ ]* 1.3 統合テスト追加
  - 対応する受入基準のうち、現時点でカバレッジが不足する項目を補完
  - _Requirements: 1.1, 1.2_
```

## ガイドライン

- 各タスクは **1 commit 単位**で独立に完了可能な粒度にする
- 合計タスク数は **3〜10 件を目安**（多すぎる場合は design の File Structure Plan が大きすぎる可能性）
- 対応する `_Requirements:_` を必ず明示（トレーサビリティ確保）
- 親タスクに対する子タスクは、実装順序に沿って並べる

> **件数 enforcement との関係**: 上記「3〜10 件目安」は設計指針として有効ですが、Architect の
> 自己レビュー時に [`design-review-gate.md`](./design-review-gate.md) の **Budget overflow check**
> Mechanical Check が同じ件数を機械的に判定します（≤10 件 pass / 11〜13 件 consolidate→split /
> ≥14 件 forced split）。10 件以下の正常ケースで挙動は変化しません。カウントは **最上位
> numeric ID タスク**（`- [ ] 1.` / `- [ ] 2.` …）のみが対象で、子タスク（`1.1` 等）や deferrable
> テストタスク（`- [ ]*`）は数えません。

## 構造化 verify ブロック（stage-a-verify gate の input 契約）

stage-a-verify gate (#125) は Stage A（Developer 実装）完了直前に、`tasks.md` 中の
build/test/lint コマンドを watcher が独立再実行し、Developer の自己申告だけで build 不通が
Stage A を通過するのを防ぐゲートです。従来はコマンド特定を「verify keyword を含む行＝コマンド」
とみなすヒューリスティック抽出で行っていたため、ツール名で始まる散文（例: `- shellcheck 警告
ゼロを確認`）を誤ってコマンドとして実行する誤発火が繰り返し発生していました（#160 / #219 / #221）。

これを避けるため、Architect は再実行させたい verify コマンドを **センチネル付きの構造化ブロック**で
`tasks.md` に明示宣言できます（#224）。構造化ブロックがあると watcher はヒューリスティック推測を
行わず、ブロック内のコマンドのみを決定論的に実行対象として解決します。

### canonical 書式

センチネルコメント `<!-- stage-a-verify -->` の **直後**に fenced code block を 1 つ置き、
その中身に実行するコマンドを書きます:

```markdown
<!-- stage-a-verify -->
```sh
shellcheck local-watcher/bin/modules/*.sh && bash docs/specs/<番号>-<slug>/test-fixtures/test-extract.sh
```
```

書式規約（well-formed 条件）:

- **センチネル**: 行を trim した結果が厳密に `<!-- stage-a-verify -->` に一致する行。前後の空白は
  許容するが、行内に他テキストを混ぜないこと
- **直後性**: センチネル行の次行以降で空行を任意個スキップした後の **最初の非空行が fence 開始**
  （trim 後 ` ``` ` 始まり）であること。fence 以外の非空行が先に来ると malformed として扱われる
- **fence 言語タグ**: ` ```sh ` / ` ```bash ` 等の言語タグは許容（タグ自体はコマンド中身に含まれない）
- **fence 終了**: 次に現れる ` ``` ` 行で閉じる。EOF まで閉じないと malformed
- **中身**: fence 内が trim 後すべて空だと malformed（空ブロック）。非空なら元の改行・インデントを
  保持してそのまま実行される

### 中身は散文ではなく実行可能コマンド

ブロックの中身は **実行可能なコマンドそのもの**として記述します（散文・説明箇条書き・タスク記述を
書かない）。複数行コマンドや `&&` / `||` / `;` 連結を含められます。watcher は中身を `bash -c` に
**そのまま**渡し、連結記号を watcher 側で解釈しません:

```markdown
<!-- stage-a-verify -->
```sh
shellcheck install.sh setup.sh &&
  actionlint .github/workflows/*.yml
```
```

### 既存 checkbox 規約・numeric ID 階層規約との非干渉

構造化 verify ブロックは **タスク行ではなく補助ブロック**です。本ファイル「Checkbox 形式の
必須化」節および [`design-review-gate.md`](./design-review-gate.md) の Budget overflow check /
checkbox enforcement check の判定パターンは、いずれも行頭 `- [ ]` / `- [ ]*` + numeric ID で
始まるタスク行を対象とします。センチネル行（`<!-- ... -->`）も fence 行（` ``` `）も fence 中身も
これらの判定パターンに **マッチしない**ため、ブロックを追加してもタスク件数カウント・checkbox
enforcement は一切影響を受けません。

### 配置場所

ブロックの配置場所は任意です（パースはセンチネル基準で見出しに依存しません）。推奨は `tasks.md`
末尾の `## Verify` 見出し配下にまとめる形ですが、`## Verify` 見出し自体は必須ではありません:

```markdown
## Verify

本 spec の実装後、watcher が再実行すべき verify コマンドを以下の構造化ブロックで宣言する。

<!-- stage-a-verify -->
```sh
npm test && npm run lint && npm run build
```
```

### verify 対象が無い spec はブロックを省略できる

verify すべき build/test/lint コマンドが存在しない spec（純ドキュメント変更等）では、構造化ブロックを
省略して構いません。その場合 watcher は `STAGE_A_VERIFY_COMMAND` env → ヒューリスティック抽出 →
SKIPPED の順に fallback します（解決順序の詳細は README「Stage A Verify Gate (#125)」節を参照）。
構造化ブロックを持たない既存 spec は従来どおりヒューリスティック / env 経路で動作します（後方互換）。

なお、`design` モードを経由せず Architect が `tasks.md` を生成しない **design-less impl**
（tasks.md 不在。#204 等）は、構造化ブロック / ヒューリスティック抽出の入力となる `tasks.md`
自体が存在しないため stage-a-verify gate の **対象外（SKIP）**となります。これは未実装の
取りこぼしではなく「watcher は verify コマンドを推測しない」設計思想（#224 / #228 / #230）に
基づく **意図された仕様**であり、design-less impl の regression は Developer が実行するテストと
Reviewer の AC 判定で担保します（詳細は README「Stage A Verify Gate (#125)」節の
「design-less impl（tasks.md 不在）は gate 対象外」を参照）。

## 参考

- [cc-sdd `tasks.md` テンプレート](https://github.com/gotalab/cc-sdd/blob/main/.kiro/settings/templates/specs/tasks.md)
