# Requirements Document

## Introduction
altpocketにMCP (Model Context Protocol) Server機能を追加し、AIエージェントが保存済み記事データにプログラマティックにアクセスできるインターフェースを提供する。MCP Serverは記事の一覧取得・検索・詳細取得・タグ操作のツールと、過去24時間の新着記事リソースを公開する。これにより、AIエージェントが記事スクラップの解析、興味関心分析、トレンド抽出などを自律的に実行できる基盤を構築する。

## Requirements

### Requirement 1: MCP Server基盤
**Objective:** AIエージェント開発者として、altpocketのMCP Serverにstdioトランスポートで接続したい。これにより、Claude DesktopやCline等のMCPクライアントからaltpocketの記事データにアクセスできるようにするため。

#### Acceptance Criteria
1. The MCP Server shall MCPプロトコル（JSON-RPC 2.0 over stdio）を実装し、`initialize`・`tools/list`・`tools/call`・`resources/list`・`resources/read`リクエストに応答する
2. The MCP Server shall `cmd/mcp/main.go`を実行エントリーポイントとし、既存の`internal/store`パッケージを介してPostgreSQLに接続する
3. The MCP Server shall 環境変数`DATABASE_URL`によるDB接続設定を既存の`internal/config`パッケージと共有する
4. When MCPクライアントが`initialize`リクエストを送信した場合, the MCP Server shall サーバー名・バージョン・対応capabilities（tools, resources）を返却する
5. If 不正なJSON-RPCリクエストを受信した場合, the MCP Server shall MCPプロトコルに準拠したエラーレスポンス（エラーコード・メッセージ）を返却する

### Requirement 2: 記事一覧取得ツール（list_items）
**Objective:** AIエージェントとして、保存済み記事の一覧をページネーション付きで取得したい。これにより、ユーザーの記事コレクション全体を把握できるようにするため。

#### Acceptance Criteria
1. When AIエージェントが`list_items`ツールを呼び出した場合, the MCP Server shall 記事一覧（ID、URL、タイトル、概要、タグ、取得状態、作成日時）をJSON形式で返却する
2. The `list_items` tool shall `page`（デフォルト: 1）および`per_page`（デフォルト: 30、最大: 50）パラメータによるページネーションをサポートする
3. The `list_items` tool shall `sort`パラメータ（`newest`または`oldest`、デフォルト: `newest`）による並び替えをサポートする
4. When 結果が存在しない場合, the MCP Server shall 空配列とページネーション情報（total_count: 0）を返却する

### Requirement 3: 記事検索ツール（search_items）
**Objective:** AIエージェントとして、キーワードやタグで記事を検索したい。これにより、特定のトピックに関する記事を効率的に発見できるようにするため。

#### Acceptance Criteria
1. When AIエージェントが`search_items`ツールを`query`パラメータ付きで呼び出した場合, the MCP Server shall タイトル・概要・本文・URLを横断検索した結果を関連度順で返却する
2. When AIエージェントが`search_items`ツールを`tags`パラメータ付きで呼び出した場合, the MCP Server shall 指定されたタグすべてを持つ記事のみに絞り込んだ結果を返却する
3. The `search_items` tool shall `query`と`tags`の同時指定をサポートし、両方の条件をAND結合して検索する
4. The `search_items` tool shall ページネーション（`page`、`per_page`）をサポートする
5. If `query`も`tags`も指定されなかった場合, the MCP Server shall パラメータ不足エラーを返却する

### Requirement 4: 記事詳細取得ツール（get_item）
**Objective:** AIエージェントとして、特定の記事の全文コンテンツを取得したい。これにより、記事の内容を詳細に分析できるようにするため。

#### Acceptance Criteria
1. When AIエージェントが`get_item`ツールを`id`パラメータ付きで呼び出した場合, the MCP Server shall 記事の全情報（メタデータ+全文コンテンツ+タグ一覧）を返却する
2. If 指定されたIDの記事が存在しない場合, the MCP Server shall 「記事が見つかりません」エラーを返却する
3. While 記事の取得状態が`pending`または`fetching`の場合, the MCP Server shall 全文コンテンツをnullとし、取得状態（fetch_status）を明示して返却する

### Requirement 5: タグ一覧取得ツール（list_tags）
**Objective:** AIエージェントとして、利用可能なタグの一覧と各タグの記事数を把握したい。これにより、記事コレクションの分類構造を理解できるようにするため。

#### Acceptance Criteria
1. When AIエージェントが`list_tags`ツールを呼び出した場合, the MCP Server shall 全タグ（名前、正規化名、記事数）を記事数降順で返却する
2. When AIエージェントが`list_tags`ツールを`query`パラメータ付きで呼び出した場合, the MCP Server shall タグ名で前方一致フィルタした結果を返却する

### Requirement 6: 新着記事リソース（recent-articles）
**Objective:** AIエージェントとして、過去24時間以内に保存された記事を一括取得したい。これにより、定期的な記事要約配信やトレンド分析の入力データとして活用できるようにするため。

#### Acceptance Criteria
1. When AIエージェントが`altpocket://recent-articles`リソースを読み取った場合, the MCP Server shall 過去24時間以内に作成された記事一覧（ID、URL、タイトル、概要、タグ、作成日時）を新しい順で返却する
2. The `recent-articles` resource shall 結果が0件の場合でも空配列を含む有効なJSONレスポンスを返却する
3. The `recent-articles` resource shall リソースのURIとして`altpocket://recent-articles`、名前として「新着記事（過去24時間）」を`resources/list`で公開する

### Requirement 7: 認証とセキュリティ
**Objective:** システム管理者として、MCP Serverへのアクセスを認可されたユーザーのみに制限したい。これにより、記事データの不正アクセスを防止するため。

#### Acceptance Criteria
1. The MCP Server shall 環境変数`MCP_USER_EMAIL`で指定されたユーザーのデータのみにアクセスを制限する
2. If `MCP_USER_EMAIL`に対応するユーザーがデータベースに存在しない場合, the MCP Server shall 起動時にエラーを出力して終了する
3. The MCP Server shall stdioトランスポートのみをサポートし、ネットワーク経由の接続を受け付けない
