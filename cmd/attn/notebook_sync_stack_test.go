package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func notebookPath(root string, parts ...string) string {
	return filepath.Join(append([]string{root}, parts...)...)
}

func writeNotebookFile(t *testing.T, root string, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func awaitNotebookState(t *testing.T, pair *testworld.OutpostPair, state protocol.OutpostNotebookState) protocol.OutpostNotebook {
	t.Helper()
	for _, row := range pair.App.Initial.Outposts {
		if row.ID == pair.Host && row.Notebook != nil && row.Notebook.State == state {
			return *row.Notebook
		}
	}
	message := testworld.Await(pair.App, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		for _, row := range m.Outposts {
			if row.ID == pair.Host && row.Notebook != nil && row.Notebook.State == state {
				return true
			}
		}
		return false
	})
	for _, row := range message.Outposts {
		if row.ID == pair.Host {
			return *row.Notebook
		}
	}
	t.Fatal("missing outpost")
	return protocol.OutpostNotebook{}
}

func awaitNotebookFile(t *testing.T, peer *testworld.Peer, root string, path, content string, absent bool) {
	t.Helper()
	matches := func() bool {
		data, err := os.ReadFile(filepath.Join(root, path))
		if absent {
			return os.IsNotExist(err)
		}
		return err == nil && string(data) == content
	}
	if matches() {
		return
	}
	testworld.Await(peer, protocol.EventFsChanged, func(_ protocol.FsChangedMessage) bool { return matches() })
}

func TestNotebookSyncKeepsBothMachinesFilesAndMovesAsideAnUnknownOutpost(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	homeRoot, outpostRoot := filepath.Join(home.Dir, "notebook"), filepath.Join(outpost.Dir, "notebook")
	writeNotebookFile(t, homeRoot, "index.md", "home index")
	writeNotebookFile(t, homeRoot, "nested/first.md", "first version")
	writeNotebookFile(t, outpostRoot, "index.md", "stale outpost scaffold")
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	outpost.Start()
	home.Start()
	for _, stack := range []*testworld.Stack{home, outpost} {
		requireStdout(t, stack.Attn("settings", "get", "notebook_sync_state"), "off")
		if _, err := os.Stat(filepath.Join(stack.Dir, "notebook-sync")); !os.IsNotExist(err) {
			t.Fatalf("unpaired daemon created a sync index: %v", err)
		}
	}
	pair.Connect()
	state := awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	outpostRoot = notebookProfileRoot(t, outpost, pair.App.SelectedProfile())
	remoteApp := outpost.TrustedApp()
	remoteApp.Send(protocol.FsWatchMessage{Cmd: protocol.CmdFsWatch, RequestID: protocol.Ptr("sync-root"), Root: protocol.Ptr(outpostRoot)})
	watch := testworld.Await(remoteApp, protocol.EventFsWatchResult, func(m protocol.FsWatchResultMessage) bool { return m.RequestID == "sync-root" })
	if !watch.Success {
		t.Fatal(protocol.Deref(watch.Error))
	}
	if protocol.Deref(state.PreviousRoot) == "" {
		t.Fatalf("moved folder not named: %+v", state)
	}
	if data, err := os.ReadFile(filepath.Join(*state.PreviousRoot, "index.md")); err != nil || string(data) != "stale outpost scaffold" {
		t.Fatalf("saved outpost: %q, %v", data, err)
	}
	awaitNotebookFile(t, remoteApp, outpostRoot, "index.md", "home index", false)
	awaitNotebookFile(t, remoteApp, outpostRoot, "nested/first.md", "first version", false)
	t.Logf("first sync in_sync; moved stale outpost to %s", *state.PreviousRoot)

	roots := map[*testworld.Stack]string{home: homeRoot, outpost: outpostRoot}
	for _, direction := range []struct {
		from, to *testworld.Stack
		peer     *testworld.Peer
		prefix   string
	}{
		{home, outpost, remoteApp, "from-home"}, {outpost, home, pair.App, "from-outpost"},
	} {
		name := direction.prefix + ".md"
		writeNotebookFile(t, roots[direction.from], name, "created")
		awaitNotebookFile(t, direction.peer, roots[direction.to], name, "created", false)
		writeNotebookFile(t, roots[direction.from], name, "edited")
		awaitNotebookFile(t, direction.peer, roots[direction.to], name, "edited", false)
		if err := os.Remove(notebookPath(roots[direction.from], name)); err != nil {
			t.Fatal(err)
		}
		awaitNotebookFile(t, direction.peer, roots[direction.to], name, "", true)
		data, err := os.ReadFile(filepath.Join(direction.to.Dir, "notebook-trash", filepath.Base(outpostRoot), name))
		if err != nil || string(data) != "edited" {
			t.Fatalf("received deletion trash: %q, %v", data, err)
		}
		t.Logf("%s create, edit and delete arrived; receiver kept edited content in notebook-trash", direction.prefix)
	}
	requireFailure(t, home.Attn("settings", "set", "notebook.root", filepath.Join(t.TempDir(), "home-notebook")), "settings: ", "outpost")
	localName, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	requireFailure(t, outpost.Attn("settings", "set", "notebook.root", filepath.Join(t.TempDir(), "outpost-notebook")), "settings: ", localName)
	t.Log("root changes refused on both sides, naming peers")

	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "false"))
	requireStdout(t, home.Attn("settings", "get", "notebook_sync_state"), "off")
	writeNotebookFile(t, homeRoot, "disabled-home.md", "home only")
	writeNotebookFile(t, outpostRoot, "disabled-outpost.md", "outpost only")
	requireStdout(t, outpost.Attn("enrollment", "leave"), "no longer enrolled")
	if _, err := os.Stat(filepath.Join(outpost.Dir, "notebook-sync")); !os.IsNotExist(err) {
		t.Fatalf("leave kept sync index: %v", err)
	}
	requireStdout(t, outpost.Attn("settings", "set", "outpost_listen", ""))
	requireStdout(t, outpost.Attn("settings", "get", "notebook_sync_state"), "off")
	for _, check := range []struct {
		stack *testworld.Stack
		name  string
	}{{home, "disabled-outpost.md"}, {outpost, "disabled-home.md"}} {
		if _, err := os.Stat(notebookPath(roots[check.stack], check.name)); !os.IsNotExist(err) {
			t.Fatalf("disabled sync received %s: %v", check.name, err)
		}
	}
	t.Log("Outposts off disconnected home; leave dropped outpost index and retained Notebook; listener off reports sync off")
}

