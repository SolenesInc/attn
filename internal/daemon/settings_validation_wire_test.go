package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestTheDaemonAcceptsOnlyValidSettingValues(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	for _, tc := range []struct {
		key, value string
		accepted   bool
	}{
		{"projects_directory", t.TempDir(), true},
		{"projects_directory", "", false},
		{"projects_directory", "relative/path", false},
		{"new_session_agent", "codex", true},
		{"new_session_agent", "claude", true},
		{"new_session_agent", "copilot", true},
		{"new_session_agent", "", true},
		{"new_session_agent", "pi", false},
		{"new_session_agent", "gpt", false},
		{"claude_executable", "", true},
		{"codex_executable", "", true},
		{"copilot_executable", "", true},
		{"claude_executable", "not-a-real-binary-123", false},
		{"reviewer_model", "", true},
		{"reviewer_model", "claude-sonnet-4-6", true},
		{"gardenScale", "1.2", true},
		{"gardenScale", "", true},
		{"gardenScale", "3.0", false},
		{"gardenScale", "big", false},
		{"ticketBoardScale", "1.2", false},
		{"tailscale_enabled", "false", true},
		{"tailscale_enabled", "maybe", false},
		{"queue_crew_enabled", "true", true},
		{"queue_crew_enabled", "maybe", false},
		{"keybindings_config", "", true},
		{"keybindings_config", `{"version":1,"overrides":{"session.new":{"key":"m","meta":true}}}`, true},
		{"keybindings_config", "{not json", false},
		{"sidebar_harness_logos_enabled", "false", true},
		{"sidebar_harness_logos_enabled", "sometimes", false},
		{"unknown_setting", "value", false},
		{"chief_context_window_cap", "", true},
		{"chief_context_window_cap", "128000", true},
		{"chief_context_window_cap", "5000", false},
		{"chief_context_window_cap", "9000000", false},
		{"chief_context_window_cap", "lots", false},
		{"default_context_window_cap_claude", "", true},
		{"default_context_window_cap_claude", "800000", true},
		{"default_context_window_cap_codex", "5000", false},
		{"default_context_window_cap_claude", "lots", false},
		{"headless_context_window_cap", "", true},
		{"headless_context_window_cap", "200000", true},
		{"headless_context_window_cap", "1", false},
		{"chief_effort_claude", "high", true},
		{"chief_effort_claude", "", true},
		{"default_model_claude", "opus", true},
		{"default_model_claude", "", true},
		{"default_effort_claude", "high", true},
		{"default_effort_claude", "", true},
		{"new_session_destination_local_/Users/v/projects/attn", "new_worktree", true},
		{"new_session_destination_endpoint_ep-1_/srv/projects/attn", "main_repo", true},
		{"new_session_destination_local_/Users/v/projects/attn", "", true},
		{"new_session_destination_local_/Users/v/projects/attn", "somewhere_else", false},
	} {
		ack := gardenAdvisorSetSetting(app, tc.key, tc.value)
		if got := protocol.Deref(ack.Success); got != tc.accepted {
			t.Errorf("set %s = %q was accepted=%v (%s), want accepted=%v", tc.key, tc.value, got, protocol.Deref(ack.Error), tc.accepted)
		}
	}
}
