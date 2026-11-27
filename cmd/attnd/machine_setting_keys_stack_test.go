package main_test

import (
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnOutpostReadsTheHomesLocalSettingsUnderTheirLocalKeys(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	remote := outpost.App()
	pair.Connect()
	const key = "new_session_yolo_local"
	requireStdout(t, home.Attn("settings", "set", key, "true"), "true")
	testworld.Await(remote, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		for key, value := range m.Settings {
			if strings.HasPrefix(key, "new_session_yolo_") && value == "true" {
				return true
			}
		}
		return false
	})
	got := outpost.Attn("settings", "get", key)
	if got.Code != 0 || strings.TrimSpace(got.Stdout) != "true" {
		t.Errorf("outpost settings get %s = %+v, want true", key, got)
	}
	var list protocol.SettingsListResult
	outpost.Attn("settings", "list", "--json").JSON(t, &list)
	for _, entry := range list.Entries {
		if entry.Key == key && protocol.Deref(entry.Value) == "true" {
			return
		}
	}
	t.Errorf("outpost settings list is missing %s=true", key)
}

func TestAnOutpostThatLeavesKeepsItsOwnLocalSettingsAcrossARestart(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	const oldKey = "new_session_yolo_local"
	const newKey = "code_review_setup_offer_local_/work/review"
	requireStdout(t, outpost.Attn("settings", "set", oldKey, "true"), "true")
	pair.Connect()
	mark := pair.App.EventCount()
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "false"), "false")
	testworld.AwaitSince(pair.App, mark, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		return len(m.Outposts) == 1 && m.Outposts[0].State == protocol.OutpostStateLost
	})
	requireStdout(t, outpost.Attn("enrollment", "leave"), "no longer enrolled")
	check := func(phase, key, want string) {
		t.Helper()
		got := outpost.Attn("settings", "get", key)
		if got.Code != 0 || strings.TrimSpace(got.Stdout) != want {
			t.Errorf("%s: settings get %s = %+v, want %q", phase, key, got, want)
		}
	}
	check("after leave", oldKey, "true")
	requireStdout(t, outpost.Attn("settings", "set", newKey, "off"), "off")
	check("before restart", newKey, "off")
	own := outpost.App()
	if err := os.MkdirAll(outpost.Path("work"), 0o755); err != nil {
		t.Fatal(err)
	}
	spawned := testworld.Request(own, protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: "after-leave", Agent: protocol.AgentShellValue, Cwd: outpost.Path("work"), ProfileID: own.SelectedProfile(), Placement: &protocol.SessionPlacement{}, Cols: 80, Rows: 24}, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == "after-leave" })
	if !spawned.Success {
		t.Fatalf("spawn after leave: %s", protocol.Deref(spawned.Error))
	}
	outpost.Stop()
	outpost.Start()
	check("after restart", oldKey, "true")
	check("after restart", newKey, "off")
	found := false
	for _, session := range outpost.App().Initial.Sessions {
		if session.ID != "after-leave" {
			continue
		}
		found = true
		if session.Host != nil {
			t.Errorf("a session started after leave reads as on %q after a restart, want this machine", *session.Host)
		}
	}
	if !found {
		t.Error("the session started after leave is gone after a restart")
	}
}
