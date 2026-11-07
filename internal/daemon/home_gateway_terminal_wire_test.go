package daemon_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/cloudlink"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/protocol/homegateway"
	"github.com/victorarias/attn/internal/testworld"
)

type phoneTerminal struct {
	phone    *testPhone
	id       string
	screens  int
	shown    []byte
	screenAt int
	lastSeq  int
	received int
	granted  int
	keepUp   bool
	resized  []homegateway.TerminalResized
}

const shownTail = 64 << 10

func (p *testPhone) attach(terminal string, cols, rows, credit int) *phoneTerminal {
	p.t.Helper()
	p.send(homegateway.TerminalAttach{Cmd: "terminal_attach", TerminalID: terminal, Cols: cols, Rows: rows, Credit: credit})
	return &phoneTerminal{phone: p, id: terminal, screenAt: -1, lastSeq: -1, granted: credit}
}

func (p *testPhone) frame() []byte {
	p.t.Helper()
	guard := time.NewTimer(fakeagent.HangGuard)
	defer guard.Stop()
	select {
	case frame := <-p.fromHome:
		return frame
	case <-guard.C:
		p.t.Fatalf("the home sent nothing for fakeagent.HangGuard=%s", fakeagent.HangGuard)
		return nil
	}
}

func (pt *phoneTerminal) grant(bytes int) {
	pt.granted += bytes
	pt.phone.send(homegateway.TerminalCredit{Cmd: "terminal_credit", TerminalID: pt.id, Bytes: bytes})
}

// read takes the home's next message for this terminal and checks it against the promises a screen and its frames make.
func (pt *phoneTerminal) read() string {
	t := pt.phone.t
	t.Helper()
	for {
		frame := pt.phone.frame()
		if frame[0] == protocol.BinaryFrameTypePtyOutput {
			terminal, seq, data, err := protocol.DecodePtyOutputFrame(frame)
			if err != nil || string(terminal) != pt.id {
				t.Fatalf("output frame for %q (err %v), want terminal %s", terminal, err, pt.id)
			}
			if pt.screens == 0 {
				t.Fatalf("an output frame (seq %d) came before the terminal's screen", seq)
			}
			if int(seq) <= pt.screenAt || int(seq) < pt.lastSeq {
				t.Fatalf("an output frame has seq %d after a screen at seq %d and a frame at seq %d; want above the screen and never lower than the frame before", seq, pt.screenAt, pt.lastSeq)
			}
			if len(frame) > cloudlink.MaxMessageBytes {
				t.Fatalf("an output frame of %d bytes is past the Cloud Relay's %d-byte message", len(frame), cloudlink.MaxMessageBytes)
			}
			pt.lastSeq = int(seq)
			pt.shown = append(pt.shown, data...)
			pt.shown = pt.shown[max(0, len(pt.shown)-shownTail):]
			pt.consumed(len(data))
			return "output"
		}
		var head struct {
			Event      string `json:"event"`
			TerminalID string `json:"terminal_id"`
		}
		if err := json.Unmarshal(frame, &head); err != nil {
			t.Fatalf("the home sent %q, neither JSON nor an output frame", frame)
		}
		switch head.Event {
		case "sessions", "asks", "ask", "garden_seeds", "garden":
			continue
		case "terminal_screen":
			var screen homegateway.TerminalScreen
			_ = json.Unmarshal(frame, &screen)
			ansi, err := base64.StdEncoding.DecodeString(screen.Ansi)
			if err != nil || screen.Cols <= 0 || screen.Rows <= 0 {
				t.Fatalf("terminal_screen %s does not decode to a sized ANSI screen: %v", frame, err)
			}
			pt.screens++
			pt.shown = ansi
			pt.screenAt = screen.Seq
			pt.lastSeq = screen.Seq
			pt.consumed(len(ansi))
			return "screen"
		case "terminal_resized":
			var resized homegateway.TerminalResized
			_ = json.Unmarshal(frame, &resized)
			pt.resized = append(pt.resized, resized)
			return "resized"
		default:
			t.Fatalf("the home sent %s while the phone awaited its terminal", frame)
		}
	}
}

func (pt *phoneTerminal) consumed(bytes int) {
	pt.received += bytes
	if pt.received > pt.granted {
		pt.phone.t.Fatalf("the home sent %d terminal bytes against %d bytes of credit", pt.received, pt.granted)
	}
	if pt.keepUp {
		pt.grant(bytes)
	}
}

func (pt *phoneTerminal) awaitShown(text string) {
	pt.phone.t.Helper()
	for !bytes.Contains(pt.shown, []byte(text)) {
		pt.read()
	}
}

func (pt *phoneTerminal) awaitResized(cols, rows int) {
	pt.phone.t.Helper()
	for {
		for _, r := range pt.resized {
			if r.Cols == cols && r.Rows == rows {
				pt.resized = nil
				return
			}
		}
		pt.read()
	}
}

