package main_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type reverseWorld struct {
	t            *testing.T
	ssh          *testworld.SSH
	home, laptop *testworld.Stack
	app          *testworld.Peer
	name         string
	copied       <-chan struct{}
}

func newReverseWorld(t *testing.T, opts ...testworld.StackOption) *reverseWorld {
	t.Helper()
	return newReverseWorldWith(t, func(w *reverseWorld) { w.copied = w.laptop.PassesOf(pausepoint.CopyBatchApplied) }, opts...)
}

func newReverseWorldWith(t *testing.T, prepare func(*reverseWorld), opts ...testworld.StackOption) *reverseWorld {
	t.Helper()
	w := &reverseWorld{t: t, ssh: testworld.NewSSH(t), home: testworld.NewStack(t, opts...), laptop: testworld.NewStack(t, opts...)}
	prepare(w)
	for _, s := range []*testworld.Stack{w.home, w.laptop} {
		w.ssh.Use(s.World)
		s.Vars = append(s.Vars, "SHELL=/bin/bash")
		s.Start()
	}
	w.ssh.Route("home", w.home)
	w.app = enableOutposts(t, w.home)
	for _, s := range []*testworld.Stack{w.home, w.laptop} {
		requireStdout(t, s.Attn("settings", "set", "remote_home_enabled", "true"), "true")
	}
	name, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	w.name = name
	t.Cleanup(func() {
		if t.Failed() {
			for _, s := range []*testworld.Stack{w.home, w.laptop} {
				s.LogDaemonTail()
			}
		}
	})
	return w
}

func (w *reverseWorld) join() protocol.OutpostInfo {
	w.t.Helper()
	mark := w.app.EventCount()
	requireStdout(w.t, w.laptop.Attn("enrollment", "join", "home"), "dial:       home (bridged)")
	return outpostStateSince(w.app, w.name, protocol.OutpostStateConnected, mark)
}

// awaitCopiedSetting waits until the laptop's copy of the home's setting key reads value, reading it again after
// each copy batch the laptop applies.
func (w *reverseWorld) awaitCopiedSetting(key, value string) {
	w.t.Helper()
	for strings.TrimSpace(w.laptop.Attn("settings", "get", key).Stdout) != value {
		select {
		case <-w.copied:
		case <-time.After(fakeagent.HangGuard):
			w.t.Fatalf("the laptop's copy of %s did not read %q within %s", key, value, fakeagent.HangGuard)
		}
	}
}

func (w *reverseWorld) status() string {
	w.t.Helper()
	result := w.laptop.Attn("enrollment", "status")
	if result.Code != 0 {
		w.t.Fatalf("enrollment status exited %d: %s", result.Code, result.Stderr)
	}
	return result.Stdout
}

// awaitHomeUnreachable waits until an app on s is told the home is unreachable in a way match accepts, connecting
// again each time s closes the connection at a change.
func awaitHomeUnreachable(t *testing.T, s *testworld.Stack, match func(protocol.HomeUnreachableMessage) bool) {
	t.Helper()
	for {
		p := s.ConnectApp()
		first := testworld.AwaitEvent(p, "initial_state or home_unreachable", func(e protocol.WebSocketEvent) bool {
			return e.Event == protocol.EventInitialState || e.Event == protocol.EventHomeUnreachable
		})
		if first.Event == protocol.EventHomeUnreachable && match(lastHomeUnreachable(t, p)) {
			p.Close()
			return
		}
		p.Closed()
		p.Close()
	}
}

func lastHomeUnreachable(t *testing.T, p *testworld.Peer) protocol.HomeUnreachableMessage {
	t.Helper()
	log := p.Log()
	for i := len(log) - 1; i >= 0; i-- {
		var told protocol.HomeUnreachableMessage
		if json.Unmarshal(log[i], &told) == nil && told.Event == protocol.EventHomeUnreachable {
			return told
		}
	}
	t.Fatal("the app has no home_unreachable")
	return protocol.HomeUnreachableMessage{}
}

