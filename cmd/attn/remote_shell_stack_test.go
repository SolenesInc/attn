package main_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestRemoteShellRunsThroughTheHome(t *testing.T) {
	remote, home := testworld.NewStack(t), testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, remote)
	for _, s := range []*testworld.Stack{remote, home} {
		s.Vars = append(s.Vars, "SHELL=/bin/bash")
		s.Start()
	}
	pair.Connect()
	app := pair.App
	host := pair.Host
	caller := home.Spawn(app, fakeagent.Claude, home.Path("agent"))
	home.Launched(caller)
	run := func(args ...string) testworld.Result {
		return home.Run(testworld.Invocation{Session: caller, Args: append([]string{"terminal"}, args...)})
	}
	if err := os.MkdirAll(remote.Path("shell"), 0700); err != nil {
		t.Fatal(err)
	}
	var opened protocol.TerminalOpenResult
	got := run("open", "--host", "outpost", "--cwd", remote.Path("shell"), "--json", "--", "printf 'host=%s\\n' \"$ATTN_HOST\"")
	if got.Code != 0 {
		t.Fatalf("open remote shell: %+v", got)
	}
	got.JSON(t, &opened)
	app.AwaitScreen(string(opened.SessionID), "host=outpost")
	session := testworld.AwaitSession(app, string(opened.SessionID), func(s protocol.Session) bool { return protocol.Deref(s.Host) == host })
	if session.Directory != remote.Path("shell") {
		t.Fatalf("directory=%s", session.Directory)
	}
	got = run("run", string(opened.TerminalID), "printf input-reached-outpost", "--wait")
	if got.Code != 0 || !strings.Contains(got.Stdout, "input-reached-outpost") {
		t.Fatalf("remote input/read: %+v", got)
	}

	local := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), home.Path("local"))
	app.TypeLine(local, "printf 'local-host=%s\\n' \"${ATTN_HOST-unset}\"")
	app.AwaitScreen(local, "local-host=unset")
	split := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), session.Directory, func(msg *protocol.SpawnSessionMessage) {
		msg.Host = protocol.Ptr(host)
		msg.SpawnedFrom = &opened.SessionID
	})
	app.TypeLine(split, "printf 'split-host=%s\\n' \"$ATTN_HOST\"; pwd")
	app.AwaitScreen(split, "split-host=outpost")
	app.AwaitScreen(split, session.Directory)
	got = run("run", string(opened.TerminalID), "printf 'blocked-ready\\n'; sleep 600")
	if got.Code != 0 {
		t.Fatalf("start blocking shell command: %+v", got)
	}
	app.AwaitScreen(string(opened.SessionID), "blocked-ready")
	got = run("wait", string(opened.TerminalID), "--until", "prompt", "--timeout", "0.01")
	if got.Code != 124 {
		t.Fatalf("remote wait timeout: %+v", got)
	}
	got = run("send", string(opened.TerminalID), "--keys", "C-c")
	if got.Code != 0 {
		t.Fatalf("input queued behind canceled wait: %+v", got)
	}
	got = run("wait", string(opened.TerminalID), "--until", "prompt")
	if got.Code != 0 {
		t.Fatalf("shell did not return to prompt after cancel: %+v", got)
	}
	got = run("run", string(opened.TerminalID), "attn seed ls", "--wait")
	if got.Code != 0 {
		t.Fatalf("remote CLI forward: %+v", got)
	}
	app.Send(protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: opened.TerminalID, Cols: 93, Rows: 31})
	got = run("run", string(opened.TerminalID), "stty size", "--wait")
	if got.Code != 0 || strings.TrimSpace(got.Stdout) != "31 93" {
		t.Fatalf("remote resize: %+v", got)
	}
	reloaded := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: opened.SessionID, Cols: 93, Rows: 31}, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return r.ID == opened.SessionID })
	if !reloaded.Success {
		t.Fatalf("remote reload: %s", protocol.Deref(reloaded.Error))
	}
	got = run("send", string(opened.TerminalID), "--text", "printf '__reload_%s\\n' ready\r")
	if got.Code != 0 {
		t.Fatalf("send after remote reload: %+v", got)
	}
	app.AwaitScreen(string(opened.SessionID), "__reload_ready")
	got = run("wait", string(opened.TerminalID), "--until", "prompt")
	if got.Code != 0 {
		t.Fatalf("first prompt after remote reload: %+v", got)
	}
	got = run("run", string(opened.TerminalID), "printf 'reloaded-host=%s' \"$ATTN_HOST\"", "--wait")
	if got.Code != 0 || !strings.Contains(got.Stdout, "reloaded-host=outpost") {
		t.Fatalf("reload host: %+v", got)
	}
	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: protocol.SessionID(split)})
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == split })
	clean := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), session.Directory, func(msg *protocol.SpawnSessionMessage) { msg.Host = protocol.Ptr(host) })
	app.TypeLine(clean, "exit 0")
	testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool { return e.Session != nil && string(e.Session.ID) == clean })

	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: opened.TerminalID, Data: "stty raw -echo; python3 -c 'import sys; print(\"paste-ready\",flush=True); data=sys.stdin.buffer.read(300000); print(\"paste-bytes=\"+str(len(data)),flush=True)'; stty sane\r"})
	app.AwaitScreen(string(opened.SessionID), "paste-ready")
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: opened.TerminalID, Data: strings.Repeat("x", 300000)})
	app.AwaitScreen(string(opened.SessionID), "paste-bytes=300000")
	mark := len(app.Log())
	got = run("run", string(opened.TerminalID), "exit 1")
	if got.Code != 0 {
		t.Fatalf("exit: %+v", got)
	}
	testworld.AwaitSince(app, mark, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.SessionID == opened.SessionID && e.ExitCode == 1 })
	testworld.AwaitSince(app, mark, protocol.EventSessionStateChanged, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && e.Session.ID == opened.SessionID && e.Session.State == protocol.SessionStateIdle && protocol.Deref(e.Session.StateReason) == "process_exited"
	})
	exited := testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: opened.TerminalID}, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == opened.TerminalID })
	if !exited.Success || exited.Exit == nil || exited.Exit.Code != 1 {
		t.Fatalf("exit attach: %+v", exited)
	}
	t.Logf("host=%s terminal=%s remote input/read/exit verified; exit 1 reached process_exited idle", host, opened.TerminalID)
}

