package daemon

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginCommandEnv_UsesLoginShellEnvironment(t *testing.T) {
	t.Setenv("PATH", "/daemon/bin")

	d := NewForTesting(filepath.Join(t.TempDir(), "plugin-runtime.sock"))
	d.loginShellEnv = []string{
		"PATH=/user/bin:/usr/bin:/bin",
		"USER_ONLY=present",
	}

	env := d.pluginCommandEnv("ATTN_PLUGIN_NAME=test-plugin")
	if got := envValue(env, "PATH"); got != "/user/bin:/usr/bin:/bin" {
		t.Fatalf("PATH=%q, want login-shell PATH", got)
	}
	if got := envValue(env, "USER_ONLY"); got != "present" {
		t.Fatalf("USER_ONLY=%q, want present", got)
	}
	if got := envValue(env, "ATTN_PLUGIN_NAME"); got != "test-plugin" {
		t.Fatalf("ATTN_PLUGIN_NAME=%q, want test-plugin", got)
	}
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}