func (pt *phoneTerminal) typeLine(text string) {
	pt.phone.send(homegateway.TerminalInput{Cmd: "terminal_input", TerminalID: pt.id, Data: text + "\r"})
}

func welcomedPhone(t *testing.T, w *world, profile string) (*testPhone, []homegateway.HomeGatewaySession) {
	t.Helper()
	phone := connectPhone(t, w, "phone", profile)
	phone.hello(homegateway.APIVersion)
	var welcome homegateway.HomeGatewayWelcome
	phone.next("welcome", &welcome)
	return phone, phone.sessions()
}

const phoneCredit = 256 << 10

func TestAPhoneOpensATerminalSeesItsScreenThenOutputAndOwnsItsSizeUntilTheDesktopTakesItBack(t *testing.T) {
	w := newWorld(t)
	desktop := w.App()
	setSetting(t, desktop, "phone_access_enabled", "true")
	session := w.Spawn(desktop, shellHarness, w.Path("shop"))
	terminal := desktop.Terminal(session)
	desktop.Send(protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: protocol.TerminalID(terminal), Cols: 120, Rows: 36})
	desktop.TypeLine(session, "echo before-the-phone")
	desktop.AwaitScreen(session, "before-the-phone\r\n")

	phone, sessions := welcomedPhone(t, w, desktop.SelectedProfile())
	if len(sessions) != 1 || protocol.Deref(sessions[0].TerminalID) != terminal {
		t.Fatalf("sessions = %+v, want the shell with terminal_id %s", sessions, terminal)
	}
	opened := phone.attach(terminal, 50, 30, phoneCredit)
	if kind := opened.read(); kind != "screen" || !bytes.Contains(opened.shown, []byte("before-the-phone")) {
		t.Fatalf("the first message after attach was %s showing %q, want a screen with the earlier output", kind, opened.shown)
	}

	mark := len(desktop.Log())
	phone.send(homegateway.TerminalFocus{Cmd: "terminal_focus", TerminalID: terminal})
	awaitPtyResized(desktop, mark, terminal, 50, 30)
	opened.typeLine(`printf 'phone=%s.\n' "$(stty size)"`)
	opened.awaitShown("phone=30 50.")

	mark = len(desktop.Log())
	desktop.Send(protocol.TerminalFocusMessage{Cmd: protocol.CmdTerminalFocus, ID: protocol.TerminalID(terminal)})
	awaitPtyResized(desktop, mark, terminal, 120, 36)
	opened.awaitResized(120, 36)

	opened.typeLine(`printf 'typed=%s.\n' "$(stty size)"`)
	opened.awaitShown("typed=30 50.")
	desktop.AwaitScreen(session, "typed=30 50.")

	mark = len(desktop.Log())
	phone.send(homegateway.TerminalDetach{Cmd: "terminal_detach", TerminalID: terminal})
	awaitPtyResized(desktop, mark, terminal, 120, 36)

	again := phone.attach(terminal, 50, 30, phoneCredit)
	again.read()
	phone.send(homegateway.TerminalFocus{Cmd: "terminal_focus", TerminalID: terminal})
	mark = len(desktop.Log())
	again.typeLine(`printf 'again=%s.\n' "$(stty size)"`)
	again.awaitShown("again=30 50.")
	phoneLink{phone}.Close()
	awaitPtyResized(desktop, mark, terminal, 120, 36)
}

func TestAPhonePromptReachesTheAgentThroughTheSessionMonitor(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "phone_access_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(session)
	app.AwaitScreen(session, "? for shortcuts")

	phone, _ := welcomedPhone(t, w, app.SelectedProfile())
	phone.send(homegateway.SubmitPrompt{Cmd: "submit_prompt", RequestID: "r1", SessionID: session, Text: "Yes, land it."})
	var result homegateway.SubmitResult
	phone.next("submit_result", &result)
	if !result.Delivered || result.RequestID != "r1" {
		t.Fatalf("submit_result = %+v, want r1 delivered", result)
	}
	if got := agent.Prompted(); got != "Yes, land it." {
		t.Fatalf("the agent was prompted with %q, want the phone's text", got)
	}

	phone.send(homegateway.SubmitPrompt{Cmd: "submit_prompt", RequestID: "r2", SessionID: "not-a-session", Text: "hello"})
	phone.next("submit_result", &result)
	if result.Delivered || result.RequestID != "r2" || protocol.Deref(result.Message) == "" {
		t.Fatalf("submit_result for an unknown session = %+v, want r2 refused with a reason", result)
	}
}

