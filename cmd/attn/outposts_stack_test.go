package main_test

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func outpostState(p *testworld.Peer, name string, state protocol.OutpostState) protocol.OutpostInfo {
	var found protocol.OutpostInfo
	testworld.Await[protocol.OutpostsUpdatedMessage](p, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
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

func outpostStateOnConnect(p *testworld.Peer, name string, state protocol.OutpostState) protocol.OutpostInfo {
	p.T.Helper()
	for _, row := range p.Initial.Outposts {
		if row.Name == name && row.State == state {
			return row
		}
	}
	return outpostState(p, name, state)
}

func enableOutposts(t *testing.T, s *testworld.Stack) *testworld.Peer {
	t.Helper()
	p := s.App()
	requireStdout(t, s.Attn("settings", "set", "outposts_enabled", "true"), "true")
	testworld.Await[protocol.OutpostsUpdatedMessage](p, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 0 })
	return p
}

func TestOutpostsPairRestartRefuseSecondHomeAndRelease(t *testing.T) {
	a := testworld.NewSSHAgent(t)
	remote, home, second := testworld.NewStack(t), testworld.NewStack(t), testworld.NewStack(t)
	for _, s := range []*testworld.Stack{remote, home, second} {
		a.Authorize(s.World)
		s.Start()
	}
	address := bridgeAddress(t)
	requireStdout(t, remote.Attn("settings", "set", "outpost_listen", address), address)
	p := enableOutposts(t, home)
	p.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Address: address})
	first := outpostState(p, "outpost", protocol.OutpostStateConnected)
	if first.DaemonID == nil || first.HostKeyFingerprint == nil || first.Build == nil {
		t.Fatalf("unpaired connected row: %+v", first)
	}
	remote.Stop()
	remote.Start()
	again := outpostState(p, "outpost", protocol.OutpostStateConnected)
	if protocol.Deref(again.DaemonID) != protocol.Deref(first.DaemonID) || protocol.Deref(again.HostKeyFingerprint) != protocol.Deref(first.HostKeyFingerprint) {
		t.Fatalf("restart changed pins: %+v -> %+v", first, again)
	}
	q := enableOutposts(t, second)
	q.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "same outpost", Address: address})
	refused := outpostState(q, "same outpost", protocol.OutpostStateRefused)
	for _, word := range []string{protocol.Deref(p.Initial.DaemonInstanceID), protocol.Deref(q.Initial.DaemonInstanceID), "attn enrollment leave"} {
		if !strings.Contains(protocol.Deref(refused.LastError), word) {
			t.Fatalf("refusal omits %q: %+v", word, refused)
		}
	}
	p.Send(protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: first.ID})
	testworld.Await[protocol.OutpostsUpdatedMessage](p, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 0 })
	requireStdout(t, remote.Attn("enrollment", "leave"), "no longer enrolled")
	q.Send(protocol.RepairOutpostMessage{Cmd: protocol.CmdRepairOutpost, ID: refused.ID})
	outpostState(q, "same outpost", protocol.OutpostStateConnected)
	q.Send(protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: refused.ID})
	testworld.Await[protocol.OutpostsUpdatedMessage](q, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 0 })
	second.Stop()
	second.Start()
	if got := second.App().Initial.Outposts; len(got) != 0 {
		t.Fatalf("removed outpost resurrected: %+v", got)
	}
}

func TestOutpostsDisabledCommandsAndRetainedRows(t *testing.T) {
	s := testworld.NewStack(t)
	s.Start()
	p := s.App()
	for _, command := range []any{protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "x", Address: "127.0.0.1:1"}, protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: "missing"}, protocol.RepairOutpostMessage{Cmd: protocol.CmdRepairOutpost, ID: "missing"}} {
		p.Send(command)
		refused := testworld.Refused(p)
		if !strings.Contains(protocol.Deref(refused.Error), "outposts_enabled") {
			t.Fatalf("disabled command: %+v", refused)
		}
	}
	p = enableOutposts(t, s)
	p.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "unreachable", Address: "127.0.0.1:1"})
	row := outpostState(p, "unreachable", protocol.OutpostStateRefused)
	if !strings.Contains(protocol.Deref(row.LastError), "no SSH_AUTH_SOCK") {
		t.Fatalf("missing agent: %+v", row)
	}
	requireStdout(t, s.Attn("settings", "set", "outposts_enabled", "false"), "false")
	if off := outpostState(p, "unreachable", protocol.OutpostStateLost); off.ID != row.ID || off.Reach != protocol.OutpostReachUnreachable {
		t.Fatalf("row while Outposts is off: %+v", off)
	}
	s.Stop()
	s.Start()
	p = s.App()
	requireStdout(t, s.Attn("settings", "set", "outposts_enabled", "true"), "true")
	again := outpostState(p, "unreachable", protocol.OutpostStateRefused)
	if again.ID != row.ID {
		t.Fatalf("switch lost row: %+v -> %+v", row, again)
	}
}

