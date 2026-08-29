package toolhome

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests MUST set this — Dir panics under go test otherwise.
const EnvVar = "ATTN_TOOL_HOME"

// Point ATTN_TOOL_HOME at a temp dir from TestMain or t.Setenv, never redirect
// HOME.
func Dir() (string, error) {
	if override := strings.TrimSpace(os.Getenv(EnvVar)); override != "" {
		return filepath.Clean(override), nil
	}
	if testing.Testing() {
		panic("toolhome: ATTN_TOOL_HOME is not set under go test — tests must never resolve real HOME " +
			"through tool-dotfile paths (~/.claude, ~/.codex, ~/.copilot, ~/.agents). " +
			"Set ATTN_TOOL_HOME to a temp dir (os.Setenv in a package TestMain, or t.Setenv(toolhome.EnvVar, t.TempDir()) per-test). " +
			"Never redirect HOME to work around this.")
	}
	return os.UserHomeDir()
}
