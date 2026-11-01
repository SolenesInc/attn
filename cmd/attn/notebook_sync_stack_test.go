package main_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func notebookPath(s *testworld.Stack, parts ...string) string {
	return filepath.Join(append([]string{s.Dir, "notebook"}, parts...)...)
}

func writeFileAt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeNotebookFile(t *testing.T, stack *testworld.Stack, name, content string) {
	t.Helper()
	writeFileAt(t, notebookPath(stack, name), content)
}

func outpostCopyPlacement(outpost *testworld.Stack, name, homeFolder string) string {
	sum := sha256.Sum256([]byte(homeFolder))
	return notebookPath(outpost) + "-" + name + "-" + hex.EncodeToString(sum[:])[:8]
}

func reportedCopy(t *testing.T, outpost *testworld.Stack, seed string, profile ...string) string {
	t.Helper()
	var shown protocol.SeedShowResult
	outpost.Attn(append([]string{"seed", "show", seed, "--json"}, profile...)...).JSON(t, &shown)
	dir := protocol.Deref(shown.ArtifactDir)
	if filepath.Base(dir) != seed || filepath.Base(filepath.Dir(dir)) != "seeds" {
		t.Fatalf("the outpost shows seed folder %q for %s", dir, seed)
	}
	return filepath.Dir(filepath.Dir(dir))
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

func fileHolds(path, content string, absent bool) bool {
	data, err := os.ReadFile(path)
	if absent {
		return os.IsNotExist(err)
	}
	return err == nil && string(data) == content
}

func awaitHomeFile(t *testing.T, app *testworld.Peer, home *testworld.Stack, path, content string, absent bool) {
	t.Helper()
	if fileHolds(path, content, absent) {
		return
	}
	defer home.CloseWatchWindows()()
	testworld.Await(app, protocol.EventFsChanged, func(_ protocol.FsChangedMessage) bool { return fileHolds(path, content, absent) })
}

func awaitOutpostFile(t *testing.T, pair *testworld.OutpostPair, path, content string, absent bool) {
	t.Helper()
	if fileHolds(path, content, absent) {
		return
	}
	defer pair.Outpost.CloseWatchWindows()()
	testworld.Await(pair.App, protocol.EventOutpostsUpdated, func(_ protocol.OutpostsUpdatedMessage) bool { return fileHolds(path, content, absent) })
}

func TestNotebookSyncKeepsBothMachinesFilesAndMovesAsideAnUnknownOutpost(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	writeNotebookFile(t, home, "index.md", "home index")
	writeNotebookFile(t, home, "nested/first.md", "first version")
	placement := outpostCopyPlacement(outpost, "notebook", notebookPath(home))
	writeFileAt(t, filepath.Join(placement, "index.md"), "stale outpost copy")
	writeNotebookFile(t, outpost, "index.md", "the outpost's own Notebook")
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
	if !strings.HasPrefix(protocol.Deref(state.PreviousRoot), placement+".before-sync-") {
		t.Fatalf("moved folder %q, want %s moved aside: %+v", protocol.Deref(state.PreviousRoot), placement, state)
	}
	if data, err := os.ReadFile(filepath.Join(*state.PreviousRoot, "index.md")); err != nil || string(data) != "stale outpost copy" {
		t.Fatalf("saved outpost copy: %q, %v", data, err)
	}
	probe := plant(t, home, "Copy probe", "-m", "Shows where the outpost keeps its copy")
	copied := reportedCopy(t, outpost, probe.ID)
	if copied != placement {
		t.Fatalf("the outpost reports its copy at %s, want %s", copied, placement)
	}
	if data, err := os.ReadFile(notebookPath(outpost, "index.md")); err != nil || string(data) != "the outpost's own Notebook" {
		t.Fatalf("the outpost's own Notebook folder changed: %q, %v", data, err)
	}
	for name, content := range map[string]string{"index.md": "home index", filepath.Join("nested", "first.md"): "first version"} {
		if !fileHolds(filepath.Join(copied, name), content, false) {
			t.Fatalf("first sync did not bring %s", name)
		}
	}
	t.Logf("first sync in_sync into %s; moved the stale copy to %s; left the outpost's own Notebook alone", copied, *state.PreviousRoot)

	for _, direction := range []struct {
		from, to, prefix, receiverDir string
		await                         func(path, content string, absent bool)
	}{
		{notebookPath(home), copied, "from-home", outpost.Dir, func(path, content string, absent bool) { awaitOutpostFile(t, pair, path, content, absent) }},
		{copied, notebookPath(home), "from-outpost", home.Dir, func(path, content string, absent bool) { awaitHomeFile(t, pair.App, home, path, content, absent) }},
	} {
		name := direction.prefix + ".md"
		writeFileAt(t, filepath.Join(direction.from, name), "created")
		direction.await(filepath.Join(direction.to, name), "created", false)
		writeFileAt(t, filepath.Join(direction.from, name), "edited")
		direction.await(filepath.Join(direction.to, name), "edited", false)
		if err := os.Remove(filepath.Join(direction.from, name)); err != nil {
			t.Fatal(err)
		}
		direction.await(filepath.Join(direction.to, name), "", true)
		data, err := os.ReadFile(filepath.Join(direction.receiverDir, "notebook-trash", name))
		if err != nil || string(data) != "edited" {
			t.Fatalf("received deletion trash: %q, %v", data, err)
		}
		t.Logf("%s create, edit and delete arrived; receiver kept edited content in notebook-trash", direction.prefix)
	}

	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "false"))
	requireStdout(t, home.Attn("settings", "get", "notebook_sync_state"), "off")
	writeNotebookFile(t, home, "disabled-home.md", "home only")
	writeFileAt(t, filepath.Join(copied, "disabled-outpost.md"), "outpost only")
	requireStdout(t, outpost.Attn("enrollment", "leave"), "no longer enrolled")
	if _, err := os.Stat(filepath.Join(outpost.Dir, "notebook-sync")); !os.IsNotExist(err) {
		t.Fatalf("leave kept sync index: %v", err)
	}
	requireStdout(t, outpost.Attn("settings", "get", "notebook_sync_state"), "off")
	for _, check := range []string{notebookPath(home, "disabled-outpost.md"), filepath.Join(copied, "disabled-home.md")} {
		if _, err := os.Stat(check); !os.IsNotExist(err) {
			t.Fatalf("disabled sync delivered %s: %v", check, err)
		}
	}
	t.Log("Outposts off disconnected home; leave dropped outpost index and retained the copy; listener off reports sync off")
}

