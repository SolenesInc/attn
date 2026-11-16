package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestColorSettingsAcceptKnownValuesAndRefuseWithTheVocabulary(t *testing.T) {
	app := newWorld(t).App()
	const override = `{"dark":{"ui":{"bg_app":"#24273a","bg_overlay":"#00000080"},"terminal":{"bg":"#24273a","bright_black":"#7f849c"}},"light":{"terminal":{"fg":"#4c4f69"}}}`

	accepted := []struct{ key, value string }{
		{"color_scheme", "attn"},
		{"color_overrides", override},
	}
	for _, set := range accepted {
		result := gardenAdvisorSetSetting(app, set.key, set.value)
		if !protocol.Deref(result.Success) {
			t.Fatalf("set %s = %s refused: %s", set.key, set.value, protocol.Deref(result.Error))
		}
		if got := settingsBroadcastFor(app, set.key).Settings[set.key]; got != set.value {
			t.Errorf("%s stored %v, want %s", set.key, got, set.value)
		}
	}

	refused := []struct {
		key, value string
		wants      []string
	}{
		{"color_scheme", "nord", []string{`color_scheme: "nord" is not a color scheme; schemes: attn, catppuccin, catppuccin-frappe`}},
		{"color_overrides", `{"dark":{"ui":{"bg_ap":"#24273a"}}}`, []string{"color_overrides: dark.ui.bg_ap is not a color name; ui names: ", "bg_app", "syntax_keyword"}},
		{"color_overrides", `{"light":{"terminal":{"brite_black":"#000000"}}}`, []string{"color_overrides: light.terminal.brite_black is not a color name; terminal names: ", "bright_black", "seed"}},
		{"color_overrides", `{"dark":{"terminal":{"bg":"red"}}}`, []string{`color_overrides: dark.terminal.bg: "red" is not a terminal color; use #rrggbb`}},
		{"color_overrides", `{"dark":{"terminal":{"bg":"#24273a80"}}}`, []string{`color_overrides: dark.terminal.bg: "#24273a80" is not a terminal color; use #rrggbb`}},
		{"color_overrides", `{"dark":{"ui":{"bg_app":"#fff"}}}`, []string{`color_overrides: dark.ui.bg_app: "#fff" is not a ui color; use #rrggbb or #rrggbbaa`}},
		{"color_overrides", `{"dusk":{}}`, []string{"color_overrides: dusk is not an appearance; use dark or light"}},
		{"color_overrides", `{"dark":{"chrome":{}}}`, []string{"color_overrides: dark.chrome is not a group; use ui or terminal"}},
	}
	for _, set := range refused {
		result := gardenAdvisorSetSetting(app, set.key, set.value)
		if protocol.Deref(result.Success) {
			t.Errorf("set %s = %s accepted", set.key, set.value)
			continue
		}
		message := protocol.Deref(result.Error)
		for _, want := range set.wants {
			if !strings.Contains(message, want) {
				t.Errorf("set %s = %s refused with %q, want it to contain %q", set.key, set.value, message, want)
			}
		}
	}

	for _, key := range []string{"color_scheme", "color_overrides"} {
		result := gardenAdvisorSetSetting(app, key, "")
		if !protocol.Deref(result.Success) {
			t.Fatalf("clearing %s refused: %s", key, protocol.Deref(result.Error))
		}
		if got, _ := settingsBroadcastFor(app, key).Settings[key].(string); got != "" {
			t.Errorf("%s after clearing is %q, want empty", key, got)
		}
	}
}

func settingsBroadcastFor(app *testworld.Peer, key string) protocol.SettingsUpdatedMessage {
	return testworld.Await(app, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return protocol.Deref(m.ChangedKey) == key && m.Settings != nil
	})
}
