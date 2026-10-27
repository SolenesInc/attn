package daemon_test

import (
	"context"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTerminalCommandsAddressEachTileOfASharedConversation(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "codex_shared_enabled", "true")
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
	call := func(message any) *protocol.Response {
		t.Helper()
		r, err := cli.TerminalCommand(context.Background(), message)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	listed := call(protocol.TerminalListMessage{Cmd: protocol.CmdTerminalList, CallerSessionID: protocol.SessionID(caller), All: protocol.Ptr(true)})
	entries := map[string]protocol.TerminalEntry{}
	for _, entry := range listed.TerminalEntries {
		if string(entry.SessionID) == caller {
			entries[string(entry.TerminalID)] = entry
		}
	}
	if len(entries) != 2 || entries[firstTerminal].DesktopID != home || entries[secondTerminal].DesktopID != elsewhere.ID || entries[firstTerminal].ShortID == entries[secondTerminal].ShortID {
		t.Fatalf("listed terminals: %+v", entries)
	}
	for _, ref := range []string{caller, caller[:8]} {
		_, err := cli.TerminalCommand(context.Background(), protocol.TerminalReadMessage{Cmd: protocol.CmdTerminalRead, CallerSessionID: protocol.SessionID(caller), Target: ref, Mode: protocol.TerminalReadModeScreen})
		if err == nil || !strings.Contains(err.Error(), firstTerminal) || !strings.Contains(err.Error(), secondTerminal) {
			t.Fatalf("ambiguous session %s: %v", ref, err)
		}
	}
	_, err := cli.TerminalCommand(context.Background(), protocol.TerminalListMessage{Cmd: protocol.CmdTerminalList, CallerSessionID: protocol.SessionID(caller)})
	if err == nil || !strings.Contains(err.Error(), "pass --desktop") {
		t.Fatalf("ambiguous caller desktop: %v", err)
	}
	_, err = cli.TerminalCommand(context.Background(), protocol.TerminalInputMessage{Cmd: protocol.CmdTerminalInput, CallerSessionID: protocol.SessionID(caller), Target: firstTerminal, Desktop: protocol.Ptr(elsewhere.ID), Data: "wrong desktop"})
	if err == nil || !strings.Contains(err.Error(), "--desktop "+home) {
		t.Fatalf("selected terminal desktop: %v", err)
	}
	for _, target := range []struct{ id, desktop, text string }{{firstTerminal, home, "first independent draft"}, {secondTerminal, elsewhere.ID, "second independent draft"}} {
		testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(target.id)}, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == target.id })
		input := call(protocol.TerminalInputMessage{Cmd: protocol.CmdTerminalInput, CallerSessionID: protocol.SessionID(caller), Target: entries[target.id].ShortID, Desktop: protocol.Ptr(target.desktop), Data: target.text})
		if input.TerminalInputResult == nil || string(input.TerminalInputResult.TerminalID) != target.id {
			t.Fatalf("input target: %+v", input)
		}
		app.AwaitTerminalScreen(target.id, target.text)
		read := call(protocol.TerminalReadMessage{Cmd: protocol.CmdTerminalRead, CallerSessionID: protocol.SessionID(caller), Target: target.id, Mode: protocol.TerminalReadModeScreen})
		if read.TerminalReadResult == nil || !strings.Contains(read.TerminalReadResult.Text, target.text) {
			t.Fatalf("read selected terminal: %+v", read.TerminalReadResult)
		}
	}
	firstRead := call(protocol.TerminalReadMessage{Cmd: protocol.CmdTerminalRead, CallerSessionID: protocol.SessionID(caller), Target: firstTerminal, Mode: protocol.TerminalReadModeScreen})
	secondRead := call(protocol.TerminalReadMessage{Cmd: protocol.CmdTerminalRead, CallerSessionID: protocol.SessionID(caller), Target: secondTerminal, Mode: protocol.TerminalReadModeScreen})
	if strings.Contains(firstRead.TerminalReadResult.Text, "second independent draft") || strings.Contains(secondRead.TerminalReadResult.Text, "first independent draft") {
		t.Fatalf("drafts crossed terminals: first=%q second=%q", firstRead.TerminalReadResult.Text, secondRead.TerminalReadResult.Text)
	}
	_, err = cli.TerminalCommand(context.Background(), protocol.TerminalMoveMessage{Cmd: protocol.CmdTerminalMove, CallerSessionID: protocol.SessionID(caller), Target: caller, Desktop: home})
	if err == nil || !strings.Contains(err.Error(), firstTerminal) || !strings.Contains(err.Error(), secondTerminal) {
		t.Fatalf("ambiguous move must name both terminals: %v", err)
	}
	moved := call(protocol.TerminalMoveMessage{Cmd: protocol.CmdTerminalMove, CallerSessionID: protocol.SessionID(caller), Target: secondTerminal, Desktop: home}).TerminalMoveResult
	if moved == nil || string(moved.TerminalID) != secondTerminal || moved.DesktopID != home {
		t.Fatalf("moving the selected terminal: %+v", moved)
	}
	for _, terminal := range []string{firstTerminal, secondTerminal} {
		if desktop, _ := tileOf(t, w, app, terminal); desktop != home {
			t.Fatalf("terminal %s on %s, want %s", terminal, desktop, home)
		}
	}
}
