package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"time"

	"github.com/lucasew/fetchurl/internal/errutil"
	"github.com/lucasew/fetchurl/internal/proxy"

	"github.com/lucasew/fetchurl/internal/eviction"
	_ "github.com/lucasew/fetchurl/internal/eviction/lru"
	"github.com/lucasew/fetchurl/internal/eviction/policy"
	"github.com/lucasew/fetchurl/internal/eviction/policy/maxsize"
	"github.com/lucasew/fetchurl/internal/eviction/policy/minfree"
	"github.com/lucasew/fetchurl/internal/handler"
	"github.com/lucasew/fetchurl/internal/repository"
)

type Config struct {
	Port             int
	CacheDir         string
	MaxCacheSize     int64
	MinFreeSpace     int64
	EvictionInterval time.Duration
	EvictionStrategy string
	Upstreams        []string
}

func NewServer(ctx context.Context, cfg Config) (*http.Server, func(), error) {
	// Setup Eviction Manager
	strat, err := eviction.GetStrategy(cfg.EvictionStrategy)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize eviction strategy: %w", err)
	}

	// Setup Policies
	var policies []policy.Policy

	if cfg.MaxCacheSize > 0 {
		slog.Info("Adding MaxCacheSize policy", "max_size", cfg.MaxCacheSize)
		policies = append(policies, &maxsize.Policy{MaxBytes: cfg.MaxCacheSize})
	}

	if cfg.MinFreeSpace > 0 {
		slog.Info("Adding MinFreeSpace policy", "min_free", cfg.MinFreeSpace)
		policies = append(policies, &minfree.Policy{
			Path:         cfg.CacheDir,
			MinFreeBytes: cfg.MinFreeSpace,
		})
	}

	if len(policies) == 0 {
		slog.Info("No eviction policies configured (unlimited cache)")
	}

	mgr := eviction.NewManager(cfg.CacheDir, policies, cfg.EvictionInterval, strat)

	if err := mgr.LoadInitialState(); err != nil {
		errutil.LogMsg(err, "Failed to load initial cache state")
	}

	// Use the context from Cobra, which is canceled on shutdown
	appCtx, cancel := context.WithCancel(ctx)
	// Start eviction manager
	go mgr.Start(appCtx)

	if err := os.MkdirAll(cfg.CacheDir, 0755); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Create shared HTTP client for outbound requests
	httpClientForRequests := http.DefaultClient

	localRepo := repository.NewLocalRepository(cfg.CacheDir, mgr)

	casHandler := handler.NewCASHandler(localRepo, httpClientForRequests, cfg.Upstreams, appCtx)

	mux := http.NewServeMux()
	// Mux handling: /api/fetchurl/{algo}/{hash}
	mux.Handle("/api/fetchurl/", http.StripPrefix("/api/fetchurl", casHandler))
	mux.Handle("/api/registry/", http.StripPrefix("/api/registry", proxy.GetRewriter(casHandler)))

	addr := fmt.Sprintf(":%d", cfg.Port)
	slog.Info("Starting server (CAS)", "addr", addr, "cache_dir", cfg.CacheDir, "upstreams", len(cfg.Upstreams))

	server := &http.Server{
		Addr:    addr,
		Handler: LogMiddleware(mux),
	}

	cleanup := func() {
		cancel()
	}

	return server, cleanup, nil
}

func LogMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Info("request", "method", r.Method, "path", r.URL.Path)
		h.ServeHTTP(w, r)
	})
}
