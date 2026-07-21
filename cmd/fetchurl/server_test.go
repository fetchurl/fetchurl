package main

import (
	"reflect"
	"testing"

	"github.com/spf13/viper"
)

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
