package main_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func outpostReachSince(p *testworld.Peer, mark int, name string, reach protocol.OutpostReach) protocol.OutpostInfo {
	p.T.Helper()
	var previous string
	if reach == protocol.OutpostReachReachable && mark > 0 {
		rows := p.Initial.Outposts
		for _, raw := range p.Log()[:mark] {
			var update protocol.OutpostsUpdatedMessage
			if json.Unmarshal(raw, &update) == nil && update.Event == protocol.EventOutpostsUpdated {
				rows = update.Outposts
			}
		}
		for _, row := range rows {
			if row.Name == name {
				previous = row.StateSince
			}
		}
	}
	var found protocol.OutpostInfo
	testworld.AwaitSince(p, mark, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		for _, row := range m.Outposts {
			if row.Name == name && row.Reach == reach && (previous == "" || row.StateSince != previous) {
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
	resume := a.Pause(address)
	home.Advance(bridge.StallAfter)
	if stalled := outpostState(app, "outpost", protocol.OutpostStateStalled); stalled.Reach != protocol.OutpostReachReachable {
		t.Fatalf("stalled row: %+v", stalled)
	}
	mark := len(app.Log())
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: "\x15", Source: protocol.Ptr("response")})
	for i, key := range stalledLine {
		probe := fmt.Sprintf("stalled-%d", i)
		app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: string(key), ProbeID: &probe, TraceID: &probe})
	}
	testworld.Request(app, protocol.SupportSnapshotMessage{Cmd: protocol.CmdSupportSnapshot, RequestID: "before-loss", RuntimeIds: []protocol.TerminalID{terminal}}, protocol.EventSupportSnapshotResult, func(m protocol.SupportSnapshotResultMessage) bool { return m.RequestID == "before-loss" })
	home.Advance(bridge.LostAfter - bridge.StallAfter)
	outpostReachSince(app, 0, "outpost", protocol.OutpostReachUnreachable)
	for i := range stalledLine {
		probe := fmt.Sprintf("stalled-%d", i)
		result := testworld.Await(app, protocol.EventPtyInputProbeResult, func(m protocol.PtyInputProbeResultMessage) bool { return m.ProbeID == probe })
		if result.Success || protocol.Deref(result.Error) != bridge.ErrUnanswered.Error() {
			t.Fatalf("unanswered key %s: %+v", probe, result)
		}
	}
	for _, frame := range app.Log()[mark:] {
		var refused protocol.CommandErrorMessage
		if json.Unmarshal(frame, &refused) == nil && refused.Event == protocol.EventCommandError && protocol.Deref(refused.Cmd) == protocol.CmdPtyInput {
			t.Fatalf("unanswered key flashed its tile: %+v", refused)
		}
	}
	snapshot := testworld.Request(app, protocol.SupportSnapshotMessage{Cmd: protocol.CmdSupportSnapshot, RequestID: "unanswered-input", RuntimeIds: []protocol.TerminalID{terminal}}, protocol.EventSupportSnapshotResult, func(m protocol.SupportSnapshotResultMessage) bool { return m.RequestID == "unanswered-input" })
	traces := map[string]protocol.SupportInputTrace{}
	for _, trace := range snapshot.InputTraces {
		traces[trace.TraceID] = trace
	}
	for i := range stalledLine {
		probe := fmt.Sprintf("stalled-%d", i)
		if trace := traces[probe]; trace.WriteResult != "failed" || protocol.Deref(trace.ErrorClass) != "bridge_unanswered" {
			t.Fatalf("unanswered key trace %s: %+v", probe, trace)
		}
	}
	app.Send(protocol.DetachSessionMessage{Cmd: protocol.CmdDetachSession, ID: terminal})
	mark = len(app.Log())
	resume()
	home.Advance(bridge.ReconnectInterval)
	outpostReachSince(app, mark, "outpost", protocol.OutpostReachReachable)
	reattach(app)
	app.TypeLine(id, "\x15echo after-stall-$((4+4))")
	app.AwaitScreen(id, "after-stall-8")

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

	resume = a.Pause(address)
	defer resume()
	home.Stop()
	home.Start()
	app = home.App()
	if initial := app.Initial; len(initial.Outposts) != 1 || initial.Outposts[0].Reach != protocol.OutpostReachConnecting {
		t.Fatalf("initial_state outposts after a home restart with the outpost frozen: %+v", initial.Outposts)
	}
	typeRefused(t, app, terminal, "echo connecting\r")
	a.Fail(address, "ssh: connect to host machine port 22: No route to host")
	a.ExitMaster(home, host)
	outpostReachSince(app, 0, "outpost", protocol.OutpostReachUnreachable)
	typeRefused(t, app, terminal, "echo lost\r")
	mark = len(app.Log())
	home.AwaitTimer(bridge.ReconnectInterval)
	a.Restore(address)
	resume()
	home.Advance(bridge.ReconnectInterval)
	outpostReachSince(app, mark, "outpost", protocol.OutpostReachReachable)
	reattach(app)
	app.TypeLine(id, "echo back-$((5+5))")
	app.AwaitScreen(id, "back-10")
	for _, refused := range []string{"echo off", "echo connecting", "echo lost"} {
		if strings.Contains(string(app.Screen(id)), refused) {
			t.Fatalf("refused input %q reached the shell:\n%s", refused, app.Screen(id))
		}
	}
	t.Log("keys sent while stalled ended unanswered without a refusal; keys refused with Outposts off, connecting or lost named their terminal and never reached the shell")
}
