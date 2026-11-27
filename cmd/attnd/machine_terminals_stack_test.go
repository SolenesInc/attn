package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheAppMachinesTerminalsEchoWithoutTheHome(t *testing.T) {
	w := newReverseWorld(t)
	row := w.join()
	shell := fakeagent.Harness(protocol.SessionAgentShell)
	onLaptop := w.home.Spawn(w.app, shell, w.laptop.Path("on-laptop"), func(m *protocol.SpawnSessionMessage) { m.Host = &row.ID })
	atHome := w.home.Spawn(w.app, shell, w.home.Path("at-home"))
	laptopTerminal, homeTerminal := w.app.Terminal(onLaptop), w.app.Terminal(atHome)
	relayed := w.laptop.TrustedApp()
	machine := w.laptop.MachineApp()
	relayed.AwaitTerminalScreen(homeTerminal, "")
	machine.AwaitTerminalScreen(laptopTerminal, "")

	resume := w.ssh.Pause("home")
	relayed.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(homeTerminal), Data: "athome", Source: protocol.Ptr("keyboard")})
	machine.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(laptopTerminal), Data: "onlaptop", Source: protocol.Ptr("keyboard")})
	machine.AwaitTerminalScreen(laptopTerminal, "onlaptop")
	if shown := relayed.Screen(atHome); strings.Contains(string(shown), "athome") {
		t.Fatalf("the home terminal echoed while the link to the home was held: the hold is not on its path:\n%s", shown)
	}
	resume()
	relayed.AwaitTerminalScreen(homeTerminal, "athome")
	t.Log("(1) with the link to the home held, the laptop's terminal echoes on its own daemon, and a home terminal's echo waits for the link")
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
	readID := "read-away"
	read := testworld.Request(machine, protocol.TerminalReadTextMessage{Cmd: protocol.CmdTerminalReadText, TerminalID: protocol.TerminalID(terminal), Mode: protocol.TerminalReadModeScreen, RequestID: protocol.Ptr(readID)}, protocol.EventTerminalReadTextResult, func(r protocol.TerminalReadTextResultMessage) bool { return r.RequestID == readID })
	if !read.Success || !strings.Contains(protocol.Deref(read.Text), "away-4") {
		t.Fatalf("the laptop's own daemon read its terminal's screen as %+v, want it to show away-4", read)
	}
	for _, refused := range []any{
		protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: "s-offline", Agent: protocol.AgentShellValue, Cwd: w.laptop.Path("on-laptop"), Cols: 80, Rows: 24},
		protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "remote_home_enabled", Value: "false"},
	} {
		machine.Send(refused)
		if got := testworld.Refused(machine); !strings.Contains(protocol.Deref(got.Error), "home") {
			t.Fatalf("the laptop's own daemon answered %+v to %+v, want it sent to the home", got, refused)
		}
	}
	t.Log("(2) with the home stopped, the relayed app is told the home is unreachable, the laptop's own daemon still shows, reads and types into its terminal, and refuses what the home owns")

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
