package main_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

// linkDelay stands for a laptop reaching its home over the internet: an echo through the home costs at least
// two of them.
const linkDelay = 50 * time.Millisecond

func echoTimes(t *testing.T, p *testworld.Peer, terminal, tag string) []time.Duration {
	t.Helper()
	p.AwaitTerminalScreen(terminal, "")
	var samples []time.Duration
	for i := range 7 {
		token := fmt.Sprintf("%s%dq", tag, i)
		started := time.Now()
		p.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(terminal), Data: token, Source: protocol.Ptr("keyboard")})
		p.AwaitTerminalScreen(terminal, token)
		samples = append(samples, time.Since(started))
	}
	slices.Sort(samples)
	return samples
}

func median(samples []time.Duration) time.Duration { return samples[len(samples)/2] }

func TestTheAppMachinesTerminalsEchoWithoutTheHome(t *testing.T) {
	w := newReverseWorld(t)
	w.ssh.Delay("home", linkDelay)
	row := w.join()
	shell := fakeagent.Harness(protocol.SessionAgentShell)
	onLaptop := w.home.Spawn(w.app, shell, w.laptop.Path("on-laptop"), func(m *protocol.SpawnSessionMessage) { m.Host = &row.ID })
	atHome := w.home.Spawn(w.app, shell, w.home.Path("at-home"))
	laptopTerminal, homeTerminal := w.app.Terminal(onLaptop), w.app.Terminal(atHome)

	relayed := w.laptop.TrustedApp()
	machine := w.laptop.MachineApp()
	local := echoTimes(t, machine, laptopTerminal, "ml")
	home := echoTimes(t, relayed, homeTerminal, "hr")
	through := echoTimes(t, relayed, laptopTerminal, "lr")
	t.Logf("one-way link delay %s; keystroke echo medians: the laptop's terminal on its own daemon %s, a home terminal through the relay %s, the laptop's terminal through the relay %s",
		linkDelay, median(local).Round(time.Millisecond), median(home).Round(time.Millisecond), median(through).Round(time.Millisecond))
	if median(local) >= linkDelay {
		t.Fatalf("the laptop's terminal echoed in %v on its own daemon, want under one link crossing (%s): %v", median(local), linkDelay, local)
	}
	if median(home) < 2*linkDelay {
		t.Fatalf("a home terminal echoed through the relay in %v, under the round trip of %s the link costs: the delay is not on the link", median(home), 2*linkDelay)
	}
	t.Log("(1) the laptop's terminal echoes within a local round trip on its own daemon, and a home terminal's echo keeps the link's round trip")
}

func TestTheAppMachinesTerminalsStayUsableWhileTheHomeIsAway(t *testing.T) {
	w := newReverseWorld(t)
	row := w.join()
	shell := fakeagent.Harness(protocol.SessionAgentShell)
	onLaptop := w.home.Spawn(w.app, shell, w.laptop.Path("on-laptop"), func(m *protocol.SpawnSessionMessage) { m.Host = &row.ID })
	atHome := w.home.Spawn(w.app, shell, w.home.Path("at-home"))
	terminal := w.app.Terminal(onLaptop)
	pidPath := w.laptop.Path("on-laptop.pid")

	machine := w.laptop.MachineApp()
	if !hasSession(machine.Initial.Sessions, onLaptop) || hasSession(machine.Initial.Sessions, atHome) {
		t.Fatalf("the laptop's own daemon lists %+v, want its own session %s and not the home's %s", machine.Initial.Sessions, onLaptop, atHome)
	}
	machine.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(terminal), Data: "echo $$ > " + pidPath + "; echo before-$((1+1))\r"})
	machine.AwaitTerminalScreen(terminal, "before-2")
	before := pidOf(t, pidPath)

	w.home.Stop()
	w.laptop.AwaitTimer(bridge.RetryTimer)
	away := awaitHomeDial(t, w.laptop, "dialing")
	if told := lastHomeUnreachable(t, away); told.Home != w.name {
		t.Fatalf("the relayed app was told %+v, want the home %s unreachable", told, w.name)
	}
	machine.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(terminal), Data: "echo away-$((2+2))\r"})
	machine.AwaitTerminalScreen(terminal, "away-4")
	for _, refused := range []any{
		protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: "s-offline", Agent: protocol.AgentShellValue, Cwd: w.laptop.Path("on-laptop"), Cols: 80, Rows: 24},
		protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "remote_home_enabled", Value: "false"},
	} {
		machine.Send(refused)
		if got := testworld.Refused(machine); !strings.Contains(protocol.Deref(got.Error), "home") {
			t.Fatalf("the laptop's own daemon answered %+v to %+v, want it sent to the home", got, refused)
		}
	}
	t.Log("(2) with the home stopped, the relayed app is told the home is unreachable, the laptop's own daemon still shows and types into its terminal, and refuses what the home owns")

	w.home.Start()
	w.app = w.home.App()
	w.laptop.FirePending(bridge.RetryTimer)
	if closed := away.Closed(); closed.Code == 0 {
		t.Fatalf("the waiting app's connection was not closed when the home returned")
	}
	back := w.laptop.TrustedApp()
	seen := 0
	for _, s := range back.Initial.Sessions {
		if string(s.ID) == onLaptop {
			seen++
		}
	}
	if seen != 1 || !hasSession(back.Initial.Sessions, atHome) || back.Terminal(onLaptop) != terminal {
		t.Fatalf("after the home returned the relayed app lists the laptop's session %d times and terminal %s for it, want once, with %s, beside the home's %s", seen, back.Terminal(onLaptop), terminal, atHome)
	}
	machine.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(terminal), Data: "echo $$ > " + pidPath + "; echo back-$((3+3))\r"})
	machine.AwaitTerminalScreen(terminal, "back-6")
	back.AwaitTerminalScreen(terminal, "back-6")
	if got := pidOf(t, pidPath); got != before {
		t.Fatalf("the laptop's shell changed pid while the home was away: %s -> %s", before, got)
	}
	t.Log("(3) when the home returns, the relayed app shows the home's state with the laptop's terminal once, the same shell, still typed on the laptop's own daemon")
}
