package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTerminalCommandsAddressEachTileOfASharedConversation(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	caller, first, firstTerminal := sharedCodexWaiting(t, w, app)
	home, _ := tileOf(t, w, app, firstTerminal)
	elsewhere := createDesktop(app, app.SelectedProfile())
	switchDesktop(app, app.SelectedProfile(), elsewhere.ID)
	other := w.Spawn(app, fakeagent.Codex, w.Path("other"))
	second := w.Launched(other)
	secondTerminal := app.Terminal(other)
	moveOn(t, app, second, secondTerminal, "/resume "+first.ConversationID, "lock it")
	second.Reply("Locked. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, caller, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if shown := sessionShownIn(t, w, app, firstTerminal); shown != caller {
		t.Fatalf("first terminal shows %s", shown)
	}
	if shown := sessionShownIn(t, w, app, secondTerminal); shown != caller {
		t.Fatalf("second terminal shows %s", shown)
	}
	me := protocol.SessionID(caller)
	listed := terminalDo[protocol.TerminalListResult](t, cli, "terminal ls", me, map[string]any{"all": true})
	entries := map[string]protocol.TerminalEntry{}
	for _, entry := range listed.Terminals {
		if string(entry.SessionID) == caller {
			entries[string(entry.TerminalID)] = entry
		}
	}
	if len(entries) != 2 || entries[firstTerminal].DesktopID != home || entries[secondTerminal].DesktopID != elsewhere.ID || entries[firstTerminal].ShortID == entries[secondTerminal].ShortID {
		t.Fatalf("listed terminals: %+v", entries)
	}
	for _, ref := range []string{caller, caller[:8]} {
		err := cli.RunCommand("terminal read", me, map[string]any{"terminal": ref}, new(protocol.TerminalReadResult))
		if err == nil || !strings.Contains(err.Error(), firstTerminal) || !strings.Contains(err.Error(), secondTerminal) {
			t.Fatalf("ambiguous session %s: %v", ref, err)
		}
	}
	err := cli.RunCommand("terminal ls", me, map[string]any{}, new(protocol.TerminalListResult))
	if err == nil || !strings.Contains(err.Error(), "pass --desktop") {
		t.Fatalf("ambiguous caller desktop: %v", err)
	}
	err = cli.RunCommand("terminal send", me, map[string]any{"terminal": firstTerminal, "desktop": elsewhere.ID, "text": "wrong desktop"}, new(protocol.TerminalInputResult))
	if err == nil || !strings.Contains(err.Error(), "--desktop "+home) {
		t.Fatalf("selected terminal desktop: %v", err)
	}
	for _, target := range []struct{ id, desktop, text string }{{firstTerminal, home, "first independent draft"}, {secondTerminal, elsewhere.ID, "second independent draft"}} {
		testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(target.id)}, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == target.id })
		input := terminalDo[protocol.TerminalInputResult](t, cli, "terminal send", me, map[string]any{"terminal": entries[target.id].ShortID, "desktop": target.desktop, "text": target.text})
		if string(input.TerminalID) != target.id {
			t.Fatalf("input target: %+v", input)
		}
		app.AwaitTerminalScreen(target.id, target.text)
		read := terminalDo[protocol.TerminalReadResult](t, cli, "terminal read", me, map[string]any{"terminal": target.id})
		if !strings.Contains(read.Text, target.text) {
			t.Fatalf("read selected terminal: %+v", read)
		}
	}
	firstRead := terminalDo[protocol.TerminalReadResult](t, cli, "terminal read", me, map[string]any{"terminal": firstTerminal})
	secondRead := terminalDo[protocol.TerminalReadResult](t, cli, "terminal read", me, map[string]any{"terminal": secondTerminal})
	if strings.Contains(firstRead.Text, "second independent draft") || strings.Contains(secondRead.Text, "first independent draft") {
		t.Fatalf("drafts crossed terminals: first=%q second=%q", firstRead.Text, secondRead.Text)
	}
	err = cli.RunCommand("terminal move", me, map[string]any{"terminal": caller, "desktop": home}, new(protocol.TerminalMoveResult))
	if err == nil || !strings.Contains(err.Error(), firstTerminal) || !strings.Contains(err.Error(), secondTerminal) {
		t.Fatalf("ambiguous move must name both terminals: %v", err)
	}
	moved := terminalDo[protocol.TerminalMoveResult](t, cli, "terminal move", me, map[string]any{"terminal": secondTerminal, "desktop": home})
	if string(moved.TerminalID) != secondTerminal || moved.DesktopID != home {
		t.Fatalf("moving the selected terminal: %+v", moved)
	}
	for _, terminal := range []string{firstTerminal, secondTerminal} {
		if desktop, _ := tileOf(t, w, app, terminal); desktop != home {
			t.Fatalf("terminal %s on %s, want %s", terminal, desktop, home)
		}
	}
}

func terminalDo[R any](t *testing.T, cli *client.Client, name string, caller protocol.SessionID, args map[string]any) *R {
	t.Helper()
	result := new(R)
	err := cli.RunCommand(name, caller, args, result)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}