func TestOutpostSeedTransfersUseItsNotebookAndWaitForTheHome(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	seed := plant(t, home, "Outpost artifacts", "-m", "Exercise the outpost disk and the home's Garden")
	copied := reportedCopy(t, outpost, seed.ID)
	var attached protocol.SeedArtifactTransferResult
	for _, operation := range []string{"move", "copy"} {
		source := filepath.Join(outpost.Dir, operation+".md")
		if err := os.WriteFile(source, []byte(operation+" from outpost"), 0600); err != nil {
			t.Fatal(err)
		}
		outpost.Attn("seed", "attach", seed.ID, "--path", source, "--"+operation, "--json").JSON(t, &attached)
		expected := filepath.Join(copied, "seeds", seed.ID, operation+".md")
		if attached.DestinationPath != expected {
			t.Fatalf("transfer used %s, want %s", attached.DestinationPath, expected)
		}
		if data, err := os.ReadFile(notebookPath(home, "seeds", seed.ID, operation+".md")); err != nil || string(data) != operation+" from outpost" {
			t.Fatalf("returned before reaching home: %q, %v", data, err)
		}
		_, err := os.Stat(source)
		if operation == "move" && !os.IsNotExist(err) || operation == "copy" && err != nil {
			t.Fatalf("%s source after %s: %v", source, operation, err)
		}
		t.Logf("%s source=%s destination=%s; home received content before command returned", operation, source, attached.DestinationPath)
	}
	changed := filepath.Join(outpost.Dir, "copy.md")
	if err := os.WriteFile(changed, []byte("copy from outpost, changed"), 0600); err != nil {
		t.Fatal(err)
	}
	outpost.Attn("seed", "attach", seed.ID, "--path", changed, "--copy", "--json").JSON(t, &attached)
	if data, err := os.ReadFile(notebookPath(home, "seeds", seed.ID, "copy.md")); !protocol.Deref(attached.Replaced) || err != nil || string(data) != "copy from outpost, changed" {
		t.Fatalf("re-copying the changed source = replaced %t, home holds %q (%v); want the home's file replaced before the command returned", protocol.Deref(attached.Replaced), data, err)
	}
	seedDir := filepath.Join(copied, "seeds", seed.ID)
	requireStdout(t, outpost.Attn("seed", "show", seed.ID), seedDir)
	requireStdout(t, outpost.Attn("seed", "check", "add", seed.ID, "C1", "-m", "Receipt arrived"))
	requireStdout(t, outpost.Attn("seed", "check", "report", seed.ID, "C1", "-m", "Received", "--evidence", "copy.md"))
	proof := filepath.Join(outpost.Dir, "proof.log")
	if err := os.WriteFile(proof, []byte("proof from outpost"), 0600); err != nil {
		t.Fatal(err)
	}
	requireStdout(t, outpost.Attn("seed", "check", "report", seed.ID, "C1", "-m", "Received from the outpost",
		"--evidence", proof, "--evidence", filepath.Join(seedDir, "copy.md")), " · attached proof.log · ")
	if data, err := os.ReadFile(notebookPath(home, "seeds", seed.ID, "proof.log")); err != nil || string(data) != "proof from outpost" {
		t.Fatalf("the outpost's report returned before its file reached home: %q, %v", data, err)
	}
	requireStdout(t, home.Attn("seed", "show", seed.ID), "report  Received from the outpost\n", "proof.log, copy.md · ")
	destination := filepath.Join(outpost.Dir, "detached.md")
	outpost.Attn("seed", "detach", seed.ID, "--path", "move.md", "--to", destination, "--json").JSON(t, &attached)
	if data, err := os.ReadFile(destination); err != nil || !strings.Contains(string(data), "move from outpost") {
		t.Fatalf("detached destination: %q, %v", data, err)
	}
	if _, err := os.Stat(notebookPath(home, "seeds", seed.ID, "move.md")); !os.IsNotExist(err) {
		t.Fatalf("detach returned before home removal: %v", err)
	}
	if err := os.Remove(filepath.Join(seedDir, "copy.md")); err != nil {
		t.Fatal(err)
	}
	awaitHomeFile(t, pair.App, home, notebookPath(home, "seeds", seed.ID, "copy.md"), "", true)
	t.Log("detach wrote outpost destination; home removed source before return; re-copying a changed source replaced the home's file before return; check report accepted copy.md, then copied the outpost's proof.log to home and cited copy.md by its outpost folder path without copying it; seed show printed outpost artifact folder; external unlink of a copied artifact reached home")
}