func TestRemoteShellSurvivesBridgeLossAndDaemonRestarts(t *testing.T) {
	a := testworld.NewSSHAgent(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	for _, s := range []*testworld.Stack{remote, home} {
		a.Authorize(s.World)
		s.Vars = append(s.Vars, "SHELL=/bin/bash")
		s.Start()
	}
	address := bridgeAddress(t)
	requireStdout(t, remote.Attn("settings", "set", "outpost_listen", address), address)
	app := enableOutposts(t, home)
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Address: address})
	host := outpostState(app, "outpost", protocol.OutpostStateConnected).ID
	caller := home.Spawn(app, fakeagent.Claude, home.Path("caller"))
	home.Launched(caller)
	id := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(host) })
	terminal := protocol.TerminalID(app.Terminal(id))
	reattach := func() {
		result := testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: terminal}, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == terminal })
		if !result.Success {
			t.Fatalf("reattach after reconnect: %s", protocol.Deref(result.Error))
		}
	}
	echo := func(command, output string) {
		result := home.Run(testworld.Invocation{Session: caller, Args: []string{"terminal", "run", string(terminal), command, "--wait"}})
		if result.Code != 0 || !strings.Contains(result.Stdout, output) {
			t.Fatalf("remote echo: %+v", result)
		}
		app.AwaitScreen(id, output)
	}
	echo("echo before-$((1+1))", "before-2")
	remote.Signal(syscall.SIGSTOP)
	defer remote.Signal(syscall.SIGCONT)
	outpostState(app, "outpost", protocol.OutpostStateStalled)
	outpostState(app, "outpost", protocol.OutpostStateLost)
	app.Send(protocol.DetachSessionMessage{Cmd: protocol.CmdDetachSession, ID: terminal})
	for _, command := range []any{
		protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: "echo forbidden\r"},
		protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: terminal, Cols: 90, Rows: 30},
		protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: protocol.SessionID(id)},
	} {
		app.Send(command)
		refused := testworld.Refused(app)
		if !strings.Contains(protocol.Deref(refused.Error), "outpost is unreachable") {
			t.Fatalf("offline command: %+v", refused)
		}
	}
	app.Send(protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: protocol.SessionID(id)})
	refused := testworld.Refused(app)
	if !strings.Contains(protocol.Deref(refused.Error), "outpost is unreachable") {
		t.Fatalf("offline close: %+v", refused)
	}
	reload := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(id), Cols: 80, Rows: 24}, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == id })
	if reload.Success || !strings.Contains(protocol.Deref(reload.Error), "outpost") {
		t.Fatalf("offline reload: %+v", reload)
	}
	split, _, _ := home.RequestSpawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(host) })
	if split.Success || !strings.Contains(protocol.Deref(split.Error), "outpost") {
		t.Fatalf("offline split: %+v", split)
	}
	attach := testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: terminal}, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == terminal })
	if attach.Success || !strings.Contains(protocol.Deref(attach.Error), "outpost") {
		t.Fatalf("offline attach: %+v", attach)
	}
	mark := len(app.Log())
	remote.Signal(syscall.SIGCONT)
	outpostStateSince(app, "outpost", protocol.OutpostStateConnected, mark)
	reattach()
	echo("echo reconnected-$((2+2))", "reconnected-4")
	mark = len(app.Log())
	remote.Stop()
	outpostStateSince(app, "outpost", protocol.OutpostStateLost, mark)
	app.Send(protocol.DetachSessionMessage{Cmd: protocol.CmdDetachSession, ID: terminal})
	mark = len(app.Log())
	remote.Start()
	outpostStateSince(app, "outpost", protocol.OutpostStateConnected, mark)
	reattach()
	echo("echo outpost-restart-$((3+3))", "outpost-restart-6")
	home.Stop()
	home.Start()
	app = home.App()
	outpostStateOnConnect(app, "outpost", protocol.OutpostStateConnected)
	echo("echo home-restart-$((4+4))", "home-restart-8")
	mark = len(app.Log())
	remote.Stop()
	outpostStateSince(app, "outpost", protocol.OutpostStateLost, mark)
	killWorker(t, remote.Dir, string(terminal))
	mark = len(app.Log())
	remote.Start()
	outpostStateSince(app, "outpost", protocol.OutpostStateConnected, mark)
	testworld.AwaitSession(app, id, func(s protocol.Session) bool { return s.State == protocol.SessionStateRecoverable })

	mark = len(app.Log())
	remote.Stop()
	outpostStateSince(app, "outpost", protocol.OutpostStateLost, mark)
	mark = len(app.Log())
	remote.Start()
	outpostStateSince(app, "outpost", protocol.OutpostStateConnected, mark)
	sessions, err := home.Client().Query("")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		if string(s.ID) == id && s.State != protocol.SessionStateRecoverable {
			t.Fatalf("reconnect overwrote recovered state: %+v", s)
		}
	}
	revived := testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: terminal, AttachPolicy: protocol.Ptr(protocol.AttachPolicyRevive), Cols: protocol.Ptr(80), Rows: protocol.Ptr(24)}, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == terminal })
	if !protocol.Deref(revived.Revived) {
		t.Fatalf("shell was not revived: %s", protocol.Deref(revived.Error))
	}
	echo("echo revived-$((5+5))", "revived-10")
	testworld.AwaitSession(app, id, func(s protocol.Session) bool { return s.State != protocol.SessionStateRecoverable })
	t.Log("lost bridge refused input/resize/close/split/kill/reload/attach; live shell survived both daemon restarts; missing worker became recoverable")
}

