# プロジェクトガイド（Claude Code 全エージェント共通）

このファイルは Claude Code 本体および全サブエージェントが毎回参照するプロジェクト憲章です。
**すべてのエージェントは、作業開始前にこのファイルを読み直してください。**

altpocket は Pocket 互換のセルフホスト型「あとで読む」サービスです。Web UI（SSR）・Chrome
拡張・MCP の 3 系統から同一 API を叩く Go モノレポで、保存と本文取得を分離した非同期構成
を採ります。詳細なプロダクト方針は `.kiro/steering/product.md` を参照。

---

## 技術スタック

ソース・オブ・トゥルースは `go.mod` / `.golangci.yml` / `.github/workflows/ci.yml` /
`.kiro/steering/tech.md`。本節はエージェントが冒頭で押さえるべきサマリです。

- **言語**: Go 1.25（`go.mod` の `go 1.25.0` を正とする）
- **HTTP ルーティング**: chi v5（`github.com/go-chi/chi/v5`）
- **DB ドライバ**: pgx v5（`github.com/jackc/pgx/v5`）
- **データベース**: PostgreSQL 16（pg_trgm を検索に利用）
- **HTML テンプレート**: 標準 `html/template`（SSR、`templates/`）
- **本文抽出**: goquery（`github.com/PuerkitoBio/goquery`）
- **OAuth / Sheets**: `golang.org/x/oauth2` + `google.golang.org/api`
- **JWT**: `github.com/golang-jwt/jwt/v5`
- **MCP**: `github.com/modelcontextprotocol/go-sdk`（`internal/mcpserver`）
- **ログ**: 標準 `log/slog`（JSON 構造化、stdout）
- **拡張機能**: Chrome Extension MV3（`extension/`、サイドパネル統一・popup 不使用、
  `background.js` は Service Worker）
- **拡張機能テスト**: Node.js 標準 `node --test`
- **Lint**: `golangci-lint` v2.11.4（`govet` / `errcheck` / `staticcheck`）
- **CI**: GitHub Actions（`.github/workflows/ci.yml`）— `go test ./...` →
  `golangci-lint` → API/Worker Docker build
- **デプロイ**: Docker Compose（`deploy/docker-compose.production.yml`、Caddy 前段）

実行バイナリは `cmd/api`（HTTP API + SSR UI）と `cmd/worker`（本文取得・期限切れセッション
削除）の 2 つに分離。アプリ本体は `internal/` 配下にレイヤード配置（`server`, `store`,
`auth`, `fetcher`, `urlnorm`, `tag`, `ui`, `mcpserver`, `ratelimit`, `config`, `db`,
`logger`）。詳細は `.kiro/steering/structure.md`。

---

## コード規約

### 共通（言語非依存・必ず守る）

- **単一責務**: 関数は 1 つのことだけをする。複数責務が混ざっていたら分割する
- **マジックナンバーは定数化**: 意味のある名前を付けて共有
- **エラーを明示的に扱う**: silent fail を作らない。Go なら `error` 値、外部境界では `%w`
  で wrap してチェーンを保つ
- **公開 API にドキュメンテーションコメント**: シグネチャだけで意図が読み取れないなら
  「目的・引数・返り値・副作用」を Go doc コメントとして残す
- **直線的に書く**: 深いネスト・goroutine の入れ子・複雑なチャネル合成より、直線的に読める
  書き方を優先する
- **テストは対象コードの近傍に配置**: 離れた場所に散らさない（Go なら同パッケージの
  `*_test.go`）

### Go プロジェクトの規約

- **フォーマット**: `gofmt` を必須とする。CI / lint で違反検出されないこと
- **Import 順序**: ① 標準ライブラリ → ② プロジェクト内部（`altpocket/...`）→
  ③ サードパーティ の 3 ブロックを空行で分離する（`.kiro/steering/structure.md` に従う）
- **命名**: 公開 API は `PascalCase`、内部補助は `camelCase`、HTTP ハンドラは `handle*`
  プレフィックス（例: `handleCreateItem`, `handleGetItem`）
- **データ契約**: JSON タグと SQL カラム名は `snake_case` で揃える
- **レイヤ分離**:
  - HTTP ハンドラ（`internal/server`）は認証・バリデーション・レスポンス整形に集中する
  - DB 操作は `internal/store` に集約し、ハンドラから直接 `pgxpool` を触らない
  - 共有ルール（`urlnorm`, `tag`, `ratelimit`, `logger`, `config`）は独立パッケージ化
- **エラー**: `errors.Is` / `errors.As` で sentinel error を識別し、wrap には `fmt.Errorf("...: %w", err)`
  を使う。`pgx.ErrNoRows` と他のエラーを混同しない（404 vs 500 を区別する）
