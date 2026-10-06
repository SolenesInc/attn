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
	terminal := reopened.Terminal(shell)
	reopened.Send(protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal), AttachPolicy: protocol.Ptr(protocol.AttachPolicyRelaunchRestore)})
	snapshot.Await()
	app.TypeLine(shell, `printf 'during-%02d\n' 1 2 3`)
	app.AwaitScreen(shell, "during-03")
	snapshot.Release()
	if result := testworld.Await(reopened, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal }); !result.Success {
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
	terminal := p.Terminal(session)
	result := testworld.Request(p, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal), AttachPolicy: protocol.Ptr(policy)},
		protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal })
	if !result.Success {
		t.Fatalf("attach %s refused: %s", session, protocol.Deref(result.Error))
	}
}

func TestAnObserverSeededFromAScreenSnapshotMissesNoOutput(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	sequenced := s.PauseAt(pausepoint.PtyOutputSequenced)
	s.Start()
	app := s.App()
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
	sequenced.Await()
	attachWithPolicy(t, app, shell, protocol.AttachPolicyFreshSpawn)
	observer := s.App()
	attachWithPolicy(t, observer, shell, protocol.AttachPolicyFreshSpawn)
	terminal := observer.Terminal(shell)
	testworld.Request(observer, protocol.GetScreenSnapshotMessage{Cmd: protocol.CmdGetScreenSnapshot, ID: protocol.TerminalID(terminal)},
		protocol.EventGetScreenSnapshotResult, func(r protocol.GetScreenSnapshotResultMessage) bool { return string(r.ID) == terminal && r.Success })
	sequenced.Release()
	app.TypeLine(shell, `printf 'line-%02d\n' 1 2 3`)
	app.AwaitScreen(shell, "line-03")
	observer.AwaitScreen(shell, "line-03")

	upToMarker := func(screen []byte) []byte {
		return screen[:bytes.LastIndex(screen, []byte("line-03"))]
	}
	if stream, seeded := upToMarker(app.Screen(shell)), upToMarker(observer.Screen(shell)); !bytes.HasSuffix(seeded, stream) {
		t.Errorf("the observer seeded from a screen snapshot shows %q, want it to end with everything the shell printed since the snapshot: %q", seeded, stream)
	}
}
