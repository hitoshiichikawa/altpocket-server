package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"altpocket/internal/config"
	"altpocket/internal/db"
	"altpocket/internal/logger"
	"altpocket/internal/ratelimit"
	"altpocket/internal/server"
	"altpocket/internal/store"
	"altpocket/internal/ui"
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
	limiter := ratelimit.New(50, 50)
	renderer, err := ui.New("templates")
	if err != nil {
		log.Error("template_load_failed", "error", err)
		os.Exit(1)
	}

	srv := server.New(cfg, st, limiter, log, renderer)
	httpServer := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: srv.Routes(),
	}

	go func() {
		log.Info("api_listen", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("http_server_error", "error", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctxShutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctxShutdown)
	log.Info("api_shutdown")
}
