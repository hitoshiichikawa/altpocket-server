package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"altpocket/internal/store"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// New creates an MCP server with all tools and resources registered.
// userID scopes all data access to the authenticated user.
func New(ds DataSource, userID string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "altpocket",
		Version: "1.0.0",
	}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_items",
		Description: "保存済み記事の一覧をページネーション付きで取得する",
	}, listItemsHandler(ds, userID))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "search_items",
		Description: "キーワードやタグで記事を検索する",
	}, searchItemsHandler(ds, userID))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_item",
		Description: "記事の全文コンテンツを含む詳細情報を取得する",
	}, getItemHandler(ds, userID))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_tags",
		Description: "タグの一覧と各タグの記事数を取得する",
	}, listTagsHandler(ds, userID))

	s.AddResource(&mcp.Resource{
		URI:         "altpocket://recent-articles",
		Name:        "新着記事（過去24時間）",
		Description: "過去24時間以内に保存された記事の一覧",
		MIMEType:    "application/json",
	}, recentArticlesHandler(ds, userID))

	return s
}

// --- list_items tool ---

type ListItemsInput struct {
	Page    int    `json:"page,omitempty" jsonschema:"ページ番号（デフォルト: 1）"`
	PerPage int    `json:"per_page,omitempty" jsonschema:"1ページあたりの件数（デフォルト: 30, 最大: 50）"`
	Sort    string `json:"sort,omitempty" jsonschema:"並び替え: newest または oldest（デフォルト: newest）"`
}

func listItemsHandler(ds DataSource, userID string) mcp.ToolHandlerFor[ListItemsInput, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, args ListItemsInput) (*mcp.CallToolResult, any, error) {
		page := args.Page
		if page < 1 {
			page = 1
		}
		perPage := args.PerPage
		if perPage < 1 || perPage > 50 {
			perPage = 30
		}
		sort := args.Sort
		if sort == "" {
			sort = "newest"
		}

		items, pag, err := ds.ListItems(ctx, userID, page, perPage, "", nil, sort)
		if err != nil {
			return errorResult("データベース接続エラー: " + err.Error()), nil, nil
		}

		return jsonResult(map[string]any{
			"items":      formatItemList(items),
			"pagination": map[string]any{"page": pag.Page, "per_page": pag.PerPage, "total": pag.Total},
		}), nil, nil
	}
}

// --- search_items tool ---

type SearchItemsInput struct {
	Query   string   `json:"query,omitempty" jsonschema:"検索キーワード"`
	Tags    []string `json:"tags,omitempty" jsonschema:"タグによる絞り込み（AND結合）"`
	Page    int      `json:"page,omitempty" jsonschema:"ページ番号（デフォルト: 1）"`
	PerPage int      `json:"per_page,omitempty" jsonschema:"1ページあたりの件数（デフォルト: 30, 最大: 50）"`
}

func searchItemsHandler(ds DataSource, userID string) mcp.ToolHandlerFor[SearchItemsInput, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, args SearchItemsInput) (*mcp.CallToolResult, any, error) {
		if args.Query == "" && len(args.Tags) == 0 {
			return errorResult("queryまたはtagsの少なくとも一方を指定してください"), nil, nil
		}

		page := args.Page
		if page < 1 {
			page = 1
		}
		perPage := args.PerPage
		if perPage < 1 || perPage > 50 {
			perPage = 30
		}

		sort := "newest"
		if args.Query != "" {
			sort = "relevance"
		}

		items, pag, err := ds.ListItems(ctx, userID, page, perPage, args.Query, args.Tags, sort)
		if err != nil {
			return errorResult("データベース接続エラー: " + err.Error()), nil, nil
		}

		return jsonResult(map[string]any{
			"items":      formatItemList(items),
			"pagination": map[string]any{"page": pag.Page, "per_page": pag.PerPage, "total": pag.Total},
		}), nil, nil
	}
}

// --- get_item tool ---

type GetItemInput struct {
	ID string `json:"id" jsonschema:"記事のUUID"`
}

func getItemHandler(ds DataSource, userID string) mcp.ToolHandlerFor[GetItemInput, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, args GetItemInput) (*mcp.CallToolResult, any, error) {
		if args.ID == "" {
			return errorResult("IDを指定してください"), nil, nil
		}

		detail, err := ds.GetItemDetail(ctx, userID, args.ID)
		if err != nil {
			return errorResult("記事が見つかりません"), nil, nil
		}

		tags := make([]map[string]string, 0, len(detail.Tags))
		for _, t := range detail.Tags {
			tags = append(tags, map[string]string{"name": t.Name, "normalized_name": t.NormalizedName})
		}

		contentFull := detail.ContentFull
		if detail.FetchStatus == "pending" || detail.FetchStatus == "fetching" {
			contentFull = ""
		}

		return jsonResult(map[string]any{
			"id":            detail.ID,
			"url":           detail.URL,
			"canonical_url": detail.CanonicalURL,
			"title":         detail.Title,
			"excerpt":       detail.Excerpt,
			"content_full":  contentFull,
			"tags":          tags,
			"fetch_status":  detail.FetchStatus,
			"created_at":    detail.CreatedAt.Format(time.RFC3339),
		}), nil, nil
	}
}

// --- list_tags tool ---

type ListTagsInput struct {
	Query string `json:"query,omitempty" jsonschema:"タグ名フィルタ（前方一致）"`
}

func listTagsHandler(ds DataSource, userID string) mcp.ToolHandlerFor[ListTagsInput, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, args ListTagsInput) (*mcp.CallToolResult, any, error) {
		tags, err := ds.ListTagsWithCountFiltered(ctx, userID, args.Query, nil)
		if err != nil {
			return errorResult("データベース接続エラー: " + err.Error()), nil, nil
		}

		result := make([]map[string]any, 0, len(tags))
		for _, t := range tags {
			result = append(result, map[string]any{
				"name":            t.Name,
				"normalized_name": t.NormalizedName,
				"count":           t.Count,
			})
		}

		return jsonResult(map[string]any{"tags": result}), nil, nil
	}
}

// --- recent-articles resource ---

func recentArticlesHandler(ds DataSource, userID string) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		since := time.Now().Add(-24 * time.Hour)
		items, err := ds.ListRecentItems(ctx, userID, since)
		if err != nil {
			return nil, fmt.Errorf("データベース接続エラー: %w", err)
		}

		data := map[string]any{
			"articles":     formatItemList(items),
			"generated_at": time.Now().Format(time.RFC3339),
			"count":        len(items),
		}

		b, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}

		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      "altpocket://recent-articles",
					MIMEType: "application/json",
					Text:     string(b),
				},
			},
		}, nil
	}
}

// --- helpers ---

func formatItemList(items []store.ItemListRow) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		tags := make([]map[string]string, 0, len(item.Tags))
		for _, t := range item.Tags {
			tags = append(tags, map[string]string{"name": t.Name, "normalized_name": t.NormalizedName})
		}
		result = append(result, map[string]any{
			"id":           item.ID,
			"url":          item.URL,
			"title":        item.Title,
			"excerpt":      item.Excerpt,
			"tags":         tags,
			"fetch_status": item.FetchStatus,
			"created_at":   item.CreatedAt.Format(time.RFC3339),
		})
	}
	return result
}

func jsonResult(data any) *mcp.CallToolResult {
	b, err := json.Marshal(data)
	if err != nil {
		return errorResult("JSONエンコードエラー: " + err.Error())
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(b)},
		},
	}
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: msg},
		},
		IsError: true,
	}
}
