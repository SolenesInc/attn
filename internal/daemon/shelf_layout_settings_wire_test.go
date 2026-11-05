package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheShelfLayoutSettingsDefaultPersistAndRefuseWhatTheyCannotHonour(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	requireSettings := func(when string, settings map[string]any, enabled, cols string) {
		t.Helper()
		if settings["shelf_layout_enabled"] != enabled || settings["narrowest_tile_cols"] != cols {
			t.Fatalf("%s: shelf_layout_enabled = %v, narrowest_tile_cols = %v, want %s and %s", when, settings["shelf_layout_enabled"], settings["narrowest_tile_cols"], enabled, cols)
		}
	}
	set := func(key, value string) protocol.SettingsUpdatedMessage {
		requestID := key + "=" + value
		return testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: key, Value: value, RequestID: protocol.Ptr(requestID)},
			protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == requestID })
	}
	requireSettings("a fresh daemon", app.Initial.Settings, "false", "66")

	for _, value := range []string{"wide", "0", "-3", "6.5"} {
		answer := set("narrowest_tile_cols", value)
		if protocol.Deref(answer.Success) {
			t.Fatalf("narrowest_tile_cols = %q was accepted", value)
		}
		if refusal := protocol.Deref(answer.Error); !strings.Contains(refusal, "narrowest_tile_cols") || !strings.Contains(refusal, value) {
			t.Fatalf("the refusal of %q must name the key and the value: %q", value, refusal)
		}
	}
	if answer := set("shelf_layout_enabled", "maybe"); protocol.Deref(answer.Success) {
		t.Fatal("shelf_layout_enabled = maybe was accepted")
	}
	requireSettings("after the refusals", w.App().Initial.Settings, "false", "66")

	for _, change := range []struct{ key, value string }{{"shelf_layout_enabled", "true"}, {"narrowest_tile_cols", "86"}} {
		if answer := set(change.key, change.value); !protocol.Deref(answer.Success) {
			t.Fatalf("set %s = %s: %s", change.key, change.value, protocol.Deref(answer.Error))
		}
	}
	requireSettings("after setting both", w.App().Initial.Settings, "true", "86")
	w.restart()
	requireSettings("after a restart", w.App().Initial.Settings, "true", "86")

	app = w.App()
	if answer := set("narrowest_tile_cols", ""); !protocol.Deref(answer.Success) {
		t.Fatalf("clearing narrowest_tile_cols: %s", protocol.Deref(answer.Error))
	}
	requireSettings("after clearing the width", w.App().Initial.Settings, "true", "66")
}