func TestAnOutpostWorksInItsProfilesOwnNotebookFolder(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	own := filepath.Join(home.Dir, "Profile Notebook")
	writeFileAt(t, filepath.Join(own, "from-home.md"), "written at the home")
	requireStdout(t, home.Attn("settings", "set", "notebook.root", own), "notebook.root = "+own)
	pair.App = home.App()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	seed := plant(t, home, "Profile notebook", "-m", "Its files sync with the outpost")
	copied := reportedCopy(t, outpost, seed.ID)
	if want := outpostCopyPlacement(outpost, "profile-notebook", own); copied != want {
		t.Fatalf("the outpost keeps the profile's folder at %s, want %s", copied, want)
	}
	if data, err := os.ReadFile(filepath.Join(copied, "from-home.md")); err != nil || string(data) != "written at the home" {
		t.Fatalf("the outpost's copy holds %q (%v), want the home's file", data, err)
	}
	writeFileAt(t, filepath.Join(own, "later.md"), "written after the first sync")
	awaitOutpostFile(t, pair, filepath.Join(copied, "later.md"), "written after the first sync", false)
	source := filepath.Join(outpost.Dir, "evidence.md")
	writeFileAt(t, source, "from the outpost")
	var attached protocol.SeedArtifactTransferResult
	outpost.Attn("seed", "attach", seed.ID, "--path", source, "--copy", "--json").JSON(t, &attached)
	if want := filepath.Join(copied, "seeds", seed.ID, "evidence.md"); attached.DestinationPath != want {
		t.Fatalf("attaching from the outpost landed at %s, want %s", attached.DestinationPath, want)
	}
	if data, err := os.ReadFile(filepath.Join(own, "seeds", seed.ID, "evidence.md")); err != nil || string(data) != "from the outpost" {
		t.Fatalf("the attach returned before reaching the profile's folder at the home: %q, %v", data, err)
	}
	requireStdout(t, outpost.Attn("seed", "show", seed.ID), filepath.Join(copied, "seeds", seed.ID))
	shell := home.Spawn(pair.App, fakeagent.Harness(protocol.SessionAgentShell), outpost.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	pair.App.TypeLine(shell, `printf '__notebook_%s\n' "$(basename "$(ATTN_TERMINAL_ID=`+pair.App.Terminal(shell)+` attn settings get notebook.root.effective)")"`)
	pair.App.AwaitScreen(shell, "__notebook_"+filepath.Base(copied))
	if got := outpost.Attn("settings", "get", "notebook.root"); got.Code == 0 || !strings.Contains(got.Stderr, "saved at the home") {
		t.Fatalf("reading a profile's notebook.root on the outpost = %+v, want it named as saved at the home", got)
	}
	if listed := outpost.Attn("settings", "list"); strings.Contains(listed.Stdout, "notebook.root ") {
		t.Fatalf("the outpost lists notebook.root with a value it does not hold:\n%s", listed.Stdout)
	}
	t.Logf("profile folder %s synced to %s; home writes arrived; outpost attach landed in %s before returning; seed show printed the copy's seed folder; an outpost shell's notebook.root.effective names the copy", own, copied, filepath.Join(own, "seeds", seed.ID))
}

func TestChangingANotebookFolderWhilePairedStartsAFreshSyncedFolder(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	writeNotebookFile(t, home, "kept.md", "in the first folder")
	writeNotebookFile(t, home, "deleted-later.md", "deleted while unsynced")
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	seed := plant(t, home, "Folder change", "-m", "Follow the profile's Notebook folder")
	first := reportedCopy(t, outpost, seed.ID)
	if data, err := os.ReadFile(filepath.Join(first, "deleted-later.md")); err != nil || string(data) != "deleted while unsynced" {
		t.Fatalf("first copy: %q, %v", data, err)
	}

	second := filepath.Join(home.Dir, "second")
	writeFileAt(t, filepath.Join(second, "second.md"), "in the second folder")
	requireStdout(t, home.Attn("settings", "set", "notebook.root", second))
	pair.App = home.App()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	moved := reportedCopy(t, outpost, seed.ID)
	if moved == first {
		t.Fatalf("the new folder reuses the old copy %s", first)
	}
	if data, err := os.ReadFile(filepath.Join(moved, "second.md")); err != nil || string(data) != "in the second folder" {
		t.Fatalf("the new folder did not reach the outpost: %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(first, "kept.md")); err != nil || string(data) != "in the first folder" {
		t.Fatalf("the old copy lost its files: %q, %v", data, err)
	}
	writeNotebookFile(t, home, "after-change.md", "written after the change")
	if err := os.Remove(notebookPath(home, "deleted-later.md")); err != nil {
		t.Fatal(err)
	}
	writeFileAt(t, filepath.Join(second, "barrier.md"), "synced after the old folder's write")
	awaitOutpostFile(t, pair, filepath.Join(moved, "barrier.md"), "synced after the old folder's write", false)
	if _, err := os.Stat(filepath.Join(first, "after-change.md")); !os.IsNotExist(err) {
		t.Fatalf("the old copy still syncs: %v", err)
	}
	t.Logf("changed folder synced into %s; old copy %s kept its files and stopped syncing", moved, first)

	requireStdout(t, home.Attn("settings", "set", "notebook.root", ""))
	pair.App = home.App()
	state := awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	if back := reportedCopy(t, outpost, seed.ID); back != first {
		t.Fatalf("pointing back synced into %s, want %s", back, first)
	}
	if !strings.Contains(protocol.Deref(state.PreviousRoot), first+".before-sync-") {
		t.Fatalf("pointing back did not move the stale copy aside: %+v", state)
	}
	aside := ""
	for _, candidate := range strings.Split(*state.PreviousRoot, ", ") {
		if strings.HasPrefix(candidate, first+".before-sync-") {
			aside = candidate
		}
	}
	if data, err := os.ReadFile(filepath.Join(aside, "deleted-later.md")); err != nil || string(data) != "deleted while unsynced" {
		t.Fatalf("the stale copy was not kept aside: %q, %v", data, err)
	}
	for _, path := range []string{notebookPath(home, "deleted-later.md"), filepath.Join(first, "deleted-later.md")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("a file deleted at the home came back at %s: %v", path, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(first, "after-change.md")); err != nil || string(data) != "written after the change" {
		t.Fatalf("the fresh copy misses the home's files: %q, %v", data, err)
	}
	t.Logf("pointing back moved the stale copy to %s and synced %s fresh; the deleted file stayed deleted", aside, first)
}

func TestAFolderThatLeftAndCameBackWhileTheOutpostWasOfflineKeepsTheHomesDeletions(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	writeNotebookFile(t, home, "deleted-offline.md", "deleted while the outpost was away")
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	first := reportedCopy(t, outpost, plant(t, home, "Offline folder change").ID)
	if !fileHolds(filepath.Join(first, "deleted-offline.md"), "deleted while the outpost was away", false) {
		t.Fatalf("the first sync missed deleted-offline.md in %s", first)
	}

	lost := pair.App.EventCount()
	pair.Disconnect()
	outpostStateSince(pair.App, "outpost", protocol.OutpostStateLost, lost)
	requireStdout(t, home.Attn("settings", "set", "notebook.root", filepath.Join(home.Dir, "second")))
	if err := os.Remove(notebookPath(home, "deleted-offline.md")); err != nil {
		t.Fatal(err)
	}
	writeNotebookFile(t, home, "while-away.md", "written while the outpost was away")
	requireStdout(t, home.Attn("settings", "set", "notebook.root", ""))

	pair.Reconnect()
	home.Advance(bridge.ReconnectInterval)
	awaitOutpostFile(t, pair, filepath.Join(first, "while-away.md"), "written while the outpost was away", false)
	pair.App = home.App()
	state := awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	for _, path := range []string{notebookPath(home, "deleted-offline.md"), filepath.Join(first, "deleted-offline.md")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("a file deleted at the home while its folder was out of the set came back at %s: %v", path, err)
		}
	}
	aside := ""
	for _, candidate := range strings.Split(protocol.Deref(state.PreviousRoot), ", ") {
		if strings.HasPrefix(candidate, first+".before-sync-") {
			aside = candidate
		}
	}
	if !fileHolds(filepath.Join(aside, "deleted-offline.md"), "deleted while the outpost was away", false) {
		t.Fatalf("the outpost's stale copy was not moved aside: %+v", state)
	}
	t.Logf("the returning folder synced %s fresh, kept the stale copy at %s, and deleted-offline.md stayed deleted on both sides", first, aside)
}

func TestAnOutpostThatRejectedTheFolderListSyncsOnceTheCauseClears(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	streamOpens := home.PauseAt(pausepoint.NotebookStreamOpen)
	state := filepath.Join(outpost.Dir, "notebook-sync")
	if err := os.MkdirAll(state, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(state, 0700) })
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	rejected := awaitNotebookError(t, pair, "outpost rejected the Notebook folder list", "permission denied")
	writeNotebookFile(t, home, "before-recovery.md", "written while the outpost rejected the list")

	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	home.AwaitTimer(daemon.NotebookFoldersRetryTimer)
	home.Advance(bridge.ReconnectInterval)
	streamOpens.Await()
	installed, err := os.ReadFile(filepath.Join(state, "state.json"))
	if err != nil || !strings.Contains(string(installed), notebookPath(outpost)) {
		t.Fatalf("the Notebook stream opened before the outpost installed the folder list: %q (%v)", installed, err)
	}
	streamOpens.Release()
	pair.App = home.App()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	copied := outpostCopyPlacement(outpost, "notebook", notebookPath(home))
	awaitOutpostFile(t, pair, filepath.Join(copied, "before-recovery.md"), "written while the outpost rejected the list", false)
	t.Logf("the row showed %q; once the folder was writable again, one retry installed the list before the stream opened, and before-recovery.md reached %s", rejected, copied)
}

func awaitNotebookError(t *testing.T, pair *testworld.OutpostPair, want ...string) string {
	t.Helper()
	names := func(row protocol.OutpostInfo) bool {
		if row.ID != pair.Host || row.Notebook == nil || row.Notebook.State != protocol.OutpostNotebookStateError {
			return false
		}
		for _, w := range want {
			if !strings.Contains(protocol.Deref(row.Notebook.Error), w) {
				return false
			}
		}
		return true
	}
	for _, row := range pair.App.Initial.Outposts {
		if names(row) {
			return protocol.Deref(row.Notebook.Error)
		}
	}
	message := testworld.Await(pair.App, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		return slices.ContainsFunc(m.Outposts, names)
	})
	i := slices.IndexFunc(message.Outposts, names)
	return protocol.Deref(message.Outposts[i].Notebook.Error)
}

