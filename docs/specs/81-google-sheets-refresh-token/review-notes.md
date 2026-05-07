# Review Notes

<!-- idd-claude:review round=1 model=claude-opus-4-7 timestamp=2026-05-01T00:00:00Z -->

## Reviewed Scope

- Branch: claude/issue-81-impl-google-sheets-refresh-token
- HEAD commit: bf0c313b89bfa5373294860b3267e82541ecce76
- Compared to: main..HEAD
- Feature Flag Protocol: opt-out（CLAUDE.md L303）→ 通常の 3 カテゴリ判定のみ適用

レビュー方針:
- requirements.md の numeric ID（1.1〜7.6 + NFR 1.x / 2.x / 3.x）の全件について、対応する実装またはテストが diff のいずれかで観測できるかを 1 つずつ突き合わせ
- impl-notes.md の AC マップは参照したが鵜呑みにせず、コードを直接読んで裏付けを取得
- `go test ./...` / `go build ./...` / `go vet -tags=integration ./internal/store/...` をローカル実行（全 pass）
- 注意点: integration test 本体の DB 越し実行はこちらの環境にも DB がないため未確認
  （Developer も同条件で compile-only 検証を申告。AC 上は単体テスト存在で要件充足、
  実 DB での挙動は smoke-test.md で人間が確認する手順になっている）

## Verified Requirements

### Requirement 1 — 暗号化書き込み

- 1.1 — `internal/store/store.go::UpsertGoogleSheetsConnection` で `crypto.Encrypt(s.encryptionKey, []byte(refreshToken))` → `base64.StdEncoding.EncodeToString` → DB へ書き込む経路を追加。`internal/store/store_encryption_test.go::TestUpsertAndGetGoogleSheetsConnection_RoundTrip` と `TestUpsertGoogleSheetsConnection_PersistedValueIsNotPlaintext` で実 DB 越しに検証
- 1.2 — `Upsert` の `ON CONFLICT (user_id) DO UPDATE SET refresh_token = EXCLUDED.refresh_token` で更新時も暗号化値を上書き。`TestUpsertGoogleSheetsConnection_NonceUniqueness` で 2 回目の Upsert も暗号化値が DB に書かれていることを観察
- 1.3 — `crypto.Encrypt` は呼び出しごとに `crypto/rand` で nonce を生成し `[nonce || ciphertext+tag]` を返す。`crypto_test.go::TestEncryptProducesUniqueNonce`（100 回） と `store_encryption_test.go::TestUpsertGoogleSheetsConnection_NonceUniqueness`（DB 越し 2 回）で検証
- 1.4 — フォーマットは「`[12B nonce] || [N+16B ciphertext+tag]` を base64 標準でエンコード」の 1 種類に統一（`crypto.go` のドキュメンテーションコメント + `Decrypt` の `blob[:NonceSize]` / `blob[NonceSize:]` 切り出し）
- 1.5 — `crypto.Decrypt` は `gcm.Open` のエラーを `ErrDecryptionFailed` に collapse して中身を捨てる（`crypto.go` L122-129）。`store.UpsertGoogleSheetsConnection` の error wrap は `fmt.Errorf("encrypt refresh_token: %w", err)` で平文を含めない。`store.GetGoogleSheetsConnection` は `ErrRefreshTokenDecryptFailed` を直接返し stored value を含めない。ハンドラの `slog.Warn("settings.google_sheets.decrypt_failed", request_id, user_id)` も鍵・暗号文・平文・OAuth レスポンスを出さない

### Requirement 2 — 復号読み出し

- 2.1 — `store.GetGoogleSheetsConnection` で base64 デコード → `crypto.Decrypt` → `c.RefreshToken = string(plaintext)`。`server.handleUISettingsGoogleExport` → `exportItemsToGoogleSheets` で `oauth2.Token{RefreshToken: conn.RefreshToken}` に渡される。`store_encryption_test.go::TestUpsertAndGetGoogleSheetsConnection_RoundTrip` で平文一致を検証
- 2.2 — `server.go::handleUISettingsGoogleExport` L1049-1053 のコメントとコードレビューで、`conn.RefreshToken` は handler のローカル変数として `exportItemsToGoogleSheets` に渡され、`oauth2.Token` のローカル変数に詰めるのみ（`server.go` L1490-1491）。グローバル変数・パッケージ変数・キャッシュへの保存なし
- 2.3 — `store.ErrRefreshTokenDecryptFailed` を export し、`handleUISettings` / `handleUISettingsGoogleExport` 双方で `errors.Is(err, store.ErrRefreshTokenDecryptFailed)` を `pgx.ErrNoRows` と同じ「未接続相当」分岐に合流させる（server.go L848-865, L1024-1048）。`internal/server/google_sheets_decrypt_test.go::TestErrRefreshTokenDecryptFailedIsExportedSentinel` で sentinel が `pgx.ErrNoRows` と区別可能であることを pin。実 DB 越しの観察は `store_encryption_test.go::TestGetGoogleSheetsConnection_WrongKeyRejected`
- 2.4 — `slog.Warn("settings.google_sheets.decrypt_failed", request_id, user_id)` を両ハンドラで記録。鍵・暗号文・平文・OAuth レスポンスは渡していない（コードレビューで確認）
- 2.5 — `store.GetGoogleSheetsConnection` の base64 デコード失敗 → `ErrRefreshTokenDecryptFailed`、GCM 認証失敗 → `ErrRefreshTokenDecryptFailed`。`store_encryption_test.go::TestGetGoogleSheetsConnection_LegacyPlaintextRejected` で `1//legacy-plaintext-refresh-token` を直接 INSERT した行が `ErrRefreshTokenDecryptFailed` で reject されることを検証

