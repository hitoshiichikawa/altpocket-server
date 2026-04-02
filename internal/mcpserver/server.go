package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"altpocket/internal/store"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// New creates an MCP server with all tools and resources registered.
// userID scopes all data access to the authenticated user.
func New(st *store.Store, userID string) *server.MCPServer {
	s := server.NewMCPServer("altpocket", "1.0.0")

	s.AddTool(listItemsTool(), listItemsHandler(st, userID))
	s.AddTool(searchItemsTool(), searchItemsHandler(st, userID))
	s.AddTool(getItemTool(), getItemHandler(st, userID))
	s.AddTool(listTagsTool(), listTagsHandler(st, userID))

	s.AddResource(recentArticlesResource(), recentArticlesHandler(st, userID))

	return s
}

// --- list_items tool ---

func listItemsTool() mcp.Tool {
	return mcp.NewTool("list_items",
		mcp.WithDescription("保存済み記事の一覧をページネーション付きで取得する"),
		mcp.WithNumber("page",
			mcp.Description("ページ番号（デフォルト: 1）"),
		),
		mcp.WithNumber("per_page",
			mcp.Description("1ページあたりの件数（デフォルト: 30, 最大: 50）"),
		),
		mcp.WithString("sort",
			mcp.Description("並び替え: newest または oldest（デフォルト: newest）"),
			mcp.Enum("newest", "oldest"),
		),
	)
}

func listItemsHandler(st *store.Store, userID string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		page := int(req.GetFloat("page", 1))
		perPage := int(req.GetFloat("per_page", 30))
		sort := req.GetString("sort", "newest")

		if page < 1 {
			page = 1
		}
		if perPage < 1 || perPage > 50 {
			perPage = 30
		}

		items, pag, err := st.ListItems(ctx, userID, page, perPage, "", nil, sort)
		if err != nil {
			return errorResult("データベース接続エラー: " + err.Error()), nil
		}

		return jsonResult(map[string]any{
			"items":      formatItemList(items),
			"pagination": map[string]any{"page": pag.Page, "per_page": pag.PerPage, "total": pag.Total},
		})
	}
}

// --- search_items tool ---

func searchItemsTool() mcp.Tool {
	return mcp.NewTool("search_items",
		mcp.WithDescription("キーワードやタグで記事を検索する"),
		mcp.WithString("query",
			mcp.Description("検索キーワード"),
		),
		mcp.WithArray("tags",
			mcp.Description("タグによる絞り込み（AND結合）"),
			mcp.Items(map[string]any{"type": "string"}),
		),
		mcp.WithNumber("page",
			mcp.Description("ページ番号（デフォルト: 1）"),
		),
		mcp.WithNumber("per_page",
			mcp.Description("1ページあたりの件数（デフォルト: 30, 最大: 50）"),
		),
	)
}

func searchItemsHandler(st *store.Store, userID string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := req.GetString("query", "")
		page := int(req.GetFloat("page", 1))
		perPage := int(req.GetFloat("per_page", 30))

		var tags []string
		if rawTags, ok := req.GetArguments()["tags"]; ok {
			if arr, ok := rawTags.([]any); ok {
				for _, v := range arr {
					if s, ok := v.(string); ok {
						tags = append(tags, s)
					}
				}
			}
		}

		if query == "" && len(tags) == 0 {
			return errorResult("queryまたはtagsの少なくとも一方を指定してください"), nil
		}

		if page < 1 {
			page = 1
		}
		if perPage < 1 || perPage > 50 {
			perPage = 30
		}

		sort := "newest"
		if query != "" {
			sort = "relevance"
		}

		items, pag, err := st.ListItems(ctx, userID, page, perPage, query, tags, sort)
		if err != nil {
			return errorResult("データベース接続エラー: " + err.Error()), nil
		}

		return jsonResult(map[string]any{
			"items":      formatItemList(items),
			"pagination": map[string]any{"page": pag.Page, "per_page": pag.PerPage, "total": pag.Total},
		})
	}
}

// --- get_item tool ---

func getItemTool() mcp.Tool {
	return mcp.NewTool("get_item",
		mcp.WithDescription("記事の全文コンテンツを含む詳細情報を取得する"),
		mcp.WithString("id",
			mcp.Description("記事のUUID"),
			mcp.Required(),
		),
	)
}

func getItemHandler(st *store.Store, userID string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := req.GetString("id", "")
		if id == "" {
			return errorResult("IDを指定してください"), nil
		}

		detail, err := st.GetItemDetail(ctx, userID, id)
		if err != nil {
			return errorResult("記事が見つかりません"), nil
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
		})
	}
}

// --- list_tags tool ---

func listTagsTool() mcp.Tool {
	return mcp.NewTool("list_tags",
		mcp.WithDescription("タグの一覧と各タグの記事数を取得する"),
		mcp.WithString("query",
			mcp.Description("タグ名フィルタ（前方一致）"),
		),
	)
}

func listTagsHandler(st *store.Store, userID string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := req.GetString("query", "")

		tags, err := st.ListTagsWithCountFiltered(ctx, userID, query, nil)
		if err != nil {
			return errorResult("データベース接続エラー: " + err.Error()), nil
		}

		result := make([]map[string]any, 0, len(tags))
		for _, t := range tags {
			result = append(result, map[string]any{
				"name":            t.Name,
				"normalized_name": t.NormalizedName,
				"count":           t.Count,
			})
		}

		return jsonResult(map[string]any{"tags": result})
	}
}

// --- recent-articles resource ---

func recentArticlesResource() mcp.Resource {
	return mcp.NewResource(
		"altpocket://recent-articles",
		"新着記事（過去24時間）",
		mcp.WithResourceDescription("過去24時間以内に保存された記事の一覧"),
		mcp.WithMIMEType("application/json"),
	)
}

func recentArticlesHandler(st *store.Store, userID string) server.ResourceHandlerFunc {
	return func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		since := time.Now().Add(-24 * time.Hour)
		items, err := st.ListRecentItems(ctx, userID, since)
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

		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      "altpocket://recent-articles",
				MIMEType: "application/json",
				Text:     string(b),
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

func jsonResult(data any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: string(b),
			},
		},
	}, nil
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: msg,
			},
		},
		IsError: true,
	}
}
