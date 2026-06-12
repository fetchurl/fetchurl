package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/lucasew/fetchurl/internal/errutil"

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
	// Use a custom dialer to prevent SSRF
	_, allowPrivate := os.LookupEnv("FETCHURL_ALLOW_PRIVATE_IPS")
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			return ValidateIP(host, allowPrivate)
		},
	}

	// Safely clone default transport or create a new one
	var transport *http.Transport
	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = defaultTransport.Clone()
	} else {
		transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
	}
	transport.DialContext = dialer.DialContext

	httpClientForRequests := &http.Client{
		Transport: transport,
	}

	localRepo := repository.NewLocalRepository(cfg.CacheDir, mgr)

	casHandler := handler.NewCASHandler(localRepo, httpClientForRequests, cfg.Upstreams, appCtx)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/fetchurl/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// Mux handling: /api/fetchurl/{algo}/{hash}
	mux.Handle("/api/fetchurl/", http.StripPrefix("/api/fetchurl", casHandler))

	addr := fmt.Sprintf(":%d", cfg.Port)
	slog.Info("Starting server (CAS)", "addr", addr, "cache_dir", cfg.CacheDir, "upstreams", len(cfg.Upstreams))

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	cleanup := func() {
		cancel()
	}

	return server, cleanup, nil
}
