# Requirements Document

## Introduction

アイテム詳細画面において、既存のタグ編集操作にタイトル編集を統合する。
「Edit tags」ボタンクリック時にタイトルとタグの両方が編集可能になり、1回の保存操作で両方を同時に更新する。
新規 `PATCH /v1/items/{id}` エンドポイントでタイトルとタグを一括更新し、既存の `PUT /v1/items/{id}/tags` は後方互換のために維持する。

## Requirements

### Requirement 1: 統合編集UI

**Objective:** ユーザーとして、「Edit tags」ボタンをクリックした際にタイトルとタグの両方を同時に編集でき、1回の保存操作でまとめて反映できるようにしたい。操作の一貫性と効率性を維持するため。

#### Acceptance Criteria
1. When ユーザーが「Edit tags」ボタンをクリックする, the Web UI shall タイトル表示をテキスト入力フィールドに切り替え、現在のタイトルを初期値として表示する（タグ編集モードと同時に開始）
2. While 編集モードが有効である, the Web UI shall タイトル入力フィールドとタグ編集UIの両方を表示し、保存ボタンとキャンセルボタンを共有する
3. When ユーザーがキャンセルボタンをクリックする, the Web UI shall タイトルとタグの両方を編集前の状態に復帰する（APIリクエストは発行しない）
4. When ユーザーが保存ボタンをクリックする, the Web UI shall タイトルとタグを1回のAPIリクエストで送信し、成功後に両方の表示を更新する

### Requirement 2: アイテム部分更新API

**Objective:** アイテムのタイトルとタグを1回のリクエストで一括更新できる新規APIエンドポイントを提供したい。セマンティクスが明確で将来の拡張にも対応できるようにするため。

#### Acceptance Criteria
1. When 認証済みユーザーがタイトルとタグを含むリクエストを送信する, the API shall 対象アイテムのタイトルとタグを一括で更新し、更新後のタイトルとタグをJSONで返却する
2. The API shall 既存の `PUT /v1/items/{id}/tags` エンドポイントをそのまま維持し、拡張機能からの既存リクエストとの後方互換性を保証する
3. When リクエスト元のユーザーがアイテムの所有者でない, the API shall 404エラーを返却する（所有権情報を漏洩しない）
4. If リクエストボディが不正またはJSONデコードに失敗した場合, the API shall 400エラーを返却する

### Requirement 3: タイトルバリデーション

**Objective:** ユーザーとして、空のタイトルを防止し、データ品質を維持したい。意図しない空タイトルやDB制約違反を防ぐため。

#### Acceptance Criteria
1. If タイトルが空文字列（トリム後）の場合, the Web UI shall 保存を実行せずにエラーメッセージを表示する
2. The API shall タイトルの前後の空白をトリムしてから保存する
3. While 保存処理が進行中である, the Web UI shall 保存ボタンを無効化し二重送信を防止する

### Requirement 4: ページタイトル同期

**Objective:** ユーザーとして、タイトル編集後にブラウザタブのページタイトルも更新され、一貫した表示を維持したい。

#### Acceptance Criteria
1. When タイトルの保存が成功する, the Web UI shall ブラウザのページタイトル（`document.title`）を「{新しいタイトル} | altpocket」形式で更新する
