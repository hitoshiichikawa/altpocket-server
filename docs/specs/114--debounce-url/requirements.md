# Requirements Document

## Introduction

`/ui/items` の検索フォームは現在フォーム送信型で、ユーザーが Enter キーを押すか
「Apply」ボタンを操作するまで結果が更新されない。文字入力中に絞り込みが反映されないため、
目的のアイテムへの到達に余計な手数がかかる。本機能では検索入力に debounce 即時反映を
導入し、入力停止後 約 300ms で結果一覧と URL の `?q=` を同期させる。Enter キー押下時は
従来通り即時反映し、ブラウザの戻る／進むで過去の検索状態を辿れるようにする。

参照: [Issue #114](https://github.com/hitoshiichikawa/altpocket-server/issues/114)

## Requirements

### Requirement 1: 入力停止後の自動絞り込み

**Objective:** As a `/ui/items` を閲覧する利用者, I want 検索ボックスに文字を入力したら
手動送信なしで一覧が更新されること, so that 目的のアイテムに最短で到達できる

#### Acceptance Criteria

1. When 利用者が検索入力欄の値を変更し最後の変更から 300ms 経過した, the system shall
   現在の入力値を `q` クエリとした絞り込み結果一覧を表示する
2. When 利用者が 300ms 以内に連続して入力を変更している, the system shall 直前の保留中
   絞り込み要求を破棄し、最後の入力値で 1 回だけ絞り込みを行う
3. When 自動絞り込みによる結果更新が完了した, the system shall 検索入力欄のフォーカス
   およびキャレット位置を更新前と同じ状態に保つ
4. When 自動絞り込みによる結果更新が完了した, the system shall 並び順 (`sort`)・1 ページ
   あたり件数 (`per_page`)・選択中タグ (`tag`) の各既存フィルタ条件を引き継ぐ
5. The system shall 自動絞り込みのトリガー対象を `q`（検索文字列）に限定し、`sort` /
   `per_page` / `tag` の変更挙動は本要件で変更しない

### Requirement 2: URL クエリの同期

**Objective:** As a `/ui/items` を閲覧する利用者, I want 検索結果の URL がいつでも現在の
絞り込み状態を表していること, so that 検索結果を共有・ブックマーク・再読込できる

#### Acceptance Criteria

1. When 自動絞り込みが結果一覧に反映された, the system shall ブラウザのアドレスバーの
   `?q=` パラメータを最新の入力値で同期更新する
2. If 入力値が空文字（または空白のみ）である, the system shall URL から `q` パラメータを
   削除した状態に同期する
3. When URL を同期更新した, the system shall 既存の `sort` / `per_page` / `tag` /
   ページネーションなど他のクエリパラメータを保持する
4. When 利用者が結果一覧を絞り込んだ後にページを再読込した, the system shall 再読込後の
   検索入力欄の値および結果一覧を URL の `q` と一致させる

### Requirement 3: ブラウザ履歴ナビゲーション

**Objective:** As a `/ui/items` を閲覧する利用者, I want ブラウザの戻る／進むで検索状態を
辿れること, so that 直前の絞り込み結果に容易に戻れる

#### Acceptance Criteria

1. When 利用者がブラウザの戻る／進む操作で過去の検索状態を呼び出した, the system shall
   呼び出された URL の `q` に一致する検索結果一覧を表示する
2. When 履歴遷移により検索状態が復元された, the system shall 検索入力欄の表示値を
   復元後の `q` と一致させる
3. The system shall 戻る操作で履歴に残すエントリの粒度（入力 1 文字ごとに 1 履歴を
   作るか、ある程度まとめて 1 履歴にするか）を Open Questions OQ-1 の判断結果に従う

### Requirement 4: Enter キーによる即時反映

**Objective:** As a `/ui/items` を閲覧する利用者, I want Enter キー押下で debounce を
待たずに即時に絞り込みが反映されること, so that 自動更新を待たずに確定操作ができる

#### Acceptance Criteria

1. When 利用者が検索入力欄で Enter キーを押下した, the system shall debounce の残り時間
   を待たずに即時に絞り込みを実行する
2. When Enter キー押下による即時絞り込みが行われた, the system shall 保留中の debounce
   による絞り込み要求が二重に走らないようにする
3. When Enter キー押下による即時絞り込みが行われた, the system shall URL の `?q=` を
   Requirement 2 と同じ規則で同期する

### Requirement 5: 空入力で未絞り込みに戻す

**Objective:** As a `/ui/items` を閲覧する利用者, I want 検索ボックスを空にしたら
絞り込みなしの一覧に戻ること, so that 検索を解除する操作が直感的に完結する

#### Acceptance Criteria

1. When 利用者が検索入力欄をクリア（空文字）した, the system shall 絞り込みなしの全件
   一覧（既存の `sort` / `per_page` / `tag` 条件のみが効いた状態）を表示する
2. When 入力値が空文字のみ（半角・全角空白のみを含む）になった, the system shall 既存の
   検索処理が空文字を未指定として扱う規則と同じ扱いをし、結果一覧は未絞り込みと同等に
   する
3. When 検索入力欄をクリアして自動絞り込みがトリガーされた, the system shall URL から
   `q` パラメータを削除する

### Requirement 6: 既存フィルタ UI との整合

**Objective:** As a `/ui/items` を閲覧する利用者, I want 検索 debounce 化が既存のフィルタ
UI（ソート・件数・タグ・モバイル用ボトムシート）と矛盾なく共存すること, so that
これまでの操作感が壊れない

#### Acceptance Criteria

1. The system shall `/ui/items` ページ上に複数存在する検索入力欄
   （デスクトップ用サイドバー検索、モバイル用上部検索バー、モバイル用フィルタ
   ボトムシート内検索）すべてについて、自動絞り込みおよび URL 同期の挙動を統一する
2. When 自動絞り込みが発生した, the system shall 既存の `Apply` ボタン送信パスや
   タグチェックボックス自動送信パスと衝突せず、二重送信を発生させない
3. Where JavaScript が無効化されたブラウザで `/ui/items` を開く場合, the system shall
   従来通りフォーム送信（Enter またはボタン）で絞り込みが行える挙動を維持する

## Non-Functional Requirements

### NFR 1: 性能・帯域

1. The system shall 検索入力中の通信を debounce で抑制し、最後の入力から 300ms 以内に
   発生する追加入力では新規リクエストを送信しない
2. The system shall debounce 経過時に直前の保留中リクエストが残っていれば後続のものに
   置き換え、利用者に提示する結果は常に最後の入力値に対するものとする

### NFR 2: 可観測性・後方互換

1. The system shall `/ui/items` の既存サーバ側ハンドラの URL クエリ仕様
   （`q` / `sort` / `per_page` / `tag` / ページネーション）を変更しない
2. The system shall 既存の自動テストスイート（Go テスト一式および拡張機能 API 契約テスト）
   を破壊しない

### NFR 3: アクセシビリティ・UX

1. The system shall 自動絞り込み完了後も検索入力欄の `aria-label` および入力フォーカス
   状態を維持し、スクリーンリーダー利用者が入力位置を見失わないようにする
2. The system shall 検索結果の更新が利用者の入力操作中に視覚的なちらつきを起こさない
   よう、debounce 待機中は前回結果の表示を維持する

## Out of Scope

- 検索アルゴリズム本体の変更（pg_trgm の調整、ranking ロジックの差し替え等）
- 検索結果のキーワードハイライト表示（Issue #116 で扱う）
- サーバ側 `/ui/items` ハンドラのクエリ仕様や DB スキーマ変更
- 検索 API の新規エンドポイント追加（既存 SSR 経路または既存 JSON エンドポイントの
  範囲で実装する想定）
- 検索履歴のサジェスト・補完機能
- タグ・ソート・件数の自動絞り込み挙動の変更（既存挙動を維持）
- モバイル用ボトムシート以外の UI レイアウト変更

## Open Questions

- OQ-1: 履歴エントリ粒度の方針  
  Issue 本文の「ブラウザ戻るで過去の検索に戻れる」要件に対し、入力 1 文字ごとに履歴を
  積む方式（`history.pushState` 連発）と、現在の URL を上書きしつつ「確定操作」のみ
  履歴に残す方式（`history.replaceState` ＋ Enter / blur 等で `pushState`）の 2 案がある。
  Issue 仮案では `history.replaceState` 主体・Enter で `pushState` が示唆されているが、
  最終的な粒度は人間判断を要する。Requirement 3 AC-3 はこの判断を待って実装方針を決める。
- OQ-2: 結果一覧の更新方式  
  既存はフルページ遷移で `/ui/items` を再描画している。debounce 即時反映を実装する
  にあたり、(a) 既存ハンドラへ非同期取得して結果フラグメントを差し替える方式、
  (b) フルリロードする方式、(c) 既存 JSON API があればそれを使う方式 のいずれを採るかは
  Architect の領分とするが、NFR 3「ちらつきを起こさない」を満たすには (a) または (c) が
  望ましい点だけ記録する。
- OQ-3: debounce 待機中のローディング表示  
  Issue 本文・コメントで明示なし。スピナー等のフィードバック有無を要件に含めるかは
  人間判断。本要件では「結果のちらつきを起こさない」のみを定義し、明示的な
  ローディングインジケータは要件・Out of Scope のいずれにも入れず Open のままとする。
- OQ-4: 入力イベントの検知対象（IME 中間入力の扱い）  
  IME による composition 中（日本語変換中）の中間入力で debounce タイマーをリセット
  するかどうか。Issue では言及なし。Architect / Developer フェーズで `compositionend`
  まで待つ方針を採るかどうか判断する必要がある。
