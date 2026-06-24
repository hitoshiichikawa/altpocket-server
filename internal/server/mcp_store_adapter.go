package server

import (
	"context"
	"time"

	"altpocket/internal/store"
)

// mcpStoreAdapter wraps *store.Store so it continues to satisfy the
// (pre-#119) mcpserver.DataSource interface even though store.ListItems /
// store.ListRecentItems now accept an extra `statuses []string` argument
// (task 2 of Issue #119).
//
// This adapter is a **transitional shim** owned by task 4 of Issue #119:
// it keeps internal/server compiling while task 5 has not yet updated
// the mcpserver.DataSource interface and the handler call sites to use
// the new signatures. Once task 5 lands the adapter is no longer needed
// and `mcpserver.New(s.store, userID)` can take *store.Store directly
// again (see docs/specs/119-read-archived/tasks.md task 5 / design.md
// `DataSource interface (mcpserver/deps.go)` section).
//
// Intentionally limited to the two methods whose signature changed:
// every other DataSource method is satisfied by embedding *store.Store
// so we do not duplicate the contract.
type mcpStoreAdapter struct {
	*store.Store
}

// newMCPStoreAdapter returns the adapter wrapping the given store.
func newMCPStoreAdapter(s *store.Store) *mcpStoreAdapter {
	return &mcpStoreAdapter{Store: s}
}

// ListItems satisfies the legacy mcpserver.DataSource shape that does
// not yet accept the statuses []string argument. We forward to the new
// store.ListItems with `nil` (= no status filter / whole-set; matches
// the existing MCP behavior prior to Issue #119, Req 6.3 backward
// compatibility). Task 5 will replace this once the mcpserver layer
// learns about the status filter end-to-end.
func (a *mcpStoreAdapter) ListItems(ctx context.Context, userID string, page, perPage int, q string, tags []string, sort string) ([]store.ItemListRow, store.Pagination, error) {
	return a.Store.ListItems(ctx, userID, page, perPage, q, tags, nil, sort)
}

// ListRecentItems satisfies the legacy mcpserver.DataSource shape that
// does not yet accept the statuses []string argument. We forward to
// the new store.ListRecentItems with `nil` (= whole-set; design.md
// `recent-articles` Resource always passes nil for Req 5.2 / Req 6.3).
func (a *mcpStoreAdapter) ListRecentItems(ctx context.Context, userID string, since time.Time) ([]store.ItemListRow, error) {
	return a.Store.ListRecentItems(ctx, userID, since, nil)
}
