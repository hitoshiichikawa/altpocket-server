package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"altpocket/internal/config"
	"altpocket/internal/db"
	"altpocket/internal/fetcher"
	"altpocket/internal/logger"
	"altpocket/internal/store"
	"log/slog"
)

func main() {
	cfg := config.Load()
	log := logger.New()

	ctx := context.Background()
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("db_connect_failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	st := store.New(pool)
	fullLimit := cfg.ContentFullLimit - 100
	if fullLimit < 100 {
		fullLimit = 100
	}
	f := fetcher.New(1_000_000, fullLimit, cfg.ContentSearchLimit)

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			cleanupSessions(ctx, st, log)
			runOnce(ctx, st, f, log)
		case <-done:
			log.Info("worker_shutdown")
			return
		}
	}
}

func cleanupSessions(ctx context.Context, st *store.Store, log *slog.Logger) {
	removed, err := st.CleanupExpiredSessions(ctx)
	if err != nil {
		log.Error("session_cleanup_failed", "error", err)
		return
	}
	if removed > 0 {
		log.Info("session_cleanup", "removed", removed)
	}
}

func runOnce(ctx context.Context, st *store.Store, f *fetcher.Fetcher, log *slog.Logger) {
	items, err := st.ClaimItemsForFetch(ctx, 50)
	if err != nil {
		log.Error("worker_claim_failed", "error", err)
		return
	}
	if len(items) == 0 {
		return
	}

	sem := make(chan struct{}, 10)
	var wg sync.WaitGroup
	for _, it := range items {
		it := it
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			ctxFetch, cancel := context.WithTimeout(ctx, 12*time.Second)
			defer cancel()

			res, err := f.Fetch(ctxFetch, it.URL)
			if err != nil {
				reason := classifyFetchError(err)
				_ = st.UpdateFetchFailure(ctx, it.ID, reason)
				log.Info("worker_fetch_failed", "item_id", it.ID, "reason", reason)
				return
			}
			err = st.UpdateFetchSuccess(ctx, it.ID, res.Title, res.Excerpt, res.ContentFull, res.ContentSearch, res.ContentBytes)
			if err != nil {
				log.Error("worker_db_update_failed", "item_id", it.ID, "error", err)
				return
			}
			if it.RefetchRequested {
				log.Info("refetch_consumed", "item_id", it.ID)
			}
			log.Info("worker_fetch_success", "item_id", it.ID)
		}()
	}
	wg.Wait()
}

func classifyFetchError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, fetcher.ErrTooLarge) {
		return "size_limit"
	}
	if errors.Is(err, fetcher.ErrTooManyRedir) {
		return "redirect_limit"
	}
	if errors.Is(err, fetcher.ErrBadStatus) {
		return "bad_status"
	}
	return "fetch_failed"
}
