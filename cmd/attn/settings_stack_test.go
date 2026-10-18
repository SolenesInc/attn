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
	for _, tc := range []struct{ key, value, reason string }{
		{"theme", "purple", "invalid theme"},
		{"unknown.key", "value", "unknown.key"},
		{"pty_backend_mode", "embedded", "read-only"},
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
	requireStdout(t, plain, "all profiles", "theme", "App color theme", "quick_capture_enabled")
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
		if entry.Key == "claude_available" || entry.Key == "codex_available" || entry.Key == "copilot_available" {
			continue
		}
		fmt.Fprintf(&registry, "%s\t%s\t%t\t%s\n", entry.Key, entry.Scope, protocol.Deref(entry.ReadOnly), entry.Description)
	}
	prompttest.Equal(t, "settings-registry", map[string]string{"registry": registry.String()})
	s.Stop()
	s.Start()
	requireStdout(t, s.Attn("settings", "get", "theme"), "dark")
	requireStdout(t, s.Attn("settings", "get", "quick_capture_enabled"), "true")
	requireStdout(t, s.Attn("settings", "set", "headless_context_window_cap", "128000"), "128000")
	requireStdout(t, s.Attn("settings", "set", "headless_context_window_cap", ""))
	reset := s.Attn("settings", "get", "headless_context_window_cap")
	if reset.Code != 0 || strings.TrimSpace(reset.Stdout) != "" {
		t.Fatalf("reset setting: %+v", reset)
	}
}
