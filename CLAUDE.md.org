# プロジェクトガイド（Claude Code 全エージェント共通）

このファイルは Claude Code 本体および全サブエージェントが毎回参照するプロジェクト憲章です。
**すべてのエージェントは、作業開始前にこのファイルを読み直してください。**

---

## 技術スタック

> このセクションは各プロジェクトで書き換えてください。以下は例です。

- Backend: Node.js 20 + TypeScript
- Frontend: React 19 + Vite
- テスト: Vitest / Playwright
- Lint / Format: ESLint + Prettier
- CI: GitHub Actions
- パッケージマネージャ: pnpm

---

## コード規約

> **2 段構成**: 「共通（言語非依存・必ず守る）」と「TypeScript プロジェクトの例（参考・要書き換え）」。
> Python / Go / Rust など他言語を使う場合は、例ブロックを自プロジェクトの慣習に沿って書き換えてください。

### 共通（言語非依存・必ず守る）

- **単一責務**: 関数は 1 つのことだけをする。複数責務が混ざっていたら分割する
- **関数サイズの制限**: 行数の目安はプロジェクトで決める（下の TS 例では 40 行）。長い関数は切り出しを検討
- **マジックナンバーは定数化**: 意味のある名前を付けて共有
- **エラーを明示的に扱う**: OO 系言語なら独自 Error 型で wrap、手続き型なら error 値、Result 系言語なら `Result` / `Option`。silent fail を作らない
- **公開 API にドキュメンテーションコメント**: シグネチャだけで意図が読み取れないなら「目的・引数・返り値・副作用」を記述
- **非同期処理は直線的に書く**: 深いネストやチェーンよりも直線的に読める書き方を優先（言語の慣習に合わせる）
- **テストは対象コードの近傍に配置する**: 離れた場所に散らさない。具体的なディレクトリ規約は言語慣習に従う

### TypeScript プロジェクトの例（参考・要書き換え）

> 他の言語を使う場合、このブロックを丸ごと削除／置換してください。

- 関数は単一責務・**40 行以内**を目安とする
- 公開 API には **JSDoc / TSDoc** を必ず付与する
- エラーは独自 Error クラスで wrap し、呼び出し側でログ出力する
- 非同期処理は `async/await` を優先し、Promise チェーンは避ける
- テストは対象コードの近傍に配置する（`__tests__/` または同一ディレクトリの `*.test.ts`）

---

## テスト規約

> **2 段構成**: ほぼすべての規約は言語非依存で共通。TS / Jest / Vitest 固有の
> 命名記法と fixture パスだけ末尾の「TypeScript プロジェクトの例」に分離しています。

### 共通（言語非依存・必ず守る）

#### 粒度の使い分け

- **単体テスト**: 純粋関数・個別モジュールのロジック。最も数が多くなる層
- **結合テスト**: DB / 外部サービスを介したユースケース。モックより実物（テスト用 DB / テストサーバ）を優先
- **E2E**: 主要ユーザーストーリーのゴールデンパスに絞る。網羅を狙わない

#### 命名と構造

- テスト名だけで「何を検証しているか」が分かるようにする（`<対象>: <条件>のとき<期待結果>` が読み取れる形式）
- 各テストは **Arrange / Act / Assert** の 3 パートに明示的に分離する
- **1 テスト = 1 検証対象**。複数観点を 1 つのテストにまとめない

#### モック方針

- **モックしてよい**: HTTP / DB / 時刻 / ファイル / 外部 SDK など、外部副作用を伴うもの
- **モックしない**: 自分が書いた純粋ロジック、テスト対象と同一モジュール内の関数
- 認証・マイグレーションなどモックと本番挙動が乖離しやすい領域は、実物に近い fixture を優先する

#### カバレッジ・観点

- 目標は **変更箇所の分岐をすべてカバー**。全体カバレッジ率は KPI にしない
- 各 AC に対して、正常系だけでなく **異常系・境界値・空入力を最低 1 ケース**用意する
- AC と 1 対 1 に紐付かないテストは spec に戻って AC を追加するか、テスト自体を削除する

#### 運用

- **flaky テスト**は quarantine せず、原因を特定して修正するか削除する。一時的 skip を入れた場合は即時に Issue 化する
- **テストデータ fixture** は言語・フレームワーク慣習に沿った場所に集約し、テスト間で共有する
- **Red → Green → Refactor**: 新規テストは一度失敗することを確認してから実装で通す（書いた瞬間に pass するテストは観点不備を疑う）

### TypeScript プロジェクトの例（参考・要書き換え）

> 他の言語を使う場合、このブロックを自プロジェクトのテストフレームワーク慣習で置き換えてください。

- **命名**: `describe('対象') > it('<条件>のとき<期待結果>')` 形式（Jest / Vitest / Mocha 共通の BDD スタイル）
- **fixture 配置**: `__fixtures__/` または `test/fixtures/` に集約
- **Snapshot**: 差分が出た時は実装変更の意図と一致しているかを必ず確認してから更新する。盲目的な `-u` は禁止（本規約は禁止事項にも記載）