func TestOutpostSeedTransfersUseItsNotebookAndWaitForTheHome(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	homeRoot := filepath.Join(home.Dir, "notebook")
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	outpostRoot := notebookProfileRoot(t, outpost, pair.App.SelectedProfile())
	seed := plant(t, home, "Outpost artifacts", "-m", "Exercise the outpost disk and the home's Garden")
	var attached protocol.SeedArtifactTransferResult
	for _, operation := range []string{"move", "copy"} {
		source := filepath.Join(outpost.Dir, operation+".md")
		if err := os.WriteFile(source, []byte(operation+" from outpost"), 0600); err != nil {
			t.Fatal(err)
		}
		outpost.Attn("seed", "attach", seed.ID, "--path", source, "--"+operation, "--json").JSON(t, &attached)
		expected := notebookPath(outpostRoot, "seeds", seed.ID, operation+".md")
		if attached.DestinationPath != expected {
			t.Fatalf("transfer used %s, want %s", attached.DestinationPath, expected)
		}
		if data, err := os.ReadFile(notebookPath(homeRoot, "seeds", seed.ID, operation+".md")); err != nil || string(data) != operation+" from outpost" {
			t.Fatalf("returned before reaching home: %q, %v", data, err)
		}
		_, err := os.Stat(source)
		if operation == "move" && !os.IsNotExist(err) || operation == "copy" && err != nil {
			t.Fatalf("%s source after %s: %v", source, operation, err)
		}
		t.Logf("%s source=%s destination=%s; home received content before command returned", operation, source, attached.DestinationPath)
	}
	shown := showSeed(t, outpost, "", seed.ID)
	if protocol.Deref(shown.ArtifactDir) != notebookPath(outpostRoot, "seeds", seed.ID) {
		t.Fatalf("outpost seed folder: %+v", shown.ArtifactDir)
	}
	requireStdout(t, outpost.Attn("seed", "show", seed.ID), notebookPath(outpostRoot, "seeds", seed.ID))
	requireStdout(t, outpost.Attn("seed", "check", "add", seed.ID, "C1", "Receipt arrived"))
	requireStdout(t, outpost.Attn("seed", "check", "report", seed.ID, "C1", "-m", "Received", "--evidence", "copy.md"))
	destination := filepath.Join(outpost.Dir, "detached.md")
	outpost.Attn("seed", "detach", seed.ID, "--path", "move.md", "--to", destination, "--json").JSON(t, &attached)
	if data, err := os.ReadFile(destination); err != nil || !strings.Contains(string(data), "move from outpost") {
		t.Fatalf("detached destination: %q, %v", data, err)
	}
	if _, err := os.Stat(notebookPath(homeRoot, "seeds", seed.ID, "move.md")); !os.IsNotExist(err) {
		t.Fatalf("detach returned before home removal: %v", err)
	}
	if err := os.Remove(notebookPath(outpostRoot, "seeds", seed.ID, "copy.md")); err != nil {
		t.Fatal(err)
	}
	awaitNotebookFile(t, pair.App, homeRoot, filepath.Join("seeds", seed.ID, "copy.md"), "", true)
	t.Log("detach wrote outpost destination; home removed source before return; check report accepted copy.md; seed show printed outpost artifact folder; external unlink of a copied artifact reached home")
}

