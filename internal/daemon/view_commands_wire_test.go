package daemon_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/github/mockserver"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const jobsView = `# Jobs

:::data file="jobs.json"
:::

:::commands
rerun: ./rerun.sh {title} --id={id}
fail: ./fail.sh
note: ./rerun.sh
:::

:::entry note
Write a note
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
	return requestViewRun(app, protocol.ViewRunMessage{DesktopID: tile.DesktopID, TileID: tile.TileID, Command: command, Part: part, Rows: rows})
}

func requestViewRun(app *testworld.Peer, run protocol.ViewRunMessage) protocol.ViewRunResultMessage {
	requestID := uuid.NewString()
	run.Cmd, run.RequestID = protocol.CmdViewRun, requestID
	return testworld.Request(app, run,
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
		if want := []string{"rerun waiting", "fail waiting", "note waiting"}; !slices.Equal(names, want) {
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

		entry := slices.IndexFunc(started.View.Parts, func(p protocol.ViewPart) bool { return p.Entry != nil })
		mark = len(app.Log())
		if ran := requestViewRun(app, protocol.ViewRunMessage{DesktopID: started.DesktopID, TileID: started.TileID, Command: "note", Part: started.View.Parts[entry].Line, Text: protocol.Ptr(shellTitle)}); !ran.Success {
			t.Fatalf("running note from the entry: %s", protocol.Deref(ran.Error))
		}
		testworld.AwaitSince(app, mark, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
			return m.Path == view && viewCommandPhase(m, "note") == "done"
		})
		if got, _ := os.ReadFile(filepath.Join(folder, "got.txt")); string(got) != "1\n["+shellTitle+"]\n" {
			t.Errorf("the entry's program received\n%s\nwant the typed text as its one argument", got)
		}

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

func sessionsShows(t *testing.T, view *protocol.ViewDocument, field, id string) bool {
	t.Helper()
	raw, shown := viewValues(view)["sessions"]
	if !shown {
		return false
	}
	var set map[string]json.RawMessage
	var rows []struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(raw), &set) != nil || json.Unmarshal(set[field], &rows) != nil {
		t.Fatalf("the sessions set %s", raw)
	}
	return slices.ContainsFunc(rows, func(r struct {
		ID string `json:"id"`
	}) bool {
		return r.ID == id
	})
}

func listOf(t *testing.T, view *protocol.ViewDocument, field string) int {
	t.Helper()
	var line int
	var find func(parts []protocol.ViewPart)
	find = func(parts []protocol.ViewPart) {
		for _, part := range parts {
			switch {
			case part.List != nil && slices.Equal(part.List.Items.Field, []string{field}):
				line = part.Line
			case part.Each != nil:
				find(part.Each.Blocks)
			}
		}
	}
	find(view.Parts)
	if line == 0 {
		t.Fatalf("the view shows no list of %s: %+v", field, view.Parts)
	}
	return line
}

func TestUnsnoozeOnHomesSessionsWakesTheAgentAndOnlyWhereItApplies(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "s1")
		setSetting(t, app, "views_enabled", "true")
		profile := app.SelectedProfile()
		if switched := requestSwitchDesktop(app, profile, profiles.HomeDesktopID(profile)); !switched.Success {
			t.Fatalf("going Home: %+v", switched)
		}
		mark := len(app.Log())
		snoozeUntil(app, "s1", time.Now().Add(time.Hour))
		snoozed := testworld.AwaitSince(app, mark, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
			return m.TileID == "tile-view-sessions" && m.View != nil && sessionsShows(t, m.View, "snoozed", "s1")
		})
		part := listOf(t, snoozed.View, "snoozed")

		if ran := runViewCommandOn(t, app, snoozed, "approve", part, "s1"); ran.Success || !strings.Contains(protocol.Deref(ran.Error), "does not apply here") {
			t.Fatalf("approve on a session's row answered %+v, want it refused", ran)
		}
		if ran := runViewCommandOn(t, app, snoozed, "follow", 0); ran.Success || !strings.Contains(protocol.Deref(ran.Error), "the app runs follow") {
			t.Fatalf("follow at the home answered %+v, want it refused", ran)
		}
		mark = len(app.Log())
		if ran := runViewCommandOn(t, app, snoozed, "unsnooze", part, "s1"); !ran.Success {
			t.Fatalf("unsnooze on the snoozed row: %s", protocol.Deref(ran.Error))
		}
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.TurnSnoozedUntil == nil })
		testworld.AwaitSince(app, mark, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
			return m.TileID == "tile-view-sessions" && m.View != nil && !sessionsShows(t, m.View, "snoozed", "s1") && viewCommandPhase(m, "unsnooze") == "done"
		})
	})
}