### Requirement 3 — 環境変数読み込みと fail-fast

- 3.1 — `internal/config/config.go::mustDecodeEncryptionKey` で `ENCRYPTION_KEY` を読み込み、`Config.EncryptionKey` に格納。`cmd/api/main.go` / `cmd/worker/main.go` の `store.New(pool, cfg.EncryptionKey)` で実際に使用。`TestLoadAcceptsValidEncryptionKey` で 32 byte 取得を確認
- 3.2 — `mustDecodeEncryptionKey` で `raw == ""` なら `panic("missing env: ENCRYPTION_KEY")`。`TestLoadPanicsWithoutEncryptionKey` で検証
- 3.3 — `crypto.DecodeKey` が `ErrMalformedKey` を返した場合 `panic("invalid env: ENCRYPTION_KEY (base64 decode failed)")`。`TestLoadPanicsWithMalformedEncryptionKey` で検証
- 3.4 — `crypto.DecodeKey` が `ErrInvalidKeyLength` を返した場合 `panic("invalid env: ENCRYPTION_KEY (must be 32 bytes after base64 decode)")`。`TestLoadPanicsWithWrongLengthEncryptionKey` で検証
- 3.5 — panic メッセージは固定文字列（`mustDecodeEncryptionKey` 各 case）で env 値を含めない。`TestLoadPanicsWithMalformedEncryptionKey` / `TestLoadPanicsWithWrongLengthEncryptionKey` で「キー値が panic message に含まれていない」ことを assert + `TestLoadEncryptionKeyPanicMessageOmitsKeyValue` で leak canary 文字列が含まれないことも追加検証

### Requirement 4 — 既存平文データの移行（Option B）

- 4.1 — `migrations/005_invalidate_legacy_refresh_tokens.sql` で `DELETE FROM user_google_sheets_connections;`。読み出し側は `pgx.ErrNoRows` または `ErrRefreshTokenDecryptFailed` のいずれでも「未接続相当」に合流する分岐を `handleUISettings` / `handleUISettingsGoogleExport` に追加（自動バックフィルなし）
- 4.2 — `docs/encryption-key-rotation.md` L21-25「二重鍵での自動再暗号化（migration ジョブ等）は実装していません」「既存平文レコードを暗号化済みデータと自動マージする処理も提供しません」と明記
- 4.3 — `README.md` L57-72「Google Sheets `refresh_token` 暗号化への移行（Issue #81）」節に番号付き手順（鍵生成→投入→migration 005 適用→API 再起動→利用者再認可）。`docs/encryption-key-rotation.md` も整備
- 4.4 — `docs/smoke-test.md` L48-101「Google Sheets `refresh_token` 暗号化のスモーク確認」で再認可後の往復成功を確認する手動 E2E 手順
- 4.5 — 自動バックフィル処理を実装していないことをコードレビューで確認（`grep -r backfill` で 0 件）。`docs/encryption-key-rotation.md` でも明示

### Requirement 5 — 鍵ローテーション方針

- 5.1 — `docs/encryption-key-rotation.md` L12-25「採用している鍵ローテーション戦略: 再認可方式」節
- 5.2 — `docs/encryption-key-rotation.md` L53-84「ローテーション手順（番号付き）」（5 ステップ）
- 5.3 — `docs/encryption-key-rotation.md` L86-93「自動化はしません（手動運用）」節
- 5.4 — `docs/encryption-key-rotation.md` L35-51「鍵生成」節（`openssl rand -base64 32` ＋ dd / Python の代替例）。`README.md` の必須環境変数節にも記載

### Requirement 6 — 環境変数ドキュメント

- 6.1 — `README.md` L25「`ENCRYPTION_KEY=replace_with_base64_of_32_random_bytes`」を必須環境変数の表に追加
- 6.2 — `.env.example` L13-15 と `deploy/.env.production.example` L19-24 に `ENCRYPTION_KEY` のサンプル行追加
- 6.3 — `README.md` L29-39「`ENCRYPTION_KEY`（必須）」節で用途（Google Sheets `refresh_token` 暗号化）と要求形式（base64 標準エンコード、デコード後 32 バイト）を明記

### Requirement 7 — 単体テスト

