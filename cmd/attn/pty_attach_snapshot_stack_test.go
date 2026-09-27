package main_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestReopeningATerminalWhileItPrintsShowsEveryLineExactlyOnce(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	snapshot := s.PauseAt(pausepoint.PtyAttachSnapshot)
	s.Start()
	app := s.App()
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
	attachWithPolicy(t, app, shell, protocol.AttachPolicyFreshSpawn)
	app.TypeLine(shell, `printf 'before-%02d\n' 1 2 3`)
	app.AwaitScreen(shell, "before-03")

	reopened := s.App()
	reopened.Send(protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: shell, AttachPolicy: protocol.Ptr(protocol.AttachPolicyRelaunchRestore)})
	snapshot.Await()
	app.TypeLine(shell, `printf 'during-%02d\n' 1 2 3`)
	app.AwaitScreen(shell, "during-03")
	snapshot.Release()
	if result := testworld.Await(reopened, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == shell }); !result.Success {
		t.Fatalf("reopening the terminal was refused: %s", protocol.Deref(result.Error))
	}
	app.TypeLine(shell, `printf 'after-%02d\n' 1 2 3`)
	reopened.AwaitScreen(shell, "after-03")

	screen := reopened.Screen(shell)
	var wrong []string
	for _, phase := range []string{"before", "during", "after"} {
		for _, n := range []string{"01", "02", "03"} {
			line := phase + "-" + n
			if count := bytes.Count(screen, []byte(line)); count != 1 {
				wrong = append(wrong, fmt.Sprintf("%s %d times", line, count))
			}
		}
	}
	if len(wrong) > 0 {
		t.Errorf("the reopened terminal shows %s, want each line once; it shows %q", strings.Join(wrong, ", "), screen)
	}
}

func attachWithPolicy(t *testing.T, p *testworld.Peer, session string, policy protocol.AttachPolicy) {
	t.Helper()
	result := testworld.Request(p, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: session, AttachPolicy: protocol.Ptr(policy)},
		protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == session })
	if !result.Success {
		t.Fatalf("attach %s refused: %s", session, protocol.Deref(result.Error))
	}
}

func TestATileSeededFromAScreenSnapshotMissesNoOutput(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	sequenced := s.PauseAt(pausepoint.PtyOutputSequenced)
	s.Start()
	app := s.App()
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
	sequenced.Await()
	attachWithPolicy(t, app, shell, protocol.AttachPolicyFreshSpawn)
	tile := s.App()
	attachWithPolicy(t, tile, shell, protocol.AttachPolicyFreshSpawn)
	testworld.Request(tile, protocol.GetScreenSnapshotMessage{Cmd: protocol.CmdGetScreenSnapshot, ID: shell},
		protocol.EventGetScreenSnapshotResult, func(r protocol.GetScreenSnapshotResultMessage) bool { return r.ID == shell && r.Success })
	sequenced.Release()
	app.TypeLine(shell, `printf 'tile-%02d\n' 1 2 3`)
	app.AwaitScreen(shell, "tile-03")
	tile.AwaitScreen(shell, "tile-03")

	upToMarker := func(screen []byte) []byte {
		return screen[:bytes.LastIndex(screen, []byte("tile-03"))]
	}
	if stream, seeded := upToMarker(app.Screen(shell)), upToMarker(tile.Screen(shell)); !bytes.HasSuffix(seeded, stream) {
		t.Errorf("the tile seeded from a screen snapshot shows %q, want it to end with everything the shell printed since the snapshot: %q", seeded, stream)
	}
}