- **panic 禁止**: ハンドラ／ライブラリ層で `panic` しない。起動時 fail-fast（`config.Load` 等）
  は許容するが、明確なメッセージで終了させる
- **ロギング**: `slog`（JSON）を使い、トークン・Cookie・OAuth 生レスポンスを出力しない
- **lint**: `.golangci.yml`（`govet` / `errcheck` / `staticcheck`）に違反しないこと

### Chrome Extension（`extension/`）の規約

- MV3 / サイドパネル統一（popup 不使用）
- `background.js` は Service Worker として拡張イベントを仲介
- API 呼び出しは `API_BASE` 経由。CORS 許可は `CORS_ALLOW_ORIGINS` に登録された
  `chrome-extension://<拡張機能ID>` のみ
- 拡張機能 API の契約は `extension_contract_test.go` で検証する（後方互換を壊さない）

### SSR / 静的アセット

- テンプレートは `templates/`、JS / CSS は `static/`
- 依存を増やさず Vanilla JS + 標準 `html/template` で完結させる方針
- インライン script は CSP との兼ね合いがあるため、追加時は影響範囲を確認する

---

## テスト規約

### 共通（言語非依存・必ず守る）

#### 粒度の使い分け

- **単体テスト**: 純粋関数・個別パッケージ（`internal/urlnorm`, `internal/tag` 等）。最も数が多い
- **結合テスト**: DB / 外部サービスを介したユースケース。モックより実物（テスト用 DB / テストサーバ）を優先
- **E2E**: 主要ユーザーストーリー（拡張機能 login/save/search/logout、PWA Quick Add 等）の
  ゴールデンパスに絞る。網羅を狙わない

#### 命名と構造

- テスト名だけで「何を検証しているか」が分かる形にする
- 各テストは **Arrange / Act / Assert** の 3 パートに明示的に分離する
- **1 テスト = 1 検証対象**。複数観点を 1 つのテストにまとめない

#### モック方針

- **モックしてよい**: HTTP（外部）/ 時刻 / ファイル / 外部 SDK
- **モックしない**: 自分が書いた純粋ロジック、`internal/store` の SQL 経路（実 DB を使う）
- 認証・マイグレーションなどモックと本番挙動が乖離しやすい領域は、実物に近い fixture を優先

#### カバレッジ・観点

- 目標は **変更箇所の分岐をすべてカバー**。全体カバレッジ率は KPI にしない
- 各 AC に対して、正常系だけでなく **異常系・境界値・空入力を最低 1 ケース**用意する
- AC と 1 対 1 に紐付かないテストは spec に戻って AC を追加するか、テスト自体を削除する

#### 運用

- **flaky テスト**は quarantine せず、原因を特定して修正するか削除する。一時的 skip を入れた場合は即時に Issue 化
- **Red → Green → Refactor**: 新規テストは一度失敗することを確認してから実装で通す

### Go プロジェクトの規約

- **配置**: `*_test.go` を対象パッケージと同一ディレクトリに置く（例:
  `internal/urlnorm/urlnorm_test.go`）
- **命名**: `TestXxx`、サブテストは `t.Run("<条件>_<期待結果>", func(t *testing.T) {...})`
- **テーブル駆動**: URL 正規化（`internal/urlnorm`）・タグ正規化（`internal/tag`）・検索クエリは
  table-driven test を優先する
- **fixture**: パッケージ直下の `testdata/` に配置する
- **DB を絡める**: 実 PostgreSQL を起動して通す。`pgxpool` レイヤをモックしない
- **拡張機能 API の契約**: `extension_contract_test.go` で API レスポンス互換性を担保する

### 拡張機能のテスト

- `node --test extension/sidepanel.test.mjs` で自動テストを通す
- 加えて手動 E2E（login / save / search / logout）を必要に応じて実施

### 実行コマンド

```bash
# Go テスト
go test ./...

# Lint
golangci-lint run

# Extension テスト
node --test extension/sidepanel.test.mjs

# API スモークテスト（要 API 起動）
API_BASE=http://localhost:8080 ./scripts/test-api.sh
```

---

## ブランチ・コミット規約

