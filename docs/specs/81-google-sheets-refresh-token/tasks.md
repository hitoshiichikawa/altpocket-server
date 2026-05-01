# Implementation Plan

各タスクは独立コミット可能な粒度。`(P)` は並列実行可能（`_Boundary:_` で分離）。
`_Depends:_` は cross-boundary な非自明な依存のみ記載する（同 `_Boundary:_` 内の
順序依存は番号順で表現）。

- [x] 1. `internal/crypto` パッケージの新規実装
- [x] 1.1 `internal/crypto/crypto.go` を新規作成し、AES-256-GCM の `Encrypt` /
      `Decrypt` / `DecodeKey` と sentinel error を実装する (P)
  - `KeySize = 32`, `NonceSize = 12` 定数を export
  - `ErrInvalidKeyLength`, `ErrMalformedKey`, `ErrEmptyPlaintext`,
    `ErrCiphertextTooShort`, `ErrDecryptionFailed` を export
  - `DecodeKey(encoded string) ([]byte, error)`: `base64.StdEncoding.DecodeString` →
    長さ検証
  - `Encrypt(key, plaintext []byte) ([]byte, error)`: `aes.NewCipher` →
    `cipher.NewGCM` → `crypto/rand` で nonce 生成 → `gcm.Seal(nonce, nonce, plaintext, nil)`
  - `Decrypt(key, blob []byte) ([]byte, error)`: 先頭 12 byte を nonce に切り出し、
    残りを `gcm.Open` に渡す。GCM 認証失敗・鍵不一致は `ErrDecryptionFailed` に丸める
  - panic しない（ライブラリ層規約）
  - 鍵・平文・nonce・暗号文を error メッセージに含めない。logger 依存を持たない
    設計とすることで NFR 1.2 を構造的に保証する
  - _Requirements: 1.3, 1.4, 2.5, NFR 1.1, NFR 1.2_
  - _Boundary: internal/crypto_

- [x] 1.2 `internal/crypto/crypto_test.go` を新規作成し、Req 7.1〜7.6 をカバーする
      単体テストを追加する (P)
  - `TestEncryptDecryptRoundTrip` — 32 byte 鍵で往復一致（Req 7.1）
  - `TestDecryptWithWrongKey` — 別の 32 byte 鍵では `ErrDecryptionFailed`（Req 7.2）
  - `TestDecryptDetectsTampering` — 末尾 1 byte / nonce 1 byte 改ざんで失敗（Req 7.3）
  - `TestEncryptProducesUniqueNonce` — 同一鍵・同一平文を 100 回暗号化し全て
    異なる暗号文（Req 7.4）
  - `TestEncryptEmptyInput` — 空文字列で `ErrEmptyPlaintext`（Req 7.5、拒否方針）
  - `TestDecodeKeyRejectsMalformedInput` — table-driven: `""`, `"!!not-base64!!"`,
    16 byte 鍵, 64 byte 鍵（Req 7.6）
  - `TestDecryptRejectsShortCiphertext` — 12 byte 未満の入力で
    `ErrCiphertextTooShort`（Req 7.6）
  - _Requirements: 7.1, 7.2, 7.3, 7.4, 7.5, 7.6_
  - _Boundary: internal/crypto_
  - _Depends: 1.1_

- [ ] 2. `internal/config` の `ENCRYPTION_KEY` 対応
- [x] 2.1 `internal/config/config.go` の `Config` 構造体に `EncryptionKey []byte`
      を追加し、`Load()` で `ENCRYPTION_KEY` 環境変数を読み込んで `crypto.DecodeKey`
      で検証、失敗時は panic
  - `mustEnv("ENCRYPTION_KEY")` で取得
  - `crypto.DecodeKey(raw)` を呼ぶ
  - エラー判定: `errors.Is(err, crypto.ErrMalformedKey)` →
    `panic("invalid env: ENCRYPTION_KEY (base64 decode failed)")`
  - `errors.Is(err, crypto.ErrInvalidKeyLength)` →
    `panic("invalid env: ENCRYPTION_KEY (must be 32 bytes after base64 decode)")`
  - panic メッセージに鍵そのもの・先頭末尾・ハッシュを含めない（Req 3.5, NFR 1.2）
  - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.5, NFR 1.2, NFR 3.1_
  - _Boundary: internal/config_
  - _Depends: 1.1_

- [ ] 2.2 `internal/config/config_test.go` に `ENCRYPTION_KEY` の異常系テストを追加
  - 既存 `setRequiredEnv` ヘルパに `t.Setenv("ENCRYPTION_KEY", "<valid base64 32 byte>")`
    を追加（既存テストを壊さない）
  - `TestLoadPanicsWithoutEncryptionKey` — `ENCRYPTION_KEY=""` で panic（Req 3.2）
  - `TestLoadPanicsWithMalformedEncryptionKey` — `"!!not-base64!!"` で panic（Req 3.3）
  - `TestLoadPanicsWithWrongLengthEncryptionKey` — 16 byte 鍵を base64 した値で panic
    （Req 3.4）
  - `TestLoadAcceptsValidEncryptionKey` — 正常系で `cfg.EncryptionKey` が 32 byte
  - panic メッセージに鍵値が含まれていないことを assert（Req 3.5, NFR 1.2）
  - _Requirements: 3.2, 3.3, 3.4, 3.5, NFR 1.2_
  - _Boundary: internal/config_
  - _Depends: 2.1_