func TestRemoteOutputResumesAfterAWorkerUpgradeWithoutDroppingTheBridge(t *testing.T) {
	old := testworld.AttnBinaryWithSnapshotFormat(t, "remote-stream-old")
	current := testworld.AttnBinaryWithSnapshotFormat(t, "remote-stream-current")
	a := testworld.NewSSHAgent(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	for _, s := range []*testworld.Stack{remote, home} {
		a.Authorize(s.World)
		s.Vars = append(s.Vars, "SHELL=/bin/bash")
	}
	upgrade := remote.PauseAt(pausepoint.PtyWorkerUpgrade)
	remote.StartBinary(old)
	home.StartBinary(current)
	address := bridgeAddress(t)
	requireStdout(t, remote.Attn("settings", "set", "outpost_listen", address), address)
	app := enableOutposts(t, home)
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Address: address})
	host := outpostState(app, "outpost", protocol.OutpostStateConnected).ID
	id := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
	terminal := protocol.TerminalID(app.Terminal(id))
	app.TypeLine(id, "echo before-upgrade-$((1+1))")
	app.AwaitScreen(id, "before-upgrade-2")
	mark := len(app.Log())
	remote.Stop()
	outpostStateSince(app, "outpost", protocol.OutpostStateLost, mark)
	app.Send(protocol.DetachSessionMessage{Cmd: protocol.CmdDetachSession, ID: terminal})
	mark = len(app.Log())
	remote.StartBinaryHeldAt(current, upgrade)
	outpostStateSince(app, "outpost", protocol.OutpostStateConnected, mark)
	attached := testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: terminal}, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == terminal })
	if !attached.Success {
		t.Fatalf("attach before worker handoff: %s", protocol.Deref(attached.Error))
	}
	mark = len(app.Log())
	upgrade.Release()
	t.Log("worker handoff released with the remote stream attached and bridge connected")
	desync := testworld.Await(app, protocol.EventPtyDesync, func(r protocol.WebSocketEvent) bool { return protocol.Deref(r.ID) == string(terminal) })
	if protocol.Deref(desync.Reason) != "runtime_stream_ended" {
		t.Fatalf("worker handoff desync: %s", protocol.Deref(desync.Reason))
	}
	attached = testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: terminal}, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == terminal })
	if !attached.Success {
		t.Fatalf("attach after worker handoff: %s", protocol.Deref(attached.Error))
	}
	app.TypeLine(id, "echo after-upgrade-$((2+2))")
	app.AwaitScreen(id, "after-upgrade-4")
	for _, frame := range app.Log()[mark:] {
		var update protocol.OutpostsUpdatedMessage
		if json.Unmarshal(frame, &update) == nil && update.Event == protocol.EventOutpostsUpdated {
			for _, row := range update.Outposts {
				if row.ID == host && row.State != protocol.OutpostStateConnected {
					t.Fatalf("worker upgrade interrupted the bridge: %s", row.State)
				}
			}
		}
	}
}