// awaitHomeDial waits, as an app on s sees it, until s's dial to its home reads state. While s cannot reach its home
// it answers the app with home_unreachable and closes the connection at the next change, so the app connects again;
// once s is its own home again, the app reads s's own settings.
func awaitHomeDial(t *testing.T, s *testworld.Stack, state string) *testworld.Peer {
	t.Helper()
	for {
		p := s.ConnectApp()
		first := testworld.AwaitEvent(p, "initial_state or home_unreachable", func(e protocol.WebSocketEvent) bool {
			return e.Event == protocol.EventInitialState || e.Event == protocol.EventHomeUnreachable
		})
		if first.Event == protocol.EventInitialState {
			if first.Settings["outpost_state"] != state {
				testworld.Await(p, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
					return m.Settings["outpost_state"] == state
				})
			}
			return p
		}
		if told := lastHomeUnreachable(t, p); told.State == state {
			return p
		}
		p.Closed()
		p.Close()
	}
}

func pidOf(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func TestAReverseOutpostDialsItsHomeAndRedialsAfterEitherRestarts(t *testing.T) {
	w := newReverseWorld(t, testworld.WithAgents(fakeagent.Claude))
	box := testworld.NewStack(t)
	box.Vars = append(box.Vars, "SHELL=/bin/bash")
	box.Start()
	w.ssh.Route("box", box)

	row := w.join()
	if row.DaemonID == nil || row.HostKeyFingerprint == nil {
		t.Fatalf("the joined machine's row is not pinned: %+v", row)
	}
	w.app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "box", Destination: "box"})
	boxRow := outpostState(w.app, "box", protocol.OutpostStateConnected)
	second := testworld.NewStack(t)
	w.ssh.Use(second.World)
	second.Start()
	requireStdout(t, second.Attn("settings", "set", "remote_home_enabled", "true"), "true")
	requireStdout(t, second.Attn("enrollment", "join", "home"), "dial:       home (bridged)")
	testworld.Await(w.app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		for _, r := range m.Outposts {
			if r.ID != row.ID && r.Name == w.name && r.State == protocol.OutpostStateConnected {
				return true
			}
		}
		return false
	})
	t.Log("a second machine joined the same home by dialing it")
	third := testworld.NewStack(t)
	w.ssh.Use(third.World)
	third.Start()
	requireStdout(t, third.Attn("settings", "set", "remote_home_enabled", "true"), "true")
	requireStdout(t, box.Attn("settings", "get", "remote_home_enabled"), "true")
	intoOutpost := third.Attn("enrollment", "join", "box")
	if intoOutpost.Code == 0 || !strings.Contains(intoOutpost.Stderr, "is an outpost of another home") {
		t.Fatalf("join into an outpost: exit %d, %s%s", intoOutpost.Code, intoOutpost.Stdout, intoOutpost.Stderr)
	}
	w.ssh.Route("third", third)
	requireStdout(t, third.Attn("settings", "set", "outposts_enabled", "true"), "true")
	intoItself := third.Attn("enrollment", "join", "third")
	if intoItself.Code == 0 || !strings.Contains(intoItself.Stderr, "this machine's own daemon") {
		t.Fatalf("join into itself: exit %d, %s%s", intoItself.Code, intoItself.Stdout, intoItself.Stderr)
	}
	requireLines(t, "status after a refused self-join", third.Attn("enrollment", "status").Stdout, "enrollment: home")
	t.Log("an outpost refuses a machine that dials it, and a machine refuses to join itself")
	for _, refused := range []struct {
		action  string
		command any
	}{
		{"update", protocol.UpdateOutpostMessage{Cmd: protocol.CmdUpdateOutpost, ID: row.ID}},
		{"re-pair", protocol.RepairOutpostMessage{Cmd: protocol.CmdRepairOutpost, ID: row.ID}},
	} {
		w.app.Send(refused.command)
		if got := protocol.Deref(testworld.Refused(w.app).Error); !strings.Contains(got, "dials this home, so the home cannot "+refused.action) {
			t.Fatalf("%s of a machine that dials the home: %q", refused.action, got)
		}
	}
	w.app.Send(protocol.ConnectOutpostInTerminalMessage{Cmd: protocol.CmdConnectOutpostInTerminal, ID: row.ID, RequestID: "connect-reverse"})
	connect := testworld.Await(w.app, protocol.EventProfileActionResult, func(m protocol.ProfileActionResultMessage) bool { return m.RequestID == "connect-reverse" })
	if connect.Success || !strings.Contains(protocol.Deref(connect.Error), "so the home cannot open ssh to") {
		t.Fatalf("connect in a terminal to a machine that dials the home: %+v", connect)
	}

	shellHarness := fakeagent.Harness(protocol.SessionAgentShell)
	shell := w.home.Spawn(w.app, shellHarness, w.laptop.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &row.ID })
	boxShell := w.home.Spawn(w.app, shellHarness, box.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &boxRow.ID })
	pidPath, boxPidPath := w.laptop.Path("shell.pid"), box.Path("shell.pid")
	w.app.TypeLine(shell, "echo $$ > "+pidPath+"; echo laptop-$((1+1))")
	w.app.TypeLine(boxShell, "echo $$ > "+boxPidPath+"; echo box-$((2+2))")
	w.app.AwaitScreen(shell, "laptop-2")
	w.app.AwaitScreen(boxShell, "box-4")
	before := pidOf(t, pidPath)
	t.Logf("the home bridges the machine that dialed it (%s) and the outpost it dials (%s) at once, and starts terminals on both", row.ID, boxRow.ID)
	caller := w.home.Spawn(w.app, fakeagent.Claude, w.home.Path("caller"))
	run := func(args ...string) testworld.Result {
		got := w.home.Run(testworld.Invocation{Session: caller, Args: args})
		if got.Code != 0 {
			t.Fatalf("attn %s: exited %d, stdout %q, stderr %q; want a terminal on %s", strings.Join(args, " "), got.Code, got.Stdout, got.Stderr, w.name)
		}
		return got
	}
	var opened protocol.TerminalOpenResult
	run("terminal", "open", "--host", w.name, "--json").JSON(t, &opened)
	awaitMachineNotebook(t, w.app, row.ID, protocol.OutpostNotebookStateInSync)
	var delegation delegated
	run("delegate", "--brief", "Work on the laptop", "--model", "opus", "--cwd", w.laptop.Path("shell"), "--host", w.name).JSON(t, &delegation)
	for way, id := range map[string]string{"attn terminal open --host": string(opened.SessionID), "attn delegate --host": delegation.SessionID} {
		if got := testworld.AwaitSession(w.app, id, func(protocol.Session) bool { return true }); protocol.Deref(got.Host) != row.ID {
			t.Fatalf("%s started session %s on host %q, want the machine that dials the home %s", way, id, protocol.Deref(got.Host), row.ID)
		}
	}
	t.Log("attn terminal open --host and attn delegate --host start terminals on the machine that dials the home")
	requireLines(t, "status while bridged", w.status(), "dial:       home (bridged)")

	w.home.Stop()
	w.home.Start()
	w.app = w.home.App()
	for _, r := range w.app.Initial.Outposts {
		if r.ID == row.ID && r.State == protocol.OutpostStateConnecting {
			t.Fatalf("after the home restarts, the row of a machine that has not dialed yet says connecting: %+v", r)
		}
	}
	mark := w.app.EventCount()
	stopRetries := w.laptop.FireTimers(bridge.RetryTimer)
	outpostStateSince(w.app, w.name, protocol.OutpostStateConnected, mark)
	stopRetries()
	requireLines(t, "status after the home restarted", w.status(), "dial:       home (bridged)")
	w.app.TypeLine(shell, "echo $$ > "+pidPath+"; echo after-home-$((3+3))")
	w.app.AwaitScreen(shell, "after-home-6")
	if got := pidOf(t, pidPath); got != before {
		t.Fatalf("the shell changed pid across the home's restart: %s -> %s", before, got)
	}
	t.Log("the machine redialed after the home restarted, with its shell alive")

	mark = w.app.EventCount()
	w.laptop.Stop()
	outpostStateSince(w.app, w.name, protocol.OutpostStateLost, mark)
	mark = w.app.EventCount()
	w.laptop.Start()
	outpostStateSince(w.app, w.name, protocol.OutpostStateConnected, mark)
	w.app = w.home.App()
	w.app.TypeLine(shell, "echo $$ > "+pidPath+"; echo after-laptop-$((4+4))")
	w.app.AwaitScreen(shell, "after-laptop-8")
	if got := pidOf(t, pidPath); got != before {
		t.Fatalf("the shell changed pid across the machine's own restart: %s -> %s", before, got)
	}
	w.app.TypeLine(boxShell, "echo box-still-$((5+5))")
	w.app.AwaitScreen(boxShell, "box-still-10")
	t.Log("the machine dialed again when its own daemon restarted, with its shell alive; the ssh outpost stayed bridged")
}

