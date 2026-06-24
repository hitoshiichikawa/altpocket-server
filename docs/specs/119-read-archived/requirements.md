# Requirements Document

## Introduction

altpocket は read-later サービスでありながら、現状はアイテムが「未読 / 既読 / アーカイブ済み」の
ユーザー可視な状態を持たない。`FetchStatus`（success / fetching / pending / failed）は本文取得の
進捗を示すのみで、読了消化や整理の指標として機能しない。本 Issue では Items にユーザー可視な
3 状態（`unread` / `read` / `archived`）を導入し、Web UI からの状態遷移操作、デフォルト「未読のみ」
表示、「All」「Archived」への切替、状態に応じた視覚区別、および MCP 経由での状態の一貫した参照
範囲を定義する。状態管理方式は Issue コメント（needs-decisions auto-continue, 2026-06-23）により
**Option A: 3 値の明示的な状態フィールド**を採用済みであり、読了日時のタイムスタンプ管理は
スコープ外とする。

## Requirements

### Requirement 1: アイテム状態モデル

**Objective:** As a read-later サービスのユーザー, I want アイテムを未読・既読・アーカイブの 3 状態で管理できる, so that 読了済みや棚上げしたアイテムを新着と区別して整理できる

#### Acceptance Criteria

1. The Items Service shall 各アイテムに対し `unread` / `read` / `archived` のいずれか 1 つの状態を保持する
2. The Items Service shall 新規に保存されたアイテムの初期状態を `unread` として扱う
3. When 本機能のスキーマ変更適用後にユーザーが既存アイテム一覧を参照したとき, the Items Service shall 既存の全アイテムを `unread` 状態として返す
4. When ユーザーがアイテムの状態を変更したとき, the Items Service shall 変更後の状態を永続化し、以降のリロード / 再ログイン後も同じ状態を返す
5. The Items Service shall `unread` / `read` / `archived` 以外の状態値をアイテムに設定することを拒否する
6. The Items Service shall アイテムの状態と既存の `FetchStatus`（success / fetching / pending / failed）を独立した 2 軸として扱う

### Requirement 2: 状態遷移操作（Web UI）

**Objective:** As a Web UI 利用者, I want カードから既読化・未読戻し・アーカイブ化を 1 アクションで行える, so that 読み終えたアイテムや棚上げするアイテムを即座に整理できる

#### Acceptance Criteria

1. The Web UI shall 各アイテムカードに「既読切り替え（unread ⇄ read）」のアクション要素を表示する
2. The Web UI shall 各アイテムカードに「アーカイブする（→ archived）」のアクション要素を表示する
3. When ユーザーが `unread` 状態のアイテムで既読切り替えを実行したとき, the Web UI shall 当該アイテムの状態を `read` に変更する
4. When ユーザーが `read` 状態のアイテムで既読切り替えを実行したとき, the Web UI shall 当該アイテムの状態を `unread` に変更する
5. When ユーザーがアーカイブ操作を実行したとき, the Web UI shall 当該アイテムの状態を `archived` に変更する
6. While アイテムが `archived` 状態, the Web UI shall ユーザーが当該アイテムを `unread` に戻す（アーカイブ解除）操作要素を提供する
7. If 状態変更操作が失敗したとき, the Web UI shall 操作前の状態を維持し、ユーザーに失敗を通知する
8. When 状態変更操作が成功したとき, the Web UI shall 当該カードの表示状態（後述 Requirement 4 の視覚区別および現在のフィルタ条件に従った表示／非表示）を再リロードなしで反映する

### Requirement 3: 一覧フィルタとタブ切替

**Objective:** As a Web UI 利用者, I want 未読 / すべて / アーカイブを切り替えて一覧表示できる, so that 未読消化を中心に据えつつ、必要に応じて履歴・アーカイブも参照できる

#### Acceptance Criteria

