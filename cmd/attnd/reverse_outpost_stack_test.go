package main_test

import (
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type reverseWorld struct {
	t            *testing.T
	ssh          *testworld.SSH
	home, laptop *testworld.Stack
	app          *testworld.Peer
	name         string
}

func newReverseWorld(t *testing.T) *reverseWorld {
	t.Helper()
	w := &reverseWorld{t: t, ssh: testworld.NewSSH(t), home: testworld.NewStack(t), laptop: testworld.NewStack(t)}
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

func (w *reverseWorld) status() string {
	w.t.Helper()
	result := w.laptop.Attn("enrollment", "status")
	if result.Code != 0 {
		w.t.Fatalf("enrollment status exited %d: %s", result.Code, result.Stderr)
	}
	return result.Stdout
}

func awaitHomeDial(t *testing.T, p *testworld.Peer, state string) {
	t.Helper()
	testworld.Await(p, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return m.Settings["outpost_state"] == state
	})
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
	w := newReverseWorld(t)
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

	shell := w.home.Spawn(w.app, fakeagent.Harness(protocol.SessionAgentShell), w.laptop.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &row.ID })
	boxShell := w.home.Spawn(w.app, fakeagent.Harness(protocol.SessionAgentShell), box.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &boxRow.ID })
	pidPath, boxPidPath := w.laptop.Path("shell.pid"), box.Path("shell.pid")
	w.app.TypeLine(shell, "echo $$ > "+pidPath+"; echo laptop-$((1+1))")
	w.app.TypeLine(boxShell, "echo $$ > "+boxPidPath+"; echo box-$((2+2))")
	w.app.AwaitScreen(shell, "laptop-2")
	w.app.AwaitScreen(boxShell, "box-4")
	before := pidOf(t, pidPath)
	t.Logf("the home bridges the machine that dialed it (%s) and the outpost it dials (%s) at once", row.ID, boxRow.ID)
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
	lp := w.laptop.App()
	row := w.join()
	requireLines(t, "status while bridged", w.status(), "dial:       home (bridged)", "enrolled to ")

	w.ssh.Fail("home", "ssh: connect to host home port 22: No route to host")
	w.ssh.ExitMaster(w.laptop, protocol.Deref(row.DaemonID))
	awaitHomeDial(t, lp, "dialing")
	unreachable := w.status()
	requireLines(t, "status while the home is unreachable", unreachable, "dial:       home (dialing)", "last error: ")
	t.Logf("unreachable home:\n%s", unreachable)

	requireStdout(t, w.home.Attn("settings", "set", "remote_home_enabled", "false"), "false")
	w.ssh.Restore("home")
	stopRetries := w.laptop.FireTimers(bridge.RetryTimer)
	awaitHomeDial(t, lp, "refused")
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
	lp = w.laptop.App()
	w.app.Send(protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: row.ID})
	testworld.Await(w.app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 0 })
	stopRetries = w.laptop.FireTimers(bridge.RetryTimer)
	testworld.Await(lp, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return m.Settings["outpost_state"] == "off" && m.Settings["outpost_home"] == ""
	})
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
	lp := w.laptop.App()
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

	w.join()
	t.Log("after its session closed, the machine joined the home")
	requireStdout(t, w.laptop.Attn("enrollment", "leave"), "no longer enrolled")

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
