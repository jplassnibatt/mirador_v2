package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"mirador/internal/db"
	"mirador/internal/handlers"
	"mirador/internal/templates"
)

// version is injected at build time via -ldflags "-X main.version=...".
// Defaults to "dev" for local runs.
var version = "dev"

func main() {
	addr := flag.String("addr", envOr("MIRADOR_ADDR", "127.0.0.1:8080"), "listen address (localhost only by default)")
	dbPath := flag.String("db", envOr("MIRADOR_DB", ""), "SQLite database path (default: user config directory)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	path := *dbPath
	if path == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			logger.Error("cannot determine user config directory", "error", err)
			os.Exit(1)
		}
		path = filepath.Join(configDir, "mirador", "mirador.db")
	}

	store, err := db.Open(path)
	if err != nil {
		logger.Error("cannot open database", "path", path, "error", err)
		os.Exit(1)
	}
	defer store.Close()

	templateSets, err := templates.Parse()
	if err != nil {
		logger.Error("cannot parse templates", "error", err)
		os.Exit(1)
	}

	handler := handlers.New(store, templateSets, logger, version)
	server := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		logger.Error("cannot listen", "addr", *addr, "error", err)
		os.Exit(1)
	}

	logger.Info("mirador started",
		"url", fmt.Sprintf("http://%s", listener.Addr().String()),
		"database", store.Path(),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutting down")
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
