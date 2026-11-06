package main_test

import (
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/testworld"
	"slices"
	"strings"
	"testing"
)

func TestOutpostSettingsCopyRoutePersistAndRestoreLocalValues(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	for _, change := range []struct {
		s          *testworld.Stack
		key, value string
	}{
		{outpost, "theme", "light"}, {outpost, "default_model_codex", "outpost-only"}, {outpost, "default_model_claude", "standalone"},
		{outpost, "default_context_window_cap_codex", "128000"},
		{home, "theme", "dark"}, {home, "default_model_claude", "home-model"},
		{home, "default_context_window_cap_codex", "256000"},
	} {
		requireStdout(t, change.s.Attn("settings", "set", change.key, change.value), change.value)
	}
	remote := outpost.App()
	pair.Connect()
	await := func(key, value string) {
		t.Helper()
		testworld.Await(remote, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings[key] == value })
	}
	await("theme", "dark")
	requireStdout(t, outpost.Attn("settings", "get", "theme"), "dark")
	requireStdout(t, outpost.Attn("settings", "get", "default_model_claude"), "home-model")
	requireStdout(t, outpost.Attn("settings", "get", "default_context_window_cap_codex"), "256000")
	if got := outpost.Attn("settings", "get", "default_model_codex"); got.Code != 0 || strings.TrimSpace(got.Stdout) != "" {
		t.Fatalf("home's unset family must mask the old local value: %+v", got)
	}
	var entries protocol.SettingsListResult
	outpost.Attn("settings", "list", "--all", "--json").JSON(t, &entries)
	for _, key := range []string{"theme", "default_model_claude"} {
		found := false
		for _, entry := range entries.Entries {
			if entry.Key == key {
				found = true
				if protocol.Deref(entry.ReadOnly) || !protocol.Deref(entry.SetAtHome) {
					t.Fatalf("a home setting on an outpost must say it is set at the home, not read-only: %+v", entry)
				}
			}
		}
		if !found {
			t.Fatalf("missing %s", key)
		}
	}
	requireStdout(t, outpost.Attn("settings", "list", "--all"), "theme", "set at the home")
	requireStdout(t, outpost.Attn("settings", "set", "theme", "system"), "theme = system")
	await("theme", "system")
	requireStdout(t, home.Attn("settings", "get", "theme"), "system")
	remote.Send(protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "default_model_claude", Value: "from-outpost-websocket"})
	await("default_model_claude", "from-outpost-websocket")
	requireStdout(t, home.Attn("settings", "get", "default_model_claude"), "from-outpost-websocket")
	requireStdout(t, outpost.Attn("settings", "set", "worktree_sweep_enabled", "false"), "false")
	if got := home.Attn("settings", "get", "worktree_sweep_enabled"); got.Code != 0 || strings.TrimSpace(got.Stdout) != "true" {
		t.Fatalf("machine write reached the home: %+v", got)
	}
	requireStdout(t, home.Attn("settings", "set", "default_model_claude", ""))
	await("default_model_claude", "")
	requireStdout(t, home.Attn("settings", "set", "auto_settle_enabled", "true"), "true")
	await("auto_settle_enabled", "true")
	home.Stop()
	outpost.Stop()
	outpost.Start()
	for key, value := range map[string]string{"theme": "system", "worktree_sweep_enabled": "false", "auto_settle_enabled": "true"} {
		requireStdout(t, outpost.Attn("settings", "get", key), value)
	}
	result := outpost.Attn("settings", "set", "theme", "dark")
	if result.Code == 0 || !strings.Contains(result.Stderr, "unreachable") {
		t.Fatalf("offline home write: %+v", result)
	}
	requireStdout(t, outpost.Attn("settings", "set", "worktree_sweep_enabled", "true"), "true")
	requireStdout(t, outpost.Attn("enrollment", "leave"), "no longer enrolled")
	for key, value := range map[string]string{"theme": "light", "default_model_codex": "outpost-only", "default_model_claude": "standalone", "worktree_sweep_enabled": "true"} {
		requireStdout(t, outpost.Attn("settings", "get", key), value)
	}
	t.Log("home setting copied and read-only; outpost home write reached home; machine write stayed local; offline restart retained home values; leave restored standalone values including family keys")
}

func TestEveryFeatureFollowsTheHomeOnItsOutposts(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex)), testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	remote := outpost.App()
	pair.Connect()
	var perMachine []*setting.Setting
	for _, feature := range setting.Features() {
		if feature.Class == setting.Machine {
			perMachine = append(perMachine, feature)
			continue
		}
		requireStdout(t, home.Attn("settings", "set", feature.Key, "true"), "true")
		testworld.Await(remote, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings[feature.Key] == "true" })
		requireStdout(t, outpost.Attn("settings", "get", feature.Key), "true")
	}
	var keys []string
	for _, feature := range perMachine {
		keys = append(keys, feature.Key)
	}
	if want := []string{"analytics.enabled", "background_login_env_enabled", "outposts_enabled", "pty_shared_host_enabled", "shelf_layout_enabled"}; !slices.Equal(slices.Sorted(slices.Values(keys)), want) {
		t.Fatalf("features still decided per machine = %v, want only the named exceptions %v", keys, want)
	}
	t.Log("every feature except the named exceptions reads the home's value on an outpost")
}

func TestOutpostEnrollmentAppliesUnsetHomeDefaultsToRunningServices(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	for _, stack := range []*testworld.Stack{home, outpost} {
		stack.Vars = append(stack.Vars, "ATTN_HEADLESS_TASKS=")
	}
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	requireStdout(t, outpost.Attn("settings", "set", "headless_tasks.enabled", "false"), "false")
	if got := outpost.App().Initial.Settings["headless_tasks.enabled"]; got != "false" {
		t.Fatalf("standalone mode = %v, want false", got)
	}
	pair.Connect()
	if got := outpost.App().Initial.Settings["headless_tasks.enabled"]; got != "true" {
		t.Fatalf("enrolled service mode = %v, want home's unset/default true", got)
	}
	home.Stop()
	requireStdout(t, outpost.Attn("enrollment", "leave"), "no longer enrolled")
	if got := outpost.App().Initial.Settings["headless_tasks.enabled"]; got != "false" {
		t.Fatalf("service mode after leave = %v, want restored standalone false", got)
	}
	t.Log("first enrollment applies home defaults to the running headless service; leave restores its standalone mode")
}
