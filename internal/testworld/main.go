package testworld

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/toolhome"
)

var processDir string

func Main(m *testing.M, env ...string) int {
	fakeagent.Main()
	dir, err := os.MkdirTemp("", "attn-test-process-*")
	if err != nil {
		panic("testworld.Main: " + err.Error())
	}
	defer os.RemoveAll(dir)
	processDir = dir
	config.ScopeTestEnvironment(filepath.Join(dir, "data"))
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		panic("testworld.Main: " + err.Error())
	}
	if err := os.WriteFile(filepath.Join(bin, "tailscale"), []byte(`#!/bin/sh
case "$*" in
"status --json") printf '%s\n' '{"BackendState":"NoState"}' ;;
"serve status --json") printf '%s\n' '{}' ;;
*) printf 'unexpected tailscale %s\n' "$*" >&2; exit 2 ;;
esac
`), 0o755); err != nil {
		panic("testworld.Main: " + err.Error())
	}
	_ = os.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	toolHome := filepath.Join(dir, "toolhome")
	_ = os.Setenv(toolhome.EnvVar, toolHome)
	_ = os.Setenv("CODEX_HOME", filepath.Join(toolHome, ".codex"))
	for _, pair := range env {
		key, value, _ := strings.Cut(pair, "=")
		_ = os.Setenv(key, value)
	}
	return m.Run()
}
