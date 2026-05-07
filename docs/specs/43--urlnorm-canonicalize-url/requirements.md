# Requirements Document

## Introduction

`internal/urlnorm` の `Canonicalize` は `net/url` の `url.Parse` のみで入力検証を済ませており、
スキームやホストの妥当性を確認していない。その結果、空文字列・相対パス・`javascript:` 等の
非 HTTP(S) スキーム・ホストを欠いた URL が「正規化済み URL」として通り抜け、後段の本文取得
（`internal/fetcher`）や UI 表示（`templates/`）で危険な値や無意味な値が扱われるリスクがある。
本要件では `Canonicalize` に対して許可スキーム（`http` / `https`）とホスト存在の検証を追加し、
不正入力を呼び出し側で識別可能なエラーとして拒否する。既存の正常系（クエリ正規化・末尾
スラッシュ整形）の挙動は変更しない。

参照: [Issue #43](https://github.com/hitoshiichikawa/altpocket-server/issues/43)

## Requirements

### Requirement 1: スキームの検証

**Objective:** As an `urlnorm.Canonicalize` の呼び出し側, I want HTTP(S) 以外のスキームを持つ
入力を拒否してもらうこと, so that `javascript:` 等の危険スキームや非 Web URL が後段の保存・
取得・表示処理に流れない

#### Acceptance Criteria

1. When 入力 URL のスキームが `http` または `https` のいずれでもない（例: `javascript:`,
   `data:`, `file:`, `ftp:`, 大文字混在で正規化前の任意スキーム等）, the `Canonicalize`
   function shall エラーを返し、`canonicalURL` と `canonicalHash` は空文字列とする
2. When 入力 URL がスキームを欠いている（例: `example.com/path`, `/relative/path`,
   `//example.com/path`）, the `Canonicalize` function shall エラーを返し、
   `canonicalURL` と `canonicalHash` は空文字列とする
3. When 入力 URL のスキームが大文字混在（例: `HTTP://example.com`, `Https://example.com`）
   である, the `Canonicalize` function shall 既存の `net/url` 標準挙動に従って小文字スキーム
   と同等に扱い、`http` / `https` として受け入れる

### Requirement 2: ホストの検証

**Objective:** As an `urlnorm.Canonicalize` の呼び出し側, I want ホストを持たない入力を
拒否してもらうこと, so that 解決対象を特定できない URL が DB に保存されない

#### Acceptance Criteria

1. When 入力 URL の `Host` が空文字列である（例: `http:///path`, `https://`）, the
   `Canonicalize` function shall エラーを返し、`canonicalURL` と `canonicalHash` は空文字列
   とする
2. When 入力が空文字列 `""` である, the `Canonicalize` function shall エラーを返し、
   `canonicalURL` と `canonicalHash` は空文字列とする

### Requirement 3: エラーの識別可能性（sentinel error）

**Objective:** As `internal/server` などの呼び出し側, I want スキーム / ホスト不正による
拒否を `errors.Is` で識別できること, so that 既存の `errInvalidURL` への変換などエラー応答
を呼び出し側で一貫して制御できる

#### Acceptance Criteria

1. The `urlnorm` package shall 不正入力（Requirement 1 / Requirement 2 で拒否されるもの）
   に対して返すエラーを公開された sentinel error として提供する
2. When `Canonicalize` が Requirement 1 または Requirement 2 の理由で拒否した, the
   returned error shall `errors.Is` でその sentinel error と一致する
3. When `Canonicalize` が `url.Parse` 自体の構文エラーで失敗した, the returned error shall
   `url.Parse` 由来のエラーをラップして返し、Requirement 3 AC-1 の sentinel error とは
   区別できるものとする
4. The `urlnorm` package shall sentinel error の名称・型を godoc コメントで公開し、
   呼び出し側が依存できる公開 API として明示する

### Requirement 4: 既存正常系の互換性維持

**Objective:** As 既存の `Canonicalize` 利用箇所（`internal/server/server.go` の
`createItem` 等）, I want 通常の HTTP(S) URL に対する正規化結果が変化しないこと, so that
既存に保存済みのアイテムや既存テストの期待値が破壊されない

#### Acceptance Criteria

1. When 入力が `http://example.com` または `https://example.com/` のような正当な絶対 URL
   である, the `Canonicalize` function shall 従来と同一の `canonicalURL` と `canonicalHash`
   を返す
2. When 入力が `https://example.com/page?utm_source=a&x=1` 等のトラッキングパラメータ
   （`utm_*` / `fbclid` / `gclid`）を含む正当な URL である, the `Canonicalize` function
   shall 既存仕様どおりトラッキングパラメータを除去した `canonicalURL` を返す
3. When 入力が `https://example.com/page/` 等の末尾スラッシュ付き正当な URL である, the
   `Canonicalize` function shall 既存仕様どおり末尾スラッシュを除去した `canonicalURL`
   を返す（ただしルート `/` は維持する）
4. The `Canonicalize` function shall 戻り値の型・順序・引数シグネチャを変更しない
   （`func Canonicalize(raw string) (canonicalURL string, canonicalHash string, err error)`）

### Requirement 5: テストカバレッジ

**Objective:** As リグレッションを防ぎたい開発者, I want 新規拒否ケースと既存正常系の双方
が単体テストでカバーされていること, so that 将来の改修で挙動退行が CI で検出される

#### Acceptance Criteria

1. The `internal/urlnorm/urlnorm_test.go` shall Requirement 1 で拒否される代表入力
   （最低でも `javascript:alert(1)` / `/relative/path` / スキーム欠落 URL）について
   エラーが返ることを検証するケースを追加する
2. The `internal/urlnorm/urlnorm_test.go` shall Requirement 2 で拒否される代表入力
   （最低でも 空文字列 `""` および ホスト欠落 URL）についてエラーが返ることを検証する
   ケースを追加する
3. The `internal/urlnorm/urlnorm_test.go` shall Requirement 3 の sentinel error を
   `errors.Is` で識別できることを検証するケースを追加する
4. The `internal/urlnorm/urlnorm_test.go` shall Requirement 4 で挙げた既存正常系
   （`utm_*` 除去 / `fbclid` 除去 / `gclid` 除去 / 末尾スラッシュ整形 / ルート `/` 維持）
   を引き続き検証するケースを保持する（既存ケースを削除・弱体化しない）

## Non-Functional Requirements

### NFR 1: 後方互換性

1. The `Canonicalize` function shall 公開シグネチャ（パッケージパス・関数名・引数・戻り値の
   型と順序）を変更しない
2. The `urlnorm` package shall 既存の正規化ルール（`utm_*` / `fbclid` / `gclid` 除去、
   末尾スラッシュ整形、クエリのキー昇順ソート）の挙動を変更しない

### NFR 2: 可観測性

1. The `Canonicalize` function shall 拒否時に返すエラーメッセージへ秘匿情報（セッション
   トークン・Cookie 値等）を含めない
2. The `Canonicalize` function shall 呼び出し側がログ出力する際に秘匿情報の流出が起きない
   よう、エラーメッセージに含めるのは「拒否理由（スキーム不正 / ホスト欠落 等）」のみ
   とする

### NFR 3: パフォーマンス

1. The `Canonicalize` function shall 追加の検証によって 1 回あたりの呼び出しに新規の I/O
   （DNS 解決・ネットワーク通信・ファイルアクセス等）を発生させない

## Out of Scope

- IDN（国際化ドメイン名）の Punycode 変換・正規化挙動の追加または変更
- DNS 解決による到達性チェック
- ホスト名のフォーマット（IP リテラル・予約語ホスト・private IP 帯域等）に関する追加
  バリデーション
- ポート番号の許可リスト・拒否リスト
- パスやクエリ値の XSS / SQLi 等の内容バリデーション
- 既存の `utm_*` / `fbclid` / `gclid` 以外のトラッキングパラメータの除去ルール拡張
- 呼び出し側（`internal/server` の `createItem` 等）のエラー応答仕様の変更
  （sentinel error を介した既存 `errInvalidURL` → HTTP 400 のフローはそのまま維持する想定）
- 既に DB に保存済みの不正 URL レコードに対するバックフィル / クリーンアップ

## Open Questions

- なし（Issue 本文に記載された方針で要件は確定できる。実装上の sentinel error 名や
  メッセージ文言、テストケースの個別命名は Architect / Developer の領分とする）