- 7.1 — `crypto_test.go::TestEncryptDecryptRoundTrip`
- 7.2 — `crypto_test.go::TestDecryptWithWrongKey`
- 7.3 — `crypto_test.go::TestDecryptDetectsTampering`（4 サブケース: nonce_first_byte / nonce_last_byte / ciphertext_first_byte / tag_last_byte_offset）
- 7.4 — `crypto_test.go::TestEncryptProducesUniqueNonce`（100 回）
- 7.5 — `crypto_test.go::TestEncryptEmptyInput`（空文字列＋nil で `ErrEmptyPlaintext`、拒否方針を明示）
- 7.6 — `crypto_test.go::TestDecodeKeyRejectsMalformedInput`（empty / not_base64 / 16 byte / 64 byte / 31 byte / 33 byte）+ `TestDecryptRejectsShortCiphertext`（nil / empty / shorter_than_nonce / exactly_nonce_size）+ `TestEncryptRejectsWrongKeyLength` + `TestDecryptRejectsWrongKeyLength`

### Non-Functional Requirements

- NFR 1.1 — `crypto.Encrypt`/`Decrypt` は `aes.NewCipher`（KeySize=32）+ `cipher.NewGCM` を使用。ECB / CBC-without-MAC / 独自暗号は不使用
- NFR 1.2 — 鍵を logger / panic message / error message に出さない実装。`config_test.go::TestLoadEncryptionKeyPanicMessageOmitsKeyValue` で leak canary 検証。コードレビューで `slog.Warn` 引数・error wrap 文字列も確認
- NFR 1.3 — 復号後 plaintext は `handleUISettingsGoogleExport` のローカル変数として `exportItemsToGoogleSheets` に渡し、`oauth2.Token` のローカル変数に詰めるのみ。グローバル変数・キャッシュ・goroutine 共有 channel への格納なし（コメントで明示・コードレビューで確認）
- NFR 2.1 — `handleUISettings` の HTTP メソッド/ステータス、`handleUISettingsGoogleExport` のリダイレクト先パス（`/ui/settings?status=google_not_connected`）は既存と同一。`TestSettingsNoticeGoogleNotConnectedReusedForDecryptFailure` で notice 文言の再利用契約も pin
- NFR 2.2 — `go test ./...` 全 pass（ローカル確認済み）。`extension_contract_test.go` への変更なし
- NFR 2.3 — `git diff main..HEAD -- migrations/002_*.sql` が空。`migrations/005_*.sql` を新規追加のみ
- NFR 3.1 — `mustDecodeEncryptionKey` の panic メッセージが「`missing env: X`」「`invalid env: X (base64 decode failed)`」「`invalid env: X (must be 32 bytes after base64 decode)`」のいずれかで、どの検証で失敗したかを stdout に出力
- NFR 3.2 — `slog.Warn("settings.google_sheets.decrypt_failed", ...)` の固定イベント名でカウント可能

## Boundary 確認

tasks.md の `_Boundary:_` は以下と整合:

- `internal/crypto`（task 1.1, 1.2）— `internal/crypto/crypto.go` / `crypto_test.go` のみ
- `internal/config`（task 2.1, 2.2）— `internal/config/config.go` / `config_test.go` のみ
- `internal/store`（task 3.1, 3.2）— `internal/store/store.go` / `store_encryption_test.go` のみ
- `cmd/api`（task 4.1）— `cmd/api/main.go` のみ
- `internal/server`（task 5.1, 5.2）— `internal/server/server.go` 修正 + `internal/server/google_sheets_decrypt_test.go` 新規。tasks.md の boundary は server の中であり、新規 _test.go の追加は同 boundary 内のテスト追加として許容範囲（テストは対象コードの近傍に置くプロジェクト規約とも整合）
- `migrations`（task 6.1）— `migrations/005_*.sql` のみ。`002` は不変更
- `README.md`（task 7.1）/ `env-examples`（task 7.2）/ `docs/encryption-key-rotation.md`（task 7.3）/ `docs/smoke-test.md`（task 8.1）— それぞれ該当ファイルのみ

`cmd/worker/main.go` の 1 行変更（`store.New(pool, cfg.EncryptionKey)`）は tasks.md に明示の boundary がないが、task 3.1 で `store.New` シグネチャを変更したことによる必要的なビルド連鎖（変更しないとプロジェクトがコンパイルできない）であり、design.md L339-340 と impl-notes.md D2 で説明されている。新機能の追加ではなく機械的な caller 更新であり、boundary 逸脱としては reject 不要と判断。

## Findings

なし

## Summary

Issue #81 の全要件（Req 1.1〜7.6 + NFR 1.x / 2.x / 3.x）に対応する実装と単体テストが揃っており、設計 PR で確定した design.md / tasks.md の境界も逸脱していません。logger 観点（鍵・平文・OAuth レスポンスの非露出）はコードレビューと leak canary テスト（`TestLoadEncryptionKeyPanicMessageOmitsKeyValue`）で多重防御。AC 未カバー / missing test / boundary 逸脱のいずれも検出されませんでした。

RESULT: approve