func TestAPhoneGetsNoMoreOutputThanItsCreditAndAFreshScreenAfterFallingBehind(t *testing.T) {
	w := newWorld(t)
	desktop := w.App()
	setSetting(t, desktop, "phone_access_enabled", "true")
	session := w.Spawn(desktop, shellHarness, w.Path("shop"))
	terminal := desktop.Terminal(session)

	keeping, _ := welcomedPhone(t, w, desktop.SelectedProfile())
	watching := keeping.attach(terminal, 80, 24, phoneCredit)
	watching.keepUp = true
	watching.read()
	watching.typeLine("echo ready")
	watching.awaitShown("ready\r\n")

	lagging, _ := welcomedPhone(t, w, desktop.SelectedProfile())
	behind := lagging.attach(terminal, 80, 24, cloudlink.MaxMessageBytes)
	if kind := behind.read(); kind != "screen" {
		t.Fatalf("the first message after attach was %s, want the screen", kind)
	}
	flooded := 2 << 20
	watching.typeLine(fmt.Sprintf(`head -c %d /dev/zero | tr '\0' x; echo; echo flood-$((1+1))`, flooded))
	watching.awaitShown("flood-2\r\n")

	behind.grant(phoneCredit)
	screensBefore := behind.screens
	behind.awaitShown("flood-2")
	if behind.screens == screensBefore {
		t.Fatalf("the phone fell %d bytes behind and caught up by replaying them; want a fresh screen", flooded)
	}
}

func awaitPtyResized(p *testworld.Peer, mark int, terminal string, cols, rows int) {
	p.T.Helper()
	testworld.AwaitSince(p, mark, protocol.EventPtyResized, func(e protocol.WebSocketEvent) bool {
		return protocol.Deref(e.ID) == terminal && protocol.Deref(e.Cols) == cols && protocol.Deref(e.Rows) == rows
	})
}

func TestAPhoneReachesOnlyTheTerminalsOfItsPairingsProfile(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	setSetting(t, app, "phone_access_enabled", "true")
	side := createProfile(app, "Side")
	sideApp := w.AppOn(side.ID)
	theirs := w.Spawn(sideApp, shellHarness, w.Path("theirs"))
	terminal := sideApp.Terminal(theirs)
	sideApp.Send(protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: protocol.TerminalID(terminal), Cols: 120, Rows: 36})
	sideApp.TypeLine(theirs, `printf 'before=%s.\n' "$(stty size)"`)
	sideApp.AwaitScreen(theirs, "before=36 120.")

	phone, sessions := welcomedPhone(t, w, app.SelectedProfile())
	if len(sessions) != 0 {
		t.Fatalf("the phone of the home profile sees %+v, want no sessions", sessions)
	}
	for _, command := range []any{
		homegateway.TerminalAttach{Cmd: "terminal_attach", TerminalID: terminal, Cols: 50, Rows: 30, Credit: phoneCredit},
		homegateway.TerminalResize{Cmd: "terminal_resize", TerminalID: terminal, Cols: 50, Rows: 30},
		homegateway.TerminalFocus{Cmd: "terminal_focus", TerminalID: terminal},
		homegateway.TerminalInput{Cmd: "terminal_input", TerminalID: terminal, Data: "echo from-the-phone\r"},
	} {
		phone.send(command)
		var refused homegateway.TerminalError
		phone.next("terminal_error", &refused)
		if refused.TerminalID != terminal {
			t.Fatalf("terminal_error = %+v after %+v, want one for terminal %s", refused, command, terminal)
		}
	}

	sideApp.TypeLine(theirs, `printf 'after=%s.\n' "$(stty size)"`)
	sideApp.AwaitScreen(theirs, "after=36 120.")
	if bytes.Contains(sideApp.Screen(theirs), []byte("from-the-phone")) {
		t.Fatal("a phone paired with another profile typed into this terminal")
	}
}

func TestAPhoneThatFallsBehindWhileItsScreenIsTakenGetsAScreenThatCoversWhatItMissed(t *testing.T) {
	w := newWorld(t)
	desktop := w.App()
	setSetting(t, desktop, "phone_access_enabled", "true")
	session := w.Spawn(desktop, shellHarness, w.Path("shop"))
	terminal := desktop.Terminal(session)

	keeping, _ := welcomedPhone(t, w, desktop.SelectedProfile())
	watching := keeping.attach(terminal, 80, 24, phoneCredit)
	watching.keepUp = true
	watching.read()

	capturing := w.PauseAt(pausepoint.HomeGatewayScreenCaptured)
	behind, _ := welcomedPhone(t, w, desktop.SelectedProfile())
	opened := behind.attach(terminal, 80, 24, phoneCredit)
	capturing.Await()
	watching.typeLine(`printf 'marker-%s' 2; head -c 1300000 /dev/zero | tr '\0' '\a'; echo; echo done-$((1+1))`)
	watching.awaitShown("done-2\r\n")
	capturing.Release()

	opened.awaitShown("done-2")
	if !bytes.Contains(opened.shown, []byte("marker-2")) {
		t.Fatalf("the phone shows %q at the end of what it got after falling behind during its first screen; want the marker the dropped output drew", opened.shown[max(0, len(opened.shown)-200):])
	}
}
