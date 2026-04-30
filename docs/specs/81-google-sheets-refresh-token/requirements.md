# Requirements Document

## Introduction

altpocket は Google Sheets エクスポート機能のために、利用者が許可した Google アカウントの
`refresh_token` を `user_google_sheets_connections.refresh_token` に保存している。現状この
値はアプリ層・DB ともに平文で扱われており、DB ダンプ流出や読み取り権限が漏洩した場合に
利用者の Google アカウント権限が悪用されるリスクがある。本機能では `refresh_token` を
アプリ層で対称暗号化（AES-GCM）してから保存し、利用時にアプリ層で復号する経路を確立する。
暗号鍵は環境変数で管理し、未設定／不正フォーマット時は起動を fail-fast させる。既存利用者の
平文トークンの取り扱いは「再認可方式（Option B）」を採用し、自動バックフィルは行わない。

参照: [Issue #81](https://github.com/hitoshiichikawa/altpocket-server/issues/81)

## Requirements

### Requirement 1: refresh_token の暗号化書き込み

**Objective:** As a altpocket を運用する開発者, I want Google Sheets 接続の `refresh_token`
が DB に書き込まれる時点で暗号化されていること, so that DB ダンプ流出時にトークンを
そのまま悪用されない

#### Acceptance Criteria

1. When 利用者の Google Sheets 接続を新規に保存する処理が呼び出された, the system shall
   `refresh_token` を AES-GCM（鍵長 256bit）で暗号化した値として `user_google_sheets_connections`
   の対応カラムに格納する
2. When 利用者の既存 Google Sheets 接続を更新する処理が呼び出された, the system shall
   新しい `refresh_token` を暗号化した値として上書き保存し、平文値を DB に残さない
3. When `refresh_token` を暗号化する, the system shall 暗号化のたびに新しい nonce を生成し、
   暗号文と nonce を一体（または同一カラム内）で復号可能な形式で永続化する
4. The system shall 暗号化された `refresh_token` の永続化形式（バイト列のエンコード方法・
   nonce／認証タグの位置）を 1 種類に統一し、同一鍵で書き込んだ値を復号側が一意に
   解釈できるようにする
5. The system shall 暗号化処理および永続化形式について、暗号鍵そのもの・平文 `refresh_token`・
   復号後の値・OAuth レスポンス全文をログ・エラーメッセージ・メトリクスに出力しない

### Requirement 2: refresh_token の復号読み出し

**Objective:** As a Google Sheets エクスポートを実行する利用者, I want 接続済みアカウントで
エクスポートが従来通り動作すること, so that 暗号化導入後も既存ユースケースが壊れない

#### Acceptance Criteria

1. When Google Sheets エクスポートまたは接続情報参照のため `refresh_token` が必要になった,
   the system shall DB から暗号化された値を読み出してアプリ層で復号し、復号後の平文値を
   Google API クライアントに渡す
2. While 1 件の `refresh_token` を復号している, the system shall 復号後の平文値をリクエスト
   コンテキストの寿命を超えてプロセス内に保持しない（永続化・グローバル変数・キャッシュ等
   への保管をしない）
3. If 暗号文の復号に失敗した（鍵不一致／改ざん検知／フォーマット不整合）, the system shall
   呼び出し元へ復号失敗エラーを返し、その接続を「未接続相当」または「再認可が必要」として
   既存のエラーフローに合流させる
4. If 暗号文の復号に失敗した, the system shall 復号失敗を構造化ログに記録するが、暗号文・
   鍵・平文値そのものはログ出力しない
5. The system shall 平文と判別可能な値（暗号化されていないレガシーデータ）を読み出した
   場合に、それを通常の復号成功として扱わない

### Requirement 3: 暗号鍵の環境変数読み込みと fail-fast

**Objective:** As a altpocket を運用する開発者, I want 暗号鍵の取り扱いミスが起動時に
明示的に検知されること, so that 鍵未設定のまま稼働して暗号化が無効化される事故を防げる

#### Acceptance Criteria

1. When アプリケーション（`cmd/api` および `cmd/worker` のうち、`refresh_token` の暗号化／
   復号を行う側）が起動する, the system shall 環境変数 `ENCRYPTION_KEY`（または同等の
   ドキュメント化された変数名）から暗号鍵を読み込む
2. If `ENCRYPTION_KEY` が未設定または空文字である, the system shall 起動を中止し、
   どの環境変数が必要かを示すエラーメッセージを出して終了する（fail-fast）
3. If `ENCRYPTION_KEY` の値が想定するエンコード形式（例: base64／hex）として復号できない,
   the system shall 起動を中止し、フォーマット異常を示すエラーメッセージを出して終了する
4. If `ENCRYPTION_KEY` をデコードした結果のバイト長が AES-GCM が要求する鍵長（256bit = 32
   バイト）と一致しない, the system shall 起動を中止し、必要な鍵長を示すエラーメッセージを
   出して終了する
5. The system shall 起動失敗のエラーメッセージに鍵そのもの（生値・先頭末尾の一部・ハッシュ）
   を含めない

### Requirement 4: 既存平文データの移行（再認可方式 / Option B）

**Objective:** As a altpocket をすでに利用している自分自身（セルフホスト運用者兼利用者）,
I want 暗号化導入時の移行手順が明確であること, so that 既存の平文 `refresh_token` を
安全な状態へ移行できる

#### Acceptance Criteria

1. When 暗号化機能を本番環境に投入する, the system shall 既存の `user_google_sheets_connections`
   レコードのうち平文 `refresh_token` を保持している行を、利用者が再認可するまで「未接続
   相当」として扱う（自動バックフィル／自動再暗号化は行わない）
2. The system shall 既存平文レコードを暗号化済みデータと自動マージしないことをドキュメントで
   明示する（移行は再認可によってのみ完了する）
3. The system shall 移行手順として、既存の `user_google_sheets_connections` レコードを
   削除または無効化する SQL／運用手順、および利用者に Google Sheets 連携の再設定（再認可）
   を求める案内を、リポジトリ内ドキュメント（`README.md` 必須環境変数節か関連ドキュメント、
   または `docs/` 配下の専用節）に記載する
4. When 利用者が再認可フローを完了した, the system shall Requirement 1 に従い `refresh_token`
   を暗号化して保存し、以後 Requirement 2 の読み出し経路で復号して使う
5. The system shall 自動バックフィル（既存平文値をアプリ起動時／バッチで暗号化する処理）を
   実装しない

### Requirement 5: 鍵ローテーション方針のドキュメント化

**Objective:** As a altpocket を長期運用する開発者, I want 鍵ローテーションが必要になった
ときの手順が事前に文書化されていること, so that 鍵漏洩疑いや定期更新時に判断・実行できる

#### Acceptance Criteria

1. The system shall 鍵ローテーション方針（鍵差し替え時に既存暗号化済みレコードをどう扱うか／
   再認可で対応するか／二重鍵での再暗号化を行うか）をリポジトリ内ドキュメントに明記する
2. The system shall 鍵差し替え時の運用手順（新しい `ENCRYPTION_KEY` の生成方法・反映方法・
   既存接続の取り扱い）を箇条書きまたは番号付き手順としてドキュメントに記載する
3. The system shall 鍵自動ローテーション機構を実装しないこと（手動運用に留めること）を
   ドキュメントで明示する
4. The system shall 鍵の生成例（例: 32 バイト乱数を base64 等でエンコードする方法）を
   コマンドサンプル付きで提示する

### Requirement 6: 環境変数ドキュメントの更新

**Objective:** As a altpocket を初めてセットアップする開発者, I want 必須環境変数として
`ENCRYPTION_KEY` が示されていること, so that 起動失敗の原因をすぐに特定できる

#### Acceptance Criteria

1. When 開発者が `README.md` の「必須環境変数」節を参照した, the system shall
   `ENCRYPTION_KEY`（または同等変数名）が必須環境変数の一覧に記載されていることを示す
2. The system shall 環境変数サンプルファイル（`deploy/.env.production.example` および
   `.env.example` 相当が存在する場合はそれら）に `ENCRYPTION_KEY` のキーと記入例を追加する
3. The system shall ドキュメント上で `ENCRYPTION_KEY` の用途（Google Sheets 接続の
   `refresh_token` の暗号化）と要求される形式（エンコード／鍵長）を明記する

### Requirement 7: 単体テストでの暗号化・復号往復検証

**Objective:** As a altpocket をメンテナンスする開発者, I want 暗号化／復号の挙動が単体
テストで保証されていること, so that 将来の変更で互換性が壊れたときに即座に検知できる

#### Acceptance Criteria

1. The system shall 同一の鍵で暗号化した値を復号すると元の平文 `refresh_token` 文字列に
   一致することを検証する単体テストを含む
2. The system shall 異なる鍵で復号を試みた場合に復号エラーを返すことを検証する単体テストを
   含む
3. The system shall 暗号文または認証タグを 1 バイト改ざんした入力に対して復号が失敗する
   ことを検証する単体テストを含む
4. The system shall 同じ平文・同じ鍵で暗号化を 2 回行った結果が、毎回異なる暗号文（nonce が
   異なる）になることを検証する単体テストを含む
5. The system shall 空文字列の `refresh_token` を入力した場合の挙動（暗号化を拒否するか／
   または無害な値として扱うか）を 1 ケース以上の単体テストで明示する
6. The system shall 鍵長不正・base64 不正・nil／空入力など想定外入力に対するエラー経路を
   最低 1 ケースずつ単体テストで覆う

## Non-Functional Requirements

### NFR 1: セキュリティ

1. The system shall `refresh_token` の暗号化方式として AES-GCM（256bit 鍵）を用い、独自暗号
   またはモード非認証の方式（ECB／CBC without MAC 等）を用いない
2. The system shall 暗号鍵を DB・ログ・トレース・エラーレスポンス・メトリクスに出力しない
3. The system shall 復号後の `refresh_token` 平文を必要最小限のスコープでのみ保持し、
   永続化キャッシュやグローバル変数に格納しない

### NFR 2: 互換性・後方互換

1. The system shall 既存の Google Sheets エクスポート API・UI（`/ui/settings/google/...`）の
   外部から見える挙動（HTTP メソッド・レスポンスステータス・リダイレクト先）を変更しない
2. The system shall 既存の自動テストスイート（`go test ./...` および `extension_contract_test.go`）
   を破壊しない
3. The system shall マイグレーションは forward-only で追加し、既存の `migrations/002_*.sql` の
   中身は変更しない

### NFR 3: 運用性

1. The system shall `ENCRYPTION_KEY` 不整合（未設定／フォーマット不正／鍵長不一致）による
   起動失敗を運用者が特定できるよう、構造化ログまたは標準エラー出力にどの検証で失敗したか
   を出力する（鍵そのものは出さない）
2. The system shall 復号失敗エラーを `slog` の構造化ログでカウント可能な形（識別可能な
   イベント名／キー）で記録する

## Out of Scope

- HSM / クラウド KMS（GCP KMS, AWS KMS, Vault 等）連携。本要件では環境変数管理に留める
- 暗号鍵の自動ローテーション機構（cron／鍵バージョニング／ダブルキー再暗号化の自動化）
- 既存平文 `refresh_token` の自動バックフィル／自動再暗号化（再認可方式 = Option B のため
  対象外）
- `extension_refresh_tokens.token_hash`（拡張機能向けリフレッシュトークン）の暗号化
  方式変更。同テーブルは別設計（ハッシュ保存）であり本 Issue の対象外
- セッション Cookie・JWT・他の秘匿値の暗号化
- 暗号鍵以外の環境変数（`SESSION_SECRET` / `JWT_SECRET` / `GOOGLE_CLIENT_SECRET`）の
  取り扱い変更
- DB カラム型・インデックスの大幅な再設計（暗号文を保持できる程度の最小調整に留める）
- 監査ログ・SIEM 連携などのセキュリティ運用基盤の整備

## Open Questions

- OQ-1: 既存 `refresh_token` カラムの再利用 vs 新規カラム追加  
  既存 `user_google_sheets_connections.refresh_token`（`TEXT NOT NULL`）の値域（base64 等で
  エンコードした暗号文）に切り替えるか、暗号化用カラムを新設して旧カラムを段階的に廃止
  するかは設計判断（Architect 領分）。要件としては「既存平文は再認可で破棄」「暗号化形式は
  1 種類に統一」を満たす範囲で実装方針は問わない。
- OQ-2: 環境変数名  
  Issue 本文では `ENCRYPTION_KEY` が候補として挙がっている。複数の暗号化用途が将来増える
  可能性を考慮し、より具体的な名前（例: `GOOGLE_SHEETS_REFRESH_TOKEN_KEY`）を採るかは
  人間判断。本要件では `ENCRYPTION_KEY` を既定とし、最終決定は実装着手前に確定する想定。
- OQ-3: 復号失敗時の UX  
  Requirement 2 AC-3 では「未接続相当」または「再認可が必要」として扱うとしているが、
  利用者向けに表示するメッセージ（`/ui/settings` のステータス文言）の文言・パラメータ名は
  既存メッセージとの整合を取る必要がある。最終文言は実装段階で確認する。
- OQ-4: ローカル開発時のデフォルト鍵  
  開発容易性のため `APP_ENV=development` で鍵未設定時にフォールバック値を使うか否か。
  本要件では Requirement 3 AC-2 により「環境を問わず未設定なら fail-fast」を既定とするが、
  運用上の都合で例外を入れたい場合は人間判断を要する（現状は fail-fast を維持する想定）。