func TestAReverseOutpostShowsItsDialAndBothWaysOutEndIt(t *testing.T) {
	w := newReverseWorld(t)
	row := w.join()
	requireLines(t, "status while bridged", w.status(), "dial:       home (bridged)", "enrolled to ")

	requireStdout(t, w.home.Attn("settings", "set", "remote_home_enabled", "false"), "false")
	w.awaitCopiedSetting("remote_home_enabled", "false")
	w.ssh.Fail("home", "ssh: connect to host home port 22: No route to host")
	w.ssh.ExitMaster(w.laptop, protocol.Deref(row.DaemonID))
	awaitHomeDial(t, w.laptop, "dialing")
	unreachable := w.status()
	requireLines(t, "status while the home is unreachable", unreachable, "dial:       home (dialing)", "last error: ")
	t.Logf("unreachable home:\n%s", unreachable)

	w.ssh.Fail("home", "victor@home: Permission denied (publickey).")
	stopRetries := w.laptop.FireTimers(bridge.RetryTimer)
	awaitHomeDial(t, w.laptop, "refused")
	stopRetries()
	requireLines(t, "status while ssh refuses", w.status(), "dial:       home (refused)", "Permission denied")

	w.ssh.Restore("home")
	stopRetries = w.laptop.FireTimers(bridge.RetryTimer)
	awaitHomeUnreachable(t, w.laptop, func(told protocol.HomeUnreachableMessage) bool {
		return strings.Contains(protocol.Deref(told.Reason), "does not take machines that dial it")
	})
	stopRetries()
	refused := w.status()
	requireLines(t, "status while the home refuses", refused, "dial:       home (refused)", "does not take machines that dial it", "remote_home_enabled true")
	t.Logf("refused:\n%s", refused)
	for _, r := range w.home.App().Initial.Outposts {
		if r.ID == row.ID && r.State == protocol.OutpostStateConnecting {
			t.Fatalf("the home shows connecting for a machine whose dial it refused: %+v", r)
		}
	}

	requireStdout(t, w.home.Attn("settings", "set", "remote_home_enabled", "true"), "true")
	mark := w.app.EventCount()
	requireStdout(t, w.laptop.Attn("enrollment", "join", "home"), "dial:       home")
	stopRetries = w.laptop.FireTimers(bridge.RetryTimer)
	outpostStateSince(w.app, w.name, protocol.OutpostStateConnected, mark)
	stopRetries()

	mark = w.app.EventCount()
	requireStdout(t, w.laptop.Attn("enrollment", "leave"), "no longer enrolled")
	outpostStateSince(w.app, w.name, protocol.OutpostStateLost, mark)
	left := w.status()
	requireLines(t, "status after leave", left, "enrollment: home")
	if strings.Contains(left, "dial:") {
		t.Fatalf("the machine still dials after leave:\n%s", left)
	}
	t.Log("leave on the machine ended the bridge and the dialing")

	row = w.join()
	w.app.Send(protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: row.ID})
	testworld.Await(w.app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 0 })
	stopRetries = w.laptop.FireTimers(bridge.RetryTimer)
	awaitHomeDial(t, w.laptop, "off")
	stopRetries()
	removed := w.status()
	requireLines(t, "status after remove at the home", removed, "enrollment: home")
	if strings.Contains(removed, "dial:") {
		t.Fatalf("the machine still dials after the home removed it:\n%s", removed)
	}
	t.Log("remove at the home ended the bridge, and the machine stopped dialing and left on the refusal")
}