func TestRemovingAnOutpostClosesItsTerminals(t *testing.T) {
	for _, online := range []bool{true, false} {
		t.Run(fmt.Sprintf("online=%v", online), func(t *testing.T) {
			a := testworld.NewSSHAgent(t)
			remote, home := testworld.NewStack(t), testworld.NewStack(t)
			for _, s := range []*testworld.Stack{remote, home} {
				a.Authorize(s.World)
				s.Vars = append(s.Vars, "SHELL=/bin/bash")
				s.Start()
			}
			address := bridgeAddress(t)
			requireStdout(t, remote.Attn("settings", "set", "outpost_listen", address), address)
			app := enableOutposts(t, home)
			app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Address: address})
			host := outpostState(app, "outpost", protocol.OutpostStateConnected).ID
			remoteApp := remote.App()
			id := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(host) })
			terminal := protocol.TerminalID(app.Terminal(id))
			app.TypeLine(id, "echo remove-$((1+1))")
			app.AwaitScreen(id, "remove-2")
			if !online {
				mark := app.EventCount()
				remote.Stop()
				outpostStateSince(app, "outpost", protocol.OutpostStateLost, mark)
			}
			app.Send(protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: host})
			testworld.Await(app, protocol.EventOutpostsUpdated, func(e protocol.OutpostsUpdatedMessage) bool { return len(e.Outposts) == 0 })
			sessions, err := home.Client().Query("")
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range sessions {
				if protocol.Deref(s.Host) == host {
					t.Fatalf("removed outpost still has a live row: %+v", s)
				}
			}
			if !online {
				requireStdout(t, remote.Attn("enrollment", "leave"), "no longer enrolled")
				remote.Start()
				remoteApp = remote.App()
			}
			result := testworld.Request(remoteApp, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: terminal}, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == terminal })
			if result.Success && protocol.Deref(result.Running) {
				t.Fatalf("removed terminal still runs: %+v", result)
			}
			t.Logf("online=%v: home has no live host rows; outpost terminal no longer runs", online)
		})
	}
}

