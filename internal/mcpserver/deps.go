package mcpserver

import (
	"context"
	"time"

	"altpocket/internal/store"
)

// DataSource is the subset of the store needed by MCP tool/resource handlers.
// Defined as an interface so handlers can be unit tested with a fake.
// *store.Store satisfies this interface.
type DataSource interface {
	ListItems(ctx context.Context, userID string, page, perPage int, q string, tags []string, sort string) ([]store.ItemListRow, store.Pagination, error)
	GetItemDetail(ctx context.Context, userID, itemID string) (store.ItemDetail, error)
	ListTagsWithCountFiltered(ctx context.Context, userID, q string, selectedTags []string) ([]store.Tag, error)
	ListRecentItems(ctx context.Context, userID string, since time.Time) ([]store.ItemListRow, error)
}

// KeyValidator is the subset of the store needed by the auth middleware.
// *store.Store satisfies this interface.
type KeyValidator interface {
	ValidateMCPAPIKey(ctx context.Context, keyHash string) (string, error)
}
