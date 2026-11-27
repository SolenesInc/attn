package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAskKeepsTheMachineAndFolderItWasAskedFromAcrossARestart(t *testing.T) {
	home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	recovered := home.PauseEvery(pausepoint.OutpostRecoveryApplied)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	recovered.Await()
	recovered.Release()
	app := pair.App
	testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		return slices.ContainsFunc(m.Outposts, func(row protocol.OutpostInfo) bool {
			return row.ID == pair.Host && row.Notebook != nil && row.Notebook.State == protocol.OutpostNotebookStateInSync
		})
	})
	setSetting(t, app, "asks_enabled", "true")

	homeFolder, outpostFolder := home.Path("shop"), outpost.Path("far-shop")
	homeAsker := home.Spawn(app, fakeagent.Claude, homeFolder)
	outpostAsker := home.Spawn(app, fakeagent.Claude, outpostFolder, func(m *protocol.SpawnSessionMessage) {
		m.Host = protocol.Ptr(pair.Host)
	})
	outpost.LaunchedCarrying(app.Terminal(outpostAsker))

	ask := func(cli interface {
		RunCommand(string, protocol.SessionID, any, any) error
	}, session, folder string) protocol.Ask {
		t.Helper()
		var opened protocol.Ask
		if err := cli.RunCommand("ask", protocol.SessionID(session), command.AskArgs{Markdown: cacheAsk, BaseDir: folder}, &opened); err != nil {
			t.Fatalf("ask from %s: %v", session, err)
		}
		return opened
	}
	fromHome := ask(home.Client(), homeAsker, homeFolder)
	fromOutpost := ask(outpost.Client(), outpostAsker, outpostFolder)

	type place struct{ host, cwd string }
	want := map[string]place{fromHome.ID: {"", homeFolder}, fromOutpost.ID: {pair.Host, outpostFolder}}
	placesOf := func(asks []protocol.Ask) map[string]place {
		places := map[string]place{}
		for _, a := range asks {
			places[a.ID] = place{protocol.Deref(a.Host), protocol.Deref(a.Cwd)}
		}
		return places
	}
	for _, opened := range []protocol.Ask{fromHome, fromOutpost} {
		if got := placesOf([]protocol.Ask{opened})[opened.ID]; got != want[opened.ID] {
			t.Errorf("ask %s opened at %+v, want %+v", opened.ID, got, want[opened.ID])
		}
	}
	check := func(when string, shown []protocol.Ask) {
		t.Helper()
		got := placesOf(shown)
		for id, place := range want {
			if got[id] != place {
				t.Errorf("%s the app shows ask %s at %+v, want %+v", when, id, got[id], place)
			}
		}
		t.Logf("%s the app shows the asks at %+v", when, got)
	}
	check("before the restart", testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 2 }).Asks)

	home.Stop()
	home.Start()
	check("after the restart", home.App().Initial.Asks)
}
