# Requirements Document

## Introduction

拡張機能からURLを登録する際、サーバー側のworkerがページを取得できない可能性（認証壁、JS描画、地域制限等）を考慮し、拡張機能側でページのtitleおよび本文の先頭最大200文字をあらかじめ取得し、登録APIリクエストに含める。これにより、worker取得が失敗した場合でもアイテムにtitleとexcerptが設定され、一覧・検索の利便性を維持する。

## Requirements

### Requirement 1: 拡張機能によるページメタデータの事前取得

**Objective:** ユーザーとして、拡張機能からURLを保存するとき、ページのtitleと本文プレビューが自動的に取得されてほしい。worker取得が失敗してもアイテムに表示可能な情報が残るようにするため。

#### Acceptance Criteria

1. When ユーザーがサイドパネルの保存ボタンを押した時, the Extension shall アクティブタブから `document.title` を取得し、登録APIリクエストの `title` フィールドに含める
2. When ユーザーがサイドパネルの保存ボタンを押した時, the Extension shall アクティブタブの本文テキスト（`article`, `main`, `[role="main"]`, または `body` を優先順に選択）から先頭最大200文字を抽出し、登録APIリクエストの `excerpt` フィールドに含める
3. If `chrome.scripting.executeScript` の実行が失敗した場合（権限不足、chrome:// ページ等）, the Extension shall `title` と `excerpt` を空文字のまま登録APIリクエストを送信する（保存自体は中断しない）
4. The Extension shall 抽出したテキストを正規化する（連続空白を単一スペースに置換し、前後の空白を除去する）

### Requirement 2: 登録APIのメタデータ受け入れ

**Objective:** APIとして、拡張機能から送信されるtitleとexcerptを受け取り、アイテム作成時に保存したい。workerフェッチの有無にかかわらず初期表示情報を確保するため。

#### Acceptance Criteria

1. When `POST /v1/items` リクエストに `title` フィールドが含まれている時, the API shall 値を items テーブルの `title` カラムに保存する
2. When `POST /v1/items` リクエストに `excerpt` フィールドが含まれている時, the API shall 値を items テーブルの `excerpt` カラムに保存する
3. When `POST /v1/items` リクエストに `title` または `excerpt` フィールドが含まれていない時, the API shall 既存の動作と同様にデフォルト値（空文字）で保存する（後方互換性を維持する）
4. The API shall `title` を最大500文字、`excerpt` を最大200文字でサーバー側で切り詰める

### Requirement 3: Worker取得とのプレフィル値の共存

**Objective:** システムとして、拡張機能が送信したプレフィル値とworkerが取得した値を適切に共存させたい。より高品質な情報が得られた場合は上書きし、取得失敗時はプレフィル値を保持するため。

#### Acceptance Criteria

1. When workerがページ取得に成功し、titleを抽出できた時, the Worker shall 既存のtitle値（プレフィル値を含む）を上書きする
2. When workerがページ取得に成功し、excerptを抽出できた時, the Worker shall 既存のexcerpt値（プレフィル値を含む）を上書きする
3. When workerがページ取得に失敗した時, the Worker shall 拡張機能が送信したプレフィル値のtitleとexcerptをそのまま保持する
4. While アイテムの `fetch_status` が `failed` の状態で, the System shall プレフィル済みのtitleとexcerptをアイテム一覧・詳細画面に表示する

### Requirement 4: 本文抽出ロジックの制約

**Objective:** 拡張機能の本文抽出が、パフォーマンスとプライバシーに配慮した安全な範囲で動作するようにしたい。

#### Acceptance Criteria

1. The Extension shall 本文抽出時に `script`, `style`, `noscript`, `nav`, `aside`, `footer`, `form`, `[hidden]`, `[aria-hidden="true"]` 要素を除外する
2. The Extension shall 本文抽出の対象文字数を最大200文字に制限する（既存の `extractPageCapture` のロジックを再利用可能）
3. If アクティブタブのURLが `chrome://`, `chrome-extension://`, `about:` で始まる場合, the Extension shall コンテンツ抽出をスキップし、`title` と `excerpt` を空文字とする