func TestANestedNotebookFolderStaysOutOfSyncWhileTheOthersSync(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	outpost.Start()
	home.Start()
	app := home.App()
	createProfile := func(name string) string {
		t.Helper()
		created := testworld.Request(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: name, Name: name}, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == name })
		if !created.Success {
			t.Fatalf("create %s: %+v", name, created)
		}
		return created.Profile.ID
	}
	sideID := createProfile("Side")
	defaultFolder, inner := notebookPath(home), notebookPath(home, "inner")
	requireStdout(t, home.Attn("settings", "set", "notebook.root", inner, "--profile", "Side"))
	pair.Connect()
	pairing := awaitNotebookError(t, pair, "Side", inner, "Default", defaultFolder)
	writeFileAt(t, filepath.Join(defaultFolder, "outer.md"), "the outer folder syncs")
	awaitOutpostFile(t, pair, filepath.Join(outpostCopyPlacement(outpost, "notebook", defaultFolder), "outer.md"), "the outer folder syncs", false)
	launch, _, _ := home.RequestSpawn(pair.App, fakeagent.Claude, outpost.Path("side"), func(m *protocol.SpawnSessionMessage) {
		m.Host, m.ProfileID = protocol.Ptr(pair.Host), sideID
	})
	if launch.Success || !strings.Contains(protocol.Deref(launch.Error), "does not sync with outpost") || !strings.Contains(protocol.Deref(launch.Error), inner) {
		t.Fatalf("launching Side's agent on the outpost: success=%v error=%q, want a refusal naming its left-out folder", launch.Success, protocol.Deref(launch.Error))
	}

	requireStdout(t, home.Attn("settings", "set", "notebook.root", defaultFolder, "--profile", "Side"))
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	copies, err := filepath.Glob(notebookPath(outpost) + "-*-*")
	if want := []string{outpostCopyPlacement(outpost, "inner", inner), outpostCopyPlacement(outpost, "notebook", defaultFolder)}; err != nil || !slices.Equal(copies, want) {
		t.Fatalf("two profiles on one folder left copies %v (%v), want the old inner copy and one shared copy %v", copies, err, want)
	}
	refused := home.Attn("settings", "set", "notebook.root", inner, "--profile", "Side")
	requireFailure(t, refused, "settings: ", "Side", inner, "Default", defaultFolder)

	side, child := filepath.Join(home.Dir, "side-notebook"), notebookPath(home, "child")
	requireStdout(t, home.Attn("settings", "set", "notebook.root", side, "--profile", "Side"))
	requireStdout(t, home.Attn("settings", "set", "notebook.root", child, "--profile", "Default"))
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	createProfile("Fresh")
	fresh := awaitNotebookError(t, pair, "Default", child, "Fresh", defaultFolder)
	writeFileAt(t, filepath.Join(side, "side.md"), "another folder keeps syncing")
	awaitOutpostFile(t, pair, filepath.Join(outpostCopyPlacement(outpost, "side-notebook", side), "side.md"), "another folder keeps syncing", false)
	t.Logf("pairing with a nested folder synced the outer one and reported: %s; an equal folder synced once into %s; nesting by a setting write while paired was refused: %s; a new profile on the default folder left only the inner folder out: %s", pairing, copies[1], strings.TrimSpace(refused.Stderr), fresh)
}

