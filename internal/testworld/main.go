package testworld

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fakessh"
	"github.com/victorarias/attn/internal/stepclock"
	"github.com/victorarias/attn/internal/toolhome"
)

var processDir string

func shellWithNoProfile() string {
	return filepath.Join(processDir, "login-shell")
}

func Main(m *testing.M, env ...string) int {
	fakessh.Main()
	stepclock.Process()
	dir, err := os.MkdirTemp("", "attn-test-process-*")
	if err != nil {
		panic("testworld.Main: " + err.Error())
	}
	defer os.RemoveAll(dir)
	processDir = dir
	if err := os.WriteFile(shellWithNoProfile(), []byte("#!/bin/sh\n[ \"$1\" = -l ] && shift\nexec /bin/sh \"$@\"\n"), 0o755); err != nil {
		panic("testworld.Main: " + err.Error())
	}
	config.ScopeTestEnvironment(filepath.Join(dir, "data"))
	toolHome := filepath.Join(dir, "toolhome")
	_ = os.Setenv(toolhome.EnvVar, toolHome)
	_ = os.Setenv("CODEX_HOME", filepath.Join(toolHome, ".codex"))
	for _, pair := range env {
		key, value, _ := strings.Cut(pair, "=")
		_ = os.Setenv(key, value)
	}
	return m.Run()
}
