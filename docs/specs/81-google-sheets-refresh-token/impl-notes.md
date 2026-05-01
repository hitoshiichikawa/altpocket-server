# Implementation Notes: Issue #81 — Google Sheets refresh_token 暗号化

## 概要

`docs/specs/81-google-sheets-refresh-token/{requirements,design,tasks}.md` に従って
8 タスク（1.1〜8.1）を順番に消化しました。設計 PR で人間レビュー済みの spec は
書き換えていません（進捗マーカー `- [ ]` → `- [x]` の行内更新のみ）。

## 受入基準とテストの対応表

| Requirement | カバーするテスト・実装 |
|---|---|
| 1.1 新規保存時に AES-GCM 暗号化 | `internal/store/store_encryption_test.go::TestUpsertAndGetGoogleSheetsConnection_RoundTrip` / `internal/crypto/crypto_test.go::TestEncryptDecryptRoundTrip` / `store.UpsertGoogleSheetsConnection` の Encrypt 経路 |
| 1.2 更新時も暗号化、平文を残さない | `TestUpsertGoogleSheetsConnection_PersistedValueIsNotPlaintext`（DB 直接 SELECT で平文≠ストア値を assert） |
| 1.3 暗号化のたびに新 nonce | `crypto_test.go::TestEncryptProducesUniqueNonce`（100 回ユニーク） / `store_encryption_test.go::TestUpsertGoogleSheetsConnection_NonceUniqueness`（DB 越し 2 回） |
| 1.4 永続化形式を 1 種類に統一 | `crypto.Encrypt`/`Decrypt` のレイアウト規約 + `crypto_test.go::TestEncryptDecryptRoundTrip` のサイズ assert |
| 1.5 鍵・平文・OAuth 応答をログに出さない | `store.UpsertGoogleSheetsConnection` で平文を error に含めない実装 + `internal/server/google_sheets_decrypt_test.go::TestErrRefreshTokenDecryptFailedIsExportedSentinel` がエラーチェーンの中身を検証（rest はコードレビュー観点） |
| 2.1 復号して Google API へ平文を渡す | `store_encryption_test.go::TestUpsertAndGetGoogleSheetsConnection_RoundTrip`（Get で平文一致） |
| 2.2 平文をリクエストコンテキスト超えて保持しない | `internal/server/server.go::handleUISettingsGoogleExport` の実装（ローカル変数、グローバル/cache 不使用、コメントで明示） |
| 2.3 復号失敗は呼び出し元に返し「未接続相当」に合流 | `store_encryption_test.go::TestGetGoogleSheetsConnection_WrongKeyRejected` / `internal/server/google_sheets_decrypt_test.go::TestErrRefreshTokenDecryptFailedIsExportedSentinel`（sentinel 区別が壊れないこと） |
| 2.4 復号失敗を構造化ログに記録、機密値は出さない | `handleUISettings` / `handleUISettingsGoogleExport` の `slog.Warn("settings.google_sheets.decrypt_failed", request_id, user_id)`（コードレビュー観点） |
| 2.5 平文判別可能な値を成功扱いしない | `store_encryption_test.go::TestGetGoogleSheetsConnection_LegacyPlaintextRejected` |
| 3.1 起動時 ENCRYPTION_KEY 読み込み | `internal/config/config_test.go::TestLoadAcceptsValidEncryptionKey` |
| 3.2 未設定なら fail-fast | `config_test.go::TestLoadPanicsWithoutEncryptionKey` |
| 3.3 base64 不正なら fail-fast | `config_test.go::TestLoadPanicsWithMalformedEncryptionKey` |
| 3.4 鍵長 32 byte でないなら fail-fast | `config_test.go::TestLoadPanicsWithWrongLengthEncryptionKey` |
| 3.5 エラーメッセージに鍵を含めない | `config_test.go::TestLoadPanicsWithMalformedEncryptionKey` / `TestLoadPanicsWithWrongLengthEncryptionKey` / `TestLoadEncryptionKeyPanicMessageOmitsKeyValue`（leak canary） |
| 4.1 既存平文行を「未接続相当」に | `migrations/005_invalidate_legacy_refresh_tokens.sql`（DELETE）+ `store_encryption_test.go::TestGetGoogleSheetsConnection_LegacyPlaintextRejected` |
| 4.2 自動マージしないことを文書化 | `docs/encryption-key-rotation.md` の「採用している鍵ローテーション戦略」節 |
| 4.3 移行手順を README/docs に記載 | `README.md` の「Google Sheets `refresh_token` 暗号化への移行（Issue #81）」節 |
| 4.4 再認可後は Req 1/2 経路で動く | `docs/smoke-test.md` の「Google Sheets `refresh_token` 暗号化のスモーク確認」節（手動 E2E） |
| 4.5 自動バックフィルを実装しない | コードに該当処理なし（コードレビュー観点）+ `docs/encryption-key-rotation.md` で明示 |
| 5.1 鍵ローテーション方針を明記 | `docs/encryption-key-rotation.md`「採用している鍵ローテーション戦略: 再認可方式」 |
| 5.2 鍵差し替え運用手順 | `docs/encryption-key-rotation.md`「ローテーション手順（番号付き）」 |
| 5.3 自動化しないことを明示 | `docs/encryption-key-rotation.md`「自動化はしません」節 |
| 5.4 鍵生成例 | `docs/encryption-key-rotation.md` 「鍵生成」節 + `README.md` |
| 6.1 README 必須環境変数節に追加 | `README.md` |
| 6.2 .env.example / production.example に追加 | `.env.example` / `deploy/.env.production.example` |
| 6.3 用途と要求形式を明記 | `README.md` 「`ENCRYPTION_KEY`（必須）」節 |
| 7.1 暗号化／復号の往復 | `crypto_test.go::TestEncryptDecryptRoundTrip` |
| 7.2 異なる鍵で復号失敗 | `crypto_test.go::TestDecryptWithWrongKey` |
| 7.3 改ざん検知 | `crypto_test.go::TestDecryptDetectsTampering`（4 ケース） |
| 7.4 同じ平文・鍵で異なる暗号文 | `crypto_test.go::TestEncryptProducesUniqueNonce` |
| 7.5 空文字列入力の挙動 | `crypto_test.go::TestEncryptEmptyInput`（拒否方針 `ErrEmptyPlaintext`） |
| 7.6 鍵長不正・base64 不正・nil／空入力 | `crypto_test.go::TestDecodeKeyRejectsMalformedInput` / `TestDecryptRejectsShortCiphertext` / `TestEncryptRejectsWrongKeyLength` / `TestDecryptRejectsWrongKeyLength` |
| NFR 1.1 AES-GCM 256bit | `crypto.Encrypt` / `Decrypt` 実装（`aes.NewCipher` + `cipher.NewGCM` + 32 byte 鍵） |
| NFR 1.2 鍵をログ等に出さない | `config_test.go::TestLoadEncryptionKeyPanicMessageOmitsKeyValue` + 全コードレビュー観点 |
| NFR 1.3 平文を最小スコープで保持 | `handleUISettingsGoogleExport` の実装＋コメント |
| NFR 2.1 外部 API・UI 挙動を変えない | `handleUISettingsGoogleExport` のリダイレクト先・HTTP ステータスは既存と同一 + `internal/server/google_sheets_decrypt_test.go::TestSettingsNoticeGoogleNotConnectedReusedForDecryptFailure` で notice 文言再利用を assert |
| NFR 2.2 既存テスト破壊しない | `go test ./...` 全 pass + `extension_contract_test.go` 不変更 |
| NFR 2.3 既存マイグレーション 002 不変更 | `migrations/005_*.sql` を新規追加のみ |
| NFR 3.1 起動失敗の理由を構造化／stderr | `mustDecodeEncryptionKey` の panic メッセージ（"missing env: X" / "invalid env: X (理由)"） |
| NFR 3.2 復号失敗をカウント可能なイベント名で記録 | `slog.Warn("settings.google_sheets.decrypt_failed", ...)`（identifier が固定） |

