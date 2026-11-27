package main_test

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const buildBeforeHomeIDs = "90dd08587830aa15e6c2158d10f006dfc325f5e4"

func enrollmentOf(t *testing.T, s *testworld.Stack) enrollmentJSON {
	t.Helper()
	var status enrollmentJSON
	s.Attn("enrollment", "status", "--json").JSON(t, &status)
	return status
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func copyHomeData(t *testing.T, from, to string) {
	t.Helper()
	for _, name := range []string{"attn.db", "attn.db-wal", "attn.db-shm", enrollment.RecordFileName, filepath.Join("notebook-sync", "state.json")} {
		data, err := os.ReadFile(filepath.Join(from, name))
		if os.IsNotExist(err) && strings.HasPrefix(name, "attn.db-") {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(to, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(to, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

type notebookSyncState struct {
	Home, HomeDevice string
	Folders          map[string]json.RawMessage
}

func notebookSyncStateOf(t *testing.T, s *testworld.Stack) notebookSyncState {
	t.Helper()
	var state notebookSyncState
	if err := json.Unmarshal(readFile(t, filepath.Join(s.Dir, "notebook-sync", "state.json")), &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func awaitOutpostNotebookInSync(t *testing.T, app *testworld.Peer, host string) {
	t.Helper()
	inSync := func(rows []protocol.OutpostInfo) bool {
		for _, row := range rows {
			if row.ID == host && row.Notebook != nil && row.Notebook.State == protocol.OutpostNotebookStateInSync {
				return true
			}
		}
		return false
	}
	if inSync(outpostsShownAt(app, len(app.Log()))) {
		return
	}
	testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return inSync(m.Outposts) })
}

func requireNoCopyMovedAside(t *testing.T, copied string) {
	t.Helper()
	aside, err := filepath.Glob(copied + ".before-sync-*")
	if err != nil || len(aside) != 0 {
		t.Fatalf("the outpost moved its Notebook copy aside: %v %v", aside, err)
	}
}

func TestAHomeIDOutlivesTheDaemonThatRanTheHome(t *testing.T) {
	previous := testworld.AttnBinaryFromCommit(t, buildBeforeHomeIDs)
	current := testworld.AttnUpdateBinary(t, "home-ids", "source", "")
	ssh := testworld.NewSSH(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	remote.RunFrom(previous)
	home.RunFrom(previous)
	home.InstallsOnOutposts(previous, "")
	ssh.Use(home.World)
	ssh.Route("outpost", remote)
	notebookRoot := notebookPath(home)
	writeFileAt(t, filepath.Join(notebookRoot, "index.md"), "written before home ids")
	copied := outpostCopyPlacement(remote, "notebook", notebookRoot)
	t.Cleanup(func() {
		if t.Failed() {
			remote.LogDaemonTail()
		}
	})
	home.StartRelease()
	app := enableOutposts(t, home)
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: "outpost"})
	row := awaitOutpostInstall(app)
	if row.Update.Phase != protocol.OutpostUpdatePhaseDone || row.State != protocol.OutpostStateConnected {
		t.Fatalf("Add on the build before home ids: %+v %+v", row, row.Update)
	}
	host := row.ID
	awaitOutpostNotebookInSync(t, app, host)
	if !fileHolds(filepath.Join(copied, "index.md"), "written before home ids", false) {
		t.Fatalf("the build before home ids did not sync the Notebook into %s", copied)
	}
	folders := notebookSyncStateOf(t, remote).Folders
	remoteApp := remote.App()
	requireStdout(t, home.Attn("settings", "set", "theme", "light"), "light")
	testworld.Await(remoteApp, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["theme"] == "light" })
	oldHomeDaemon := protocol.Deref(app.Initial.DaemonInstanceID)
	if record := readFile(t, filepath.Join(remote.Dir, enrollment.RecordFileName)); !bytes.Contains(record, []byte(oldHomeDaemon)) || bytes.Contains(record, []byte("home_id")) {
		t.Fatalf("the build before home ids recorded %s, want only the home's daemon id %s", record, oldHomeDaemon)
	}
	shell := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
	pidPath := filepath.Join(remote.Dir, "shell.pid")
	app.TypeLine(shell, "echo $$ > "+pidPath+"; echo before-$((1+1))")
	app.AwaitScreen(shell, "before-2")
	pid := readFile(t, pidPath)

	home.Stop()
	home.RunFrom(current)
	home.InstallsOnOutposts(current, "")
	home.Start()
	id := homeID(t, home)
	app = home.App()
	outpostStateOnConnect(app, "outpost", protocol.OutpostStateRefused)
	app.Send(protocol.UpdateOutpostMessage{Cmd: protocol.CmdUpdateOutpost, ID: host})
	row = awaitOutpostInstall(app)
	if row.Update.Phase != protocol.OutpostUpdatePhaseDone || row.State != protocol.OutpostStateConnected {
		t.Fatalf("Update to the build with home ids: %+v %+v", row, row.Update)
	}
	remote.RunFrom(ssh.InstallPath(remote))
	if status := enrollmentOf(t, remote); status.Role != "outpost" || status.HomeID != id || status.Replaced != "" {
		t.Fatalf("after its first bridge the outpost's enrollment = %+v, want outpost of home %s with no daemon id left", status, id)
	}
	if theme := remote.App().Initial.Settings["theme"]; theme != "light" {
		t.Fatalf("the outpost shows the home's theme as %q after its first bridge, want the copied %q", theme, "light")
	}
	awaitOutpostNotebookInSync(t, app, host)
	writeFileAt(t, filepath.Join(notebookRoot, "upgraded.md"), "written after home ids")
	awaitOutpostFile(t, &testworld.OutpostPair{App: app, Outpost: remote}, filepath.Join(copied, "upgraded.md"), "written after home ids", false)
	synced := notebookSyncStateOf(t, remote)
	if synced.Home != id || synced.HomeDevice != oldHomeDaemon || !slices.Equal(slices.Sorted(maps.Keys(synced.Folders)), slices.Sorted(maps.Keys(folders))) {
		t.Fatalf("after the upgrade the outpost's Notebook sync state is home %s device %s folders %v, want home %s device %s folders %v", synced.Home, synced.HomeDevice, slices.Sorted(maps.Keys(synced.Folders)), id, oldHomeDaemon, slices.Sorted(maps.Keys(folders)))
	}
	requireNoCopyMovedAside(t, copied)
	t.Logf("(4) Notebook folders %v kept their ids and the copy %s; sync state moved to home %s", slices.Sorted(maps.Keys(folders)), copied, id)
	app.AwaitScreen(shell, "before-2")
	app.TypeLine(shell, "echo $$ > "+pidPath+"; echo upgraded-$((2+2))")
	app.AwaitScreen(shell, "upgraded-4")
	if again := readFile(t, pidPath); !bytes.Equal(again, pid) {
		t.Fatalf("the shell's pid went from %s to %s across the upgrade", pid, again)
	}
	mark := app.EventCount()
	requireStdout(t, remote.Attn("daemon", "stop"))
	outpostStateSince(app, "outpost", protocol.OutpostStateLost, mark)
	stopRetries := home.FireTimers(bridge.RetryTimer)
	outpostStateSince(app, "outpost", protocol.OutpostStateConnected, mark)
	stopRetries()
	if listed := remote.Attn("list", "--json"); !strings.Contains(listed.Stdout, shell) {
		t.Fatalf("after the outpost restarted it no longer holds the shell %s: %+v", shell, listed)
	}
	t.Logf("(1) the outpost enrolled to daemon %s took home id %s at its first bridge; shell pid %s kept", oldHomeDaemon, id, strings.TrimSpace(string(pid)))

	enrolled := readFile(t, filepath.Join(remote.Dir, enrollment.RecordFileName))
	home.Stop()
	daemonIDPath := filepath.Join(home.Dir, enrollment.DaemonIDFileName)
	replacedDaemonID := readFile(t, daemonIDPath)
	if err := os.WriteFile(daemonIDPath, []byte("d-0123456789abcdef0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home.Start()
	app = home.App()
	newDaemon := protocol.Deref(app.Initial.DaemonInstanceID)
	if newDaemon == oldHomeDaemon || homeID(t, home) != id {
		t.Fatalf("the home folder runs daemon %s with home id %s, want another daemon presenting home id %s", newDaemon, homeID(t, home), id)
	}
	outpostStateOnConnect(app, "outpost", protocol.OutpostStateConnected)
	writeFileAt(t, filepath.Join(notebookRoot, "new-daemon.md"), "written by another daemon of the home")
	awaitOutpostFile(t, &testworld.OutpostPair{App: app, Outpost: remote}, filepath.Join(copied, "new-daemon.md"), "written by another daemon of the home", false)
	awaitOutpostNotebookInSync(t, app, host)
	if state := notebookSyncStateOf(t, remote); state.Home != id || state.HomeDevice != newDaemon {
		t.Fatalf("with another daemon at the home the outpost syncs with home %s device %s, want home %s device %s", state.Home, state.HomeDevice, id, newDaemon)
	}
	requireNoCopyMovedAside(t, copied)
	t.Logf("(5) Notebook sync resumed into %s with daemon %s as the home's device, no copy moved aside", copied, newDaemon)
	home.Stop()
	if err := os.WriteFile(daemonIDPath, replacedDaemonID, 0o600); err != nil {
		t.Fatal(err)
	}
	moved := testworld.NewStack(t)
	moved.RunFrom(current)
	moved.InstallsOnOutposts(current, "")
	ssh.Use(moved.World)
	copyHomeData(t, home.Dir, moved.Dir)
	moved.Start()
	movedApp := moved.App()
	if daemon := protocol.Deref(movedApp.Initial.DaemonInstanceID); daemon == oldHomeDaemon || homeID(t, moved) != id {
		t.Fatalf("the moved home runs daemon %s with home id %s, want a new daemon presenting home id %s", daemon, homeID(t, moved), id)
	}
	outpostStateOnConnect(movedApp, "outpost", protocol.OutpostStateConnected)
	movedApp.AwaitScreen(shell, "upgraded-4")
	movedApp.TypeLine(shell, "echo $$ > "+pidPath+"; echo moved-$((3+3))")
	movedApp.AwaitScreen(shell, "moved-6")
	if again := readFile(t, pidPath); !bytes.Equal(again, pid) {
		t.Fatalf("the shell's pid went from %s to %s when the home moved", pid, again)
	}
	if after := readFile(t, filepath.Join(remote.Dir, enrollment.RecordFileName)); !bytes.Equal(after, enrolled) {
		t.Fatalf("the move rewrote the outpost's enrollment:\n%s\nwas\n%s", after, enrolled)
	}

	stranger := testworld.NewStack(t)
	stranger.RunFrom(current)
	stranger.InstallsOnOutposts(current, "")
	ssh.Use(stranger.World)
	stranger.Start()
	strangerApp := enableOutposts(t, stranger)
	strangerApp.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "taken", Destination: "outpost"})
	taken := outpostState(strangerApp, "taken", protocol.OutpostStateRefused)
	for _, word := range []string{"paired to home", id, homeID(t, stranger), "attn enrollment leave"} {
		if !strings.Contains(protocol.Deref(taken.LastError), word) {
			t.Fatalf("the refusal of another home omits %q: %+v", word, taken)
		}
	}
	t.Logf("(2) daemon %s presenting home id %s bridged with the shell alive; home %s was refused: %s", protocol.Deref(movedApp.Initial.DaemonInstanceID), id, homeID(t, stranger), protocol.Deref(taken.LastError))

	record := `{"role": "outpost", "home_id": "` + id + `", "home_name": "moved"}` + "\n"
	if err := os.WriteFile(filepath.Join(home.Dir, enrollment.RecordFileName), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	home.Start()
	if daemon := protocol.Deref(home.App().Initial.DaemonInstanceID); daemon != oldHomeDaemon {
		t.Fatalf("the old home's folder runs daemon %s, want %s", daemon, oldHomeDaemon)
	}
	if status := enrollmentOf(t, home); status.IsHome || status.Role != "outpost" {
		t.Fatalf("the old home's enrollment = %+v, want an outpost", status)
	}
	planted := home.Attn("seed", "plant", "Written where the home used to run", "-m", "never lands here")
	if planted.Code == 0 || !strings.Contains(planted.Stderr, "unreachable") {
		t.Fatalf("a seed planted at the old home's daemon %+v, want it refused as the home being unreachable", planted)
	}
	if listed := moved.Attn("seed", "ls"); strings.Contains(listed.Stdout, "Written where the home used to run") {
		t.Fatalf("the old home's daemon wrote to the home's garden: %s", listed.Stdout)
	}
	t.Logf("(3) daemon %s, recorded as an outpost, refused to plant as the home: %s", oldHomeDaemon, strings.TrimSpace(planted.Stderr))
}
