package daemon_test

import (
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASharedCodexThatExitsBeforeItsPlacementFailsLetsItsAppServerStop(t *testing.T) {
	w := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	started := w.PauseAt(pausepoint.AgentTerminalStarted)
	released := w.PauseAt(pausepoint.CodexViewReleaseChecked)
	w.Start()
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	notes, desktop, anchor := w.RequestSpawn(app, fakeagent.Harness("shell"), w.Path("notes"))
	if !notes.Success {
		t.Fatalf("spawn the shell beside which Codex launches: %s", protocol.Deref(notes.Error))
	}
	cwd := w.Path("shop")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	w.SetCodexFolderTrust(cwd, "trusted")
	shop := protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: protocol.SessionID(uuid.NewString()), Agent: string(fakeagent.Codex),
		Cwd: cwd, ProfileID: app.SelectedProfile(), Cols: 100, Rows: 30,
		Placement: &protocol.SessionPlacement{DesktopID: protocol.Ptr(desktop), AnchorPaneID: protocol.Ptr(anchor)},
	}
	launched := w.NextLaunch()
	app.Send(shop)
	started.Await()
	launched().Exit(1)
	released.Await()
	released.Release()
	if closed := closeTileFromApp(w.App(), desktop, anchor); !closed.Success {
		t.Fatalf("close the tile Codex launches beside: %s", protocol.Deref(closed.Error))
	}
	started.Release()
	if placed := testworld.Await(app, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == shop.ID }); placed.Success {
		t.Fatal("a shared Codex launch beside a tile that closed under it was placed")
	}
	w.CodexServer().AwaitStopped()
}