func TestAFileDeletedAtTheHomeWhileItsFolderWasNestedStaysDeleted(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	outpost.Start()
	home.Start()
	inner, outer := notebookPath(home, "inner"), notebookPath(home)
	requireStdout(t, home.Attn("settings", "set", "notebook.root", filepath.Join(home.Dir, "default-notebook")))
	created := testworld.Request(home.App(), protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: "side", Name: "Side"}, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "side" })
	if !created.Success {
		t.Fatalf("create Side: %+v", created)
	}
	requireStdout(t, home.Attn("settings", "set", "notebook.root", inner, "--profile", "Side"))
	writeFileAt(t, filepath.Join(inner, "deleted.md"), "deleted at the home while nested")
	pair.Connect()
	copied := outpostCopyPlacement(outpost, "inner", inner)
	awaitOutpostFile(t, pair, filepath.Join(copied, "deleted.md"), "deleted at the home while nested", false)

	created = testworld.Request(pair.App, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: "fresh", Name: "Fresh"}, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "fresh" })
	if !created.Success {
		t.Fatalf("create Fresh: %+v", created)
	}
	awaitNotebookError(t, pair, "Side", inner, "Fresh", outer)
	if err := os.Remove(filepath.Join(inner, "deleted.md")); err != nil {
		t.Fatal(err)
	}
	home.Stop()
	home.Start()
	pair.App = home.App()
	nested := awaitNotebookError(t, pair, "Side", inner, "Fresh", outer)

	requireStdout(t, home.Attn("settings", "set", "notebook.root", filepath.Join(home.Dir, "fresh-notebook"), "--profile", "Fresh"))
	writeFileAt(t, filepath.Join(inner, "after.md"), "written once the nesting resolved")
	awaitOutpostFile(t, pair, filepath.Join(copied, "after.md"), "written once the nesting resolved", false)
	awaitOutpostFile(t, pair, filepath.Join(copied, "deleted.md"), "", true)
	pair.App = home.App()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	if _, err := os.Stat(filepath.Join(inner, "deleted.md")); !os.IsNotExist(err) {
		t.Fatalf("a file deleted at the home while its folder was nested came back at the home: %v", err)
	}
	t.Logf("the folder reported %q across a home restart, and deleted.md stayed deleted at %s and %s once the nesting resolved", nested, inner, copied)
}