const pullRequestsView = `:::data attn="pull-requests"
:::

:::list yours
title: {title}
controls: merge
:::

::::each review
:::list rows
title: {title}
group-controls: mute-repo
controls: approve
:::
::::
`

type pullRequestsShown struct {
	Yours []struct {
		ID string `json:"id"`
	} `json:"yours"`
	Review []struct {
		ID   string `json:"id"`
		Rows []struct {
			ID string `json:"id"`
		} `json:"rows"`
	} `json:"review"`
}

func pullRequestsOf(t *testing.T, view *protocol.ViewDocument) pullRequestsShown {
	t.Helper()
	var shown pullRequestsShown
	if raw, ok := viewValues(view)["pull-requests"]; ok {
		if err := json.Unmarshal([]byte(raw), &shown); err != nil {
			t.Fatalf("the pull-requests set %s: %v", raw, err)
		}
	}
	return shown
}

func awaitPhase(app *testworld.Peer, view string, from int, command, phase string) {
	app.T.Helper()
	testworld.AwaitSince(app, from, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
		return m.Path == view && viewCommandPhase(m, command) == phase
	})
}

func TestAPullRequestsViewApprovesMergesAndMutesARepoThroughGitHub(t *testing.T) {
	gh := mockserver.New()
	t.Cleanup(gh.Close)
	gh.Config.SetKeepAlivesEnabled(false)
	gh.AddPR(mockserver.MockPR{Repo: "acme/shop", Number: 1, Title: "Add checkout", Role: "author"})
	gh.AddPR(mockserver.MockPR{Repo: "acme/docs", Number: 2, Title: "Fix typo", Role: "reviewer"})
	t.Setenv("ATTN_MOCK_GH_URL", gh.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "s1")
		setSetting(t, app, "views_enabled", "true")
		folder := w.Path("prs")
		view := filepath.Join(folder, "view.md")
		writeViewFolder(t, folder, map[string]string{"view.md": pullRequestsView})
		if _, err := cli.OpenMarkdown(view, "s1"); err != nil {
			t.Fatalf("open: %v", err)
		}
		shown := awaitRunTile(app, view, 0, func(v *protocol.ViewDocument) bool {
			prs := pullRequestsOf(t, v)
			return len(prs.Yours) == 1 && len(prs.Review) == 1 && len(prs.Review[0].Rows) == 1
		})
		if len(shown.View.Problems) != 0 {
			t.Fatalf("the view has problems: %+v", shown.View.Problems)
		}
		prs := pullRequestsOf(t, shown.View)
		yours, review := listOf(t, shown.View, "yours"), listOf(t, shown.View, "rows")
		repo, reviewed := prs.Review[0].ID, prs.Review[0].Rows[0].ID

		if ran := runViewCommandOn(t, app, shown, "approve", review, repo); ran.Success || !strings.Contains(protocol.Deref(ran.Error), "does not apply here") {
			t.Fatalf("approve on a repository's group answered %+v, want it refused", ran)
		}
		mark := len(app.Log())
		if ran := runViewCommandOn(t, app, shown, "approve", review, repo, reviewed); !ran.Success {
			t.Fatalf("approve on the review row: %s", protocol.Deref(ran.Error))
		}
		awaitPhase(app, view, mark, "approve", "done")
		if !gh.HasApproveRequest("acme/docs", 2) {
			t.Fatalf("GitHub saw no approval of acme/docs#2: %+v", gh.Requests())
		}
		mark = len(app.Log())
		if ran := runViewCommandOn(t, app, shown, "merge", yours, prs.Yours[0].ID); !ran.Success {
			t.Fatalf("merge on your row: %s", protocol.Deref(ran.Error))
		}
		awaitPhase(app, view, mark, "merge", "done")
		if !gh.HasMergeRequest("acme/shop", 1, "squash") {
			t.Fatalf("GitHub saw no squash merge of acme/shop#1: %+v", gh.Requests())
		}
		mark = len(app.Log())
		if ran := runViewCommandOn(t, app, shown, "mute-repo", review, repo); !ran.Success {
			t.Fatalf("mute-repo on the repository's group: %s", protocol.Deref(ran.Error))
		}
		awaitRunTile(app, view, mark, func(v *protocol.ViewDocument) bool { return len(pullRequestsOf(t, v).Review) == 0 })
	})
}
