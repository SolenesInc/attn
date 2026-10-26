package main_test

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func bridgeAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := l.Addr().String()
	_ = l.Close()
	return address
}

func awaitOutpostListener(p *testworld.Peer, state string) {
	testworld.Await[protocol.SettingsUpdatedMessage](p, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["outpost_state"] == state })
}

func TestOutpostListenerSettingsPairingAndReplacement(t *testing.T) {
	a := testworld.NewSSHAgent(t)
	s := testworld.NewStack(t)
	a.Authorize(s.World)
	s.Start()
	app := s.App()
	requireStdout(t, s.Attn("settings", "get", "outpost_state"), "off")
	address := bridgeAddress(t)
	requireStdout(t, s.Attn("settings", "set", "outpost_listen", address), address)
	awaitOutpostListener(app, "listening")
	identity := bridge.Identity{DaemonID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Build: bridge.Build{Version: buildinfo.Version, SourceFingerprint: buildinfo.SourceFingerprint, ProtocolVersion: protocol.ProtocolVersion}}
	dial := func() (*bridge.Conn, *bridge.Pin) {
		t.Helper()
		c, pin, err := bridge.Dial(context.Background(), bridge.DialConfig{Address: address, Self: identity, AgentSocket: a.Socket})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c, pin
	}
	t.Log("dial first")
	first, pin := dial()
	awaitOutpostListener(app, "bridged")
	requireStdout(t, s.Attn("settings", "get", "outpost_home"), identity.DaemonID)
	t.Log("dial replacement")
	second, again := dial()
	if *pin != *again {
		t.Fatalf("pins changed: %+v -> %+v", pin, again)
	}
	t.Log("await predecessor close")
	<-first.Done()
	if second.Peer().DaemonID != pin.DaemonID {
		t.Fatal("replacement has wrong peer")
	}
	if r := s.Attn("settings", "set", "outpost_state", "off"); r.Code == 0 || !strings.Contains(r.Stderr, "read-only") {
		t.Fatalf("computed setting write: %+v", r)
	}
	requireStdout(t, s.Attn("settings", "set", "outpost_listen", ""), "outpost_listen =")
	awaitOutpostListener(app, "off")
	t.Log("await replacement close")
	<-second.Done()
	occupied, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	requireStdout(t, s.Attn("settings", "set", "outpost_listen", address), address)
	awaitOutpostListener(app, "error")
	requireStdout(t, s.Attn("settings", "get", "outpost_last_error"), address)
	requireStdout(t, s.Attn("settings", "set", "outpost_listen", ""), "outpost_listen =")
}

func TestOutpostTeardownAcceptsAnAlreadyRemovedTerminal(t *testing.T) {
	a := testworld.NewSSHAgent(t)
	s := testworld.NewStack(t)
	a.Authorize(s.World)
	s.Start()
	address := bridgeAddress(t)
	requireStdout(t, s.Attn("settings", "set", "outpost_listen", address), address)
	app := s.App()
	id := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shell"))
	app.TypeLine(id, "echo ready-$((1+1))")
	app.AwaitScreen(id, "ready-2")
	identity := bridge.Identity{DaemonID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Build: bridge.Build{Version: buildinfo.Version, SourceFingerprint: buildinfo.SourceFingerprint, ProtocolVersion: protocol.ProtocolVersion}}
	conn, _, err := bridge.Dial(context.Background(), bridge.DialConfig{Address: address, Self: identity, AgentSocket: a.Socket})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	terminal := protocol.TerminalID(app.Terminal(id))
	remove := protocol.TerminalRemoveMessage{Cmd: protocol.CmdTerminalRemove, TerminalID: terminal}
	if err := conn.Call(context.Background(), "terminal", remove, nil); err != nil {
		t.Fatalf("removing the terminal: %v", err)
	}
	kill := protocol.TerminalKillMessage{Cmd: protocol.CmdTerminalKill, TerminalID: terminal, Signal: "SIGTERM"}
	if err := conn.Call(context.Background(), "terminal", kill, nil); err == nil {
		t.Fatal("an ordinary kill of a missing terminal succeeded")
	}
	kill.EndingSessionID = protocol.Ptr(protocol.SessionID(id))
	if err := conn.Call(context.Background(), "terminal", kill, nil); err != nil {
		t.Fatalf("teardown of a terminal already removed by copy cleanup: %v", err)
	}
	if err := conn.Call(context.Background(), "terminal", remove, nil); err != nil {
		t.Fatalf("removing a terminal already gone: %v", err)
	}
}
