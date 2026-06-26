package mcpserver

import (
	"context"
	"time"

	"altpocket/internal/store"
)

// DataSource is the subset of the store needed by MCP tool/resource handlers.
// Defined as an interface so handlers can be unit tested with a fake.
// *store.Store satisfies this interface.
//
// ListItems / ListRecentItems include a `statuses []string` argument (Issue
// #119). `nil` / empty means "no status filter" (whole-set, matching the
// pre-#119 MCP behavior and Req 6.3 backward compatibility). The mcpserver
// handlers translate the Tool input `Status` string into this slice via
// `mcpStatusFilter`; the `recent-articles` Resource always passes `nil`
// since it does not accept structured input arguments (design.md
// "`recent-articles` Resource の status 引数取扱" section).
type DataSource interface {
	ListItems(ctx context.Context, userID string, page, perPage int, q string, tags []string, statuses []string, sort string) ([]store.ItemListRow, store.Pagination, error)
	GetItemDetail(ctx context.Context, userID, itemID string) (store.ItemDetail, error)
	ListTagsWithCountFiltered(ctx context.Context, userID, q string, selectedTags []string) ([]store.Tag, error)
	ListRecentItems(ctx context.Context, userID string, since time.Time, statuses []string) ([]store.ItemListRow, error)
}

// KeyValidator is the subset of the store needed by the auth middleware.
// *store.Store satisfies this interface.
type KeyValidator interface {
	ValidateMCPAPIKey(ctx context.Context, keyHash string) (string, error)
}
