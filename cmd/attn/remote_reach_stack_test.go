package main_test

import (
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func outpostReachSince(p *testworld.Peer, mark int, name string, reach protocol.OutpostReach) protocol.OutpostInfo {
	p.T.Helper()
	var found protocol.OutpostInfo
	testworld.AwaitSince(p, mark, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		for _, row := range m.Outposts {
			if row.Name == name && row.Reach == reach {
				found = row
				return true
			}
		}
		return false
	})
	return found
}

func typeRefused(t *testing.T, p *testworld.Peer, terminal protocol.TerminalID, keys string) {
	t.Helper()
	for _, key := range keys {
		p.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: string(key)})
	}
	awaitInputRefusals(t, p, terminal, len(keys))
}

func awaitInputRefusals(t *testing.T, p *testworld.Peer, terminal protocol.TerminalID, count int) {
	t.Helper()
	for range count {
		refused := testworld.Await(p, protocol.EventCommandError, func(e protocol.CommandErrorMessage) bool {
			return protocol.Deref(e.Cmd) == protocol.CmdPtyInput
		})
		if protocol.Deref(refused.TerminalID) != string(terminal) {
			t.Fatalf("refused key: %+v", refused)
		}
	}
}

func TestRemoteTerminalRefusalsNameTheirTerminalAndNothingIsReplayed(t *testing.T) {
	a := testworld.NewSSH(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	for _, s := range []*testworld.Stack{remote, home} {
		a.Use(s.World)
		s.Vars = append(s.Vars, "SHELL=/bin/bash")
		s.Start()
	}
	address := bridgeAddress(t)
	a.Route(address, remote)
	app := enableOutposts(t, home)
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: address})
	host := outpostReachSince(app, 0, "outpost", protocol.OutpostReachReachable).ID
	id := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(host) })
	terminal := protocol.TerminalID(app.Terminal(id))
	reattach := func(p *testworld.Peer) {
		t.Helper()
		result := testworld.Request(p, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: terminal}, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == terminal })
		if !result.Success {
			t.Fatalf("reattach: %s", protocol.Deref(result.Error))
		}
	}
	app.TypeLine(id, "echo ready-$((1+1))")
	app.AwaitScreen(id, "ready-2")

	stalledLine := "echo stalled-$((3+4))\r"
	remote.Signal(syscall.SIGSTOP)
	if stalled := outpostState(app, "outpost", protocol.OutpostStateStalled); stalled.Reach != protocol.OutpostReachReachable {
		t.Fatalf("stalled row: %+v", stalled)
	}
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: "\x15", Source: protocol.Ptr("response")})
	for _, key := range stalledLine {
		app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: string(key)})
	}
	awaitInputRefusals(t, app, terminal, len(stalledLine))
	outpostReachSince(app, 0, "outpost", protocol.OutpostReachUnreachable)
	app.Send(protocol.DetachSessionMessage{Cmd: protocol.CmdDetachSession, ID: terminal})
	mark := len(app.Log())
	remote.Signal(syscall.SIGCONT)
	outpostReachSince(app, mark, "outpost", protocol.OutpostReachReachable)
	reattach(app)
	app.TypeLine(id, "echo after-stall-$((4+4))")
	app.AwaitScreen(id, "after-stall-8")
	if strings.Contains(string(app.Screen(id)), "stalled-7") {
		t.Fatalf("keys refused at lost ran later:\n%s", app.Screen(id))
	}

	mark = len(app.Log())
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "false"), "false")
	off := outpostReachSince(app, mark, "outpost", protocol.OutpostReachUnreachable)
	if off.ID != host {
		t.Fatalf("Outposts off row: %+v", off)
	}
	typeRefused(t, app, terminal, "echo off\r")
	app.Send(protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: protocol.SessionID(id)})
	if refused := testworld.Refused(app); !strings.Contains(protocol.Deref(refused.Error), "outpost is unreachable") {
		t.Fatalf("close with Outposts off: %+v", refused)
	}
	app.Send(protocol.DetachSessionMessage{Cmd: protocol.CmdDetachSession, ID: terminal})
	mark = len(app.Log())
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "true"), "true")
	outpostReachSince(app, mark, "outpost", protocol.OutpostReachReachable)
	reattach(app)

	remote.Signal(syscall.SIGSTOP)
	defer remote.Signal(syscall.SIGCONT)
	home.Stop()
	home.Start()
	app = home.App()
	if initial := app.Initial; len(initial.Outposts) != 1 || initial.Outposts[0].Reach != protocol.OutpostReachConnecting {
		t.Fatalf("initial_state outposts after a home restart with the outpost frozen: %+v", initial.Outposts)
	}
	typeRefused(t, app, terminal, "echo connecting\r")
	outpostReachSince(app, 0, "outpost", protocol.OutpostReachUnreachable)
	typeRefused(t, app, terminal, "echo lost\r")
	mark = len(app.Log())
	remote.Signal(syscall.SIGCONT)
	outpostReachSince(app, mark, "outpost", protocol.OutpostReachReachable)
	reattach(app)
	app.TypeLine(id, "echo back-$((5+5))")
	app.AwaitScreen(id, "back-10")
	for _, refused := range []string{"stalled-7", "echo off", "echo connecting", "echo lost"} {
		if strings.Contains(string(app.Screen(id)), refused) {
			t.Fatalf("refused input %q reached the shell:\n%s", refused, app.Screen(id))
		}
	}
	t.Log("stall then lost, Outposts off, and connecting after a frozen home restart: every refused key named its terminal; none reached the shell")
}
