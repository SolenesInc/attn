package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheSidebarWidthDefaultsPersistsAndRefusesWhatIsOutOfBounds(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	requireWidth := func(when string, settings map[string]any, want string) {
		t.Helper()
		if settings["sidebar_width"] != want {
			t.Fatalf("%s: sidebar_width = %v, want %s", when, settings["sidebar_width"], want)
		}
	}
	set := func(value string) protocol.SettingsUpdatedMessage {
		requestID := "sidebar_width=" + value
		return testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "sidebar_width", Value: value, RequestID: protocol.Ptr(requestID)},
			protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == requestID })
	}
	requireWidth("a fresh daemon", app.Initial.Settings, "240")

	for _, value := range []string{"199", "601", "abc"} {
		answer := set(value)
		if protocol.Deref(answer.Success) {
			t.Fatalf("sidebar_width = %q was accepted", value)
		}
		refusal := protocol.Deref(answer.Error)
		for _, named := range []string{"sidebar_width", "200", "600", value} {
			if !strings.Contains(refusal, named) {
				t.Fatalf("the refusal of %q must name %q: %q", value, named, refusal)
			}
		}
	}
	requireWidth("after the refusals", w.App().Initial.Settings, "240")

	for _, value := range []string{"200", "600"} {
		if answer := set(value); !protocol.Deref(answer.Success) {
			t.Fatalf("set sidebar_width = %s: %s", value, protocol.Deref(answer.Error))
		}
		requireWidth("after setting "+value, w.App().Initial.Settings, value)
	}
	w.restart()
	requireWidth("after a restart", w.App().Initial.Settings, "600")

	app = w.App()
	if answer := set(""); !protocol.Deref(answer.Success) {
		t.Fatalf("clearing sidebar_width: %s", protocol.Deref(answer.Error))
	}
	requireWidth("after clearing", w.App().Initial.Settings, "240")
}
