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
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/protocol/gateway"
	"github.com/victorarias/attn/internal/testworld"
)

type phoneTerminal struct {
	phone    *testPhone
	id       string
	screens  int
	shown    []byte
	lastSeq  int
	received int
	granted  int
	resized  []gateway.TerminalResized
}

func (p *testPhone) attach(terminal string, cols, rows, credit int) *phoneTerminal {
	p.t.Helper()
	p.send(gateway.TerminalAttach{Cmd: "terminal_attach", TerminalID: terminal, Cols: cols, Rows: rows, Credit: credit})
	return &phoneTerminal{phone: p, id: terminal, lastSeq: -1, granted: credit}
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
	pt.phone.send(gateway.TerminalCredit{Cmd: "terminal_credit", TerminalID: pt.id, Bytes: bytes})
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
			if int(seq) <= pt.lastSeq {
				t.Fatalf("an output frame has seq %d, at or below the %d the device already shows", seq, pt.lastSeq)
			}
			if len(frame) > cloudlink.MaxMessageBytes {
				t.Fatalf("an output frame of %d bytes is past the relay's %d-byte message", len(frame), cloudlink.MaxMessageBytes)
			}
			pt.lastSeq = int(seq)
			pt.shown = append(pt.shown, data...)
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
		case "sessions":
			continue
		case "terminal_screen":
			var screen gateway.TerminalScreen
			_ = json.Unmarshal(frame, &screen)
			ansi, err := base64.StdEncoding.DecodeString(screen.Ansi)
			if err != nil || screen.Cols <= 0 || screen.Rows <= 0 {
				t.Fatalf("terminal_screen %s does not decode to a sized ANSI screen: %v", frame, err)
			}
			pt.screens++
			pt.shown = ansi
			pt.lastSeq = screen.Seq
			pt.consumed(len(ansi))
			return "screen"
		case "terminal_resized":
			var resized gateway.TerminalResized
			_ = json.Unmarshal(frame, &resized)
			pt.resized = append(pt.resized, resized)
			return "resized"
		default:
			t.Fatalf("the home sent %s while the device awaited its terminal", frame)
		}
	}
}

func (pt *phoneTerminal) consumed(bytes int) {
	pt.received += bytes
	if pt.received > pt.granted {
		pt.phone.t.Fatalf("the home sent %d terminal bytes against %d bytes of credit", pt.received, pt.granted)
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
	pt.phone.send(gateway.TerminalInput{Cmd: "terminal_input", TerminalID: pt.id, Data: text + "\r"})
}

func welcomedPhone(t *testing.T, w *world, profile string) (*testPhone, []gateway.GatewaySession) {
	t.Helper()
	phone := connectPhone(t, w, "phone", profile)
	phone.hello(gateway.APIVersion)
	var welcome gateway.GatewayWelcome
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
	phone.send(gateway.TerminalFocus{Cmd: "terminal_focus", TerminalID: terminal})
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
	phone.send(gateway.TerminalDetach{Cmd: "terminal_detach", TerminalID: terminal})
	awaitPtyResized(desktop, mark, terminal, 120, 36)

	again := phone.attach(terminal, 50, 30, phoneCredit)
	again.read()
	phone.send(gateway.TerminalFocus{Cmd: "terminal_focus", TerminalID: terminal})
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
	phone.send(gateway.SubmitPrompt{Cmd: "submit_prompt", RequestID: "r1", SessionID: session, Text: "Yes, land it."})
	var result gateway.SubmitResult
	phone.next("submit_result", &result)
	if !result.Delivered || result.RequestID != "r1" {
		t.Fatalf("submit_result = %+v, want r1 delivered", result)
	}
	if got := agent.Prompted(); got != "Yes, land it." {
		t.Fatalf("the agent was prompted with %q, want the phone's text", got)
	}

	phone.send(gateway.SubmitPrompt{Cmd: "submit_prompt", RequestID: "r2", SessionID: "not-a-session", Text: "hello"})
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
	desktop.Send(protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: protocol.TerminalID(terminal), Cols: 80, Rows: 24})
	desktop.TypeLine(session, "echo ready")
	desktop.AwaitScreen(session, "ready\r\n")

	phone, _ := welcomedPhone(t, w, desktop.SelectedProfile())
	opened := phone.attach(terminal, 80, 24, cloudlink.MaxMessageBytes)
	if kind := opened.read(); kind != "screen" {
		t.Fatalf("the first message after attach was %s, want the screen", kind)
	}
	flooded := 4 << 20
	desktop.TypeLine(session, fmt.Sprintf(`head -c %d /dev/zero | tr '\0' x; echo; echo flood-$((1+1))`, flooded))
	desktop.AwaitScreen(session, "flood-2\r\n")

	opened.grant(phoneCredit)
	screensBefore := opened.screens
	opened.awaitShown("flood-2")
	if opened.screens == screensBefore {
		t.Fatalf("the phone fell %d bytes behind and caught up by replaying them; want a fresh screen", flooded)
	}
	if opened.received > opened.granted {
		t.Fatalf("received %d bytes against %d of credit", opened.received, opened.granted)
	}
}

func awaitPtyResized(p *testworld.Peer, mark int, terminal string, cols, rows int) {
	p.T.Helper()
	testworld.AwaitSince(p, mark, protocol.EventPtyResized, func(e protocol.WebSocketEvent) bool {
		return protocol.Deref(e.ID) == terminal && protocol.Deref(e.Cols) == cols && protocol.Deref(e.Rows) == rows
	})
}
