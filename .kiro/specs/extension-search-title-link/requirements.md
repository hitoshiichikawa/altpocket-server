# Requirements Document

## Introduction

Chrome拡張機能の記事検索結果一覧において、各記事のタイトル文字列をクリック可能なリンクに変更し、Web UIの記事詳細画面（`/ui/items/{id}`）へ直接遷移できるようにする。
現状、タイトルはプレーンテキストとして表示されており、記事詳細を確認するには別途Web UIを開いて手動で探す必要がある。この機能により、拡張機能からWeb UIへのシームレスな導線を実現する（issue#67）。

## Requirements

### Requirement 1: タイトルリンクの表示

**Objective:** ユーザーとして、拡張機能の検索結果一覧のタイトルをクリックしてWeb UIの記事詳細画面を開きたい。記事の本文や詳細情報に素早くアクセスできるようにするため。

#### Acceptance Criteria

1. When ユーザーが拡張機能で記事一覧または検索結果を表示する, the Extension Sidepanel shall 各記事のタイトル文字列をハイパーリンクとしてレンダリングする
2. The Extension Sidepanel shall タイトルリンクのhref属性を `{webBaseURL}/ui/items/{item.id}` の形式で生成する
3. When ユーザーがタイトルリンクをクリックする, the Extension Sidepanel shall 新しいブラウザタブでWeb UIの記事詳細画面を開く（`target="_blank"` かつ `rel="noopener noreferrer"`）
4. If 記事のタイトルが空文字またはundefinedである場合, the Extension Sidepanel shall "(untitled)" というフォールバックテキストをリンクとして表示する

### Requirement 2: WebベースURLの解決

**Objective:** 拡張機能として、Web UIの記事詳細画面へのリンクを正しく生成するために、WebベースURLを適切に解決したい。異なるデプロイ環境でも一貫して動作するようにするため。

#### Acceptance Criteria

1. The Extension Sidepanel shall API接続先のベースURL設定からWeb UIのベースURLを導出する
2. While APIベースURLが設定されている状態で, the Extension Sidepanel shall `/ui/items/{id}` パスを結合してWeb UI詳細画面への完全なURLを構築する

### Requirement 3: 既存UIとの一貫性

**Objective:** ユーザーとして、タイトルリンクが既存のUIデザインと調和していることを期待する。視覚的な違和感なく利用できるようにするため。

#### Acceptance Criteria

1. The Extension Sidepanel shall タイトルリンクにXSS対策としてエスケープ処理を適用する（既存の `escapeHTML` 関数を利用）
2. The Extension Sidepanel shall 既存の「Show original」リンクの動作を変更せず維持する
3. The Extension Sidepanel shall タイトルリンクのスタイルを既存のitem-cardデザインと視覚的に調和させる
