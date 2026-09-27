package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/testworld"
)

func TestADaemonInstalledAsALinuxTreeFindsTheAppRuntimeInItsResources(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	tree := filepath.Join(s.Dir, "install", "attn-lx")
	host := filepath.Join(tree, "resources", "app-runtime", "attn-app-runtime")
	for _, dir := range []string{filepath.Join(tree, "bin"), filepath.Dir(host)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(host, []byte("#!/bin/sh\nexec sleep 86400\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(tree, "bin", "attn")
	if err := os.Link(testworld.AttnBinary(t), installed); err != nil {
		t.Fatal(err)
	}
	s.RunFrom(installed)
	s.Start()

	status := s.Attn("app", "runtime", "status")
	if got := strings.TrimSpace(statusRow(t, status, "binary:")); got != host {
		t.Fatalf("attn app runtime status under %s names binary %q, want the tree's %s\n%s", tree, got, host, status.Stdout)
	}
}