func outpostStateSince(p *testworld.Peer, name string, state protocol.OutpostState, mark int) protocol.OutpostInfo {
	p.T.Helper()
	var found protocol.OutpostInfo
	testworld.AwaitSince(p, mark, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		for _, row := range m.Outposts {
			if row.Name == name && row.State == state {
				found = row
				return true
			}
		}
		return false
	})
	return found
}

func TestRemoteWaitLetsInputFinishTheShell(t *testing.T) {
	a := testworld.NewSSHAgent(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	waiting := remote.PauseAt(pausepoint.PtyWaitBlock)
	for _, s := range []*testworld.Stack{remote, home} {
		a.Authorize(s.World)
		s.Vars = append(s.Vars, "SHELL=/bin/bash")
		s.Start()
	}
	address := bridgeAddress(t)
	requireStdout(t, remote.Attn("settings", "set", "outpost_listen", address), address)
	app := enableOutposts(t, home)
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Address: address})
	host := outpostState(app, "outpost", protocol.OutpostStateConnected).ID
	caller := home.Spawn(app, fakeagent.Claude, home.Path("agent"))
	home.Launched(caller)
	id := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
	terminal := app.Terminal(id)
	app.TypeLine(id, "printf 'blocking-command-ready\n'; sleep 600")
	app.AwaitScreen(id, "blocking-command-ready")
	wait := home.Launch(testworld.Invocation{Session: caller, Args: []string{"terminal", "wait", terminal, "--until", "exit", "--json"}})
	waiting.Await()
	got := home.Run(testworld.Invocation{Session: caller, Args: []string{"terminal", "read", terminal}})
	if got.Code != 0 || !strings.Contains(got.Stdout, "blocking-command-ready") {
		t.Fatalf("read blocked behind wait: %+v", got)
	}
	got = home.Run(testworld.Invocation{Session: caller, Args: []string{"terminal", "send", terminal, "--keys", "C-c"}})
	if got.Code != 0 {
		t.Fatalf("input blocked behind wait: %+v", got)
	}
	app.TypeLine(id, "exit 7")
	testworld.Await(app, protocol.EventSessionExited, func(m protocol.SessionExitedMessage) bool { return string(m.SessionID) == id })
	waiting.Release()
	result := wait.Wait()
	var exited protocol.TerminalWaitResult
	result.JSON(t, &exited)
	if result.Code != 7 || !protocol.Deref(exited.Exited) || protocol.Deref(exited.ExitCode) != 7 {
		t.Fatalf("wait did not observe exit: %+v %+v", result, exited)
	}
}

