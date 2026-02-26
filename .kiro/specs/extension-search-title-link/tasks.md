# Implementation Plan

- [x] 1. renderItems関数にタイトルリンクを追加する
- [x] 1.1 renderItems内でapiBaseを取得し、タイトルをリンク付きでレンダリングする
  - ループ開始前に `getConfiguredAPIBase()` を1回呼び出し、結果をローカル変数に保持する
  - 各記事について `item.id` の存在を確認し、apiBaseとitem.idの両方が有効な場合はタイトルを `<a>` タグで囲む
  - href属性に `${apiBase}/ui/items/${item.id}` を設定し、`escapeHTML()` でエスケープする
  - `target="_blank"` と `rel="noopener noreferrer"` を付与して新しいタブで開くようにする
  - タイトルが空文字またはundefinedの場合は "(untitled)" をリンクテキストとして使用する（既存のフォールバックロジックを維持）
  - apiBaseが空またはitem.idが欠損する場合はリンクなしのプレーンテキストにフォールバックする
  - 既存の「Show original」リンクの構造と動作を変更しない
  - _Requirements: 1.1, 1.2, 1.3, 1.4, 2.1, 2.2, 3.1, 3.2_

- [x] 2. タイトルリンクのスタイルを追加する
  - `.item-title a` セレクタで `color: inherit` と `text-decoration: none` を設定し、既存の見た目を維持する
  - `.item-title a:hover` で `color: var(--accent)` を設定し、クリッカブルであることをhover時に示す
  - _Requirements: 3.3_

- [x] 3. タイトルリンクのテストを追加する
- [x] 3.1 タイトルリンクの生成と属性を検証するテストを追加する
  - 記事一覧表示時にタイトルが `<a>` タグでラップされていることを検証する
  - href属性が `${apiBase}/ui/items/${item.id}` の形式であることを検証する
  - `target="_blank"` と `rel="noopener noreferrer"` がリンクに含まれることを検証する
  - 既存の「Show original」リンクが変更されていないことを確認する
  - _Requirements: 1.1, 1.2, 1.3, 3.1, 3.2_

- [x] 3.2 フォールバック動作を検証するテストを追加する
  - タイトルが空の場合に "(untitled)" がリンクテキストとして表示されることを検証する
  - apiBase未設定時にタイトルがリンクなしのプレーンテキストになることを検証する
  - item.idが欠損する記事のタイトルがリンクなしになることを検証する
  - _Requirements: 1.4, 2.1, 2.2_
