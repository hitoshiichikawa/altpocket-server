# プロジェクトガイド（Claude Code 全エージェント共通）

このファイルは Claude Code 本体および全サブエージェントが毎回参照するプロジェクト憲章です。
**すべてのエージェントは、作業開始前にこのファイルを読み直してください。**

---

## 技術スタック

- 言語 / ランタイム: Go 1.25（モジュール名 `altpocket`）
- HTTP ルータ: `go-chi/chi/v5`
- データベース: PostgreSQL 16 + `jackc/pgx/v5`
- マイグレーション: `migrations/NNN_<description>.sql`（番号付き SQL ファイル、追記のみ）
- 認証: `golang-jwt/jwt/v5` + Google OAuth2（`golang.org/x/oauth2`）
- MCP 連携: `modelcontextprotocol/go-sdk`
- HTML パース: `PuerkitoBio/goquery`
- フロントエンド: Go `html/template` によるサーバサイドレンダリング（`templates/`）+ Vanilla JS / PWA（`static/`）
- Chrome 拡張: `extension/` 配下（別ビルド、配布用）
- バイナリ: `cmd/api`（HTTP サーバ）/ `cmd/worker`（バックグラウンドワーカー）
- テスト: Go 標準 `testing` パッケージ（table-driven）
- Lint: `golangci-lint` v2.11.4
- コンテナ: Docker（`deploy/api.Dockerfile`, `deploy/worker.Dockerfile`）/ `docker-compose.yml`
- CI: GitHub Actions（`go test ./...` + `golangci-lint` + Docker build）

### ディレクトリ構成（要点）

```
cmd/<binary>/        # main パッケージ（api, worker）
internal/<domain>/   # ドメイン単位のパッケージ（auth, store, fetcher, mcpserver, ...）
migrations/          # 番号付き SQL マイグレーション
templates/           # html/template
static/              # PWA 用アセット
deploy/              # Dockerfile 等
docs/specs/          # Kiro / cc-sdd 準拠の spec
```

---

## コード規約

- **パッケージ配置**: ドメイン単位で `internal/<domain>/` に分割する（例: `auth`, `store`, `fetcher`, `mcpserver`）。`cmd/` 配下は `main` パッケージとエントリポイントのみ
- **命名**: パッケージ名は小文字・単数形・略語なし。エクスポート識別子は `CamelCase`、非エクスポートは `camelCase`。Go の一般的イディオム（`ctx`, `err`, receiver は短い英小文字 1-2 文字）に従う
- **Go doc コメント**: 公開（大文字始まり）関数・型・メソッド・パッケージには doc コメントを付与し、**対象名で始める**（例: `// Normalize trims and lowercases ...`）
- **関数**: 単一責務を原則とし、目安として 40 行以内。早期 return を優先しネストを浅く保つ
- **エラーハンドリング**:
  - `fmt.Errorf("...: %w", err)` で wrap し、呼び出し側は `errors.Is` / `errors.As` で判定する
  - エラーメッセージ文字列の比較による分岐はしない
  - センチネルエラーは `var ErrXxx = errors.New(...)` で公開する
- **ロギング**: `internal/logger` 経由に統一する。`fmt.Println` / `log.Println` の直接呼び出しは避ける
- **`context.Context`**: HTTP ハンドラ・外部 I/O・DB アクセスなど cancellable な処理では **第 1 引数として必ず受け取り伝播**させる（`context.Background()` をライブラリ内部で生成しない）
- **並行処理**: goroutine を起動する側が終了条件とリーク防止に責任を持つ（`context` キャンセル / `sync.WaitGroup` / `errgroup`）。無期限 goroutine は作らない
- **マジック値の排除**: マジックナンバー・マジックストリングはパッケージレベル `const` として命名する
- **SQL / DB**:
  - `pgx/v5` の API を使用する（`database/sql` は使わない）
  - プレースホルダは `$1, $2, ...`。**文字列連結で SQL を組み立てない**（SQL インジェクション防止）
  - 新規スキーマ変更は `migrations/NNN_<description>.sql` として追記する。**既存マイグレーションは変更しない**
- **HTTP ハンドラ**: `chi` のルータに登録する。リクエスト境界でのバリデーションと、レスポンス前のエラーマッピングを明示的に行う
- **設定値**: 秘匿値・環境差分は `internal/config` 経由で環境変数から読む。コード中にハードコードしない
- **インポート順**: 標準ライブラリ → サードパーティ → 内部（`altpocket/...`）の 3 グループに空行で区切る（`goimports` に準拠）
- **フォーマット / Lint**: `gofmt` / `goimports` / `golangci-lint run` を通過させる。CI の `golangci-lint` v2.11.4 を green にするまで PR を完了としない
- **テスト配置**: 対象コードと同一パッケージ内に `*_test.go` として配置する（Go 標準）

---

## テスト規約

### 粒度の使い分け

