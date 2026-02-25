# Requirements Document

## Introduction
この仕様は、altpocket の Chrome Extension 内で保存済み記事を閲覧できる体験を定義する。  
対象は「記事を探して選び、内容を読む」ための要件であり、実装方式は定義しない。

## Project Description (Input)
extensionに記事を閲覧できる機能

## Requirements

### Requirement 1: 認証状態に応じた初期画面
**Objective:** As a 未認証ユーザー, I want 迷わずログイン操作に進みたい, so that 記事閲覧機能へ最短で到達できる

#### Acceptance Criteria
1. If ユーザーが未認証状態でSide Panelを開いた場合, then the Extension Article Reader shall ログインボタンのみを表示する.
2. The Extension Article Reader shall 未認証画面で記事一覧、検索欄、運用リンクを表示しない.
3. When ユーザーがログインに成功したとき, the Extension Article Reader shall 閲覧画面へ遷移する.
4. If ログイン処理が失敗した場合, then the Extension Article Reader shall 閲覧画面へ遷移しない.
5. While 未認証状態, the Extension Article Reader shall 認証が必要な記事APIを呼び出さない.

### Requirement 2: 閲覧画面の固定ユーティリティ導線
**Objective:** As a 認証済みユーザー, I want 主要導線をスクロール不要で使いたい, so that 閲覧中でも即座に遷移とログアウトができる

#### Acceptance Criteria
1. When 認証済み画面が表示されたとき, the Extension Article Reader shall 画面上部に `altpocket` ラベルを表示する.
2. The Extension Article Reader shall 画面上部に `Go to website` と `Log out` の導線を表示する.
3. When ユーザーが `Go to website` を選択したとき, the Extension Article Reader shall Webサイトのアイテム一覧ページをブラウザタブで開く.
4. When ユーザーが `Log out` を選択したとき, the Extension Article Reader shall 拡張機能の認証トークンを破棄して未認証画面へ遷移する.
5. The Extension Article Reader shall これらユーティリティ導線を記事一覧スクロール領域の外側に表示する.

### Requirement 3: 保存セクションと非同期取得連携
**Objective:** As a 認証済みユーザー, I want 閲覧画面から現在タブを保存したい, so that 閲覧と収集を同一画面で連続実行できる

#### Acceptance Criteria
1. The Extension Article Reader shall ユーティリティ導線の下に保存セクションを表示する.
2. The Extension Article Reader shall 保存セクションに `Save current tab` 操作を提供する.
3. The Extension Article Reader shall 保存セクションにタグ入力、タグ候補、選択済みタグ表示を提供する.
4. When ユーザーが `Save current tab` を実行したとき, the Extension Article Reader shall URLとタグを保存APIへ送信する.
5. If 新規保存が成立した場合, then the Extension Article Reader shall 本文候補を抽出して非同期キャプチャAPIへ送信する.

### Requirement 4: 区切り線配下の検索とコンテンツ一覧
**Objective:** As a 認証済みユーザー, I want 保存操作と閲覧操作を視覚的に分離したい, so that 情報が混在せず一覧確認に集中できる

#### Acceptance Criteria
1. The Extension Article Reader shall 保存セクションの下に視覚的なセクション区切りを表示する.
2. The Extension Article Reader shall セクション区切りの下に検索入力とコンテンツ一覧を表示する.
3. The Extension Article Reader shall 検索対象として件名、本文検索用テキスト、タグを含める.
4. The Extension Article Reader shall 各記事行に件名、タグ一覧、行内 `Show original` 操作を表示する.
5. The Extension Article Reader shall 記事本文テキストと記事ステータス表示（例: ready/pending/failed）を一覧上に表示しない.

### Requirement 5: 責務境界と障害時挙動
**Objective:** As a サービス運用者, I want 拡張機能責務とエラー時挙動を明確にしたい, so that 運用の一貫性と復帰性を維持できる

#### Acceptance Criteria
1. The Extension Article Reader shall 記事編集、記事削除、再フェッチ要求機能を提供しない.
2. Where 追加操作（編集・削除・再フェッチ）が必要な場合, the Extension Article Reader shall Webサイト導線を通じて実施できるようにする.
3. If 一覧取得または保存処理で認証エラーが返された場合, then the Extension Article Reader shall セッション失効として未認証画面へ遷移する.
4. If APIオリジンへのアクセス権限が不足している場合, then the Extension Article Reader shall 権限不足を通知し対象操作を中断する.
5. If ネットワーク障害で一覧取得または保存が失敗した場合, then the Extension Article Reader shall 通信失敗を識別可能な形で通知する.
