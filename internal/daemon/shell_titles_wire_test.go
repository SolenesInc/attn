package daemon_test

import (
	"context"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestEmbeddedShellPublishesItsTitleAndKeepsDeliveringOutput(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	caller := w.Spawn(app, fakeagent.Claude, w.Path("caller"))
	w.Launched(caller)
	cli := w.Client()
	shell := w.Spawn(app, shellHarness, w.Path("shop"))
	listedName := func(want string) {
		t.Helper()
		r, err := cli.TerminalCommand(context.Background(), protocol.TerminalListMessage{Cmd: protocol.CmdTerminalList, CallerSessionID: protocol.SessionID(caller), All: protocol.Ptr(true)})
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range r.TerminalEntries {
			if string(entry.SessionID) == shell {
				if entry.Name != want {
					t.Fatalf("listed shell name=%q, want %q", entry.Name, want)
				}
				return
			}
		}
		t.Fatalf("shell missing from terminal list: %+v", r.TerminalEntries)
	}
	testworld.AwaitSession(app, shell, func(s protocol.Session) bool { return protocol.Deref(s.Title) == "shop" })
	listedName("shop")
	app.TypeLine(shell, `printf '\033]0;embedded prompt\007'; printf 'title output %s\n' delivered`)
	testworld.AwaitSession(app, shell, func(s protocol.Session) bool { return protocol.Deref(s.Title) == "embedded prompt" })
	app.AwaitScreen(shell, "title output delivered")
	listedName("embedded prompt")
	if err := cli.RenameSession(protocol.SessionID(shell), "my server"); err != nil {
		t.Fatal(err)
	}
	listedName("my server")
	if err := cli.RenameSession(protocol.SessionID(shell), ""); err != nil {
		t.Fatal(err)
	}
	listedName("embedded prompt")
}
