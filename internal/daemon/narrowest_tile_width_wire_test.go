package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheNarrowestTileWidthDefaultsPersistsAndRefusesWhatItCannotHonour(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	requireCols := func(when string, settings map[string]any, cols string) {
		t.Helper()
		if settings["narrowest_tile_cols"] != cols {
			t.Fatalf("%s: narrowest_tile_cols = %v, want %s", when, settings["narrowest_tile_cols"], cols)
		}
	}
	set := func(key, value string) protocol.SettingsUpdatedMessage {
		requestID := key + "=" + value
		return testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: key, Value: value, RequestID: protocol.Ptr(requestID)},
			protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == requestID })
	}
	requireCols("a fresh daemon", app.Initial.Settings, "66")

	for _, value := range []string{"wide", "0", "-3", "6.5"} {
		answer := set("narrowest_tile_cols", value)
		if protocol.Deref(answer.Success) {
			t.Fatalf("narrowest_tile_cols = %q was accepted", value)
		}
		if refusal := protocol.Deref(answer.Error); !strings.Contains(refusal, "narrowest_tile_cols") || !strings.Contains(refusal, value) {
			t.Fatalf("the refusal of %q must name the key and the value: %q", value, refusal)
		}
	}
	requireCols("after the refusals", w.App().Initial.Settings, "66")

	if answer := set("narrowest_tile_cols", "86"); !protocol.Deref(answer.Success) {
		t.Fatalf("set narrowest_tile_cols = 86: %s", protocol.Deref(answer.Error))
	}
	requireCols("after setting it", w.App().Initial.Settings, "86")
	w.restart()
	requireCols("after a restart", w.App().Initial.Settings, "86")

	app = w.App()
	if answer := set("narrowest_tile_cols", ""); !protocol.Deref(answer.Success) {
		t.Fatalf("clearing narrowest_tile_cols: %s", protocol.Deref(answer.Error))
	}
	requireCols("after clearing the width", w.App().Initial.Settings, "66")
}
