package main_test

import (
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/testworld"
	"os"
	"path/filepath"
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
	home.UseSharedHostBinary(filepath.Join(home.Dir, "no-host-yet"))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	requireStdout(t, home.Attn("settings", "set", "narrowest_tile_cols", "86"), "86")
	remote := outpost.App()
	pair.Connect()
	testworld.Await(remote, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["narrowest_tile_cols"] == "86" })
	for _, feature := range setting.Features() {
		value := "true"
		if feature.Default == setting.On {
			value = "false"
		}
		requireStdout(t, home.Attn("settings", "set", feature.Key, value), value)
		testworld.Await(remote, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings[feature.Key] == value })
		requireStdout(t, outpost.Attn("settings", "get", feature.Key), value)
	}
}

func setSharedHostAtTheHomeAndAwaitOutpost(t *testing.T, home *testworld.Stack, remote *testworld.Peer, value, enabled, active string) {
	t.Helper()
	mark := remote.EventCount()
	requireStdout(t, home.Attn("settings", "set", "pty_shared_host_enabled", value))
	testworld.AwaitSince(remote, mark, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return protocol.Deref(m.ChangedKey) == "pty_shared_host_active" && m.Settings["pty_shared_host_enabled"] == enabled && m.Settings["pty_shared_host_active"] == active
	})
}

func TestAnOutpostsNewTerminalsFollowTheHomesSharedHostSetting(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	home.UseSharedHostBinary(filepath.Join(home.Dir, "no-host-yet"))
	outpost.UseSharedHostBinary(testworld.SharedHostBinary(t))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	remote := outpost.App()
	pair.Connect()
	setSharedHostAtTheHomeAndAwaitOutpost(t, home, remote, "false", "false", "false")
	outpost.Stop()
	outpost.Start()
	remote = outpost.App()
	if active := remote.Initial.Settings["pty_shared_host_active"]; active != "false" {
		t.Fatalf("after a restart the outpost reports pty_shared_host_active = %v, though the home turned the host off", active)
	}
	setSharedHostAtTheHomeAndAwaitOutpost(t, home, remote, "", "true", "true")
}

func TestAnOutpostWithNoHostFollowsTheSharedHostSettingQuietly(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	home.UseSharedHostBinary(filepath.Join(home.Dir, "no-host-yet"))
	outpost.UseSharedHostBinary(testworld.SharedHostBinary(t))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	requireStdout(t, outpost.Attn("settings", "set", "pty_shared_host_enabled", "true"), "true")
	pair.Connect()
	outpost.Stop()
	if err := os.RemoveAll(filepath.Join(outpost.Dir, "pty-hosts")); err != nil {
		t.Fatal(err)
	}
	outpost.UseSharedHostBinary(filepath.Join(outpost.Dir, "no-host-yet"))
	outpost.Vars = append(outpost.Vars, "ATTN_PTY_SKIP_STARTUP_PROBE=false")
	outpost.Start()
	remote := outpost.App()
	if active := remote.Initial.Settings["pty_shared_host_active"]; active != "false" {
		t.Fatalf("an outpost with no host reports pty_shared_host_active = %v", active)
	}
	for _, warning := range remote.Initial.Warnings {
		if strings.Contains(warning.Message, "Shared PTY host") {
			t.Fatalf("the outpost warned about a value it did not choose: %s", warning.Message)
		}
	}
	setSharedHostAtTheHomeAndAwaitOutpost(t, home, remote, "false", "false", "false")
	setSharedHostAtTheHomeAndAwaitOutpost(t, home, remote, "", "true", "false")
}

func TestAnOutpostCannotCutItsOwnBridgeAndAnUnchangedHomeValueKeepsItUp(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t, testworld.WithoutAgentKit())
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	requireStdout(t, home.Attn("doc", "define", "tests/outpost-settings", "items"), "items")
	watch := outpost.Launch(testworld.Invocation{Args: []string{"doc", "watch", "tests/outpost-settings", "items", "--json"}})
	watch.AwaitStdout(`"documents": []`)
	for _, value := range []string{"true", "false"} {
		got := outpost.Attn("settings", "set", "outposts_enabled", value)
		if got.Code != 1 || !strings.Contains(got.Stderr, "can only change at the home") {
			t.Fatalf("set %s on the outpost = %+v, want a refusal naming the home", value, got)
		}
		requireStdout(t, home.Attn("settings", "get", "outposts_enabled"), "true")
	}
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "true"), "true")
	requireStdout(t, home.Attn("doc", "put", "tests/outpost-settings", "items", "first", `{"title":"still connected"}`))
	watch.AwaitStdout("still connected")
	requireStdout(t, outpost.Attn("settings", "get", "theme"))
	t.Log("the watch across the bridge survived the refusals and the unchanged home value")
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
