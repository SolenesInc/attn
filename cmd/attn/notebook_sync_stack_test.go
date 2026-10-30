package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func notebookPath(s *testworld.Stack, parts ...string) string {
	return filepath.Join(append([]string{s.Dir, "notebook"}, parts...)...)
}

func writeNotebookFile(t *testing.T, stack *testworld.Stack, name, content string) {
	t.Helper()
	path := filepath.Join(stack.Dir, "notebook", name)
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

func awaitNotebookFile(t *testing.T, peer *testworld.Peer, stack *testworld.Stack, path, content string, absent bool) {
	t.Helper()
	matches := func() bool {
		data, err := os.ReadFile(filepath.Join(stack.Dir, "notebook", path))
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
	writeNotebookFile(t, home, "index.md", "home index")
	writeNotebookFile(t, home, "nested/first.md", "first version")
	writeNotebookFile(t, outpost, "index.md", "stale outpost scaffold")
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
	remoteApp := outpost.App()
	state := awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	if protocol.Deref(state.PreviousRoot) == "" {
		t.Fatalf("moved folder not named: %+v", state)
	}
	if data, err := os.ReadFile(filepath.Join(*state.PreviousRoot, "index.md")); err != nil || string(data) != "stale outpost scaffold" {
		t.Fatalf("saved outpost: %q, %v", data, err)
	}
	awaitNotebookFile(t, remoteApp, outpost, "index.md", "home index", false)
	awaitNotebookFile(t, remoteApp, outpost, "nested/first.md", "first version", false)
	t.Logf("first sync in_sync; moved stale outpost to %s", *state.PreviousRoot)

	for _, direction := range []struct {
		from, to *testworld.Stack
		peer     *testworld.Peer
		prefix   string
	}{
		{home, outpost, remoteApp, "from-home"}, {outpost, home, pair.App, "from-outpost"},
	} {
		name := direction.prefix + ".md"
		writeNotebookFile(t, direction.from, name, "created")
		awaitNotebookFile(t, direction.peer, direction.to, name, "created", false)
		writeNotebookFile(t, direction.from, name, "edited")
		awaitNotebookFile(t, direction.peer, direction.to, name, "edited", false)
		if err := os.Remove(notebookPath(direction.from, name)); err != nil {
			t.Fatal(err)
		}
		awaitNotebookFile(t, direction.peer, direction.to, name, "", true)
		data, err := os.ReadFile(filepath.Join(direction.to.Dir, "notebook-trash", name))
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
	writeNotebookFile(t, home, "disabled-home.md", "home only")
	writeNotebookFile(t, outpost, "disabled-outpost.md", "outpost only")
	requireStdout(t, outpost.Attn("enrollment", "leave"), "no longer enrolled")
	if _, err := os.Stat(filepath.Join(outpost.Dir, "notebook-sync")); !os.IsNotExist(err) {
		t.Fatalf("leave kept sync index: %v", err)
	}
	requireStdout(t, outpost.Attn("settings", "get", "notebook_sync_state"), "off")
	for _, check := range []struct {
		stack *testworld.Stack
		name  string
	}{{home, "disabled-outpost.md"}, {outpost, "disabled-home.md"}} {
		if _, err := os.Stat(notebookPath(check.stack, check.name)); !os.IsNotExist(err) {
			t.Fatalf("disabled sync received %s: %v", check.name, err)
		}
	}
	t.Log("Outposts off disconnected home; leave dropped outpost index and retained Notebook; listener off reports sync off")
}

func TestOutpostSeedTransfersUseItsNotebookAndWaitForTheHome(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	seed := plant(t, home, "Outpost artifacts", "-m", "Exercise the outpost disk and the home's Garden")
	var attached protocol.SeedArtifactTransferResult
	for _, operation := range []string{"move", "copy"} {
		source := filepath.Join(outpost.Dir, operation+".md")
		if err := os.WriteFile(source, []byte(operation+" from outpost"), 0600); err != nil {
			t.Fatal(err)
		}
		outpost.Attn("seed", "attach", seed.ID, "--path", source, "--"+operation, "--json").JSON(t, &attached)
		expected := notebookPath(outpost, "seeds", seed.ID, operation+".md")
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
	shown := showSeed(t, outpost, "", seed.ID)
	if protocol.Deref(shown.ArtifactDir) != notebookPath(outpost, "seeds", seed.ID) {
		t.Fatalf("outpost seed folder: %+v", shown.ArtifactDir)
	}
	requireStdout(t, outpost.Attn("seed", "show", seed.ID), notebookPath(outpost, "seeds", seed.ID))
	requireStdout(t, outpost.Attn("seed", "check", "add", seed.ID, "C1", "Receipt arrived"))
	requireStdout(t, outpost.Attn("seed", "check", "report", seed.ID, "C1", "-m", "Received", "--evidence", "copy.md"))
	destination := filepath.Join(outpost.Dir, "detached.md")
	outpost.Attn("seed", "detach", seed.ID, "--path", "move.md", "--to", destination, "--json").JSON(t, &attached)
	if data, err := os.ReadFile(destination); err != nil || !strings.Contains(string(data), "move from outpost") {
		t.Fatalf("detached destination: %q, %v", data, err)
	}
	if _, err := os.Stat(notebookPath(home, "seeds", seed.ID, "move.md")); !os.IsNotExist(err) {
		t.Fatalf("detach returned before home removal: %v", err)
	}
	if err := os.Remove(notebookPath(outpost, "seeds", seed.ID, "copy.md")); err != nil {
		t.Fatal(err)
	}
	awaitNotebookFile(t, pair.App, home, filepath.Join("seeds", seed.ID, "copy.md"), "", true)
	t.Log("detach wrote outpost destination; home removed source before return; re-copying a changed source replaced the home's file before return; check report accepted copy.md; seed show printed outpost artifact folder; external unlink of a copied artifact reached home")
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
			remoteApp := outpost.App()

			pair.Disconnect()
			closed.Await()
			pending.Await()
			closed.Release()
			writeNotebookFile(t, home, "replacement.md", "synced by the replacement stream")
			home.AwaitTimer(bridge.ReconnectInterval)
			pair.Reconnect()
			home.Advance(bridge.ReconnectInterval)
			awaitNotebookFile(t, remoteApp, outpost, "replacement.md", "synced by the replacement stream", false)
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
	home.Stop()
	outpost.Stop()
	if err := os.Remove(notebookPath(outpost, ".attn", "notebook-sync")); err != nil {
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
	if _, err := os.Stat(notebookPath(outpost, ".attn", "notebook-sync")); !os.IsNotExist(err) {
		t.Fatalf("missing marker was recreated: %v", err)
	}
}

func TestNotebookConflictsAndMissingMarkerAreVisibleAfterReconnect(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	writeNotebookFile(t, home, "conflict.md", "baseline")
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "false"))
	writeNotebookFile(t, home, "conflict.md", "home edit")
	writeNotebookFile(t, outpost, "conflict.md", "outpost edit")
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
	if err := os.Remove(notebookPath(outpost, ".attn", "notebook-sync")); err != nil {
		t.Fatal(err)
	}
	outpost.Start()
	home.Start()
	pair.App = home.App()
	state := awaitNotebookState(t, pair, protocol.OutpostNotebookStateError)
	if !strings.Contains(protocol.Deref(state.Error), "folder marker missing") {
		t.Fatalf("marker error reason missing: %+v", state)
	}
	if _, err := os.Stat(notebookPath(outpost, ".attn", "notebook-sync")); !os.IsNotExist(err) {
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
	pair.App = home.App()
	name := filepath.Join(".private", "note.sync-conflict-20261027-120000-device.md")
	writeNotebookFile(t, home, name, "conflict copy")
	state := testworld.Await(pair.App, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		_, err := os.Stat(notebookPath(outpost, name))
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
		_, err := os.Stat(notebookPath(outpost, name))
		return os.IsNotExist(err) && len(m.Outposts) == 1 && m.Outposts[0].Notebook != nil && m.Outposts[0].Notebook.State == protocol.OutpostNotebookStateInSync && protocol.Deref(m.Outposts[0].Notebook.Conflicts) == 0
	})
	if count := protocol.Deref(state.Outposts[0].Notebook.Conflicts); count != 0 {
		t.Fatalf("deleted directory conflict count=%d, want 0", count)
	}
}