func TestReconnectDeliversACloseCommittedWhileTheBridgeBreaks(t *testing.T) {
	a := testworld.NewSSHAgent(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	applied := remote.PauseAt(pausepoint.CopyBatchApplied)
	prepared := home.PauseAt(pausepoint.SessionTeardownPrepared)
	for _, s := range []*testworld.Stack{remote, home} {
		a.Authorize(s.World)
		s.Vars = append(s.Vars, "SHELL=/bin/bash")
		s.Start()
	}
	address := bridgeAddress(t)
	requireStdout(t, remote.Attn("settings", "set", "outpost_listen", address), address)
	app := enableOutposts(t, home)
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Address: address})
	host := outpostState(app, "outpost", protocol.OutpostStateConnected).ID
	id := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
	terminal := protocol.TerminalID(app.Terminal(id))
	app.TypeLine(id, "echo ready-$((2+2))")
	app.AwaitScreen(id, "ready-4")
	applied.Await()
	applied.Release()
	app.Send(protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: protocol.SessionID(id)})
	prepared.Await()
	remote.Kill()
	prepared.Release()
	testworld.Await(app, protocol.EventSessionUnregistered, func(m protocol.WebSocketEvent) bool { return m.Session != nil && string(m.Session.ID) == id })
	remote.Start()
	applied.Await()
	applied.Release()
	peer := remote.App()
	result := testworld.Request(peer, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: terminal}, protocol.EventAttachResult, func(m protocol.AttachResultMessage) bool { return m.ID == terminal })
	if result.Success && protocol.Deref(result.Running) {
		t.Fatalf("committed close left remote worker running: %+v", result)
	}
}

func TestRemoteInputKeepsItsBytesAndProbesWithoutBlockingALocalTile(t *testing.T) {
	a := testworld.NewSSHAgent(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	for _, s := range []*testworld.Stack{remote, home} {
		a.Authorize(s.World)
		s.Vars = append(s.Vars, "SHELL=/bin/bash")
		s.Start()
	}
	address := bridgeAddress(t)
	requireStdout(t, remote.Attn("settings", "set", "outpost_listen", address), address)
	app := enableOutposts(t, home)
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Address: address})
	host := outpostState(app, "outpost", protocol.OutpostStateConnected).ID
	id := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
	terminal := protocol.TerminalID(app.Terminal(id))
	local := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), home.Path("local"))
	app.TypeLine(id, `stty raw -echo; python3 -u -c 'import sys,os,hashlib; print("raw-"+"ready",flush=True); line=sys.stdin.buffer.readline(); print("RESULT:"+hashlib.sha256(line.strip()).hexdigest()+":"+str(os.get_terminal_size().columns)+":"+str(os.get_terminal_size().lines),flush=True)'; stty sane`)
	app.AwaitScreen(id, "raw-ready")
	remote.Signal(syscall.SIGSTOP)
	defer remote.Signal(syscall.SIGCONT)
	var expected strings.Builder
	send := func(i int, data string) {
		trace := fmt.Sprintf("batch-%03d", i)
		app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: data, TraceID: &trace, ProbeID: &trace})
	}
	first := "token-000,"
	expected.WriteString(first)
	send(0, first)
	outpostState(app, "outpost", protocol.OutpostStateStalled)
	mark := len(app.Log())
	for i := 1; i < 100; i++ {
		data := fmt.Sprintf("token-%03d,", i)
		expected.WriteString(data)
		send(i, data)
	}
	app.Send(protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: terminal, Cols: 93, Rows: 31})
	send(100, "\n")
	app.TypeLine(local, "echo local-while-remote-stalled-$((2+2))")
	app.AwaitScreen(local, "local-while-remote-stalled-4")
	for _, frame := range app.Log()[mark:] {
		var update protocol.OutpostsUpdatedMessage
		if json.Unmarshal(frame, &update) == nil && update.Event == protocol.EventOutpostsUpdated {
			for _, row := range update.Outposts {
				if row.ID == host && row.State == protocol.OutpostStateLost {
					t.Fatal("local input waited until the remote bridge was lost")
				}
			}
		}
	}
	remote.Signal(syscall.SIGCONT)
	app.AwaitScreen(id, fmt.Sprintf("RESULT:%x:93:31", sha256.Sum256([]byte(expected.String()))))
	for i := 0; i <= 100; i++ {
		probe := fmt.Sprintf("batch-%03d", i)
		result := testworld.Await(app, protocol.EventPtyInputProbeResult, func(m protocol.PtyInputProbeResultMessage) bool { return m.ProbeID == probe })
		if !result.Success {
			t.Fatalf("input probe %s failed: %s", probe, protocol.Deref(result.Error))
		}
	}
	snapshot := testworld.Request(app, protocol.SupportSnapshotMessage{Cmd: protocol.CmdSupportSnapshot, RequestID: "batch-traces", RuntimeIds: []protocol.TerminalID{terminal}}, protocol.EventSupportSnapshotResult, func(m protocol.SupportSnapshotResultMessage) bool { return m.RequestID == "batch-traces" })
	traces := map[string]bool{}
	for _, trace := range snapshot.InputTraces {
		traces[trace.TraceID] = true
	}
	for i := 0; i <= 100; i++ {
		if !traces[fmt.Sprintf("batch-%03d", i)] {
			t.Fatalf("trace %d was lost from joined input", i)
		}
	}
}

