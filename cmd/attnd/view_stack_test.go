package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const needsMe = "# Needs me\n\n:::data file=\"rows.json\" as=\"rows\"\n:::\n\n:::table rows\n| Name |\n| --- |\n| {name} |\n:::\n"

func writeView(t *testing.T, folder, view string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	files["view.md"] = view
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(folder, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func viewListing(t *testing.T, s *testworld.Stack, name string) protocol.ViewListing {
	t.Helper()
	var listed protocol.ViewListResult
	s.Attn("view", "list", name, "--json").JSON(t, &listed)
	if len(listed.Views) != 1 {
		t.Fatalf("view list %s: %+v", name, listed)
	}
	return listed.Views[0]
}

func fileText(t *testing.T, path string) string {
	t.Helper()
	return string(readFile(t, path))
}

func TestAKeptViewIsEditedRestoredForkedPlacedAndDeletedThroughTheCLI(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	app := s.App()
	requireStdout(t, s.Attn("settings", "set", "views_enabled", "true"), "true")

	made := s.Path("made")
	writeView(t, made, needsMe, map[string]string{"rows.json": `[{"name": "first"}]`, "notes.txt": "not named by the view"})
	requireStdout(t, s.Attn("view", "keep", made, "--name", "needs-me", "-m", "what needs me"), "kept needs-me as version 1")
	refused(t, s.Attn("view", "keep", made, "--name", "needs-me"), 1, "already keeps a view needs-me")
	refused(t, s.Attn("view", "keep", made, "--name", "sessions"), 1, "one of attn's own views")

	exported := s.Path("export")
	requireStdout(t, s.Attn("view", "export", "needs-me", "--to", exported), "wrote version 1 of needs-me to "+exported, "attn view edit needs-me --from "+exported)
	if got := fileText(t, filepath.Join(exported, "view.md")); got != "<!-- attn-view 1 needs-me@1 -->\n"+needsMe {
		t.Fatalf("exported view file:\n%s", got)
	}
	if got := fileText(t, filepath.Join(exported, "rows.json")); got != `[{"name": "first"}]` {
		t.Fatalf("exported rows: %q", got)
	}
	if _, err := os.Stat(filepath.Join(exported, "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("a file the view does not name was kept: %v", err)
	}
	stale := s.Path("stale")
	requireStdout(t, s.Attn("view", "export", "needs-me", "--to", stale))

	edited := strings.Replace(fileText(t, filepath.Join(exported, "view.md")), "# Needs me", "# Only what needs me", 1)
	writeView(t, exported, edited, map[string]string{})
	requireStdout(t, s.Attn("view", "edit", "needs-me", "--from", exported, "-m", "only what needs you"), "saved needs-me as version 2 (was 1)")
	refused(t, s.Attn("view", "edit", "needs-me", "--from", stale), 1, "exported from version 1 of needs-me, and its newest version is 2")

	broken := s.Path("broken")
	requireStdout(t, s.Attn("view", "export", "needs-me", "--to", broken))
	writeView(t, broken, fileText(t, filepath.Join(broken, "view.md"))+"\n:::data rn=\"scripts/x.sh\"\n:::\n", map[string]string{})
	problem := s.Attn("view", "edit", "needs-me", "--from", broken)
	refused(t, problem, 1, "line 13: ")

	var read protocol.ViewReadResult
	s.Attn("view", "read", "needs-me", "--json").JSON(t, &read)
	if !strings.HasPrefix(read.Text, "<!-- attn-view 1 -->\n# Only what needs me") || len(read.Problems) != 0 || len(read.Values) != 1 {
		t.Fatalf("read after the refused edits: %+v", read)
	}

	requireStdout(t, s.Attn("view", "restore", "needs-me", "1"), "saved needs-me as version 3, restored from version 1")
	listing := viewListing(t, s, "needs-me")
	if listing.Version == nil || *listing.Version != 3 || len(listing.Versions) != 3 || protocol.Deref(listing.Versions[2].RestoredFrom) != 1 || protocol.Deref(listing.Versions[1].Note) != "only what needs you" {
		t.Fatalf("versions after the restore: %+v", listing)
	}
	s.Attn("view", "read", "needs-me", "--json").JSON(t, &read)
	if !strings.HasPrefix(read.Text, "<!-- attn-view 1 -->\n# Needs me") {
		t.Fatalf("read after restoring version 1: %q", read.Text)
	}

	requireStdout(t, s.Attn("view", "fork", "needs-me", "--name", "mine"), "kept mine as version 1, a copy of needs-me")
	forked := s.Path("forked")
	requireStdout(t, s.Attn("view", "export", "mine", "--to", forked))
	if got := fileText(t, filepath.Join(forked, "view.md")); got != "<!-- attn-view 1 mine@1 -->\n"+needsMe {
		t.Fatalf("the fork's view file:\n%s", got)
	}

	profile := app.SelectedProfile()
	home := profiles.HomeDesktopID(profile)
	app.Send(protocol.DesktopSetCurrentMessage{Cmd: protocol.CmdDesktopSetCurrent, ProfileID: profile, DesktopID: home, RequestID: "home"})
	requireStdout(t, s.Attn("view", "show", "needs-me"), "put needs-me on desktop "+home)
	requireStdout(t, s.Attn("view", "show", "needs-me"), "needs-me is already on desktop "+home)
	testworld.Await(app, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
		return m.DesktopID == home && m.TileID == "tile-view-needs-me" && m.View != nil && strings.HasPrefix(m.Content, "<!-- attn-view 1 -->\n# Needs me")
	})
	if shown := viewListing(t, s, "needs-me").Shown; len(shown) != 1 || shown[0].Desktop != "Home" {
		t.Fatalf("where needs-me is shown: %+v", shown)
	}
	requireStdout(t, s.Attn("view", "hide", "pull-requests"), "took pull-requests off desktop "+home+" (1 tile(s))")
	requireStdout(t, s.Attn("view", "show", "pull-requests"), "put pull-requests on desktop "+home)
	reset := testworld.Request(app, protocol.DesktopResetHomeMessage{Cmd: protocol.CmdDesktopResetHome, ProfileID: profile, RequestID: "reset"},
		protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "reset" })
	if shown := viewListing(t, s, "needs-me").Shown; !reset.Success || len(shown) != 1 {
		t.Fatalf("needs-me after putting Home back: %+v, shown %+v", reset, shown)
	}

	status := s.Path("status")
	writeView(t, status, ":::data run=\"scripts/status.sh\"\n:::\n\n:::status state\n:::\n", map[string]string{})
	if err := os.MkdirAll(filepath.Join(status, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(status, "scripts", "status.sh"), []byte("#!/bin/sh\necho '{\"state\": \"green\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	requireStdout(t, s.Attn("view", "keep", status, "--name", "status"), "kept status as version 1")
	requireStdout(t, s.Attn("view", "show", "status"))
	mark := app.EventCount()
	shownStatus := testworld.Await(app, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
		return m.TileID == "tile-view-status" && m.View != nil && len(m.View.Commands) == 1
	})
	app.Send(protocol.ViewStartMessage{Cmd: protocol.CmdViewStart, DesktopID: shownStatus.DesktopID, TileID: shownStatus.TileID})
	ran := testworld.AwaitSince(app, mark, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
		return m.TileID == "tile-view-status" && m.View != nil && len(m.View.Sources) == 1 && (len(m.View.Values) == 1 || m.View.Sources[0].State == "failed")
	})
	if len(ran.View.Values) != 1 {
		t.Fatalf("the kept view's script did not run: %+v", ran.View.Sources[0])
	}

	refused(t, s.Attn("view", "delete", "sessions"), 1, "cannot be deleted")
	requireStdout(t, s.Attn("view", "delete", "needs-me"), "deleted needs-me and closed 1 tile(s)")
	if shown := viewListing(t, s, "pull-requests").Shown; len(shown) != 1 {
		t.Fatalf("pull-requests after deleting needs-me: %+v", shown)
	}
	refused(t, s.Attn("view", "list", "needs-me"), 1, "keeps no view needs-me")
	requireStdout(t, s.Attn("view", "export", "mine", "--to", s.Path("mine-again")), "wrote version 1 of mine")
}

func TestAViewExportRefusesToReplaceTextAnEditChangedAndWritesNothing(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	requireStdout(t, s.Attn("settings", "set", "views_enabled", "true"), "true")
	made := s.Path("made")
	writeView(t, made, needsMe, map[string]string{"rows.json": `[{"name": "first"}]`})
	requireStdout(t, s.Attn("view", "keep", made, "--name", "needs-me"), "kept needs-me as version 1")

	mine := s.Path("mine")
	requireStdout(t, s.Attn("view", "export", "needs-me", "--to", mine), "wrote version 1 of needs-me")
	requireStdout(t, s.Attn("view", "export", "needs-me", "--to", mine), "wrote version 1 of needs-me")
	myView := strings.Replace(fileText(t, filepath.Join(mine, "view.md")), "# Needs me", "# Mine", 1)
	writeView(t, mine, myView, map[string]string{})
	if err := os.Remove(filepath.Join(mine, "rows.json")); err != nil {
		t.Fatal(err)
	}

	theirs := s.Path("theirs")
	requireStdout(t, s.Attn("view", "export", "needs-me", "--to", theirs))
	writeView(t, theirs, strings.Replace(fileText(t, filepath.Join(theirs, "view.md")), "# Needs me", "# Theirs", 1), map[string]string{})
	requireStdout(t, s.Attn("view", "edit", "needs-me", "--from", theirs), "saved needs-me as version 2")
	refused(t, s.Attn("view", "edit", "needs-me", "--from", mine), 1, "its newest version is 2")

	refused(t, s.Attn("view", "export", "needs-me", "--to", mine), 1, filepath.Join(mine, "view.md")+"; export into another folder")
	if got := fileText(t, filepath.Join(mine, "view.md")); got != myView {
		t.Fatalf("the refused export replaced the edited view file with:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(mine, "rows.json")); !os.IsNotExist(err) {
		t.Fatalf("the refused export wrote rows.json: %v", err)
	}

	requireStdout(t, s.Attn("view", "export", "needs-me", "--to", theirs), "wrote version 2 of needs-me")
	if got := fileText(t, filepath.Join(theirs, "view.md")); !strings.HasPrefix(got, "<!-- attn-view 1 needs-me@2 -->\n# Theirs\n") {
		t.Fatalf("exporting again into the folder version 2 was saved from wrote:\n%s", got)
	}
}

func TestAKeptViewThatRunsNothingDrawsFromTheHomeWhileTheOutpostItWasKeptFromIsOff(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t)
	outpost.Vars = append(outpost.Vars, "SHELL=/bin/bash")
	writeView(t, outpost.Path("work", "data"), needsMe, map[string]string{"rows.json": `[{"name": "first"}]`})
	where := outpost.Path("work", "where")
	writeView(t, where, ":::data run=\"scripts/where.sh\"\n:::\n\n:::status state\n:::\n", map[string]string{})
	if err := os.MkdirAll(filepath.Join(where, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(where, "scripts", "where.sh"), []byte("#!/bin/sh\necho \"{\\\"state\\\": \\\"$PWD\\\"}\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	pair.Connect()
	app := pair.App
	requireStdout(t, home.Attn("settings", "set", "views_enabled", "true"), "true")

	shell := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), outpost.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	app.TypeLine(shell, "attn view keep "+outpost.Path("work", "data")+" --name needs-me && attn view keep "+where+" --name where && printf '__kept_%s\\n' yes")
	app.AwaitScreen(shell, "__kept_yes")
	profile := app.SelectedProfile()
	homeDesktop := profiles.HomeDesktopID(profile)
	app.Send(protocol.DesktopSetCurrentMessage{Cmd: protocol.CmdDesktopSetCurrent, ProfileID: profile, DesktopID: homeDesktop, RequestID: "home"})
	requireStdout(t, home.Attn("view", "show", "where"), "put where on desktop "+homeDesktop)
	requireStdout(t, home.Attn("view", "show", "needs-me"), "put needs-me on desktop "+homeDesktop)

	mark := app.EventCount()
	shownWhere := testworld.Await(app, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
		return m.TileID == "tile-view-where" && m.View != nil && len(m.View.Commands) == 1
	})
	app.Send(protocol.ViewStartMessage{Cmd: protocol.CmdViewStart, DesktopID: shownWhere.DesktopID, TileID: shownWhere.TileID})
	ran := testworld.AwaitSince(app, mark, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
		return m.TileID == "tile-view-where" && m.View != nil && len(m.View.Values) == 1
	})
	if protocol.Deref(ran.Host) != pair.Host || !strings.Contains(fmt.Sprint(ran.View.Values[0].Values), outpost.Dir) {
		t.Fatalf("the view with a command ran it on host %q with %+v; want the outpost %s, in its data folder %s", protocol.Deref(ran.Host), ran.View.Values[0].Values, pair.Host, outpost.Dir)
	}
	testworld.Await(app, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
		return m.TileID == "tile-view-needs-me" && m.Host == nil && m.View != nil && len(m.View.Values) == 1
	})

	outpost.Stop()
	testworld.Await(home.App(), protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
		return m.TileID == "tile-view-needs-me" && m.Host == nil && m.View != nil && len(m.View.Values) == 1 && protocol.Deref(m.Error) == ""
	})

	exported := home.Path("where")
	requireStdout(t, home.Attn("view", "export", "where", "--to", exported))
	stamp, _, _ := strings.Cut(fileText(t, filepath.Join(exported, "view.md")), "\n")
	writeView(t, exported, stamp+"\n# Where\n\nNothing runs here now.\n", map[string]string{})
	requireStdout(t, home.Attn("view", "edit", "where", "--from", exported), "saved where as version 2")
	var read protocol.ViewReadResult
	home.Attn("view", "read", "where", "--json").JSON(t, &read)
	if !strings.HasPrefix(read.Path, home.Dir) || !strings.Contains(read.Text, "Nothing runs here now.") {
		t.Fatalf("where, once an edit took its command away, reads %+v; want it from the home's folder %s", read, home.Dir)
	}

	requireStdout(t, home.Attn("view", "export", "where", "--to", exported))
	stamp, _, _ = strings.Cut(fileText(t, filepath.Join(exported, "view.md")), "\n")
	writeView(t, exported, stamp+"\n:::data run=\"scripts/where.sh\"\n:::\n\n:::status state\n:::\n", map[string]string{})
	requireStdout(t, home.Attn("view", "edit", "where", "--from", exported), "saved where as version 3")
	home.Attn("view", "read", "where", "--json").JSON(t, &read)
	if !strings.HasPrefix(read.Path, home.Dir) || len(read.Commands) != 1 {
		t.Fatalf("where, once an edit on the home gave it a command again, reads %+v; want it with its command from the home's folder %s", read, home.Dir)
	}
}
