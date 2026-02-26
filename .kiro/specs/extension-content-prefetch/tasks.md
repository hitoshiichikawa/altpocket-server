# Implementation Plan

- [x] 1. サーバー側のtitle/excerpt受け入れ
- [x] 1.1 (P) Store層のCreateItemにtitle/excerptパラメータを追加する
  - `CreateItem` の引数に title と excerpt を追加し、INSERT文にこれらのカラムを含める
  - ON CONFLICT DO NOTHING の既存動作を維持し、重複登録時は新しいプレフィル値を無視する
  - title/excerpt が空文字の場合も正常に動作すること（DB DEFAULT と同等の挙動）
  - _Requirements: 2.1, 2.2, 2.3_

- [x] 1.2 handleCreateItemのリクエスト構造体を拡張し、バリデーションを追加する
  - リクエスト構造体に title（string, オプション）と excerpt（string, オプション）フィールドを追加する
  - title を TrimSpace 後に最大500文字で切り詰め、excerpt を TrimSpace 後に最大200文字で切り詰める
  - title/excerpt が省略された場合はデフォルト空文字で処理し、後方互換性を維持する
  - createItem 内部関数のシグネチャを更新し、title/excerpt を store 層に渡す
  - _Requirements: 2.1, 2.2, 2.3, 2.4_

- [x] 2. 拡張機能のページメタデータ事前取得
- [x] 2.1 (P) extractPagePrefill関数を実装する
  - `chrome.scripting.executeScript` を使用して、アクティブタブから `document.title` と本文テキスト（最大200文字）を取得する
  - コンテンツ選択は `article` → `main` → `[role="main"]` → `body` の優先順で行う
  - `script`, `style`, `noscript`, `nav`, `aside`, `footer`, `form`, `[hidden]`, `[aria-hidden="true"]` 要素を除外する
  - テキスト正規化を適用する（連続空白を単一スペースに置換、前後空白を除去）
  - `chrome://`, `chrome-extension://`, `about:` で始まるURLの場合は抽出をスキップし空文字を返す
  - scripting 失敗時（権限不足等）は例外を投げず `{ title: '', excerpt: '' }` を返す
  - _Requirements: 1.1, 1.2, 1.3, 1.4, 4.1, 4.2, 4.3_

- [x] 2.2 saveCurrentTabで保存前にプレフィルを取得しAPIペイロードに含める
  - 保存ボタン押下後、API呼び出し前に `extractPagePrefill` を呼び出す
  - 取得した title と excerpt を createItem のペイロード（url, tags に加えて）に含める
  - 抽出が失敗しても（空文字の場合でも）保存フローは中断しない
  - 既存の fire-and-forget キャプチャフロー（full content 用）は変更しない
  - _Requirements: 1.1, 1.2, 1.3_

- [x] 3. テストと検証
- [x] 3.1 (P) サーバー側のテストを追加・更新する
  - 契約テストにtitle/excerpt付きのcreateItemリクエストを追加し、正常に受け入れられることを検証する
  - title/excerptが省略されたリクエストが従来通り動作することを検証する（後方互換性）
  - title 500文字超過時の切り詰め動作を検証する
  - excerpt 200文字超過時の切り詰め動作を検証する
  - _Requirements: 2.1, 2.2, 2.3, 2.4_

- [x] 3.2 (P) 拡張機能のテストを追加・更新する
  - 保存リクエストに title と excerpt が含まれることを検証するテストを追加する
  - scripting が無効な場合に title/excerpt が空文字で送信されることを検証する
  - 既存の保存テスト（タグ、重複、キャプチャフロー）が引き続きパスすることを確認する
  - _Requirements: 1.1, 1.2, 1.3, 4.2_
