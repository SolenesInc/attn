package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSSHBridgeDetachesTheOutpostFromItsSessionAndReconnectsThroughTheHomeAgent(t *testing.T) {
	home, remote := testworld.NewStack(t), testworld.NewStack(t)
	agent := filepath.Join(home.Dir, "caller-agent.sock")
	home.Vars = append(home.Vars, "SSH_AUTH_SOCK="+agent)
	remote.Vars = append(remote.Vars, "SSH_CONNECTION=the-starting-ssh-session", "SSH_AUTH_SOCK=/the-starting-ssh-session/agent")
	pair := testworld.PrepareOutpostPair(t, home, remote)
	pair.SSH.RequireAgent(agent)
	if got := home.Attn("daemon", "ensure"); got.Code != 0 || !strings.Contains(got.Stdout, `"started"`) {
		t.Fatalf("attn daemon ensure: %+v", got)
	}
	t.Cleanup(func() {
		for _, s := range []*testworld.Stack{home, remote} {
			if result := s.Attn("daemon", "stop"); result.Code != 0 {
				t.Errorf("stop daemon started by ensure: %+v", result)
			}
		}
	})
	pair.Connect()
	id := home.Spawn(pair.App, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	pair.App.TypeLine(id, "printf 'daemon-ssh-env-%s-%s\\n' \"${SSH_CONNECTION-unset}\" \"${SSH_AUTH_SOCK-unset}\"")
	pair.App.AwaitScreen(id, "daemon-ssh-env-unset-unset")
	pair.App.TypeLine(id, "printf 'before-master-exit-%s\\n' $$")
	pair.App.AwaitScreen(id, "before-master-exit-")
	mark := pair.App.EventCount()
	pair.SSH.ExitMaster(home, pair.Host)
	outpostStateSince(pair.App, "outpost", protocol.OutpostStateLost, mark)
	home.Advance(bridge.ReconnectInterval)
	outpostStateSince(pair.App, "outpost", protocol.OutpostStateConnected, mark)
	if attempts := pair.SSH.MasterAttempts(); attempts != 2 {
		t.Fatalf("batch master attempts = %d, want one for Add and one for reconnect", attempts)
	}
	terminal := protocol.TerminalID(pair.App.Terminal(id))
	result := testworld.Request(pair.App, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: terminal}, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == terminal })
	if !result.Success {
		t.Fatal(protocol.Deref(result.Error))
	}
	pair.App.TypeLine(id, "printf 'after-master-exit-%s\\n' $$")
	pair.App.AwaitScreen(id, "after-master-exit-")
	t.Logf("attn daemon ensure kept SSH_AUTH_SOCK=%s for Add and reconnect; _bridge started the outpost daemon without SSH_*; its shell took typing before and after the master's exit", agent)
}

func TestSSHFailuresRefusedAndLost(t *testing.T) {
	for _, tc := range []struct {
		name, stderr string
		state        protocol.OutpostState
	}{
		{"authentication", "user@machine: Permission denied (publickey).\n", protocol.OutpostStateRefused},
		{"host key", "Host key verification failed.\n", protocol.OutpostStateRefused},
		{"network", "ssh: connect to host machine port 22: No route to host\n", protocol.OutpostStateLost},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := testworld.NewStack(t)
			ssh := testworld.NewSSH(t)
			ssh.Use(home.World)
			ssh.Fail("machine", tc.stderr)
			home.Start()
			app := enableOutposts(t, home)
			app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "machine", Destination: "machine"})
			row := outpostState(app, "machine", tc.state)
			if strings.TrimSpace(protocol.Deref(row.LastError)) != strings.TrimSpace(tc.stderr) {
				t.Fatalf("ssh stderr was lost: %+v", row)
			}
			if protocol.Deref(row.Connectable) != (tc.state == protocol.OutpostStateRefused) {
				t.Fatalf("connectable flag: %+v", row)
			}
			t.Logf("%s connectable=%v: %s", row.State, protocol.Deref(row.Connectable), protocol.Deref(row.LastError))
		})
	}
}

func TestLegacyBridgeAddressRequiresAddByDestination(t *testing.T) {
	home := testworld.NewStack(t)
	home.Start()
	app := enableOutposts(t, home)
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "legacy", Destination: "example.com:7777"})
	expected := "added by bridge address example.com:7777; remove it and add it again by the name you use with ssh"
	first := outpostState(app, "legacy", protocol.OutpostStateRefused)
	if protocol.Deref(first.LastError) != expected {
		t.Fatal(protocol.Deref(first.LastError))
	}
	home.Stop()
	home.Start()
	again := outpostStateOnConnect(home.App(), "legacy", protocol.OutpostStateRefused)
	if again.ID != first.ID || protocol.Deref(again.LastError) != expected {
		t.Fatalf("stored address: %+v", again)
	}
	t.Log(expected)
}

func TestBridgeExitsWhenDaemonClosesWhileStdinStaysOpen(t *testing.T) {
	remote := testworld.NewStack(t)
	remote.Start()
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer writer.Close()
	process := remote.Launch(testworld.Invocation{Args: []string{"_bridge"}, Input: input})
	process.AwaitStdout("SSH-2.0-Go")
	remote.Stop()
	result := process.Wait()
	if result.Code != 0 {
		t.Fatalf("bridge did not finish after daemon EOF: %+v", result)
	}
	t.Log("the daemon closed its bridge socket; _bridge exited while its stdin writer remained open")
}