- **単体テスト**: 純粋関数・個別パッケージ内のロジック。`internal/<domain>/*_test.go` として配置し、最も数が多くなる層
- **結合テスト**: DB / HTTP ハンドラ / 外部サービスを介したユースケース。PostgreSQL を絡める場合はモックではなく **`docker-compose` で立てたテスト用 DB** もしくは `testcontainers-go` の実 DB を優先する
- **E2E**: 主要ユーザーストーリーのゴールデンパスに絞る。網羅を狙わない

### 命名と構造

- **テスト関数名**: `TestXxx` の Xxx は対象識別子そのまま（例: `TestNormalize`）。条件別に分けるときは `Test<対象>_<条件>` か、後述の table-driven で `name` フィールドに条件を書く
- **table-driven**: 複数ケースは `[]struct{ name string; ... }` のスライスで表現し、`t.Run(tc.name, func(t *testing.T){ ... })` でサブテスト化する（`go test -run` で個別実行できるようにするため）
- **ケース名**: `<条件>のとき<期待結果>` 形式を徹底し、テスト名だけで検証内容が分かるようにする（例: `"空文字列のとき空文字列を返す"`）
- **Arrange / Act / Assert**: 各テスト本体は 3 パートに明示的に分離する（コメントまたは空行で区切る）
- **1 テスト = 1 検証対象**。複数観点を 1 ケース / 1 サブテストにまとめない
- **ヘルパー**: `t.Helper()` を冒頭で呼んで失敗箇所が呼び出し側に出るようにする
- **並列化**: 独立したテストは `t.Parallel()` を付ける。ループ変数のキャプチャに注意（`tc := tc` の再束縛）

### モック / フェイク方針

- **実物を優先**: PostgreSQL（`jackc/pgx`）・HTTP サーバ・時刻など、実物で回せるものは `httptest.NewServer` / `docker-compose` のテスト DB / `testcontainers-go` を使う。認証・マイグレーションは挙動が乖離しやすいので実物必須
- **インターフェース + フェイク**: 外部境界をモックする場合は、テスト対象パッケージ側で最小インターフェースを定義し、テスト内に小さなフェイク実装を書く。モック生成ライブラリは必要になってから導入する
- **モックしない**: 自分が書いた純粋ロジック、テスト対象と同一パッケージ内の関数
- **時刻**: `time.Now()` を直接呼ばず、`func() time.Time` を DI するか `clock` を持たせる。テスト側で固定値を渡す
- **HTTP クライアント**: `*http.Client` を受け取る形にし、テストでは `httptest.NewServer` か `http.RoundTripper` 差し替えで固定レスポンスを返す

### カバレッジ・観点

- 目標は **変更箇所の分岐をすべてカバー**。全体カバレッジ率は KPI にしない（参考値として `go test -cover ./...` は取る）
- 各 AC に対して、正常系だけでなく **異常系・境界値・空入力を最低 1 ケース**用意する
- AC と 1 対 1 に紐付かないテストは spec に戻って AC を追加するか、テスト自体を削除する

### 運用

- **flaky テスト**は quarantine せず、原因を特定して修正するか削除する。一時的 skip（`t.Skip`）を入れた場合は即時に Issue 化する
- **テストデータ fixture**: 対象パッケージ直下の **`testdata/`**（Go の予約ディレクトリ、ビルド対象外）に集約する
- **goldenfile**: 長い期待値を比較する場合は `testdata/*.golden` として保存し、`-update` フラグで更新する関数を用意する（盲目的な更新はしない）
- **Red → Green → Refactor**: 新規テストは一度失敗することを確認してから実装で通す（書いた瞬間に pass するテストは観点不備を疑う）
- **実行**: ローカルでは `go test ./...`、結合テストは `-tags=integration` などビルドタグで分離する（CI の `go test ./...` を遅くしないため）

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
- **Project Manager** はコードを変更しない。PR 作成と進捗管理に専念する
- Architect は Triage の `needs_architect: true` 判定時のみ PM と Developer の間に挟まれる
- Architect が起動した Issue では **設計 PR ゲート**を経由する（設計 PR を merge してから実装 PR が別途作られる）
- Developer は `design.md` / `tasks.md` を書き換えない（設計 PR で人間レビュー済みのため）。矛盾は PR 本文「確認事項」で指摘する
- 各エージェントの成果物は `docs/specs/<番号>-<slug>/` 配下に保存する（Kiro / cc-sdd 互換）
  - `requirements.md`（PM）— EARS 形式の AC、numeric 階層 ID
  - `design.md`（Architect、条件付き）— File Structure Plan / Components and Interfaces / Traceability
  - `tasks.md`（Architect、条件付き）— `_Requirements:_` / `_Boundary:_` / `_Depends:_` / `(P)` アノテーション
  - `impl-notes.md`（Developer、補足）
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

## 参考資料

- 各サブエージェントの詳細定義: `.claude/agents/*.md`
- Triage プロンプト: `~/bin/triage-prompt.tmpl`
- ワークフロー全体像: `README.md`（または idd-claude テンプレート）