他言語の目安:

- **Python**: pytest の `test_*.py` / `tests/` ディレクトリ、`pytest.fixture` / `conftest.py`
- **Go**: `*_test.go` をパッケージ内に配置、`testdata/` ディレクトリ
- **Rust**: `#[cfg(test)] mod tests` インラインまたは `tests/` ディレクトリ、`tests/common/fixtures/`

---

## ブランチ・コミット規約

- ブランチ名: `claude/issue-<番号>-<slug>` を原則とする
- コミット: [Conventional Commits](https://www.conventionalcommits.org/) に準拠する
  - `feat(scope): ...` / `fix(scope): ...` / `test(scope): ...` / `docs(scope): ...` / `refactor(scope): ...` / `chore(scope): ...`
- 1 PR = 1 Issue を原則とする（スコープが膨らむ場合は PM が Issue を分割提案する）

---

## 禁止事項

- `main` ブランチへの直接 push
- `.env` や実値を含む Secrets のコミット
- 外部サービス呼び出し時に API Key を埋め込むこと（環境変数化を徹底）
- 公開リポジトリ上の第三者コードを、ライセンス確認なしにコピペすること
- テストをコメントアウトして PR を出すこと（scope 外に分離する場合は Issue を切る）
- テストを通すために実装ではなくテスト側を書き換えて弱めること（mock を過度に強める / assert を緩める / スナップショットを盲目的に更新する等）

---

## エージェント連携ルール

- **Product Manager** は実装方針を書かない。要件と受入基準の明確化に専念する
- **Architect**（条件付き起動）は要件を変更しない。モジュール構成・データモデル・公開 IF・処理フロー・実装分割の設計に専念する
- **Developer** は仕様を追加・解釈しない。不明点があれば PM / Architect に差し戻す
- **Reviewer**（impl 系モードで自動起動）は Developer 完了後の独立レビューのみを担当し、要件・設計・実装・テストの追加や書き換えを行わない。判定は AC 未カバー / missing test / boundary 逸脱 の 3 カテゴリに限定する（スタイル / lint 観点では reject しない）
- **Project Manager** はコードを変更しない。PR 作成と進捗管理に専念する
- Architect は Triage の `needs_architect: true` 判定時のみ PM と Developer の間に挟まれる
- Architect が起動した Issue では **設計 PR ゲート**を経由する（設計 PR を merge してから実装 PR が別途作られる）
- Reviewer は impl / impl-resume の Developer 完了直後に **独立 context** で起動され、reject 時は Developer に最大 1 回だけ自動差し戻し、再 reject では `claude-failed` で人間に委ねる（差し戻しループは Reviewer 最大 2 回 / Developer 最大 2 回で打ち切り）
- **`impl-resume` の branch policy（idd-claude 側 opt-in / #67）**:
  - idd-claude 側で `IMPL_RESUME_PRESERVE_COMMITS=true` を有効化したリポジトリの場合、`impl-resume` モードは既存 origin branch の commit を温存したまま resume する。Developer は `git reset` / `git rebase` / branch 切替を行わず、未完了タスクの先頭から続行すること
  - 同条件下で `IMPL_RESUME_PROGRESS_TRACKING=true`（既定）が有効なら、Developer は各タスク完了ごとに `tasks.md` の `- [ ]` → `- [x]` 行内編集を行い、`docs(tasks): mark <task-id> as done` で **専用 commit** を積む。タスク本文 / `_Requirements:_` / `_Boundary:_` / `_Depends:_` / 順序は変更しない
  - 詳細規約は `.claude/agents/developer.md` の「impl-resume / tasks.md 進捗追跡規約」節を参照
  - opt-in 機能 OFF / 無宣言の場合は本ルールは適用されない（既定挙動: `origin/main` 起点で fresh init + force-push）
- Developer は **実装 PR** で `design.md` / `tasks.md` / `requirements.md` を書き換えない（設計 PR で人間レビュー済みのため）。矛盾は PR 本文「確認事項」で指摘する
- **PR Iteration（`needs-iteration` ラベル）の責務境界**:
  - **設計 PR (`claude/issue-<N>-design-<slug>`)** で `needs-iteration` が付いた場合、watcher が次サイクルで Architect 役割の iteration を起動する。`docs/specs/<N>-<slug>/` 配下（`requirements.md` / `design.md` / `tasks.md`）の **書き換えは許容** され、成功時 `awaiting-design-review` に遷移する
  - **実装 PR (`claude/issue-<N>-impl-<slug>`)** で `needs-iteration` が付いた場合、watcher が次サイクルで Developer 役割の iteration を起動する。`docs/specs/<N>-<slug>/` 配下の **spec 書き換えは禁止** で、矛盾は PR 本文「確認事項」で指摘するに留める。成功時 `ready-for-review` に遷移する
  - **1 PR = design or impl のどちらか**（混在禁止）。1 PR で spec 編集と実装変更を同居させない（branch 名が両 pattern に合致するケースは watcher が `ambiguous` として skip する）
  - 設計 PR iteration は idd-claude 側で `PR_ITERATION_DESIGN_ENABLED=true` の opt-in が必要
- 各エージェントの成果物は `docs/specs/<番号>-<slug>/` 配下に保存する（Kiro / cc-sdd 互換）
  - `requirements.md`（PM）— EARS 形式の AC、numeric 階層 ID
  - `design.md`（Architect、条件付き）— File Structure Plan / Components and Interfaces / Traceability
  - `tasks.md`（Architect、条件付き）— `_Requirements:_` / `_Boundary:_` / `_Depends:_` / `(P)` アノテーション
  - `impl-notes.md`（Developer、補足）
  - `review-notes.md`（Reviewer、impl 系モードのみ）— 判定結果と Findings / 最終行 `RESULT: approve|reject`
- `<slug>` は Issue タイトルを lowercase・ハイフン区切り・40 文字以内に正規化した値。既存ディレクトリがあれば流用する

## エージェントが参照する共通ルール（`.claude/rules/`）

各エージェントは作業前に以下のルールを `Read` で読み込みます。ルールの詳細は `repo-template/.claude/rules/*.md` を参照。

| ルールファイル | 参照エージェント | 役割 |
|---|---|---|
| `ears-format.md` | PM | AC の EARS 記法（When / If / While / Where / shall） |
| `requirements-review-gate.md` | PM | requirements.md の自己レビュー（Mechanical + 判断、最大 2 パス） |
| `design-principles.md` | Architect | design.md の必須セクションと詳細度の方針 |
| `design-review-gate.md` | Architect | design.md の自己レビュー（traceability / File Structure Plan 充填 / orphan 検出） |
| `tasks-generation.md` | Architect / Developer | tasks.md のアノテーション規約と numeric ID 階層 |

ルール群は [cc-sdd](https://github.com/gotalab/cc-sdd)（MIT License, Copyright gotalab）から
adapt したものです。

---

## PR 品質チェック（PjM が PR 作成時に確認する項目）

- [ ] すべての受入基準に対応する実装がある
- [ ] 単体テストが追加・通過している
- [ ] lint / format が通っている
- [ ] 既存テストが壊れていない
- [ ] ドキュメントが更新されている（必要な場合）
- [ ] PR 本文に「確認事項」セクションがある（レビュワー判断ポイントを明示）

---

## 機密情報の扱い

- 本リポジトリでは以下の情報を扱わない
  - 顧客個人情報（氏名・契約番号・保険証券番号など）
  - 本番環境の認証情報
  - 社内機密情報（M&A・人事情報など）
- もし Issue 本文に機密情報が含まれていた場合、PM エージェントは実装を進めず
  `needs-decisions` で人間にエスカレーションすること

---

## Feature Flag Protocol

> **デフォルトは opt-out です**。本節を削除する／空のままにする／値を `opt-in` 以外に
> する場合、自プロジェクトは Feature Flag Protocol を **採用しない**（= 通常の単一実装パス）と
> 解釈されます。誤って `enabled` 等の typo を書いても安全側（opt-out）に倒れます。

**採否**: opt-out

<!-- 採用する場合は上の行を `**採否**: opt-in` に変更し、規約詳細を確認してください -->
<!-- 規約詳細: `.claude/rules/feature-flag.md` -->
<!-- idd-claude:feature-flag-protocol opt-out -->

`Feature Flag Protocol` は **opt-in 制の規約**で、未完成機能を main にマージしても既存挙動を
壊さないようにする実装パターン（`if (flag) { 新挙動 } else { 旧挙動 }`）です。詳細は
`.claude/rules/feature-flag.md` を参照してください。

### この規約を採用するメリット

- 未完成機能を main にマージしても既存挙動を壊さない（リスク隔離）
- 段階的な機能リリースが可能（main 上で機能完成を待たずに細かく PR を merge できる）
- 不具合発生時に flag を false に倒すだけで切り戻し可能

### この規約を採用するデメリット

- flag 残存による技術債の管理コスト（クリーンアップ PR が別途必要）
- 両系統テストのメンテナンスコスト（同一スイートを flag-on / flag-off で 2 通り回す）

### 推奨ケース / 非推奨ケース

- **推奨**: 大規模機能で複数 PR をまたぐ実装、リリース日が確定している機能、未完成のまま main に
  載せたい機能
- **非推奨**: 単純な追加機能、テストが薄いプロジェクト、flag 削除 PR の起票運用が回らないチーム

---

## 参考資料

- 各サブエージェントの詳細定義: `.claude/agents/*.md`
- Triage プロンプト: `~/bin/triage-prompt.tmpl`
- ワークフロー全体像: `README.md`（または idd-claude テンプレート）
