package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const liveView = `# main

:::data run="scripts/status.sh" every="10s"
:::

:::status state
:::
`

const statusScript = "#!/bin/sh\necho ran >> runs.log\necho \"{\\\"state\\\": \\\"green\\\", \\\"runs\\\": $(wc -l < runs.log)}\"\n"

func writeStatusScript(t *testing.T, folder, script string) {
	t.Helper()
	path := filepath.Join(folder, "scripts", "status.sh")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func viewRuns(t *testing.T, folder string) int {
	t.Helper()
	log, err := os.ReadFile(filepath.Join(folder, "runs.log"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(log), "\n")
}

func awaitViewTile(app *testworld.Peer, path string, from int, want func(*protocol.ViewDocument) bool) protocol.DesktopTileContentMessage {
	app.T.Helper()
	return testworld.AwaitSince(app, from, protocol.EventDesktopTileContent, func(e protocol.DesktopTileContentMessage) bool {
		return e.Path == path && e.View != nil && want(e.View)
	})
}

func startView(app *testworld.Peer, tile protocol.DesktopTileContentMessage) {
	app.T.Helper()
	app.Send(protocol.ViewStartMessage{Cmd: protocol.CmdViewStart, DesktopID: tile.DesktopID, TileID: tile.TileID})
}

func runCommandState(view *protocol.ViewDocument) string {
	if len(view.Commands) != 1 {
		return ""
	}
	return string(view.Commands[0].State) + " " + protocol.Deref(view.Commands[0].Change)
}

func TestAViewRunsItsCommandOnlyOnceTheUserStartsItAndStopsWhenTheScriptChanges(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "s1")
		setSetting(t, app, "views_enabled", "true")
		folder := w.Path("main")
		view := filepath.Join(folder, "view.md")
		writeViewFolder(t, folder, map[string]string{"view.md": liveView})
		writeStatusScript(t, folder, statusScript)

		if _, err := cli.OpenMarkdown(view, "s1"); err != nil {
			t.Fatalf("open: %v", err)
		}
		opened := awaitViewTile(app, view, 0, func(*protocol.ViewDocument) bool { return true })
		if opened.View == nil || runCommandState(opened.View) != "waiting " || viewSourceStates(opened.View)["scripts/status.sh"] != "waiting " {
			t.Fatalf("the opened view is %+v, want its one command waiting and its source waiting", opened.View)
		}
		w.advance(time.Minute)
		if runs := viewRuns(t, folder); runs != 0 {
			t.Fatalf("opening the view ran its command %d times before the user started it", runs)
		}

		mark := len(app.Log())
		startView(app, opened)
		live := awaitViewTile(app, view, mark, func(v *protocol.ViewDocument) bool {
			return viewValues(v)["scripts/status.sh"] == `{"runs":1,"state":"green"}`
		})
		if runCommandState(live.View) != "started " || viewSourceStates(live.View)["scripts/status.sh"] != "ok " {
			t.Errorf("after the user started it the view is %q with source %q, want started and ok", runCommandState(live.View), viewSourceStates(live.View)["scripts/status.sh"])
		}
		mark = len(app.Log())
		w.advance(10 * time.Second)
		awaitViewTile(app, view, mark, func(v *protocol.ViewDocument) bool {
			return viewValues(v)["scripts/status.sh"] == `{"runs":2,"state":"green"}`
		})

		mark = len(app.Log())
		writeStatusScript(t, folder, statusScript+"echo changed >&2\n")
		w.advance(time.Second)
		stopped := awaitViewTile(app, view, mark, func(v *protocol.ViewDocument) bool {
			return runCommandState(v) == "changed changed since you started it"
		})
		if got := viewSourceStates(stopped.View)["scripts/status.sh"]; got != "out_of_date " {
			t.Errorf("after the script changed its source is %q, want out_of_date", got)
		}
		if got := viewValues(stopped.View)["scripts/status.sh"]; got != `{"runs":2,"state":"green"}` {
			t.Errorf("after the script changed the view's values are %s, want the last ones kept", got)
		}
		w.advance(time.Minute)
		if runs := viewRuns(t, folder); runs != 2 {
			t.Fatalf("the changed script ran %d times, want the 2 runs from before it changed", runs)
		}

		mark = len(app.Log())
		startView(app, opened)
		awaitViewTile(app, view, mark, func(v *protocol.ViewDocument) bool {
			return runCommandState(v) == "started " && viewValues(v)["scripts/status.sh"] == `{"runs":3,"state":"green"}`
		})

		app.Close()
		w.advance(10 * time.Minute)
		if runs := viewRuns(t, folder); runs != 3 {
			t.Errorf("with no window showing the view its command ran %d times, want the 3 runs from before", runs)
		}
	})
}

func TestARunThatFailsKeepsTheLastValuesAndSaysWhy(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "s1")
		setSetting(t, app, "views_enabled", "true")
		folder := w.Path("main")
		view := filepath.Join(folder, "view.md")
		writeViewFolder(t, folder, map[string]string{"view.md": liveView})
		writeStatusScript(t, folder, statusScript+"[ $(wc -l < runs.log) -lt 2 ] || { echo 'GitHub said no' >&2; exit 3; }\n")

		if _, err := cli.OpenMarkdown(view, "s1"); err != nil {
			t.Fatalf("open: %v", err)
		}
		opened := awaitViewTile(app, view, 0, func(*protocol.ViewDocument) bool { return true })
		mark := len(app.Log())
		startView(app, opened)
		awaitViewTile(app, view, mark, func(v *protocol.ViewDocument) bool {
			return viewValues(v)["scripts/status.sh"] == `{"runs":1,"state":"green"}`
		})
		mark = len(app.Log())
		w.advance(10 * time.Second)
		failed := awaitViewTile(app, view, mark, func(v *protocol.ViewDocument) bool {
			return strings.HasPrefix(viewSourceStates(v)["scripts/status.sh"], "failed ")
		})
		if got, want := viewSourceStates(failed.View)["scripts/status.sh"], "failed scripts/status.sh exited with status 3: GitHub said no"; got != want {
			t.Errorf("the failed run reads %q, want %q", got, want)
		}
		if got := viewValues(failed.View)["scripts/status.sh"]; got != `{"runs":1,"state":"green"}` {
			t.Errorf("after the failed run the values are %s, want the last good ones", got)
		}
	})
}