- [ ] 3. `internal/store` の暗号化／復号統合
- [ ] 3.1 `internal/store/store.go` の `Store` 構造体に `encryptionKey []byte` を追加し、
      `New` シグネチャを `New(db *pgxpool.Pool, encryptionKey []byte) *Store` に変更
  - `ErrRefreshTokenDecryptFailed` sentinel error を新規 export
  - `UpsertGoogleSheetsConnection` 内で `crypto.Encrypt(s.encryptionKey, []byte(refreshToken))`
    → `base64.StdEncoding.EncodeToString` → DB に書き込む（クエリ自体は既存のまま、
    バインド値が暗号文に変わる）
  - `GetGoogleSheetsConnection` 内で DB から base64 文字列を読み出し →
    `base64.StdEncoding.DecodeString` 失敗 → `ErrRefreshTokenDecryptFailed` を返す
    （Req 2.5: レガシー平文行はここで合流）
  - `crypto.Decrypt` 失敗 → `ErrRefreshTokenDecryptFailed` を返す（Req 2.3）
  - 復号成功時のみ `c.RefreshToken = string(plaintext)` を設定
  - 復号失敗時に暗号文・平文・鍵をエラーメッセージ／ログに含めない（Req 1.5, 2.4, NFR 1.2）
  - `Store.encryptionKey` は unexported かつメソッドとして公開しない（外部からの
    取り出しを構造的に防ぎ、誤ロギングを抑止する）
  - `DeleteGoogleSheetsConnection` / `SetGoogleSheetsSpreadsheetID` は変更なし
  - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5, 2.1, 2.5, NFR 1.2, NFR 2.2_
  - _Boundary: internal/store_
  - _Depends: 1.1_

- [ ] 3.2 `internal/store/store_encryption_test.go` を新規作成し、暗号化往復・
      レガシー平文拒否・別鍵拒否を実 PostgreSQL 越しに検証
  - 32 byte 鍵を `crypto/rand.Read` で生成するヘルパ
  - `TestUpsertAndGetGoogleSheetsConnection_RoundTrip` — Upsert → Get で平文一致
    （Req 1.1, 2.1）
  - `TestUpsertGoogleSheetsConnection_PersistedValueIsNotPlaintext` — Upsert 後に
    DB を直接 SELECT し、平文と異なることを検証（Req 1.1, 1.2）
  - `TestGetGoogleSheetsConnection_LegacyPlaintextRejected` — INSERT で平文を
    直接書き、Get で `ErrRefreshTokenDecryptFailed`（Req 2.5）
  - `TestGetGoogleSheetsConnection_WrongKeyRejected` — 鍵 A で Upsert、鍵 B で
    構築した Store で Get → `ErrRefreshTokenDecryptFailed`（Req 2.3）
  - `TestUpsertGoogleSheetsConnection_NonceUniqueness` — 同じ refresh_token で 2 回
    Upsert すると DB の `refresh_token` 値が異なる（Req 1.3）
  - _Requirements: 1.1, 1.2, 1.3, 2.1, 2.3, 2.5_
  - _Boundary: internal/store_
  - _Depends: 3.1_

- [ ] 4. `cmd/api/main.go` の起動シーケンス更新
- [ ] 4.1 `cmd/api/main.go` で `store.New(pool, cfg.EncryptionKey)` に変更
  - `config.Load` がすでに fail-fast 検証を済ませているため、main 側では追加検証不要
  - _Requirements: 3.1, NFR 2.1_
  - _Boundary: cmd/api_
  - _Depends: 2.1, 3.1_

- [ ] 5. ハンドラ層の復号エラー分岐
- [ ] 5.1 `internal/server/server.go` の `handleUISettings` で
      `errors.Is(err, store.ErrRefreshTokenDecryptFailed)` を判定し、`pgx.ErrNoRows`
      と同じく `connected = false` 扱いにする
  - 既存の `pgx.ErrNoRows` ブランチに合流させる
  - `slog.Warn("settings.google_sheets.decrypt_failed", "request_id", ..., "user_id", user.ID)`
    で構造化ログを残す（暗号文・鍵・平文・OAuth レスポンスは出さない）
  - _Requirements: 2.3, 2.4, NFR 1.2, NFR 2.1, NFR 3.2_
  - _Boundary: internal/server_
  - _Depends: 3.1_