func TestRemoteTileActionsDoNotBlockALocalTile(t *testing.T) {
	for _, command := range []string{protocol.CmdTerminalPointerActivity, protocol.CmdGetKittyImage, protocol.CmdDesktopCloseTile, protocol.CmdRemoveOutpost} {
		t.Run(command, func(t *testing.T) {
			a := testworld.NewSSHAgent(t)
			remote, home := testworld.NewStack(t), testworld.NewStack(t)
			for _, s := range []*testworld.Stack{remote, home} {
				a.Authorize(s.World)
				s.Vars = append(s.Vars, "SHELL=/bin/bash")
				s.Start()
			}
			address := bridgeAddress(t)
			requireStdout(t, remote.Attn("settings", "set", "outpost_listen", address), address)
			app := enableOutposts(t, home)
			app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Address: address})
			host := outpostState(app, "outpost", protocol.OutpostStateConnected).ID
			spawned, desktop, tile := home.RequestSpawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
			if !spawned.Success {
				t.Fatalf("remote spawn: %s", protocol.Deref(spawned.Error))
			}
			id := string(spawned.ID)
			terminal := protocol.TerminalID(app.Terminal(id))
			local := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), home.Path("local"))
			app.TypeLine(id, "echo remote-$((1+1))")
			app.AwaitScreen(id, "remote-2")
			app.TypeLine(local, "echo local-$((1+1))")
			app.AwaitScreen(local, "local-2")
			remote.Signal(syscall.SIGSTOP)
			defer remote.Signal(syscall.SIGCONT)
			outpostState(app, "outpost", protocol.OutpostStateStalled)
			mark := len(app.Log())
			switch command {
			case protocol.CmdTerminalPointerActivity:
				app.Send(protocol.TerminalPointerActivityMessage{Cmd: command, ID: terminal})
			case protocol.CmdGetKittyImage:
				app.Send(protocol.GetKittyImageMessage{Cmd: command, ID: terminal, ImageID: 1})
			case protocol.CmdDesktopCloseTile:
				app.Send(protocol.DesktopCloseTileMessage{Cmd: command, RequestID: "close-held", DesktopID: desktop, TileID: tile})
			case protocol.CmdRemoveOutpost:
				app.Send(protocol.RemoveOutpostMessage{Cmd: command, ID: host})
			}
			app.TypeLine(local, "echo local-during-action-$((2+2))")
			app.AwaitScreen(local, "local-during-action-4")
			for _, frame := range app.Log()[mark:] {
				var update protocol.OutpostsUpdatedMessage
				if json.Unmarshal(frame, &update) == nil && update.Event == protocol.EventOutpostsUpdated {
					for _, row := range update.Outposts {
						if row.ID == host && row.State == protocol.OutpostStateLost {
							t.Fatal("local input waited until the remote bridge was lost")
						}
					}
				}
			}
			remote.Signal(syscall.SIGCONT)
			switch command {
			case protocol.CmdTerminalPointerActivity:
				app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, ProbeID: protocol.Ptr("after-pointer")})
				result := testworld.Await(app, protocol.EventPtyInputProbeResult, func(r protocol.PtyInputProbeResultMessage) bool { return r.ProbeID == "after-pointer" })
				if !result.Success {
					t.Fatalf("input after pointer: %s", protocol.Deref(result.Error))
				}
			case protocol.CmdGetKittyImage:
				testworld.Await(app, protocol.EventKittyImageResult, func(r protocol.KittyImageResultMessage) bool { return r.ID == terminal && r.ImageID == 1 })
			case protocol.CmdDesktopCloseTile:
				result := testworld.Await(app, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "close-held" })
				if !result.Success {
					t.Fatalf("close tile: %s", protocol.Deref(result.Error))
				}
			case protocol.CmdRemoveOutpost:
				testworld.AwaitSince(app, mark, protocol.EventOutpostsUpdated, func(r protocol.OutpostsUpdatedMessage) bool { return len(r.Outposts) == 0 })
			}
		})
	}
}