## 実装上の判断（design.md / requirements.md で曖昧だった点）

### D1. design.md の「`internal/store/store_encryption_test.go` を新規作成」を build tag `integration` で実装

design.md 595 行目に「実 PostgreSQL を使う想定。CI で DB を起動する既存パターンに合流」と
あり、**既存の `internal/store/mcp_api_key_test.go` が `//go:build integration`
build tag + `TEST_DATABASE_URL` skip パターンを採用していた**ため、それに合わせました。
これにより:

- デフォルトの `go test ./...` では skip されず、コンパイルだけが検証される
- DB を起動した CI ジョブまたはローカルで `TEST_DATABASE_URL=postgres://... go test
  -tags=integration ./internal/store/...` を実行することで実 DB 検証が可能
- `internal/server` のハンドラ単位テストは追加していない（既存設計に Store interface
  が無く、interface 抽出は別 Issue 領分）。代わりに sentinel error の export contract
  と notice 文言再利用契約を `internal/server/google_sheets_decrypt_test.go` で pin

### D2. `cmd/worker/main.go` も `store.New(pool, cfg.EncryptionKey)` に変更

tasks.md 4.1 は cmd/api のみを言及していますが、`store.New` のシグネチャ変更で worker
側も build エラーになるため、worker も同時に更新しました。design.md 339-340 行目
「worker は現在 refresh_token を使わないが、将来再利用しても矛盾しないよう
config.Load 共通で読み込む」という記述と整合しており、worker でも `ENCRYPTION_KEY`
未設定なら fail-fast します（既存 `config.Load` 経由）。

### D3. `crypto.Decrypt` の wrong-key と tampered-ciphertext を区別しない

