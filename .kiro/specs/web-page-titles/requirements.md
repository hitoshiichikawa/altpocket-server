# Requirements Document

## Introduction
altpocket Web UIの各ページにおけるHTML `<title>` は現在、汎用的な英語の単語（"Items", "Item", "Settings"等）がハードコードされている。これをサービス名を含む説明的なタイトルに改善し、ブラウザタブの識別性・アクセシビリティ・SEO基盤を向上させる。

### 現状の Title 一覧
| ページ | 現在の Title |
|--------|-------------|
| ホーム (`/`) | "Sign In" |
| ユーザー登録 (`/register`) | "Register" |
| 記事一覧 (`/ui/items`) | "Items" |
| 記事詳細 (`/ui/items/{id}`) | "Item" |
| クイック追加 (`/ui/quick-add`) | "Quick Add" |
| 設定 (`/ui/settings`) | "Settings" |

## Requirements

### Requirement 1: サービス名を含む統一的なタイトル形式
**Objective:** ユーザーとして、ブラウザタブにサービス名とページ名が表示されることで、複数タブを開いた際にaltpocketのページを素早く識別したい

#### Acceptance Criteria
1. The Web UI shall 全ページの `<title>` を `{ページ名} | altpocket` の形式で表示する
2. The Web UI shall サービス名「altpocket」を全ページで一貫して接尾辞として含める

### Requirement 2: ページ内容を反映した説明的なタイトル
**Objective:** ユーザーとして、各ページのタイトルがページの機能・内容を正確に表現していることで、ブラウザ履歴やタブからページの内容を直感的に把握したい

#### Acceptance Criteria
1. When ホームページ（`/`）を表示した場合、the Web UI shall タイトルを「ログイン | altpocket」と表示する
2. When ユーザー登録ページ（`/register`）を表示した場合、the Web UI shall タイトルを「アカウント登録 | altpocket」と表示する
3. When 記事一覧ページ（`/ui/items`）を表示した場合、the Web UI shall タイトルを「記事一覧 | altpocket」と表示する
4. When 記事詳細ページ（`/ui/items/{id}`）を表示した場合、the Web UI shall タイトルを「{記事タイトル} | altpocket」の形式で表示する（記事タイトルは保存された記事のタイトルを使用）
5. When クイック追加ページ（`/ui/quick-add`）を表示した場合、the Web UI shall タイトルを「クイック追加 | altpocket」と表示する
6. When 設定ページ（`/ui/settings`）を表示した場合、the Web UI shall タイトルを「設定 | altpocket」と表示する

### Requirement 3: タイトルなし記事のフォールバック
**Objective:** ユーザーとして、記事タイトルが存在しない場合でも意味のあるページタイトルが表示されることで、ページの識別に困らないようにしたい

#### Acceptance Criteria
1. If 記事詳細ページで記事のタイトルが空文字列の場合、the Web UI shall タイトルを「(無題) | altpocket」と表示する