func TestRemoteReloadDoesNotSendQueuedInputToTheReplacementShell(t *testing.T) {
	a := testworld.NewSSHAgent(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	for _, s := range []*testworld.Stack{remote, home} {
		a.Authorize(s.World)
		s.Vars = append(s.Vars, "SHELL=/bin/bash")
		s.Start()
	}
	address := bridgeAddress(t)
	requireStdout(t, remote.Attn("settings", "set", "outpost_listen", address), address)
	app := enableOutposts(t, home)
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Address: address})
	host := outpostState(app, "outpost", protocol.OutpostStateConnected).ID
	id := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
	terminal := protocol.TerminalID(app.Terminal(id))
	app.TypeLine(id, "export GENERATION=old; echo old-env-$GENERATION")
	app.AwaitScreen(id, "old-env-old")
	remote.Signal(syscall.SIGSTOP)
	defer remote.Signal(syscall.SIGCONT)
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: ":\r"})
	outpostState(app, "outpost", protocol.OutpostStateStalled)
	marker := remote.Path("shell", "before-reload")
	for i := range 20 {
		app.Send(protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: terminal, Cols: 80 + i%2, Rows: 24})
	}
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: fmt.Sprintf("printf '%%s' \"${GENERATION-missing}\" > %q\r", marker), ProbeID: protocol.Ptr("old-input")})
	app.Send(protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(id), Cols: 80, Rows: 24})
	testworld.Request(app, protocol.SupportSnapshotMessage{Cmd: protocol.CmdSupportSnapshot, RequestID: "queued-before-resume", RuntimeIds: []protocol.TerminalID{terminal}}, protocol.EventSupportSnapshotResult, func(m protocol.SupportSnapshotResultMessage) bool { return m.RequestID == "queued-before-resume" })
	remote.Signal(syscall.SIGCONT)
	reload := testworld.Await(app, protocol.EventReloadSessionResult, func(m protocol.ReloadSessionResultMessage) bool { return string(m.ID) == id })
	if !reload.Success {
		t.Fatalf("reload: %s", protocol.Deref(reload.Error))
	}
	probe := testworld.Await(app, protocol.EventPtyInputProbeResult, func(m protocol.PtyInputProbeResultMessage) bool { return m.ProbeID == "old-input" })
	if !probe.Success {
		t.Fatalf("queued input: %s", protocol.Deref(probe.Error))
	}
	app.TypeLine(id, fmt.Sprintf("printf '__old_input_%%s=%%s\\n' receipt \"$(cat %q 2>/dev/null || printf absent)\"", marker))
	app.AwaitScreen(id, "__old_input_receipt=")
	screen := string(app.Screen(id))
	if !strings.Contains(screen, "__old_input_receipt=old") && !strings.Contains(screen, "__old_input_receipt=absent") {
		t.Fatalf("queued input ran in the replacement shell: %s", screen)
	}
}