design.md 313-314 行目「used for both wrong-key and tampering cases; callers must not
distinguish the two」に従い、GCM 認証失敗をすべて `ErrDecryptionFailed` に collapse
しています。これは GCM の特性（authentication tag 検証失敗時に内部エラーが分かれる
場合があるが、それを分岐させると oracle として攻撃に利用される）を踏まえた設計判断です。

### D4. `Store.encryptionKey` を unexported にした上で getter も提供しない

tasks.md 3.1 「Store.encryptionKey は unexported かつメソッドとして公開しない（外部
からの取り出しを構造的に防ぎ、誤ロギングを抑止する）」に従い、テストコードからも
直接読み取れない構造としました。テストでは「別鍵で構築した Store でも復号失敗する」
ことを観察することで間接的に key の挙動を検証しています。

### D5. legacy plaintext テストの実装

`TestGetGoogleSheetsConnection_LegacyPlaintextRejected` は、Google OAuth refresh_token
の典型形 `1//...` を `INSERT` しています。`1//legacy-plaintext-refresh-token` は
33 文字で、base64 標準デコード（パディング必須）に失敗するため `base64.StdEncoding.DecodeString`
の段階で `ErrRefreshTokenDecryptFailed` に collapse されます。仮に偶然 base64
として decode できる長さの平文が混ざっていたとしても、その後の `crypto.Decrypt` の
GCM 認証で失敗するので二重防御になります。

## 確認事項（PR 本文「確認事項」へ転記される想定）

実装中に気になった点で、**spec を書き換えずに**残しておきたい論点:

1. **`golangci-lint` がローカル環境にインストールされていない**ため、`go vet ./...` で
   代替検証しました（pass）。CI で `golangci-lint v2.11.4` を別途確認してください。
2. **integration テスト用の DB 接続が手元になかった**ため、`store_encryption_test.go`
   の compile-only 検証で済ませました。CI または reviewer 環境で
   `TEST_DATABASE_URL=postgres://... go test -tags=integration ./internal/store/...`
   を実行してテスト本体が通ることを確認してください（migrations 001..005 適用後の DB が
   必要）。
3. **`cmd/worker` も `ENCRYPTION_KEY` 必須化**しました（D2 参照）。design.md と整合
   していますが、既存運用者が worker だけ動かしているケースでも環境変数追加が必要に
   なる点は、リリースノート／README 移行手順で再確認をお願いします。
4. **handler 単位テストは追加せず**、sentinel error contract テストで代替しました（D1 参照）。
   `handleUISettings` / `handleUISettingsGoogleExport` の分岐網羅は手動スモークテスト
   （`docs/smoke-test.md` 追記節）で確認します。`*_test.go` で完全に網羅したい場合は
   Store interface 抽出を伴う別 Issue が適切と考えます。
5. **マイグレーション 005 は破壊的（全行 DELETE）**です。本機能の意図通りですが、
   既存利用者がいる環境で投入する前に「全員に再認可を依頼する」運用案内が必須です
   （`README.md` / `docs/encryption-key-rotation.md` で明記済み）。

## 派生タスク候補（次の Issue 候補）

- **Store interface 抽出**: `*store.Store` を interface 化することで handler 単位の
  unit test が書けるようになります。今回の Issue では scope 外としましたが、Issue #81
  の sentinel error 分岐の網羅テストは将来的にこの interface があると簡潔に書けます。
- **CI に integration test ジョブを追加**: 現状 CI では `go test ./...`（unit のみ）で、
  build tag `integration` のテストは手動実行です。Postgres コンテナを CI で起動して
  `-tags=integration` を回すジョブを追加すると `internal/store` の DB 経路の regression
  検知が自動化されます。
- **HSM / KMS 連携**: Out of Scope ですが、`docs/encryption-key-rotation.md` の最後で
  「規模が大きくなったら別 Issue で検討」と書いた通り、要件が出てから対応するのが妥当
  です。

## 追加した依存

なし。Go 標準ライブラリ（`crypto/aes` / `crypto/cipher` / `crypto/rand` /
`encoding/base64`）のみを利用しました（design.md「サードパーティ依存追加なし」と整合）。

## 実行コマンドサマリ（reviewer 向け）

```bash
# Unit / build / vet（CI と同じ）
go test ./...
go vet ./...
go build ./...

# Integration（要 PostgreSQL + migrations 001..005 適用済み）
TEST_DATABASE_URL=postgres://altpocket:altpocket@localhost:5432/altpocket?sslmode=disable \
  go test -tags=integration ./internal/store/...

# Lint（CI 環境向け、ローカル未インストールなら skip）
golangci-lint run
```

## 最終 commit / push 状態

- ブランチ: `claude/issue-81-impl-google-sheets-refresh-token`
- 実装 commit + 進捗 commit を交互に積んでおり、`tasks.md` の `- [ ]` は残っていません
- `git log --oneline main..HEAD` で全 commit が確認できます
