# Requirements Document

## Introduction

altpocket の Worker 側 fetcher（`internal/fetcher`）は、利用者が登録した任意の URL を
そのまま HTTP GET してページ本文を抽出している。現状の実装は接続先 IP を検査して
いないため、利用者が悪意ある URL を保存すると、Worker プロセスからプライベート
ネットワーク・ループバック・リンクローカル（クラウドメタデータ 169.254.169.254 を含む）
へ HTTP リクエストが発生し、SSRF として悪用される恐れがある。本要件は fetcher が
公開インターネット上のホストにのみ接続するよう接続先 IP の検査と遮断ルールを定義し、
DNS rebinding（解決時と接続時で IP が変わる TOCTOU）も防げる挙動を規定する。

参照: [Issue #76](https://github.com/hitoshiichikawa/altpocket-server/issues/76)

## Requirements

### Requirement 1: プライベート／内部ネットワーク向け接続の遮断

**Objective:** As altpocket をセルフホストで運用する管理者, I want fetcher が
プライベート／ループバック／リンクローカル系の IP へは接続しないこと, so that
利用者保存 URL を経由した SSRF で内部ネットワークやクラウドメタデータが
覗かれない

#### Acceptance Criteria

1. When fetcher が URL を取得しようとし接続先ホストの解決結果に IPv4 ループバック
   （`127.0.0.0/8`）が含まれている, the fetcher shall そのリクエストの接続を
   確立せずにエラーを返す
2. When fetcher が URL を取得しようとし接続先ホストの解決結果に IPv4 プライベート
   （`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`）が含まれている, the fetcher
   shall そのリクエストの接続を確立せずにエラーを返す
3. When fetcher が URL を取得しようとし接続先ホストの解決結果に IPv4 リンクローカル
   （`169.254.0.0/16`、クラウドメタデータ `169.254.169.254` を含む）が含まれている,
   the fetcher shall そのリクエストの接続を確立せずにエラーを返す
4. When fetcher が URL を取得しようとし接続先ホストの解決結果に IPv6 ループバック
   （`::1`）, IPv6 ユニークローカル（`fc00::/7`）, IPv6 リンクローカル（`fe80::/10`）
   のいずれかが含まれている, the fetcher shall そのリクエストの接続を確立せずに
   エラーを返す
5. When fetcher が URL を取得しようとし接続先ホストの解決結果に未指定アドレス
   （`0.0.0.0`, `::`）, ブロードキャスト（`255.255.255.255`）, IPv4-mapped IPv6
   経由のプライベート／ループバック表記（例: `::ffff:127.0.0.1`,
   `::ffff:10.0.0.1`）のいずれかが含まれている, the fetcher shall そのリクエストの
   接続を確立せずにエラーを返す
6. If 接続先 URL が IP リテラル形式（例: `http://127.0.0.1/`, `http://[::1]/`,
   `http://2130706433/` 等の整数表記）でプライベート／ループバック／リンクローカル
   範囲を直接指していた, the fetcher shall DNS 解決を行わずに接続を拒否しエラーを
   返す
7. When fetcher が解決結果として複数 IP（IPv4/IPv6 混在を含む）を取得した, the
   fetcher shall いずれか 1 つでも禁止レンジに該当する場合は接続を確立せずに
   エラーを返す
8. When fetcher が公開 IP（例: `8.8.8.8`, グローバル IPv6）に解決される URL を
   取得しようとした, the fetcher shall 従来通り接続を確立して本文を取得する

### Requirement 2: DNS Rebinding（TOCTOU）耐性

**Objective:** As altpocket をセルフホストで運用する管理者, I want DNS 解決時と
実際の TCP 接続時で IP が変わるケース（DNS rebinding）でもブロックが効くこと,
so that 攻撃者が事前検査をすり抜けて内部 IP に接続するのを防げる

#### Acceptance Criteria

1. When fetcher が TCP 接続を確立する直前に、その接続が実際に向かう IP を
   取得した, the fetcher shall その IP が Requirement 1 の禁止レンジに該当する
   場合は接続を確立せずにエラーを返す
2. When DNS 解決結果が初回参照時は公開 IP・接続直前は禁止レンジへ変化していた
   （rebinding 想定）, the fetcher shall 接続直前の IP に基づいて接続を拒否し
   エラーを返す
3. When fetcher が HTTP リダイレクト先 URL に追従する, the fetcher shall リダイ
   レクト先の URL に対しても Requirement 1 と本要件 2 の検査を再適用する
4. The fetcher shall 接続先 IP の検査を、TCP 接続を実際に開く処理経路（例:
   `Transport.DialContext` 相当）の中で都度実施し、事前 DNS 解決結果のみを根拠に
   許可してはならない

### Requirement 3: 拒否時のエラー識別と監視ログ

**Objective:** As altpocket をセルフホストで運用する管理者, I want SSRF 防御で
遮断したリクエストが他のフェッチ失敗（タイムアウト・サイズ超過等）と区別して
記録されること, so that 攻撃検知や運用調査ができる

#### Acceptance Criteria

1. When fetcher が Requirement 1 または Requirement 2 により接続を拒否した,
   the fetcher shall 既存の `ErrTooLarge` / `ErrTooManyRedir` / `ErrBadStatus` /
   `ErrNoContent` とは別に識別できる sentinel エラー（または `errors.Is` で
   判別可能なエラー値）を呼び出し元へ返す
2. When Worker が SSRF 拒否エラーを受け取った, the Worker shall 既存の
   `classifyFetchError` 経路に SSRF 拒否専用の理由コードを追加し、対象 item を
   その理由で失敗扱いとして DB に反映する
3. When SSRF 拒否が発生した, the Worker shall 構造化ログ（`slog`）に拒否で
   あることを示すイベントを 1 行残し、対象の item ID と拒否カテゴリ（例:
   loopback / private / link_local / metadata 等）を出力する
4. The Worker shall SSRF 拒否ログに、利用者が保存した URL の生クエリ文字列・
   Cookie・トークン等の機密情報を含めない（既存の `internal/logger` 統制方針を
   維持する）

### Requirement 4: テストおよびビルトイン IP 検査の検証可能性

**Objective:** As 本機能を実装・回帰テストする開発者, I want SSRF 検査ロジックが
ユニットテスト可能で、既存の fetcher テストを破壊しないこと, so that CI で
継続的に回帰検出できる

#### Acceptance Criteria

1. The fetcher shall IP 範囲判定を独立したテスト可能なユニット（パッケージ内
   関数または型）として提供し、本要件 1 の各レンジ（loopback / private /
   link-local / IPv6 各種 / IPv4-mapped IPv6 / 整数リテラル等）について
   table-driven test で検証できる
2. When 既存テスト群（`internal/fetcher/fetcher_test.go`）が `roundTripFunc`
   ベースのカスタム `Transport` でレスポンスを差し替えている, the fetcher shall
   そのテスト経路を破壊せず、既存テストが緑のまま通る挙動を維持する
3. The fetcher shall SSRF 拒否動作（loopback / private / link-local / metadata
   IP / rebinding 想定）を検証する新規テストを最低 1 ケースずつ追加できる構造
   とする
4. The fetcher shall 公開 IP に解決される URL では従来通り接続できることを示す
   正常系テスト（モックまたは公開域用ホスト名を使った形）を維持・追加できる
   構造とする

## Non-Functional Requirements

### NFR 1: セキュリティ

1. The fetcher shall 既定状態（環境変数等の追加設定なし）で Requirement 1 / 2
   の遮断を有効化し、SSRF 防御が opt-in にならない
2. The fetcher shall ループバック・プライベート・リンクローカル・メタデータ
   IP への接続を「拒否がデフォルト・許可は明示」の方針で扱い、未知の禁止
   レンジが将来追加された場合に拒否側に倒せる設計を維持する
3. The Worker shall SSRF 拒否ログに利用者保存 URL の Authorization ヘッダ・
   Cookie・OAuth トークン等を出力しない

### NFR 2: 性能・可観測性

1. The fetcher shall SSRF 検査の追加によるフェッチ 1 件あたりの所要時間増加を
   通常ケース（公開 IP に解決される URL）で 5ms 未満に抑える
2. The Worker shall SSRF 拒否を通常のフェッチ失敗とは別カテゴリで集計可能に
   する（理由コードがユニークであれば集計手段は問わない）

### NFR 3: 互換性

1. The fetcher shall 公開 IP に解決される既存 URL に対するフェッチ挙動
   （タイトル抽出・本文抽出・サイズ制限・リダイレクト上限）を本要件導入前と
   同等に保つ
2. The Worker shall 既存の `extension_contract_test.go` を含む既存テスト一式を
   破壊しない

## Out of Scope

- HTTP/HTTPS フォワードプロキシ経由でのフェッチ（プロキシ導入は対象外）
- 許可リスト方式（特定ドメインのみ許可）
- ユーザー単位での SSRF ポリシー切替 UI
- 取得済み本文に含まれる内部 URL のサニタイズ・リライト
- 拒否された URL を利用者が再試行できる UI フロー（Worker が失敗扱いにする
  までを規定し、利用者向け表示文言は別 Issue とする）
- DNSSEC 検証や DNS over HTTPS の導入

## Open Questions

- OQ-1: テスト用 loopback 許可機構の要否  
  Issue 本文で「テスト時のローカルフェッチをどう扱うか（環境変数で許可リストを
  開ける等）」が人間判断項目とされている。リポジトリ調査の結果、
  `internal/fetcher/fetcher_test.go` は `httptest.NewServer` を使わず
  `roundTripFunc` でカスタム `Transport` を差し込む形式（実 TCP 接続を伴わない）
  であるため、`DialContext` 内に SSRF 検査を実装しても **既存ユニットテストは
  影響を受けない**見込みである。一方、将来 integration / E2E で
  `httptest.NewServer`（loopback bind）を使う場合は、(a) 環境変数による loopback
  許可フラグ（例: `FETCHER_ALLOW_LOOPBACK_FOR_TESTS`）を NFR 1 の例外として導入
  する、(b) テスト側が `Fetcher.Client` を差し替えて検査をバイパスする、(c)
  検査ロジックを依存注入（`AllowedIPChecker` 相当）で差し替え可能にする、の
  いずれを採用するかを Architect が決定する。本要件はデフォルト挙動として
  loopback を遮断する点のみを確定させ、テスト用 bypass の具体的方式は Open とする
- OQ-2: SSRF 拒否時の DB 上の理由コード文字列  
  Requirement 3 AC-2 で SSRF 拒否専用の理由コードを `classifyFetchError` に
  追加することは確定しているが、文字列値（例: `ssrf_blocked` / `private_ip` /
  `blocked_ip` 等）の選定は実装ファイル `cmd/worker/main.go` の既存命名慣行
  （`timeout`, `size_limit`, `redirect_limit`, `bad_status`, `no_content`）に
  揃える前提で Architect / Developer に委ねる
- OQ-3: ログ出力時の拒否カテゴリ粒度  
  Requirement 3 AC-3 では拒否カテゴリ（loopback / private / link_local /
  metadata 等）を出力すると規定したが、実装上「IPv6 ULA」「IPv4-mapped」を
  どこまで分けてラベリングするかは運用要件次第である。最低限「拒否されたこと」
  と「IP の所属レンジが大分類で何か」が分かれば足りる