func TestJoinRefusesWhileTheMachineHasOpenSessionsAndLeaveBringsItsOwnHomeBack(t *testing.T) {
	w := newReverseWorld(t)
	box := testworld.NewStack(t)
	box.Start()
	w.ssh.Route("box", box)
	lp := enableOutposts(t, w.laptop)
	lp.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "box", Destination: "box"})
	outpostState(lp, "box", protocol.OutpostStateConnected)
	var seed struct {
		ID string `json:"id"`
	}
	w.laptop.Attn("seed", "plant", "The laptop's own seed", "--json").JSON(t, &seed)
	requireStdout(t, w.laptop.Attn("settings", "set", "garden_v2_enabled", "true"), "true")
	spawned, desktop, tile := w.laptop.RequestSpawn(lp, fakeagent.Harness(protocol.SessionAgentShell), w.laptop.Path("own"))
	if !spawned.Success {
		t.Fatalf("spawn on the laptop: %s", protocol.Deref(spawned.Error))
	}
	own := string(spawned.ID)

	refused := w.laptop.Attn("enrollment", "join", "home")
	if refused.Code == 0 {
		t.Fatalf("join succeeded with an open session of the machine's own home: %s", refused.Stdout)
	}
	requireLines(t, "join refused by an open session", refused.Stderr, "open session", own, "close them")
	t.Logf("join refused: %s", strings.TrimSpace(refused.Stderr))
	if rows := w.home.App().Initial.Outposts; len(rows) != 0 {
		t.Fatalf("a refused join left rows at the home: %+v", rows)
	}

	lp.Send(protocol.DesktopCloseTileMessage{Cmd: protocol.CmdDesktopCloseTile, RequestID: "close-own", DesktopID: desktop, TileID: tile})
	testworld.Await(lp, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == own
	})
	closedBefore := w.laptop.Attn("session", "list", "--closed")
	seedBefore := w.laptop.Attn("seed", "show", seed.ID)
	requireStdout(t, closedBefore, shortID(own))
	requireStdout(t, seedBefore, "The laptop's own seed")
	requireStdout(t, w.laptop.Attn("settings", "get", "garden_v2_enabled"), "true")

	var ownHome enrollmentJSON
	w.laptop.Attn("enrollment", "status", "--json").JSON(t, &ownHome)
	boxApp := box.App()
	mark := boxApp.EventCount()
	if boxApp.Initial.Settings["outpost_state"] != "bridged" {
		mark = awaitOutpostSettingSince(boxApp, mark, "bridged")
	}
	w.join()
	t.Log("after its session closed, the machine joined the home")
	w.awaitCopiedSetting("outposts_enabled", "true")
	awaitOutpostSettingSince(boxApp, mark, "off")
	dialed := w.ssh.MasterAttemptsTo("box")
	t.Log("while enrolled, the machine stops bridging its own outpost, though it reads the home's outposts_enabled=true")
	homeMark := w.app.EventCount()
	w.laptop.Stop()
	w.laptop.Start()
	outpostStateSince(w.app, w.name, protocol.OutpostStateConnected, homeMark)
	if again := w.ssh.MasterAttemptsTo("box"); again != dialed {
		t.Fatalf("the enrolled machine dialed its own outpost %d more times, after the home's copy of outposts_enabled or its restart", again-dialed)
	}
	left := boxApp.EventCount()
	requireStdout(t, w.laptop.Attn("enrollment", "leave"), "no longer enrolled")
	var back enrollmentJSON
	w.laptop.Attn("enrollment", "status", "--json").JSON(t, &back)
	if !back.IsHome || back.HomeID != ownHome.HomeID {
		t.Fatalf("after leave the machine is %+v, want its own home %s again", back, ownHome.HomeID)
	}
	stopRetries := w.laptop.FireTimers(bridge.RetryTimer)
	awaitOutpostSettingSince(boxApp, left, "bridged")
	stopRetries()
	t.Log("after leave, the machine bridges its own outpost again")

	closedAfter := w.laptop.Attn("session", "list", "--closed")
	requireStdout(t, closedAfter, shortID(own))
	if rowOf(closedAfter.Stdout, own) != rowOf(closedBefore.Stdout, own) {
		t.Fatalf("the machine's closed session reads differently after leave:\nbefore %q\nafter  %q", rowOf(closedBefore.Stdout, own), rowOf(closedAfter.Stdout, own))
	}
	seedAfter := w.laptop.Attn("seed", "show", seed.ID)
	requireStdout(t, seedAfter, "The laptop's own seed")
	requireStdout(t, w.laptop.Attn("settings", "get", "garden_v2_enabled"), "true")
	t.Log("after leave, the machine's own closed session, seed and setting read as they did before the join")
}

