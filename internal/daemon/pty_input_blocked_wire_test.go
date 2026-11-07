package daemon_test

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func pasteInto(app *testworld.Peer, terminal, probe, text string) protocol.PtyInputProbeResultMessage {
	return testworld.Request(app, protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(terminal), Data: text, Source: protocol.Ptr("user"), ProbeID: protocol.Ptr(probe)},
		protocol.EventPtyInputProbeResult, func(r protocol.PtyInputProbeResultMessage) bool { return r.ProbeID == probe })
}

func spawnProgramNotReadingInput(t *testing.T, w *world, app *testworld.Peer, session string) (release func()) {
	t.Helper()
	gate := w.Path("input-gate")
	if err := syscall.Mkfifo(gate, 0o600); err != nil {
		t.Fatal(err)
	}
	app.TypeLine(session, fmt.Sprintf(`stty -echo; printf 'wait''ing\n'; read _ < %s; awk 'NR != $1 { bad++ } END { print "lines=" NR, "bad=" bad+0 }'`, gate))
	app.AwaitScreen(session, "waiting")
	return func() {
		if err := os.WriteFile(gate, []byte("go\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAPasteAProgramIsNotReadingLeavesOtherPanesResponsiveAndArrivesWhole(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	stuck := w.Spawn(app, shellHarness, w.Path("stuck"))
	other := w.Spawn(app, shellHarness, w.Path("other"))
	release := spawnProgramNotReadingInput(t, w, app, stuck)

	const lines = 20000
	var paste strings.Builder
	for line := 1; line <= lines; line++ {
		fmt.Fprintf(&paste, "%d pasted line\n", line)
	}
	paste.WriteString("\x04")
	if result := pasteInto(app, app.Terminal(stuck), "blocked-paste", paste.String()); !result.Success {
		t.Fatalf("a %d-byte paste into a program not reading was refused: %s", paste.Len(), protocol.Deref(result.Error))
	}

	app.TypeLine(other, "echo other-pane-answers")
	app.AwaitScreen(other, "other-pane-answers")

	release()
	app.AwaitScreen(stuck, fmt.Sprintf("lines=%d bad=0", lines))
}

func TestInputBeyondTheQueueLimitIsRefusedNamingTheLimit(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	stuck := w.Spawn(app, shellHarness, w.Path("stuck"))
	spawnProgramNotReadingInput(t, w, app, stuck)
	terminal := app.Terminal(stuck)

	frame := strings.Repeat(strings.Repeat("x", 1023)+"\n", (protocol.MaxSocketFrameBytes-64*1024)/1024)
	for attempt := range 2 {
		if result := pasteInto(app, terminal, fmt.Sprintf("fits-%d", attempt), frame); !result.Success {
			t.Fatalf("paste %d of the largest frame into an idle queue was refused: %s", attempt, protocol.Deref(result.Error))
		}
	}
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(terminal), Data: frame, Source: protocol.Ptr("user")})
	refused := testworld.Await(app, protocol.EventCommandError, func(e protocol.CommandErrorMessage) bool {
		return protocol.Deref(e.TerminalID) == terminal && protocol.Deref(e.Cmd) == protocol.CmdPtyInput
	})
	for _, want := range []string{"not reading its input", "at most 16777216 bytes"} {
		if !strings.Contains(refused.Error, want) {
			t.Fatalf("the app was told %q, want it to say %q", refused.Error, want)
		}
	}
}

func TestEmptyInputLeavesTheTerminalTakingInput(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	session := w.Spawn(app, shellHarness, w.Path("shop"))
	if result := pasteInto(app, app.Terminal(session), "empty", ""); !result.Success {
		t.Fatalf("empty input was refused: %s", protocol.Deref(result.Error))
	}
	app.TypeLine(session, "echo still-taking-input")
	app.AwaitScreen(session, "still-taking-input")
}