func TestNotebookConflictsAndMissingMarkerAreVisibleAfterReconnect(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	homeRoot := filepath.Join(home.Dir, "notebook")
	writeNotebookFile(t, homeRoot, "conflict.md", "baseline")
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	outpostRoot := notebookProfileRoot(t, outpost, pair.App.SelectedProfile())
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "false"))
	requireStdout(t, outpost.Attn("settings", "set", "outpost_listen", ""))
	writeNotebookFile(t, homeRoot, "conflict.md", "home edit")
	writeNotebookFile(t, outpostRoot, "conflict.md", "outpost edit")
	requireStdout(t, outpost.Attn("settings", "set", "outpost_listen", pair.Address))
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "true"))
	testworld.Await(pair.App, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		for _, row := range m.Outposts {
			if row.ID == pair.Host && row.Notebook != nil && protocol.Deref(row.Notebook.Conflicts) == 1 {
				return true
			}
		}
		return false
	})
	conflicts, err := filepath.Glob(notebookPath(homeRoot, "*.sync-conflict-*"))
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("conflict copies: %v, %v", conflicts, err)
	}
	contents := map[string]bool{}
	for _, name := range []string{notebookPath(homeRoot, "conflict.md"), conflicts[0]} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		contents[string(data)] = true
	}
	if !contents["home edit"] || !contents["outpost edit"] {
		t.Fatalf("lost conflict content: %v", contents)
	}
	notifications := testworld.Request(pair.App, protocol.NotificationListMessage{Cmd: protocol.CmdNotificationList, RequestID: protocol.Ptr("notebook-conflicts")}, protocol.EventNotificationListResult, func(r protocol.NotificationListResultMessage) bool { return r.RequestID == "notebook-conflicts" })
	count := 0
	for _, notification := range notifications.Notifications {
		if notification.Kind == "notebook_sync_conflict" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("conflict notification count=%d, want 1", count)
	}
	t.Logf("conflict kept both edits; row count=1, notification count=1; copy=%s", filepath.Base(conflicts[0]))
	if err := os.Remove(conflicts[0]); err != nil {
		t.Fatal(err)
	}
	testworld.Await(pair.App, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		for _, row := range m.Outposts {
			if row.ID == pair.Host && row.Notebook != nil && protocol.Deref(row.Notebook.Conflicts) == 0 {
				return true
			}
		}
		return false
	})
	home.Stop()
	outpost.Stop()
	if err := os.Remove(notebookPath(outpostRoot, ".attn", "notebook-sync")); err != nil {
		t.Fatal(err)
	}
	outpost.Start()
	home.Start()
	pair.App = home.App()
	state := awaitNotebookState(t, pair, protocol.OutpostNotebookStateError)
	if !strings.Contains(protocol.Deref(state.Error), "folder marker missing") {
		t.Fatalf("marker error reason missing: %+v", state)
	}
	if _, err := os.Stat(notebookPath(outpostRoot, ".attn", "notebook-sync")); !os.IsNotExist(err) {
		t.Fatalf("missing marker was recreated: %v", err)
	}
	requireStdout(t, outpost.Attn("settings", "get", "notebook_sync_state"), "error")
	requireStdout(t, outpost.Attn("settings", "get", "notebook_sync_last_error"), "folder marker missing")
	t.Logf("restart preserved sync index; missing marker stayed missing, row error=%s", protocol.Deref(state.Error))
}

