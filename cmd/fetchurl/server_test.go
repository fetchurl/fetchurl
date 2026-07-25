package main

import (
	"context"
	"net"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/spf13/viper"
)

// Handler blocks only on appCtx (same pattern as CAS singleflight using AppCtx).
// Without cancelling appCtx before Shutdown, Shutdown would wait until the
// shutdown timeout while the handler never returns.
func TestRunHTTPServerCancelsAppCtxBeforeShutdown(t *testing.T) {
	appCtx, appCancel := context.WithCancel(context.Background())
	t.Cleanup(appCancel)

	started := make(chan struct{})
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-started:
			default:
				close(started)
			}
			// Ignore request context: mirrors CAS fetchAndStream(h.AppCtx, ...).
			<-appCtx.Done()
			w.WriteHeader(http.StatusOK)
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	runCtx, runCancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runHTTPServerServe(runCtx, server, appCancel, func() error {
			return server.Serve(ln)
		})
	}()

	// In-flight request that only completes when appCtx is cancelled.
	reqErr := make(chan error, 1)
	go func() {
		// Wait until serve is accepting (retry briefly).
		var last error
		for i := 0; i < 50; i++ {
			resp, err := http.Get("http://" + ln.Addr().String() + "/")
			if err == nil {
				resp.Body.Close()
				reqErr <- nil
				return
			}
			last = err
			time.Sleep(10 * time.Millisecond)
		}
		reqErr <- last
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		runCancel()
		t.Fatal("handler did not start")
	}

	runCancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runHTTPServerServe: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown blocked: app context was not cancelled before Shutdown")
	}

	select {
	case err := <-reqErr:
		if err != nil {
			t.Fatalf("client request: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request did not finish after app cancel")
	}
}

func TestNormalizeCSVList(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, nil},
		{"empty", []string{}, nil},
		{"single", []string{"http://a/api/fetchurl"}, []string{"http://a/api/fetchurl"}},
		{"already split", []string{"http://a/api/fetchurl", "http://b/api/fetchurl"}, []string{"http://a/api/fetchurl", "http://b/api/fetchurl"}},
		// viper/cast leaves comma-joined env as one element
		{"comma joined", []string{"http://a/api/fetchurl,http://b/api/fetchurl"}, []string{"http://a/api/fetchurl", "http://b/api/fetchurl"}},
		// strings.Fields on "a, b" yields trailing comma on first token
		{"fields mangled comma space", []string{"http://a/api/fetchurl,", "http://b/api/fetchurl"}, []string{"http://a/api/fetchurl", "http://b/api/fetchurl"}},
		{"trim and drop empty", []string{" http://a , , http://b "}, []string{"http://a", "http://b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeCSVList(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("normalizeCSVList(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestConfigUpstreamsFromEnv(t *testing.T) {
	// Isolate global viper used by the CLI package.
	viper.Reset()
	t.Cleanup(viper.Reset)

	viper.SetEnvPrefix("FETCHURL")
	viper.AutomaticEnv()
	mustBindEnv("upstream", "FETCHURL_UPSTREAM")

	t.Setenv("FETCHURL_UPSTREAM", "http://a:8080/api/fetchurl,http://b:8080/api/fetchurl")
	got := configUpstreams()
	want := []string{"http://a:8080/api/fetchurl", "http://b:8080/api/fetchurl"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("comma env: got %#v, want %#v", got, want)
	}

	t.Setenv("FETCHURL_UPSTREAM", "http://a:8080/api/fetchurl, http://b:8080/api/fetchurl")
	got = configUpstreams()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("comma-space env: got %#v, want %#v", got, want)
	}

	t.Setenv("FETCHURL_UPSTREAM", "http://a:8080/api/fetchurl http://b:8080/api/fetchurl")
	got = configUpstreams()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("space env: got %#v, want %#v", got, want)
	}

	t.Setenv("FETCHURL_UPSTREAM", "")
	if got = configUpstreams(); got != nil {
		t.Fatalf("empty env: got %#v, want nil", got)
	}
}

func TestServerConfigMinFreeOverridesMaxCache(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	const defaultMax = int64(1024 * 1024 * 1024)

	viper.Set("max-cache-size", defaultMax)
	viper.Set("min-free-space", int64(0))
	viper.Set("port", 8080)
	viper.Set("cache-dir", "./cache")
	viper.Set("eviction-interval", "1m")
	viper.Set("eviction-strategy", "lru")

	cfg := serverConfigFromViper()
	if cfg.MaxCacheSize != defaultMax {
		t.Fatalf("without min-free: MaxCacheSize = %d, want default %d", cfg.MaxCacheSize, defaultMax)
	}
	if cfg.MinFreeSpace != 0 {
		t.Fatalf("without min-free: MinFreeSpace = %d, want 0", cfg.MinFreeSpace)
	}

	const minFree = int64(5 * 1024 * 1024 * 1024)
	viper.Set("min-free-space", minFree)
	// Leave max-cache-size at the default; override must zero it so NewServer
	// does not install both policies.
	cfg = serverConfigFromViper()
	if cfg.MinFreeSpace != minFree {
		t.Fatalf("with min-free: MinFreeSpace = %d, want %d", cfg.MinFreeSpace, minFree)
	}
	if cfg.MaxCacheSize != 0 {
		t.Fatalf("with min-free: MaxCacheSize = %d, want 0 (overridden)", cfg.MaxCacheSize)
	}
}