func TestADeadNotebookStreamCannotOverwriteItsReplacement(t *testing.T) {
	for _, side := range []string{"home", "outpost"} {
		t.Run(side, func(t *testing.T) {
			home, outpost := testworld.NewStack(t), testworld.NewStack(t)
			target := home
			if side == "outpost" {
				target = outpost
			}
			closed := target.PauseAt(pausepoint.BridgeConnectionClosed)
			pending := target.PauseAt(pausepoint.NotebookStreamError)
			handled := target.PauseAt(pausepoint.NotebookStreamErrorHandled)
			pair := testworld.PrepareOutpostPair(t, home, outpost)
			pair.Start()
			awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
			seed := plant(t, home, "Reconnect receipt", "-m", "Attach through the replacement Notebook stream")
			copied := reportedCopy(t, outpost, seed.ID)

			pair.Disconnect()
			closed.Await()
			pending.Await()
			closed.Release()
			writeNotebookFile(t, home, "replacement.md", "synced by the replacement stream")
			home.AwaitTimer(bridge.RetryTimer)
			pair.Reconnect()
			home.Advance(bridge.ReconnectInterval)
			awaitOutpostFile(t, pair, filepath.Join(copied, "replacement.md"), "synced by the replacement stream", false)
			pair.App = home.App()
			awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
			pending.Release()
			handled.Await()
			pair.App = home.App()
			if rows := pair.App.Initial.Outposts; len(rows) != 1 || rows[0].Notebook == nil || rows[0].Notebook.State == protocol.OutpostNotebookStateError || rows[0].Notebook.Error != nil {
				t.Fatalf("dead %s stream overwrote its replacement: %+v", side, rows)
			}
			handled.Release()
			for _, stack := range []*testworld.Stack{home, outpost} {
				result := stack.Attn("settings", "get", "notebook_sync_last_error")
				requireStdout(t, result)
				if strings.TrimSpace(result.Stdout) != "" {
					t.Fatalf("dead %s stream left a Notebook error: %s", side, result.Stdout)
				}
			}
			source := filepath.Join(outpost.Dir, "receipt.md")
			if err := os.WriteFile(source, []byte("receipt after reconnect"), 0600); err != nil {
				t.Fatal(err)
			}
			requireStdout(t, outpost.Attn("seed", "attach", seed.ID, "--path", source, "--copy"))
			if data, err := os.ReadFile(notebookPath(home, "seeds", seed.ID, "receipt.md")); err != nil || string(data) != "receipt after reconnect" {
				t.Fatalf("first attach did not reach home: %q, %v", data, err)
			}
			t.Logf("%s: replacement synced replacement.md; delayed old error handled; fresh row and both CLI errors clear; first artifact attach reached home", side)
		})
	}
}

