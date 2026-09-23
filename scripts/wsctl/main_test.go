package main

import (
	"testing"

	"github.com/victorarias/attn/internal/config"
)

func TestResolveWSURL(t *testing.T) {
	cases := []struct {
		name        string
		explicitURL string
		instance    string
		want        string
	}{
		{"explicit URL wins over instance", "ws://localhost:9849/ws", "mdclick1", "ws://localhost:9849/ws"},
		{"no env falls back to dev", "", "", defaultWSURL},
		{"blank instance falls back to dev", "", "   ", defaultWSURL},
		{"default instance falls back to dev, never prod", "", "default", defaultWSURL},
		{"dev instance resolves to dev port", "", "dev", "ws://localhost:29849/ws"},
		{"named instance resolves to its derived port", "", "mdclick1", "ws://localhost:" + config.WSPortForInstance("mdclick1") + "/ws"},
		{"instance name is case-insensitive", "", "MDCLICK1", "ws://localhost:" + config.WSPortForInstance("mdclick1") + "/ws"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveWSURL(tc.explicitURL, tc.instance); got != tc.want {
				t.Fatalf("resolveWSURL(%q, %q) = %q, want %q", tc.explicitURL, tc.instance, got, tc.want)
			}
		})
	}
}

func TestResolveWSURLNeverImplicitProd(t *testing.T) {
	prod := "ws://localhost:" + config.WSPortForInstance("") + "/ws"
	for _, instance := range []string{"", "default", "DEFAULT", " ", "dev", "mdclick1"} {
		if got := resolveWSURL("", instance); got == prod {
			t.Fatalf("resolveWSURL(\"\", %q) resolved to prod %q; prod must require an explicit ATTN_WS_URL", instance, got)
		}
	}
}
