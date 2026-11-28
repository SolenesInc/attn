package main_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/procwait"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnOutpostStopsAViewsStartedCommandWhenTheHomeIsGone(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t)
	folder := filepath.Join(outpost.Path("work"), "main")
	if err := os.MkdirAll(filepath.Join(folder, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	view := filepath.Join(folder, "view.md")
	if err := os.WriteFile(view, []byte(":::data run=\"scripts/status.sh\"\n:::\n\n:::status state\n:::\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "scripts", "status.sh"), []byte("#!/bin/sh\necho $$ > started\nexec sleep 3600\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(folder, "started"), 0o644); err != nil {
		t.Fatal(err)
	}
	outpost.Vars = append(outpost.Vars, "SHELL=/bin/bash")
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	remote := outpost.App()
	pair.Connect()
	copied := remote.EventCount()
	requireStdout(t, home.Attn("settings", "set", "views_enabled", "true"), "true")
	testworld.AwaitSince(remote, copied, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return protocol.Deref(m.ChangedKey) == "views_enabled"
	})

	app := pair.App
	shell := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), outpost.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	app.TypeLine(shell, "attn open "+view+" && printf '__opened_%s\\n' yes")
	app.AwaitScreen(shell, "__opened_yes")
	tile := testworld.Await(app, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
		return protocol.Deref(m.Host) == pair.Host && m.Path == view && m.View != nil && len(m.View.Commands) == 1
	})
	app.Send(protocol.ViewStartMessage{Cmd: protocol.CmdViewStart, DesktopID: tile.DesktopID, TileID: tile.TileID})
	started, err := os.ReadFile(filepath.Join(folder, "started"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(started)))
	if err != nil {
		t.Fatalf("the command wrote %q, want its pid", started)
	}

	pair.Disconnect()
	outpost.FirePending(bridge.LostTimer)
	if exited, err := procwait.Exited(pid, beforeTheRunsTimeLimit); err != nil || !exited {
		t.Fatalf("the started command %d of a view the home showed still runs %s after the home's bridge dropped: %v", pid, beforeTheRunsTimeLimit, err)
	}
}

// beforeTheRunsTimeLimit is half the 60 s a view's run may take before attn
// stops it, so only the outpost letting go of the view can end the command.
const beforeTheRunsTimeLimit = 30 * time.Second
