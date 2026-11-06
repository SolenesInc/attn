package main_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/prompttest"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSettingsCLIDiscoversReadsAndSavesDaemonSettings(t *testing.T) {
	s := testworld.NewStack(t)
	s.Start()
	app := s.App()
	unset := s.Attn("settings", "get", "headless_context_window_cap")
	if unset.Code != 0 || strings.TrimSpace(unset.Stdout) != "" {
		t.Fatalf("unset setting: %+v", unset)
	}
	requireStdout(t, s.Attn("settings", "set", "theme", "dark"), "theme = dark")
	testworld.Await(app, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return m.Settings["theme"] == "dark" && protocol.Deref(m.ChangedKey) == "theme"
	})
	requireStdout(t, s.Attn("settings", "get", "theme"), "dark")
	for _, key := range []string{"headless_tasks.enabled", "queue_show_hidden_sessions", "asks_enabled", "callouts_enabled", "quick_capture_enabled"} {
		requireStdout(t, s.Attn("settings", "set", key, "true"), key+" = true")
		requireStdout(t, s.Attn("settings", "get", key), "true")
	}
	requireStdout(t, s.Attn("settings", "set", "capture.shortcut", "Control+Alt+Super+Space"), "capture.shortcut = Control+Alt+Super+Space")
	requireStdout(t, s.Attn("settings", "get", "capture.shortcut"), "Control+Alt+Super+Space")
	for _, tc := range []struct{ key, value, reason string }{
		{"theme", "purple", "invalid theme"},
		{"unknown.key", "value", "unknown.key"},
		{"pty_backend_mode", "worker", "read-only"},
		{"default_model_<harness>", "some-model", "concrete key"},
	} {
		r := s.Attn("settings", "set", tc.key, tc.value)
		if r.Code != 1 || !strings.Contains(r.Stderr, tc.reason) {
			t.Fatalf("refusal for %s: %+v", tc.key, r)
		}
	}
	if r := s.Attn("settings", "get", "unknown.key"); r.Code != 1 || !strings.Contains(r.Stderr, "unknown.key") {
		t.Fatalf("unknown get: %+v", r)
	}
	if r := s.Attn("settings", "set", "theme"); r.Code != 2 {
		t.Fatalf("usage: %+v", r)
	}
	plain := s.Attn("settings", "list")
	requireStdout(t, plain, "home", "theme", "App color theme", "quick_capture_enabled")
	if strings.Contains(plain.Stdout, "pty_backend_mode") {
		t.Fatal("list includes read-only values")
	}
	requireStdout(t, s.Attn("settings", "list", "--all"), "pty_backend_mode", "headless_tasks.enabled.stored")
	var computed protocol.SettingEntry
	s.Attn("settings", "get", "headless_tasks.enabled.stored", "--json").JSON(t, &computed)
	if !protocol.Deref(computed.ReadOnly) || protocol.Deref(computed.Value) != "true" {
		t.Fatalf("computed value: %+v", computed)
	}
	prompttest.Equal(t, "settings-help", map[string]string{"help": s.Attn("settings", "--help").Stdout})
	var listed protocol.SettingsListResult
	s.Attn("settings", "list", "--all", "--json").JSON(t, &listed)
	var registry strings.Builder
	for _, entry := range listed.Entries {
		if !protocol.Deref(entry.Family) && strings.Contains(entry.Key, "_cap_") {
			continue
		}
		if entry.Key == "claude_available" || entry.Key == "codex_available" {
			continue
		}
		fmt.Fprintf(&registry, "%s\t%s\t%t\t%s\n", entry.Key, entry.Scope, protocol.Deref(entry.ReadOnly), entry.Description)
	}
	prompttest.Equal(t, "settings-registry", map[string]string{"registry": registry.String()})
	s.Stop()
	s.Start()
	requireStdout(t, s.Attn("settings", "get", "theme"), "dark")
	requireStdout(t, s.Attn("settings", "get", "quick_capture_enabled"), "true")
	requireStdout(t, s.Attn("settings", "list", "--features"), "quick_capture_enabled", "experimental", "the home", "Now", "Quick Capture")
	requireStdout(t, s.Attn("settings", "set", "worktree_sweep_enabled", "false"), "false")
	requireStdout(t, s.Attn("settings", "list"), "this machine")
	requireStdout(t, s.Attn("settings", "set", "worktree_sweep_enabled", ""))
	requireStdout(t, s.Attn("settings", "get", "worktree_sweep_enabled"), "true")
	requireStdout(t, s.Attn("settings", "set", "new_session_launcher_enabled", "TRUE"), "new_session_launcher_enabled = true")
	if got := s.App().Initial.Settings["new_session_launcher_enabled"]; got != "true" {
		t.Fatalf("the app reads a boolean written as TRUE as %v, want true: the daemon reads it as on", got)
	}
	requireStdout(t, s.Attn("settings", "set", "headless_context_window_cap", "128000"), "128000")
	requireStdout(t, s.Attn("settings", "set", "headless_context_window_cap", ""))
	reset := s.Attn("settings", "get", "headless_context_window_cap")
	if reset.Code != 0 || strings.TrimSpace(reset.Stdout) != "" {
		t.Fatalf("reset setting: %+v", reset)
	}
}

func TestSettingsCLIReportsDefaultOnSettingsAsOnUntilStoredOff(t *testing.T) {
	s := testworld.NewStack(t)
	s.Start()
	keys := []string{
		"auto_approve_enabled", "crew.autosleep_enabled", "crew.heartbeat_enabled", "headless_tasks.enabled",
		"open_sent_files_enabled", "queue_quiet_notifications", "queue_show_hidden_sessions",
		"sidebar_harness_logos_enabled", "worktree_sweep_enabled",
	}
	expect := func(want string) {
		t.Helper()
		var listed protocol.SettingsListResult
		s.Attn("settings", "list", "--json").JSON(t, &listed)
		inList := map[string]string{}
		for _, entry := range listed.Entries {
			inList[entry.Key] = protocol.Deref(entry.Value)
		}
		for _, key := range keys {
			if got := strings.TrimSpace(s.Attn("settings", "get", key).Stdout); got != want {
				t.Errorf("attn settings get %s = %q, want %q", key, got, want)
			}
			if inList[key] != want {
				t.Errorf("attn settings list shows %s = %q, want %q", key, inList[key], want)
			}
		}
	}
	expect("true")
	for _, key := range keys {
		requireStdout(t, s.Attn("settings", "set", key, "false"), key+" = false")
	}
	expect("false")
}