func TestNotebookConflictsUnderDotDirectoriesFollowTheFiles(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	homeRoot, outpostRoot := filepath.Join(home.Dir, "notebook"), filepath.Join(outpost.Dir, "notebook")
	writeNotebookFile(t, homeRoot, filepath.Join(".private", "café.sync-conflict-20261027-120000-device.md"), "startup copy")
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	initial := awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	outpostRoot = notebookProfileRoot(t, outpost, pair.App.SelectedProfile())
	if count := protocol.Deref(initial.Conflicts); count != 1 {
		t.Fatalf("startup conflict count=%d, want 1", count)
	}
	pair.App = home.App()
	name := filepath.Join(".private", "note.sync-conflict-20261027-120000-device.md")
	writeNotebookFile(t, homeRoot, name, "conflict copy")
	state := testworld.Await(pair.App, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		_, err := os.Stat(notebookPath(outpostRoot, name))
		return err == nil && len(m.Outposts) == 1 && m.Outposts[0].Notebook != nil && m.Outposts[0].Notebook.State == protocol.OutpostNotebookStateInSync && protocol.Deref(m.Outposts[0].Notebook.Conflicts) == 2
	})
	if count := protocol.Deref(state.Outposts[0].Notebook.Conflicts); count != 2 {
		t.Fatalf("dot-directory conflict count=%d, want 2", count)
	}
	pair.App = home.App()
	if err := os.RemoveAll(notebookPath(homeRoot, ".private")); err != nil {
		t.Fatal(err)
	}
	state = testworld.Await(pair.App, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		_, err := os.Stat(notebookPath(outpostRoot, name))
		return os.IsNotExist(err) && len(m.Outposts) == 1 && m.Outposts[0].Notebook != nil && m.Outposts[0].Notebook.State == protocol.OutpostNotebookStateInSync && protocol.Deref(m.Outposts[0].Notebook.Conflicts) == 0
	})
	if count := protocol.Deref(state.Outposts[0].Notebook.Conflicts); count != 0 {
		t.Fatalf("deleted directory conflict count=%d, want 0", count)
	}
}

func notebookProfileRoot(t *testing.T, stack *testworld.Stack, profile string) string {
	t.Helper()
	response, err := stack.Client().Settings("", profile, "notebook.root.effective", false)
	if err != nil || response == nil || len(response.Entries) != 1 {
		t.Fatalf("Notebook root for %s: %+v %v", profile, response, err)
	}
	return protocol.Deref(response.Entries[0].Value)
}

func TestNotebookSyncKeepsDistinctAndSharedRootsThroughProfileChanges(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	app := home.App()
	defaultID := app.SelectedProfile()
	create := func(name string) *protocol.Profile {
		t.Helper()
		result := testworld.Request(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: name, Name: name}, protocol.EventProfileActionResult, func(m protocol.ProfileActionResultMessage) bool { return m.RequestID == name })
		if !result.Success || result.Profile == nil {
			t.Fatalf("create %s: %+v", name, result)
		}
		return result.Profile
	}
	work, shared := create("Work"), create("Shared")
	defaultRoot := notebookProfileRoot(t, home, defaultID)
	workRoot := filepath.Join(home.Dir, "work-notes")
	requireStdout(t, home.Attn("settings", "set", "notebook.root", workRoot, "--profile", work.ID), workRoot)
	requireStdout(t, home.Attn("settings", "set", "notebook.root", defaultRoot, "--profile", shared.ID), defaultRoot)
	writeNotebookFile(t, defaultRoot, "same.md", "default and shared")
	writeNotebookFile(t, workRoot, "same.md", "work only")
	writeNotebookFile(t, workRoot, "crew/keel/memory/log.jsonl", "kept memory")
	pair.Connect()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	remoteDefault := notebookProfileRoot(t, outpost, defaultID)
	remoteWork := notebookProfileRoot(t, outpost, work.ID)
	if remoteDefault == remoteWork || notebookProfileRoot(t, outpost, shared.ID) != remoteDefault {
		t.Fatal("distinct or shared roots were mixed")
	}
	remote := outpost.TrustedApp()
	watch := func(root string) {
		t.Helper()
		remote.Send(protocol.FsWatchMessage{Cmd: protocol.CmdFsWatch, RequestID: protocol.Ptr(root), Root: protocol.Ptr(root)})
		result := testworld.Await(remote, protocol.EventFsWatchResult, func(m protocol.FsWatchResultMessage) bool { return m.RequestID == root })
		if !result.Success {
			t.Fatal(protocol.Deref(result.Error))
		}
	}
	watch(remoteDefault)
	watch(remoteWork)
	awaitNotebookFile(t, remote, remoteDefault, "same.md", "default and shared", false)
	awaitNotebookFile(t, remote, remoteWork, "same.md", "work only", false)
	awaitNotebookFile(t, remote, remoteWork, "crew/keel/memory/log.jsonl", "kept memory", false)
	third := create("Third")
	pair.App = home.AppOn(third.ID)
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	thirdRoot := notebookProfileRoot(t, home, third.ID)
	remoteThird := notebookProfileRoot(t, outpost, third.ID)
	watch(remoteThird)
	writeNotebookFile(t, thirdRoot, "same.md", "added while connected")
	awaitNotebookFile(t, remote, remoteThird, "same.md", "added while connected", false)
	deleted := testworld.Request(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: "delete-work", ProfileID: work.ID, ExpectedRevision: work.Revision}, protocol.EventProfileActionResult, func(m protocol.ProfileActionResultMessage) bool { return m.RequestID == "delete-work" })
	if !deleted.Success {
		t.Fatalf("delete Work: %+v", deleted)
	}
	home.Stop()
	outpost.Stop()
	outpost.Start()
	home.Start()
	pair.App = home.AppOn(defaultID)
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	for path, want := range map[string]string{filepath.Join(remoteDefault, "same.md"): "default and shared", filepath.Join(remoteThird, "same.md"): "added while connected", filepath.Join(remoteWork, "same.md"): "work only"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Fatalf("retained %s: %q %v", path, data, err)
		}
	}
	if err := os.Remove(filepath.Join(remoteThird, ".attn", "notebook-sync")); err != nil {
		t.Fatal(err)
	}
	pair.App = home.AppOn(third.ID)
	state := awaitNotebookState(t, pair, protocol.OutpostNotebookStateError)
	if !strings.Contains(protocol.Deref(state.Error), "folder marker missing") {
		t.Fatalf("Third error: %+v", state)
	}
	for _, row := range home.AppOn(defaultID).Initial.Outposts {
		if row.ID == pair.Host && (row.Notebook == nil || row.Notebook.State != protocol.OutpostNotebookStateInSync) {
			t.Fatalf("Third's error leaked into Default: %+v", row.Notebook)
		}
	}
	t.Log("distinct roots kept same-named files separate; shared profiles used one folder; crew memory synced; a connected addition and profile deletion survived restart; folder errors stayed profile-scoped")
}

