package daemon_test

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestASettingIsAcknowledgedOnlyOnceItPersistsAndRefusalsLeaveItUnchanged(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	capturePath := filepath.Join(w.Dir, "model-captures")
	for key, want := range map[string]string{
		"model_capture.enabled":          "false",
		"model_capture.interval_seconds": "10",
		"model_capture.max_gb":           "5",
		"model_capture.path":             capturePath,
	} {
		if got := app.Initial.Settings[key]; got != want {
			t.Errorf("the app first sees %s = %q, want %q", key, got, want)
		}
	}

	for _, refused := range []struct{ key, value string }{
		{"model_capture.interval_seconds", "4"},
		{"model_capture.max_gb", "101"},
		{"model_capture.path", "/tmp/elsewhere"},
		{"default_context_window_cap_codex", "1"},
	} {
		ack := gardenAdvisorSetSetting(app, refused.key, refused.value)
		if protocol.Deref(ack.Success) || protocol.Deref(ack.Error) == "" || protocol.Deref(ack.ChangedKey) != refused.key {
			t.Errorf("set %s = %s acknowledged %+v, want a failure naming its reason", refused.key, refused.value, ack)
		}
		if got, was := w.App().Initial.Settings[refused.key], app.Initial.Settings[refused.key]; got != was {
			t.Errorf("the refused %s = %s changed the setting from %q to %q", refused.key, refused.value, was, got)
		}
	}

	for key, value := range map[string]string{
		"model_capture.enabled":            "true",
		"default_context_window_cap_codex": "128000",
	} {
		if ack := gardenAdvisorSetSetting(app, key, value); !protocol.Deref(ack.Success) || protocol.Deref(ack.ChangedKey) != key {
			t.Errorf("set %s = %s acknowledged %+v, want success", key, value, ack)
		}
		if got := w.App().Initial.Settings[key]; got != value {
			t.Errorf("once set %s = %s was acknowledged, a new app reads %q", key, value, got)
		}
	}

	w.restart()
	for key, want := range map[string]string{
		"model_capture.enabled":            "true",
		"model_capture.interval_seconds":   "10",
		"model_capture.max_gb":             "5",
		"model_capture.path":               capturePath,
		"default_context_window_cap_codex": "128000",
	} {
		if got := w.App().Initial.Settings[key]; got != want {
			t.Errorf("after a restart %s = %q, want %q", key, got, want)
		}
	}
}
