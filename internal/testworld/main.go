package testworld

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fakessh"
	"github.com/victorarias/attn/internal/script"
	"github.com/victorarias/attn/internal/stepclock"
	"github.com/victorarias/attn/internal/toolhome"
)

var processDir string

func shellWithNoProfile() string {
	return filepath.Join(processDir, "login-shell")
}

// serveScript makes the test binary the child that script.Run starts.
func serveScript() {
	if len(os.Args) < 2 || os.Args[1] != script.Command {
		return
	}
	if err := script.Serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func Main(m *testing.M, env ...string) int {
	fakessh.Main()
	serveScript()
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
	if err := os.MkdirAll(toolHome, 0o700); err != nil {
		panic("testworld.Main: " + err.Error())
	}
	_ = os.Setenv(toolhome.EnvVar, toolHome)
	_ = os.Setenv("CODEX_HOME", filepath.Join(toolHome, ".codex"))
	_ = os.Unsetenv("CLAUDE_CONFIG_DIR")
	for _, pair := range env {
		key, value, _ := strings.Cut(pair, "=")
		_ = os.Setenv(key, value)
	}
	buildinfo.SnapshotFormat = attnBinarySnapshotFormat()
	return m.Run()
}