func TestNotebookMarkerErrorReachesHomeBeforeOutpostBridgeRegistration(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	writeNotebookFile(t, home, "index.md", "kept through reconnect")
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	copied := reportedCopy(t, outpost, plant(t, home, "Copy probe").ID)
	home.Stop()
	outpost.Stop()
	if err := os.Remove(filepath.Join(copied, ".attn", "notebook-sync")); err != nil {
		t.Fatal(err)
	}
	accept := outpost.PauseAt(pausepoint.OutpostBridgeAccept)
	reported := outpost.PauseAt(pausepoint.NotebookReportSent)
	outpost.Start()
	remoteApp := outpost.App()
	if reason, _ := remoteApp.Initial.Settings["notebook_sync_last_error"].(string); !strings.Contains(reason, "folder marker missing") {
		testworld.Await(remoteApp, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
			reason, _ := m.Settings["notebook_sync_last_error"].(string)
			return strings.Contains(reason, "folder marker missing")
		})
	}
	home.Start()
	accept.Await()
	reported.Await()
	rows := home.App().Initial.Outposts
	if len(rows) != 1 || rows[0].Notebook == nil || rows[0].Notebook.State != protocol.OutpostNotebookStateError || !strings.Contains(protocol.Deref(rows[0].Notebook.Error), "folder marker missing") {
		t.Fatalf("first report did not expose marker error before bridge registration: %+v", rows)
	}
	t.Logf("before outpost bridge registration, home row reports %s", protocol.Deref(rows[0].Notebook.Error))
	reported.Release()
	accept.Release()
	if _, err := os.Stat(filepath.Join(copied, ".attn", "notebook-sync")); !os.IsNotExist(err) {
		t.Fatalf("missing marker was recreated: %v", err)
	}
}

func TestNotebookConflictsAndMissingMarkerAreVisibleAfterReconnect(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	writeNotebookFile(t, home, "conflict.md", "baseline")
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	copied := reportedCopy(t, outpost, plant(t, home, "Copy probe").ID)
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "false"))
	writeNotebookFile(t, home, "conflict.md", "home edit")
	writeFileAt(t, filepath.Join(copied, "conflict.md"), "outpost edit")
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "true"))
	testworld.Await(pair.App, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		for _, row := range m.Outposts {
			if row.ID == pair.Host && row.Notebook != nil && protocol.Deref(row.Notebook.Conflicts) == 1 {
				return true
			}
		}
		return false
	})
	conflicts, err := filepath.Glob(notebookPath(home, "*.sync-conflict-*"))
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("conflict copies: %v, %v", conflicts, err)
	}
	contents := map[string]bool{}
	for _, name := range []string{notebookPath(home, "conflict.md"), conflicts[0]} {
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
	if err := os.Remove(filepath.Join(copied, ".attn", "notebook-sync")); err != nil {
		t.Fatal(err)
	}
	outpost.Start()
	home.Start()
	pair.App = home.App()
	state := awaitNotebookState(t, pair, protocol.OutpostNotebookStateError)
	if !strings.Contains(protocol.Deref(state.Error), "folder marker missing") {
		t.Fatalf("marker error reason missing: %+v", state)
	}
	if _, err := os.Stat(filepath.Join(copied, ".attn", "notebook-sync")); !os.IsNotExist(err) {
		t.Fatalf("missing marker was recreated: %v", err)
	}
	requireStdout(t, outpost.Attn("settings", "get", "notebook_sync_state"), "error")
	requireStdout(t, outpost.Attn("settings", "get", "notebook_sync_last_error"), "folder marker missing")
	t.Logf("restart preserved sync index; missing marker stayed missing, row error=%s", protocol.Deref(state.Error))
}

