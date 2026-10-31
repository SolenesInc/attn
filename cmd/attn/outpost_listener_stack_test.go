package main_test

import (
	"context"
	"net"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
	"path/filepath"
)

func bridgeAddress(t *testing.T) string {
	t.Helper()
	return "machine-" + uuid.NewString()
}

func TestOutpostTeardownAcceptsAnAlreadyRemovedTerminal(t *testing.T) {
	a := testworld.NewSSH(t)
	s := testworld.NewStack(t)
	a.Use(s.World)
	removeReplied := s.PauseAt(pausepoint.PtyRemoveReplied)
	s.Start()
	address := bridgeAddress(t)
	a.Route(address, s)
	app := s.App()
	id := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shell"))
	app.TypeLine(id, "echo ready-$((1+1))")
	app.AwaitScreen(id, "ready-2")
	identity := bridge.Identity{Instance: "default", DaemonID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Build: bridge.Build{Version: buildinfo.Version, SourceFingerprint: buildinfo.SourceFingerprint, ProtocolVersion: protocol.ProtocolVersion}}
	conn, _, err := bridge.Dial(context.Background(), bridge.DialConfig{Conn: bridgeSocket(t, s), Self: identity})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	terminal := protocol.TerminalID(app.Terminal(id))
	remove := protocol.TerminalRemoveMessage{Cmd: protocol.CmdTerminalRemove, TerminalID: terminal}
	if err := conn.Call(context.Background(), "call", remove, nil); err != nil {
		t.Fatalf("removing the terminal: %v", err)
	}
	removeReplied.Await()
	defer removeReplied.Release()
	kill := protocol.TerminalKillMessage{Cmd: protocol.CmdTerminalKill, TerminalID: terminal, Signal: "SIGTERM"}
	if err := conn.Call(context.Background(), "call", kill, nil); err == nil {
		t.Fatal("an ordinary kill of a missing terminal succeeded")
	}
	kill.EndingSessionID = protocol.Ptr(protocol.SessionID(id))
	if err := conn.Call(context.Background(), "call", kill, nil); err != nil {
		t.Fatalf("teardown of a terminal already removed by copy cleanup: %v", err)
	}
	if err := conn.Call(context.Background(), "call", remove, nil); err != nil {
		t.Fatalf("removing a terminal already gone: %v", err)
	}
}

func bridgeSocket(t *testing.T, s *testworld.Stack) net.Conn {
	t.Helper()
	c, err := net.Dial("unix", filepath.Join(s.Dir, "bridge.sock"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