func TestOutpostsDifferentBuildIsRefused(t *testing.T) {
	a := testworld.NewSSHAgent(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	remote.RunFrom(testworld.AttnBinaryAt(t, "other"))
	for _, s := range []*testworld.Stack{remote, home} {
		a.Authorize(s.World)
		s.Start()
	}
	address := bridgeAddress(t)
	requireStdout(t, remote.Attn("settings", "set", "outpost_listen", address), address)
	p := enableOutposts(t, home)
	p.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "other build", Address: address})
	row := outpostState(p, "other build", protocol.OutpostStateRefused)
	for _, word := range []string{"other", "dev", protocol.Deref(p.Initial.DaemonInstanceID)} {
		if !strings.Contains(protocol.Deref(row.LastError), word) {
			t.Fatalf("refusal omits %q: %+v", word, row)
		}
	}
}

func TestOutpostsFrozenPeerStallsLosesAndRecovers(t *testing.T) {
	a := testworld.NewSSHAgent(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	for _, s := range []*testworld.Stack{remote, home} {
		a.Authorize(s.World)
		s.Start()
	}
	address := bridgeAddress(t)
	requireStdout(t, remote.Attn("settings", "set", "outpost_listen", address), address)
	p := enableOutposts(t, home)
	p.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "frozen", Address: address})
	first := outpostState(p, "frozen", protocol.OutpostStateConnected)
	remote.Signal(syscall.SIGSTOP)
	defer remote.Signal(syscall.SIGCONT)
	stalled := outpostState(p, "frozen", protocol.OutpostStateStalled)
	if stalled.LastError != nil {
		t.Fatalf("stall invents an error: %+v", stalled)
	}
	lost := outpostState(p, "frozen", protocol.OutpostStateLost)
	if !strings.Contains(protocol.Deref(lost.LastError), "10s") {
		t.Fatalf("loss omits threshold: %+v", lost)
	}
	remote.Signal(syscall.SIGCONT)
	again := outpostState(p, "frozen", protocol.OutpostStateConnected)
	if again.LastError != nil || again.LastErrorAt != nil || protocol.Deref(again.DaemonID) != protocol.Deref(first.DaemonID) {
		t.Fatalf("recovery: %+v", again)
	}
}