1. The Web UI shall ライブラリ一覧の初期表示で `unread` 状態のアイテムのみを表示する
2. The Web UI shall 「Unread」「All」「Archived」の 3 つの状態タブ（または同等の切替 UI）を提供する
3. When ユーザーが「Unread」タブを選択したとき, the Web UI shall `unread` 状態のアイテムのみを一覧に表示する
4. When ユーザーが「All」タブを選択したとき, the Web UI shall `unread` と `read` 状態のアイテムを一覧に表示し、`archived` を除外する
5. When ユーザーが「Archived」タブを選択したとき, the Web UI shall `archived` 状態のアイテムのみを一覧に表示する
6. The Web UI shall 状態タブの選択と既存のタグフィルタ / 検索クエリ / ソート / ページ送りを併用可能とする
7. While 状態タブが「Unread」以外に設定されている, the Web UI shall 現在選択中の状態タブを #115 のアクティブフィルタ表示と矛盾しない方法で常時可視化する
8. The Web UI shall 状態タブの選択をページ遷移後も保持し、ユーザーが意図的に切替操作するまで初期値（Unread）に戻さない（保持の永続単位は確認事項 (b) に従う）

### Requirement 4: 状態の視覚区別

**Objective:** As a Web UI 利用者, I want カードの色やスタイルでアイテム状態を一目で識別できる, so that スクロール中に未読 / 既読 / アーカイブ / 取得失敗を迷わず認識できる

#### Acceptance Criteria

1. The Web UI shall `unread` / `read` / `archived` の 3 状態に対して、それぞれ視覚的に区別可能なカードスタイルを適用する
2. The Web UI shall `FetchStatus = failed` を本機能の 3 状態とは別軸として、引き続き視覚的に識別可能な形で提示する
3. Where #12 のカードアクセントバー色分けが共存する, the Web UI shall 本機能の状態区別を #12 の色定義と衝突しない範囲で表現する
4. The Web UI shall 状態を識別する視覚要素を、色覚多様性に配慮した形（色のみに依存しないテキストラベル / アイコン / 形状のいずれかの併用）で提供する

### Requirement 5: MCP 経由の状態可視性

**Objective:** As a MCP クライアント利用者, I want MCP ツール経由でも本機能の状態を一貫した範囲で参照できる, so that 外部クライアントから取得した結果と Web UI で見える内容に予期しないズレが生じない

#### Acceptance Criteria

1. The MCP Server shall 公開する各アイテムオブジェクトに `unread` / `read` / `archived` のいずれかの状態フィールドを含める
2. The MCP Server shall 「新着取得」相当の機能で返すアイテム集合の状態フィルタ既定値を、本仕様内で明文化された 1 つの値に固定する（既定値は確認事項 (a) に従って確定する）
3. Where MCP クライアントが状態を引数で指定した, the MCP Server shall 指定された状態のアイテムのみを返す（受け付ける状態値は確認事項 (a) に従って確定する）
4. The MCP Server shall Web UI からの状態変更が永続化された後、後続の MCP 呼び出しで更新後の状態を返す

### Requirement 6: 後方互換とデータ移行時の保護

**Objective:** As a 既存ユーザー, I want 本機能リリース後に既存データと既存挙動が壊れない, so that バージョンアップ作業後すぐに普段通りライブラリを利用できる

#### Acceptance Criteria

1. When 本機能のスキーマ変更を既存環境に適用したとき, the Items Service shall 既存の全アイテムを `unread` 状態として保持し、データ消失や状態未設定アイテムを生まない
2. The Items Service shall 本機能の追加によって既存の URL 正規化 / タグ / 検索 / 取得済み本文の挙動を変更しない
3. The MCP Server shall 既存 MCP クライアントが状態フィールドを送信しないリクエストでも、本機能導入前と比較して破壊的に異なる挙動を返さない（既定範囲は確認事項 (a) に従う）

## Non-Functional Requirements

### NFR 1: パフォーマンス

1. While 1 ユーザーあたりのアイテム件数が 10,000 件以下, the Web UI shall ライブラリ一覧の初期表示（Unread タブ・既定ページサイズ）の体感応答時間を、本機能導入前の同条件比で +20% 以内に抑える
2. When ユーザーが状態タブを切り替えたとき, the Web UI shall 1 秒以内に新しい一覧を提示する（1 ユーザーあたり 10,000 件以下、既定ページサイズ前提）
3. When ユーザーが個別アイテムの状態を変更したとき, the Web UI shall 操作完了の視覚的フィードバックを 500ms 以内に提示する