func TestAJoinRefusedForItsBuildLeavesOnceTheHomeRemovesIt(t *testing.T) {
	w := newReverseWorldWith(t, func(w *reverseWorld) { w.home.RunFrom(testworld.AttnUpdateBinary(t, "home-build", "source", "")) })
	lp := w.laptop.App()
	var ownHome enrollmentJSON
	w.laptop.Attn("enrollment", "status", "--json").JSON(t, &ownHome)
	refused := w.laptop.Attn("enrollment", "join", "home")
	if refused.Code == 0 || !strings.Contains(refused.Stderr, "build") {
		t.Fatalf("a join across builds: exit %d, %s%s", refused.Code, refused.Stdout, refused.Stderr)
	}
	row := outpostState(w.app, w.name, protocol.OutpostStateRefused)
	testworld.Await(lp, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["outpost_state"] == "refused" })
	requireLines(t, "status after a join refused for its build", w.status(), "dial:       home (refused)", "enrolled to ")
	t.Logf("join refused for its build: %s", strings.TrimSpace(refused.Stderr))

	removed := lp.EventCount()
	w.app.Send(protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: row.ID})
	testworld.Await(w.app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 0 })
	stopRetries := w.laptop.FireTimers(bridge.RetryTimer)
	testworld.AwaitSince(lp, removed, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return m.Settings["outpost_state"] == "off" && m.Settings["outpost_home"] == ""
	})
	stopRetries()
	if rows := w.home.App().Initial.Outposts; len(rows) != 0 {
		t.Fatalf("the machine joined again after the home removed it: %+v", rows)
	}
	if left := w.status(); strings.Contains(left, "dial:") {
		t.Fatalf("the machine still dials after the home removed it:\n%s", left)
	}
	var back enrollmentJSON
	w.laptop.Attn("enrollment", "status", "--json").JSON(t, &back)
	if !back.IsHome || back.HomeID != ownHome.HomeID {
		t.Fatalf("after it left on the removal the machine is %+v, want its own home %s again", back, ownHome.HomeID)
	}
	t.Log("after the home removed the machine its first join recorded, the machine's next dial was a redial, and it left")
}

// awaitOutpostSettingSince waits until the app on an outpost reads its home bridge as state, from event mark on, and
// returns the event count after that read.
func awaitOutpostSettingSince(p *testworld.Peer, mark int, state string) int {
	p.T.Helper()
	testworld.AwaitSince(p, mark, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return m.Settings["outpost_state"] == state
	})
	return p.EventCount()
}