- [ ] 5.2 `internal/server/server.go` の `handleUISettingsGoogleExport` で
      `errors.Is(err, store.ErrRefreshTokenDecryptFailed)` を `pgx.ErrNoRows` と同じく
      `/ui/settings?status=google_not_connected` 相当へリダイレクトする分岐を追加。
      復号後の平文 `refresh_token` をリクエスト寿命を超えて保持しないことを保証する
  - 既存の `errors.Is(err, pgx.ErrNoRows)` 分岐の直後に or 条件で合流
  - `slog.Warn("settings.google_sheets.decrypt_failed", ...)` を記録（Req 2.4）
  - HTTP メソッド・ステータス・リダイレクト先パスを変更しない（NFR 2.1）
  - 復号後の `conn.RefreshToken` は `oauth2.Token` のローカル変数として `exportItemsToGoogleSheets`
    に渡すのみ。グローバル変数・パッケージレベルキャッシュ・goroutine 間共有 channel
    に格納しない（Req 2.2, NFR 1.3）
  - 復号後の値・OAuth レスポンス全文・暗号文・鍵をログに出さない（Req 1.5, NFR 1.2）
  - _Requirements: 1.5, 2.2, 2.3, 2.4, NFR 1.2, NFR 1.3, NFR 2.1, NFR 2.2, NFR 3.2_
  - _Boundary: internal/server_
  - _Depends: 3.1_

- [ ] 6. 既存平文データの無効化マイグレーション
- [ ] 6.1 `migrations/005_invalidate_legacy_refresh_tokens.sql` を新規追加
  - `DELETE FROM user_google_sheets_connections;` の 1 行
  - 冒頭にコメントで「forward-only / 再認可方式 / 詳細は README と
    docs/encryption-key-rotation.md」を明記
  - 既存 `migrations/002_*.sql` は変更しない（Req NFR 2.3）
  - _Requirements: 4.1, 4.5, NFR 2.3_
  - _Boundary: migrations_

- [ ] 7. ドキュメント整備
- [ ] 7.1 `README.md` の必須環境変数節に `ENCRYPTION_KEY` を追加し、移行手順節を新設 (P)
  - 必須環境変数表に `ENCRYPTION_KEY=<base64 of 32 random bytes>` を追加
  - `ENCRYPTION_KEY` の用途（Google Sheets `refresh_token` 暗号化）と要求形式
    （base64 エンコード、デコード後 32 byte）を本文に明記（Req 6.3）
  - 鍵生成例コマンド: `openssl rand -base64 32`（Req 5.4）
  - 移行手順節（migration 005 適用 → API 再起動 → 利用者再認可）を追加（Req 4.3）
  - 鍵ローテーションは `docs/encryption-key-rotation.md` 参照とリンク
  - migrations の psql 実行例リストに 005 を追記
  - _Requirements: 4.3, 5.4, 6.1, 6.3_
  - _Boundary: README.md_

- [ ] 7.2 `.env.example` と `deploy/.env.production.example` に `ENCRYPTION_KEY`
      のサンプル行を追加 (P)
  - `.env.example`: `ENCRYPTION_KEY=replace_with_base64_of_32_random_bytes`
  - `deploy/.env.production.example`: 既存 `SESSION_SECRET` の近傍に同様サンプル行
    （生成例コマンドはコメントで併記）
  - _Requirements: 6.2_
  - _Boundary: env-examples_

- [ ] 7.3 `docs/encryption-key-rotation.md` を新規作成（鍵ローテーション運用手順書） (P)
  - 鍵ローテーション戦略: **再認可方式**（既存暗号化行を新鍵で読めなくし、利用者
    再認可で再構築。二重鍵での自動再暗号化は行わない）（Req 5.1）
  - 鍵生成例: `openssl rand -base64 32`（Req 5.4）
  - 番号付き手順: 新鍵生成 → DB の関連行を DELETE（手動 SQL 例を提示）→
    環境変数差し替え → API 再起動 → 利用者再認可案内（Req 5.2）
  - 自動化しないことを明示（Req 5.3）
  - 既存平文データの自動マージは行わないことを明示（Req 4.2, 4.5）
  - 関連: `README.md` 移行手順節へのリンク
  - _Requirements: 4.2, 4.5, 5.1, 5.2, 5.3, 5.4_
  - _Boundary: docs/encryption-key-rotation.md_

- [ ] 8. 結合スモークテストの手順整備
- [ ] 8.1 `docs/smoke-test.md`（既存ドキュメント）に Google Sheets 再認可フローの
      スモーク手順を追記
  - migration 005 適用 → `ENCRYPTION_KEY` を投入 → API 起動
  - `/ui/settings` で Google 接続 → エクスポート実行 → スプレッドシートが作成されること
  - DB を直接覗いて `refresh_token` が平文でないこと（base64 文字列・元の値と異なる）
  - `ENCRYPTION_KEY` を別の有効な鍵に差し替えて再起動 → `/ui/settings` で
    `connected = false` 表示／エクスポートで `google_not_connected` リダイレクト
  - _Requirements: 4.4, 2.3_
  - _Boundary: docs/smoke-test.md_
  - _Depends: 4.1, 5.1, 5.2, 6.1, 7.1_