### NFR 2: 認可・データ分離

1. The Items Service shall ユーザーが他ユーザーの所有するアイテムの状態を読み取ること、および変更することを拒否する
2. The MCP Server shall MCP API キーに紐付くユーザーが所有するアイテムのみを状態フィルタの対象とする

### NFR 3: 可観測性

1. The Items Service shall 状態変更操作についてユーザー識別子・対象アイテム識別子・遷移前後の状態を構造化ログとして記録し、トークン / Cookie / 本文の生値を含めない

### NFR 4: アクセシビリティ

1. The Web UI shall 既読トグル / アーカイブ操作要素を、キーボード操作（Tab フォーカス + Enter / Space）のみで実行可能とする
2. The Web UI shall 既読トグル / アーカイブ操作要素および状態タブに、状態を読み上げ可能なテキスト（aria-label 等）を付与する

## Out of Scope

- 既読率ダッシュボード（消化率の集計・可視化）
- アーカイブ済みアイテムの自動クリーンアップ（定期削除・容量上限による自動削除）
- 既読日時 / アーカイブ日時のタイムスタンプ管理および期間集計（Option A 採用により、タイムスタンプはスコープ外）
- 状態に基づく通知 / リマインダ機能
- Chrome 拡張機能 UI への状態操作要素の追加（拡張機能向けレスポンスへの状態フィールド露出可否は確認事項 (e) で決定）
- 一括選択による複数アイテムの一括状態変更
- 検索結果やタグフィルタの母集団にアーカイブを含める／除外するの設定 UI（既定挙動は Requirement 3 に従う）

## 確認事項

以下は Issue 本文・コメント・既存ドキュメントから一意に確定できなかったため、要件としては
プレースホルダにとどめ、design 着手前または同フェーズで人間判断を仰ぐ:

- (a) **MCP の状態フィルタ既定値と受け付ける値**: 「新着取得（`ListRecentItems` 相当）」が
  既定で `unread` のみを返すか / `unread` + `read` を返すか / `archived` を含めるかを確定する
  必要がある。あわせて、MCP クライアントが状態を引数で指定する場合に受理する状態値の集合
  （単一状態 / 複数状態 / 「全状態」スイッチの有無）も決定する。Issue の判断委ね項目に該当
- (b) **状態タブ選択の保持永続単位**: 状態タブの選択を「URL クエリ（リンク共有可能）」
  「セッション内のみ（同一ブラウザ・ログイン継続中）」「ユーザー設定として永続化」のいずれで
  保持するか。Requirement 3.8 の保持挙動の粒度確定のため
- (c) **キーボードショートカット導入の可否と割当**: Issue では `e` / `a` ショートカット案と既存
  `e` キーとの衝突回避が論点として挙げられている。本リポジトリの `static/` を確認した時点で
  既存のグローバルショートカットハンドラは発見できなかったが、Chrome 拡張・MCP 側を含めた
  「`e` キーが暗黙に予約されている用途」の有無、および本 Issue でショートカットを正式機能として
  含めるか optional とするか、含める場合の割当（`e` / `a` / その他）を決定する必要がある。
  決定によって Requirement 2 にショートカット用の AC を追加する
- (d) **「All」タブにアーカイブを含めるかの最終確認**: Issue 受入基準は「『すべて』『アーカイブ』
  のタブ／フィルタで切り替えられる」とのみ記述しており、「All」が `archived` を含むか否かを
  明示していない。Requirement 3.4 は「`unread` + `read` のみで `archived` を除外」を仮置きしている
  が、想定と異なる場合は確定が必要
- (e) **拡張機能 API の状態フィールド露出**: 本機能で `extension_contract_test.go` が検証する
  拡張機能向けレスポンスに状態フィールドを追加するか、後方互換維持のためレスポンスからは
  状態フィールドを除外するかを決定する必要がある（Out of Scope に暫定で「拡張機能 UI への状態
  操作追加」を入れているが、レスポンスフィールド露出の可否は別判断）

## Related

- Related: #12 #115
