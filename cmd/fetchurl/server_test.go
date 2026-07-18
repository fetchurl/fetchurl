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