func TestOutpostsChangedIdentityRequiresRepair(t *testing.T) {
	for _, sameKey := range []bool{false, true} {
		name := "new host key"
		if sameKey {
			name = "same key new id"
		}
		t.Run(name, func(t *testing.T) {
			a := testworld.NewSSHAgent(t)
			old, home, replacement := testworld.NewStack(t), testworld.NewStack(t), testworld.NewStack(t)
			for _, s := range []*testworld.Stack{old, home, replacement} {
				a.Authorize(s.World)
			}
			old.Start()
			home.Start()
			address := bridgeAddress(t)
			requireStdout(t, old.Attn("settings", "set", "outpost_listen", address), address)
			p := enableOutposts(t, home)
			p.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "replaced", Address: address})
			first := outpostState(p, "replaced", protocol.OutpostStateConnected)
			old.Stop()
			if sameKey {
				key, err := os.ReadFile(filepath.Join(old.Dir, "bridge_host_key"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(replacement.Dir, "bridge_host_key"), key, 0600); err != nil {
					t.Fatal(err)
				}
			}
			replacement.Start()
			remote := replacement.App()
			requireStdout(t, replacement.Attn("settings", "set", "outpost_listen", address), address)
			refused := outpostState(p, "replaced", protocol.OutpostStateRefused)
			words := []string{address, protocol.Deref(first.DaemonID), "re-pair it in Settings → Outposts"}
			if sameKey {
				words = append(words, protocol.Deref(remote.Initial.DaemonInstanceID))
			} else {
				words = append(words, protocol.Deref(first.HostKeyFingerprint), "received SHA256:")
			}
			for _, word := range words {
				if !strings.Contains(protocol.Deref(refused.LastError), word) {
					t.Fatalf("refusal omits %q: %+v", word, refused)
				}
			}
			p.Close()
			p = home.App()
			replacement.Signal(syscall.SIGSTOP)
			defer replacement.Signal(syscall.SIGCONT)
			p.Send(protocol.RepairOutpostMessage{Cmd: protocol.CmdRepairOutpost, ID: first.ID})
			connecting := outpostState(p, "replaced", protocol.OutpostStateConnecting)
			if protocol.Deref(connecting.LastError) != protocol.Deref(refused.LastError) || protocol.Deref(connecting.LastErrorAt) != protocol.Deref(refused.LastErrorAt) {
				t.Fatalf("repair cleared the last error before success: before=%+v after=%+v", refused, connecting)
			}
			replacement.Signal(syscall.SIGCONT)
			repaired := outpostState(p, "replaced", protocol.OutpostStateConnected)
			if protocol.Deref(repaired.DaemonID) != protocol.Deref(remote.Initial.DaemonInstanceID) {
				t.Fatalf("repair kept old pin: %+v", repaired)
			}
		})
	}
}

func TestOutpostsAddressAliasDoesNotReplaceTheWorkingBridge(t *testing.T) {
	a := testworld.NewSSHAgent(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	for _, s := range []*testworld.Stack{remote, home} {
		a.Authorize(s.World)
		s.Start()
	}
	address := bridgeAddress(t)
	requireStdout(t, remote.Attn("settings", "set", "outpost_listen", address), address)
	p := enableOutposts(t, home)
	p.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "original", Address: address})
	first := outpostState(p, "original", protocol.OutpostStateConnected)
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	p.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "alias", Address: net.JoinHostPort("localhost", port)})
	refused := outpostState(p, "alias", protocol.OutpostStateRefused)
	if !strings.Contains(protocol.Deref(refused.LastError), "same outpost as original ("+address+")") {
		t.Fatalf("alias refusal: %+v", refused)
	}
	for _, row := range home.App().Initial.Outposts {
		if row.ID == first.ID && (row.State != protocol.OutpostStateConnected || row.StateSince != first.StateSince) {
			t.Fatalf("alias replaced the working bridge: before=%+v after=%+v", first, row)
		}
	}
}

func TestOutpostsUnchangedRetryKeepsItsStateAndPublishesNoFacts(t *testing.T) {
	a := testworld.NewSSHAgent(t)
	home := testworld.NewStack(t)
	a.Authorize(home.World)
	home.Start()
	p := enableOutposts(t, home)
	p.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "offline", Address: bridgeAddress(t)})
	first := outpostState(p, "offline", protocol.OutpostStateLost)
	facts := func() int64 {
		t.Helper()
		var status struct {
			Producers []struct {
				Name   string `json:"name"`
				Events int64  `json:"events"`
			} `json:"producers"`
		}
		home.Attn("bus", "status", "--json").JSON(t, &status)
		for _, row := range status.Producers {
			if row.Name == "outpost.changed" {
				return row.Events
			}
		}
		return 0
	}
	before := facts()
	a.AwaitConnections(t, 4)
	snapshot := home.App().Initial.Outposts
	if len(snapshot) != 1 {
		t.Fatalf("retry lost its row: %+v", snapshot)
	}
	after := snapshot[0]
	if after.State != first.State || after.StateSince != first.StateSince || protocol.Deref(after.LastError) != protocol.Deref(first.LastError) {
		t.Fatalf("retry changed failure: before=%+v after=%+v", first, after)
	}
	if got := facts(); got != before {
		t.Fatalf("unchanged retries published outpost.changed: %d -> %d", before, got)
	}
}
