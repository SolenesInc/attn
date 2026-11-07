package daemon_test

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func pasteInto(app *testworld.Peer, terminal, probe, text string) protocol.PtyInputProbeResultMessage {
	return testworld.Request(app, protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(terminal), Data: text, Source: protocol.Ptr("user"), ProbeID: protocol.Ptr(probe)},
		protocol.EventPtyInputProbeResult, func(r protocol.PtyInputProbeResultMessage) bool { return r.ProbeID == probe })
}

func spawnProgramNotReadingInput(t *testing.T, w *world, app *testworld.Peer, session string) (release func()) {
	t.Helper()
	return spawnProgramReadingInputOnRelease(t, w, app, session, `awk 'NR != $1 { bad++ } END { print "lines=" NR, "bad=" bad+0 }'`)
}

func spawnProgramReadingInputOnRelease(t *testing.T, w *world, app *testworld.Peer, session, program string) (release func()) {
	t.Helper()
	gate := w.Path("input-gate")
	if err := syscall.Mkfifo(gate, 0o600); err != nil {
		t.Fatal(err)
	}
	app.TypeLine(session, fmt.Sprintf(`stty -echo; printf 'wait''ing\n'; read _ < %s; %s`, gate, program))
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

func TestADeliveryAProgramIsNotReadingFailsAndNeverArrivesLater(t *testing.T) {
	daemon.ShortenInputPlacementWindow(t)
	w := newWorld(t)
	app := w.App()
	stuck := w.Spawn(app, shellHarness, w.Path("stuck"))
	release := spawnProgramReadingInputOnRelease(t, w, app, stuck, `awk '/last-line-of-the-delivery/ { late++ } /\[201~/ { closed++ } END { print "arrived=" late+0, "closed=" closed+0 }'`)

	delivery := strings.Repeat("a line of feedback\n", 16<<10) + "last-line-of-the-delivery\n"
	if got := submitSessionAnnotationFeedback(app, stuck, delivery); got.status != "error" || !strings.Contains(got.err, "not reading its input") {
		t.Fatalf("a delivery to a program not reading = %+v, want an error saying the program is not reading", got)
	}

	release()
	if result := pasteInto(app, app.Terminal(stuck), "end-of-input", "\n\x04"); !result.Success {
		t.Fatalf("input after the failed delivery was refused: %s", protocol.Deref(result.Error))
	}
	app.AwaitScreen(stuck, "arrived=0 closed=1")
}

func TestAQuickCaptureTheAgentsTerminalWillNotTakeStaysUnread(t *testing.T) {
	daemon.ShortenInputPlacementWindow(t)
	w := newWorld(t, fakeagent.Claude)
	app := w.TrustedApp()
	session, agent := mailIdleAgent(w, app, "shop")
	agent.StopReadingTerminal()
	fill := protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(app.Terminal(session)), Data: strings.Repeat("x\n", 512*1024), Source: protocol.Ptr("agent"), ProbeID: protocol.Ptr("fill")}
	if result := testworld.Request(app, fill, protocol.EventPtyInputProbeResult, func(r protocol.PtyInputProbeResultMessage) bool { return r.ProbeID == "fill" }); !result.Success {
		t.Fatalf("input ahead of the capture was refused: %s", protocol.Deref(result.Error))
	}

	sent := sendQuickCapture(t, app, sessionMailbox(session), "a capture behind the stuck input")

	agent.Exit(0)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })
	if capture := quickCaptureRecord(t, app, sent.CaptureID); capture.ReadAt != nil {
		t.Fatalf("the capture = %+v, want it unread: the agent never took it", capture)
	}
}

func TestInputDuringADeliveryIsAnsweredWhileTheDeliveryWaitsForItsTerminal(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, phone := w.App(), w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(session)
	awaitClaudeAtItsPrompt(app, session)
	other := w.Spawn(app, shellHarness, w.Path("other"))

	placing := w.PauseAt(pausepoint.SessionInputPlacing)
	requestID := uuid.NewString()
	app.Send(protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: requestID, SessionID: protocol.SessionID(session), Text: "the delivery"})
	placing.Await()
	app.TypeLine(other, "echo other-pane-answers")
	app.AwaitScreen(other, "other-pane-answers")
	if result := pasteInto(phone, app.Terminal(session), "key-from-another-client", "k"); !result.Success {
		t.Fatalf("a key from another client during the delivery was refused: %s", protocol.Deref(result.Error))
	}
	placing.Release()

	if got := testworld.Await(app, protocol.EventSessionAnnotationsSubmitResult, func(r protocol.SessionAnnotationsSubmitResultMessage) bool { return r.RequestID == requestID }); got.Status != "delivered" {
		t.Fatalf("the delivery = %+v, want delivered", got)
	}
	if got := agent.Prompted(); !strings.Contains(got, "the delivery") {
		t.Fatalf("the agent was prompted %q, want it to hold the delivery", got)
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