func TestNestedNotebookRootsStayLocalAndRefusePairingOrPairedChanges(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	app := home.App()
	work := testworld.Request(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: "work", Name: "Work"}, protocol.EventProfileActionResult, func(m protocol.ProfileActionResultMessage) bool { return m.RequestID == "work" })
	if !work.Success || work.Profile == nil {
		t.Fatalf("create Work: %+v", work)
	}
	root := notebookProfileRoot(t, home, app.SelectedProfile())
	nested := filepath.Join(root, "nested")
	setRoot := func(path string) testworld.Result {
		return home.Attn("settings", "set", "notebook.root", path, "--profile", work.Profile.ID)
	}
	requireStdout(t, setRoot(nested), nested)
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "true"))
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "must-refuse", Address: "127.0.0.1:1"})
	refused := testworld.Await(app, protocol.EventCommandError, func(m protocol.CommandErrorMessage) bool { return protocol.Deref(m.Cmd) == "add_outpost" })
	for _, want := range []string{"Default", "Work", root, nested, "nested"} {
		if !strings.Contains(refused.Error, want) {
			t.Fatalf("pairing refusal %q lacks %q", refused.Error, want)
		}
	}
	if rows := home.App().Initial.Outposts; len(rows) != 0 {
		t.Fatalf("refused pairing was saved: %+v", rows)
	}
	separate := filepath.Join(home.Dir, "work-notes")
	requireStdout(t, setRoot(separate), separate)
	pair.Connect()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	requireFailure(t, setRoot(nested), "settings:", "Default", "Work", root, nested, "nested")
	selected := home.AppOn(work.Profile.ID)
	selected.Send(protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, RequestID: protocol.Ptr("nested"), Key: "notebook.root", Value: nested})
	changed := testworld.Await(selected, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == "nested" })
	if protocol.Deref(changed.Success) || !strings.Contains(protocol.Deref(changed.Error), root) || !strings.Contains(protocol.Deref(changed.Error), nested) {
		t.Fatalf("app nested root: %+v", changed)
	}
	if actual := notebookProfileRoot(t, home, work.Profile.ID); actual != separate {
		t.Fatalf("refused write changed root to %s", actual)
	}
	pair.App.Send(protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: pair.Host})
	testworld.Await(pair.App, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 0 })
	requireStdout(t, setRoot(nested), nested)
	t.Log("unpaired nesting stayed available; pairing and both CLI/app paired root writes named both profiles and paths; refusal saved no peer or root; removing the peer allowed nesting again")
}
