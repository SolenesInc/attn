package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const jobsView = `# Jobs

:::data file="jobs.json"
:::

:::commands
rerun: ./rerun.sh {title} --id={id}
fail: ./fail.sh
:::

:::list jobs
title: {title}
controls: rerun, fail
:::
`

const shellTitle = "build $(touch pwned) `touch pwned`; rm -rf . && echo \"quoted\" 'single' | cat > pwned *"

const rerunScript = "#!/bin/sh\nprintf '%s\\n' \"$#\" > got.txt\nfor word in \"$@\"; do printf '[%s]\\n' \"$word\" >> got.txt; done\n"

func runViewCommandOn(t *testing.T, app *testworld.Peer, tile protocol.DesktopTileContentMessage, command string, part int, rows ...string) protocol.ViewRunResultMessage {
	t.Helper()
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.ViewRunMessage{Cmd: protocol.CmdViewRun, RequestID: requestID, DesktopID: tile.DesktopID, TileID: tile.TileID, Command: command, Part: part, Rows: rows},
		protocol.EventViewRunResult, func(r protocol.ViewRunResultMessage) bool { return r.RequestID == requestID })
}

func viewCommandPhase(tile protocol.DesktopTileContentMessage, command string) string {
	for _, ran := range tile.ViewCommands {
		if ran.Command == command {
			return strings.TrimSpace(string(ran.Phase) + " " + protocol.Deref(ran.Error))
		}
	}
	return ""
}

func listLine(t *testing.T, view *protocol.ViewDocument) int {
	t.Helper()
	i := slices.IndexFunc(view.Parts, func(p protocol.ViewPart) bool { return p.List != nil })
	if i < 0 {
		t.Fatalf("the view shows no list: %+v", view.Parts)
	}
	return view.Parts[i].Line
}

func TestAViewsOwnCommandRunsFromARowWithEachFieldAsOneArgumentOnlyOnceStarted(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "s1")
		setSetting(t, app, "views_enabled", "true")
		folder := w.Path("jobs")
		view := filepath.Join(folder, "view.md")
		writeViewFolder(t, folder, map[string]string{
			"view.md":   jobsView,
			"jobs.json": `{"jobs": [{"id": "j1", "title": "lint"}, {"id": "j2", "title": ` + strconv.Quote(shellTitle) + `}]}`,
		})
		for name, script := range map[string]string{"rerun.sh": rerunScript, "fail.sh": "#!/bin/sh\necho 'the runner is gone' >&2\nexit 3\n"} {
			if err := os.WriteFile(filepath.Join(folder, name), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
		}

		if _, err := cli.OpenMarkdown(view, "s1"); err != nil {
			t.Fatalf("open: %v", err)
		}
		opened := awaitRunTile(app, view, 0, func(v *protocol.ViewDocument) bool { return len(v.Values) > 0 })
		var names []string
		for _, command := range opened.View.Commands {
			names = append(names, protocol.Deref(command.Name)+" "+string(command.State))
		}
		if want := []string{"rerun waiting", "fail waiting"}; !slices.Equal(names, want) {
			t.Fatalf("the opened view lists commands %q, want %q", names, want)
		}
		part := listLine(t, opened.View)

		refused := runViewCommandOn(t, app, opened, "rerun", part, "j2")
		if refused.Success || !strings.Contains(protocol.Deref(refused.Error), "press r") {
			t.Fatalf("a run before the user started the view's commands answered %+v, want it refused", refused)
		}
		if _, err := os.Stat(filepath.Join(folder, "got.txt")); !os.IsNotExist(err) {
			t.Fatalf("the refused command ran: %v", err)
		}

		mark := len(app.Log())
		startView(app, opened)
		started := awaitRunTile(app, view, mark, func(v *protocol.ViewDocument) bool {
			return !slices.ContainsFunc(v.Commands, func(c protocol.ViewRunCommand) bool { return c.State != protocol.ViewRunStateStarted })
		})

		mark = len(app.Log())
		if ran := runViewCommandOn(t, app, started, "rerun", part, "j2"); !ran.Success {
			t.Fatalf("running rerun on the second row: %s", protocol.Deref(ran.Error))
		}
		testworld.AwaitSince(app, mark, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
			return m.Path == view && viewCommandPhase(m, "rerun") == "busy"
		})
		testworld.AwaitSince(app, mark, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
			return m.Path == view && viewCommandPhase(m, "rerun") == "done"
		})
		got, err := os.ReadFile(filepath.Join(folder, "got.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if want := "2\n[" + shellTitle + "]\n[--id=j2]\n"; string(got) != want {
			t.Errorf("the program received\n%s\nwant the title as one argument and the id as another:\n%s", got, want)
		}
		if _, err := os.Stat(filepath.Join(folder, "pwned")); !os.IsNotExist(err) {
			t.Errorf("a shell ran the title: %v", err)
		}

		mark = len(app.Log())
		if ran := runViewCommandOn(t, app, started, "fail", part, "j1"); !ran.Success {
			t.Fatalf("running fail: %s", protocol.Deref(ran.Error))
		}
		testworld.AwaitSince(app, mark, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
			return m.Path == view && viewCommandPhase(m, "fail") == "failed fail exited with status 3: the runner is gone"
		})

		gone := runViewCommandOn(t, app, started, "rerun", part, "j9")
		if gone.Success {
			t.Errorf("a run from a row the view does not show answered %+v, want it refused", gone)
		}
	})
}

func TestAViewThatDefinesOneOfAttnsCommandsIsShownTheProblem(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "s1")
		setSetting(t, app, "views_enabled", "true")
		folder := w.Path("merging")
		view := filepath.Join(folder, "view.md")
		writeViewFolder(t, folder, map[string]string{"view.md": ":::commands\nmerge: ./merge.sh {id}\n:::\n"})

		if _, err := cli.OpenMarkdown(view, "s1"); err != nil {
			t.Fatalf("open: %v", err)
		}
		opened := awaitRunTile(app, view, 0, func(*protocol.ViewDocument) bool { return true })
		if len(opened.View.Problems) != 1 || opened.View.Problems[0].Text != `"merge" is one of attn's commands, so the view cannot define it` {
			t.Errorf("the view's problems are %+v, want merge refused as one of attn's commands", opened.View.Problems)
		}
		if len(opened.View.Commands) != 0 {
			t.Errorf("the view lists commands %+v, want none from a definition with a problem", opened.View.Commands)
		}
	})
}