- ブランチ名: `claude/issue-<番号>-<slug>` を原則とする
- コミット: [Conventional Commits](https://www.conventionalcommits.org/) 準拠
  - `feat(scope)` / `fix(scope)` / `test(scope)` / `docs(scope)` / `refactor(scope)` /
    `chore(scope)` / `style(scope)`
  - scope の例: `items`, `tags`, `fetcher`, `worker`, `auth`, `extension`, `mcp`, `deploy`
- 1 PR = 1 Issue を原則とする（スコープが膨らむ場合は PM が Issue 分割を提案）

---

## マイグレーション規約

- マイグレーションファイルは `migrations/NNN_<name>.sql`（例: `004_mcp_api_keys.sql`）
- **既存番号は再利用しない**、過去ファイルの中身は変更しない（追記・修正禁止）
- 新規スキーマ変更は次番号で追加する
- 適用は `psql ... -f migrations/...` で手動（自動化方針は Issue #87 で議論中）
- forward-only を基本とし、down マイグレーションは必須としない

---

## 環境変数とシークレット

- 機密情報（`SESSION_SECRET` / `JWT_SECRET` / `GOOGLE_CLIENT_SECRET` / OAuth 関連）は
  **必ず環境変数**で管理する
- `.env` / `deploy/.env.production` は **コミット禁止**（`.gitignore` 登録済み）
- `APP_ENV=production` では `CORS_ALLOW_ORIGINS` 必須（未設定時は起動 panic）
- ログにトークン・Cookie・OAuth 生レスポンスを出力しない（`internal/logger` で統制）

必須環境変数の一覧は `README.md` の「必須環境変数」を参照。

---

## 禁止事項

- `main` ブランチへの直接 push
- `.env` や実値を含む Secrets のコミット
- 外部サービス呼び出し時に API Key を埋め込むこと（環境変数化を徹底）
- 公開リポジトリ上の第三者コードを、ライセンス確認なしにコピペすること
- テストをコメントアウトして PR を出すこと（scope 外に分離する場合は Issue を切る）
- テストを通すために実装ではなくテスト側を書き換えて弱めること
  （mock を過度に強める / assert を緩める / `*_test.go` を盲目的に書き換える等）
- ハンドラ／ライブラリ層での `panic`（起動時 fail-fast を除く）
- 既存マイグレーション（`migrations/NNN_*.sql`）の中身を書き換えること
- 拡張機能 API レスポンスの後方互換を壊すこと（`extension_contract_test.go` を盲目的に更新しない）
- ログにトークン・Cookie・OAuth 生レスポンスを出力すること

---

## エージェント連携ルール

- **Product Manager** は実装方針を書かない。要件と受入基準の明確化に専念する
- **Architect**（条件付き起動）は要件を変更しない。モジュール構成・データモデル・公開 IF・
  処理フロー・実装分割の設計に専念する
- **Developer** は仕様を追加・解釈しない。不明点があれば PM / Architect に差し戻す
- **Reviewer**（impl 系モードで自動起動）は Developer 完了後の独立レビューのみを担当し、
  要件・設計・実装・テストの追加や書き換えを行わない。判定は AC 未カバー / missing test /
  boundary 逸脱 の 3 カテゴリに限定する（スタイル / lint 観点では reject しない）
- **Project Manager** はコードを変更しない。PR 作成と進捗管理に専念する
- Architect は Triage の `needs_architect: true` 判定時のみ PM と Developer の間に挟まれる
- Architect が起動した Issue では **設計 PR ゲート**を経由する（設計 PR を merge してから実装 PR が別途作られる）
- Reviewer は impl / impl-resume の Developer 完了直後に **独立 context** で起動され、
  reject 時は Developer に最大 1 回だけ自動差し戻し、再 reject では `claude-failed` で
  人間に委ねる（差し戻しループは Reviewer 最大 2 回 / Developer 最大 2 回で打ち切り）
- **`impl-resume` の branch policy（idd-claude 側 opt-in / #67）**:
  - idd-claude 側で `IMPL_RESUME_PRESERVE_COMMITS=true` を有効化したリポジトリの場合、
    `impl-resume` モードは既存 origin branch の commit を温存したまま resume する。Developer は
    `git reset` / `git rebase` / branch 切替を行わず、未完了タスクの先頭から続行する
  - 同条件下で `IMPL_RESUME_PROGRESS_TRACKING=true`（既定）が有効なら、Developer は各タスク完了
    ごとに `tasks.md` の `- [ ]` → `- [x]` 行内編集を行い、`docs(tasks): mark <task-id> as done`
    で **専用 commit** を積む。タスク本文 / `_Requirements:_` / `_Boundary:_` / `_Depends:_` /
    順序は変更しない
  - 詳細規約は `.claude/agents/developer.md` の「impl-resume / tasks.md 進捗追跡規約」節を参照
  - opt-in 機能 OFF / 無宣言の場合は本ルールは適用されない（既定挙動: `origin/main` 起点で fresh init + force-push）
- Developer は **実装 PR** で `design.md` / `tasks.md` / `requirements.md` を書き換えない
  （設計 PR で人間レビュー済みのため）。矛盾は PR 本文「確認事項」で指摘する
- **PR Iteration（`needs-iteration` ラベル）の責務境界**:
  - **設計 PR (`claude/issue-<N>-design-<slug>`)** で `needs-iteration` が付いた場合、
    watcher が次サイクルで Architect 役割の iteration を起動する。`docs/specs/<N>-<slug>/`
    配下（`requirements.md` / `design.md` / `tasks.md`）の **書き換えは許容** され、
    成功時 `awaiting-design-review` に遷移する
  - **実装 PR (`claude/issue-<N>-impl-<slug>`)** で `needs-iteration` が付いた場合、
    watcher が次サイクルで Developer 役割の iteration を起動する。`docs/specs/<N>-<slug>/`
    配下の **spec 書き換えは禁止** で、矛盾は PR 本文「確認事項」で指摘するに留める。
    成功時 `ready-for-review` に遷移する
  - **1 PR = design or impl のどちらか**（混在禁止）。1 PR で spec 編集と実装変更を同居させない
  - 設計 PR iteration は idd-claude 側で `PR_ITERATION_DESIGN_ENABLED=true` の opt-in が必要
- 各エージェントの成果物は `docs/specs/<番号>-<slug>/` 配下に保存する（Kiro / cc-sdd 互換）
  - `requirements.md`（PM）— EARS 形式の AC、numeric 階層 ID
  - `design.md`（Architect、条件付き）— File Structure Plan / Components and Interfaces / Traceability
  - `tasks.md`（Architect、条件付き）— `_Requirements:_` / `_Boundary:_` / `_Depends:_` / `(P)` アノテーション
  - `impl-notes.md`（Developer、補足）
  - `review-notes.md`（Reviewer、impl 系モードのみ）— 判定結果と Findings / 最終行 `RESULT: approve|reject`
- `<slug>` は Issue タイトルを lowercase・ハイフン区切り・40 文字以内に正規化した値。既存ディレクトリがあれば流用する

## エージェントが参照する共通ルール（`.claude/rules/`）

各エージェントは作業前に以下のルールを `Read` で読み込みます。

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
- [ ] `go test ./...` がローカルで通っている（CI と同一コマンド）
- [ ] `golangci-lint run` がエラーゼロ
- [ ] 拡張機能を変更した場合、`node --test extension/sidepanel.test.mjs` が成功している
- [ ] DB スキーマを変更した場合、`migrations/NNN_*.sql` が新規番号で追加されている
- [ ] 環境変数を追加した場合、`README.md` の「必須環境変数」と `.env.example` /
      `deploy/.env.production.example` が更新されている
- [ ] 既存テストが壊れていない（特に `extension_contract_test.go`）
- [ ] PR 本文に「確認事項」セクションがある（レビュワー判断ポイントを明示）

---

## 機密情報の扱い

- 本リポジトリは個人／小規模チーム向けセルフホストサービス。**顧客 PII や本番認証情報は
  扱わない**前提（自分のデータのみ）
- それでも以下は絶対にコミット・ログ出力しないこと:
  - OAuth client secret、JWT 署名キー、セッションシークレット
  - `refresh_token` / `access_token` / セッション Cookie の生値
  - DB 接続文字列に含まれるパスワード（`DATABASE_URL`）
- もし Issue 本文に第三者の機密情報・PII が含まれていた場合、PM エージェントは実装を進めず
  `needs-decisions` で人間にエスカレーションすること

---

## Feature Flag Protocol

> **デフォルトは opt-out です**。本節を `opt-in` に変えない限り、通常の単一実装パスで動作します。

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

---

## 関連ドキュメント

- `README.md` — クイックスタート、必須環境変数、API 概要、拡張機能ロード手順、デプロイ
- `AGENTS.md` — Codex 向けの最小ガイド（このファイルの subset、Codex 利用時のみ参照）
- `.kiro/steering/product.md` — プロダクト方針・価値提案
- `.kiro/steering/tech.md` — 技術選定の意思決定
- `.kiro/steering/structure.md` — ディレクトリ・命名・レイヤ規約
- `.kiro/specs/<feature>/` — 個別仕様（Kiro spec 形式）
- `docs/specs/<番号>-<slug>/` — idd-claude エージェントが生成する spec 成果物
- `docs/smoke-test.md` — API スモークテスト手順
- `docs/production-docker-deploy*.md` — 本番デプロイ（Linux / Windows）
- `docs/ui-design-system.md` — UI デザインシステム
- 各サブエージェントの詳細定義: `.claude/agents/*.md`
- ワークフロー全体像: idd-claude テンプレート README
