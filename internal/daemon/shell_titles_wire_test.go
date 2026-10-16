package daemon_test

import (
	"context"
	"testing"

	"github.com/victorarias/attn/internal/client"
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

func TestClosedShellsKeepTheirLastDisplayNameInHistory(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	cli := w.Client()
	closedNames := make(map[string]string)
	for _, mode := range []string{"normal exit", "app close", "CLI close", "failed exit", "custom name"} {
		shell := w.Spawn(app, shellHarness, w.Path("shop"))
		testworld.AwaitSession(app, shell, func(s protocol.Session) bool { return protocol.Deref(s.Title) == "shop" })
		app.TypeLine(shell, `printf '\033]0;saved shell name\007'`)
		testworld.AwaitSession(app, shell, func(s protocol.Session) bool { return protocol.Deref(s.Title) == "saved shell name" })
		want := "saved shell name"
		switch mode {
		case "normal exit":
			app.TypeLine(shell, "exit 0")
		case "failed exit":
			app.TypeLine(shell, "exit 7")
			testworld.AwaitSession(app, shell, func(s protocol.Session) bool { return s.TerminalExit != nil && s.TerminalExit.Code == 7 })
			if got := protocol.Deref(queriedSession(t, cli, shell).Title); got != want {
				t.Errorf("failed shell title = %q, want %q", got, want)
			}
			fallthrough
		case "app close":
			if result := closeFromApp(app, shell); !result.Accepted {
				t.Fatalf("%s refused: %+v", mode, result)
			}
		case "custom name":
			want = "my server"
			if err := cli.RenameSession(protocol.SessionID(shell), want); err != nil {
				t.Fatal(err)
			}
			fallthrough
		case "CLI close":
			if err := cli.Unregister(protocol.SessionID(shell)); err != nil {
				t.Fatal(err)
			}
		}
		if got := awaitClosed(app, shell).Label; got != want {
			t.Errorf("%s closed name = %q, want %q", mode, got, want)
		}
		if err := cli.Unregister(protocol.SessionID(shell)); err != nil {
			t.Fatal(err)
		}
		closedNames[shell] = want
	}
	w.restart()
	cli = w.Client()
	for id, want := range closedNames {
		if got := showSession(t, cli, id).Label; got != want {
			t.Errorf("closed shell %s after restart = %q, want %q", id, got, want)
		}
	}
	entries := ledger(t, cli, client.SessionListOptions{Closed: true}).Entries
	if len(entries) != len(closedNames) {
		t.Fatalf("closed ledger has %d shells, want %d", len(entries), len(closedNames))
	}
	for _, entry := range entries {
		if want := closedNames[string(entry.ID)]; entry.Label != want {
			t.Errorf("listed shell %s = %q, want %q", entry.ID, entry.Label, want)
		}
	}
}
