package main

import (
	"testing"

	"github.com/victorarias/attn/internal/config"
)

func TestResolveWSURLNeverImplicitProd(t *testing.T) {
	prod := "ws://localhost:" + config.WSPortForInstance("") + "/ws"
	for _, instance := range []string{"", "default", "DEFAULT", " ", "dev", "mdclick1"} {
		if got := resolveWSURL("", instance); got == prod {
			t.Fatalf("resolveWSURL(\"\", %q) resolved to prod %q; prod must require an explicit ATTN_WS_URL", instance, got)
		}
	}
}