func TestNotebookConflictsUnderDotDirectoriesFollowTheFiles(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	writeNotebookFile(t, home, filepath.Join(".private", "café.sync-conflict-20261027-120000-device.md"), "startup copy")
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	initial := awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	if count := protocol.Deref(initial.Conflicts); count != 1 {
		t.Fatalf("startup conflict count=%d, want 1", count)
	}
	copied := reportedCopy(t, outpost, plant(t, home, "Copy probe").ID)
	pair.App = home.App()
	name := filepath.Join(".private", "note.sync-conflict-20261027-120000-device.md")
	writeNotebookFile(t, home, name, "conflict copy")
	state := testworld.Await(pair.App, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		_, err := os.Stat(filepath.Join(copied, name))
		return err == nil && len(m.Outposts) == 1 && m.Outposts[0].Notebook != nil && m.Outposts[0].Notebook.State == protocol.OutpostNotebookStateInSync && protocol.Deref(m.Outposts[0].Notebook.Conflicts) == 2
	})
	if count := protocol.Deref(state.Outposts[0].Notebook.Conflicts); count != 2 {
		t.Fatalf("dot-directory conflict count=%d, want 2", count)
	}
	pair.App = home.App()
	if err := os.RemoveAll(notebookPath(home, ".private")); err != nil {
		t.Fatal(err)
	}
	state = testworld.Await(pair.App, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		_, err := os.Stat(filepath.Join(copied, name))
		return os.IsNotExist(err) && len(m.Outposts) == 1 && m.Outposts[0].Notebook != nil && m.Outposts[0].Notebook.State == protocol.OutpostNotebookStateInSync && protocol.Deref(m.Outposts[0].Notebook.Conflicts) == 0
	})
	if count := protocol.Deref(state.Outposts[0].Notebook.Conflicts); count != 0 {
		t.Fatalf("deleted directory conflict count=%d, want 0", count)
	}
}

func TestOneProfilesNotebookFolderFailsAloneAndKeepsItsFileNamesToItself(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	outpost.Start()
	home.Start()
	app := home.App()
	defaultProfile := app.SelectedProfile()
	created := testworld.Request(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: "side", Name: "Side"}, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "side" })
	if !created.Success {
		t.Fatalf("create Side: %+v", created)
	}
	side := filepath.Join(home.Dir, "side-notebook")
	writeFileAt(t, filepath.Join(side, "secret plan.sync-conflict-20261027-120000-device.md"), "a conflict copy")
	requireStdout(t, home.Attn("settings", "set", "notebook.root", side, "--profile", "Side"))
	blocked := outpostCopyPlacement(outpost, "side-notebook", side)
	writeFileAt(t, blocked, "a file where the copy belongs")
	pair.Connect()

	state := awaitNotebookState(t, pair, protocol.OutpostNotebookStateError)
	if !strings.Contains(protocol.Deref(state.Error), blocked) {
		t.Fatalf("the error does not name the blocked copy %s: %s", blocked, protocol.Deref(state.Error))
	}
	defaultCopy := outpostCopyPlacement(outpost, "notebook", notebookPath(home))
	writeFileAt(t, filepath.Join(defaultCopy, "from-outpost.md"), "written in Default's copy")
	awaitHomeFile(t, home.AppOn(defaultProfile), home, notebookPath(home, "from-outpost.md"), "written in Default's copy", false)
	notifications := testworld.Request(pair.App, protocol.NotificationListMessage{Cmd: protocol.CmdNotificationList, RequestID: protocol.Ptr("conflicts")}, protocol.EventNotificationListResult, func(r protocol.NotificationListResultMessage) bool { return r.RequestID == "conflicts" })
	found := false
	for _, notification := range notifications.Notifications {
		if notification.Kind != "notebook_sync_conflict" {
			continue
		}
		found = true
		if strings.Contains(notification.Title+notification.Body+notification.SourceID, "secret plan") || strings.Contains(notification.Body, side) || strings.Contains(notification.Body, "Side") {
			t.Fatalf("a conflict in Side's own folder notifies every profile with %+v", notification)
		}
	}
	if !found {
		t.Fatal("no notification for the conflict copy in Side's folder")
	}
	t.Logf("Side's blocked copy failed alone (%s); Default's folder synced; the conflict notification names neither the file nor the profile", protocol.Deref(state.Error))

	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	home.Stop()
	outpost.Stop()
	outpost.Start()
	home.Start()
	pair.App = home.App()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	if !fileHolds(filepath.Join(blocked, "secret plan.sync-conflict-20261027-120000-device.md"), "a conflict copy", false) {
		t.Fatal("Side's folder did not sync after its copy was cleared")
	}
	t.Log("after the blocking file was removed and both daemons restarted, Side's folder synced")
}
