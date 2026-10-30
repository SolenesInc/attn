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

func TestSSHBridgeStartsDaemonAndRecoversAfterMasterExit(t *testing.T) {
	home, remote := testworld.NewStack(t), testworld.NewStack(t)
	remote.Vars = append(remote.Vars, "SSH_CONNECTION=the-starting-ssh-session")
	pair := testworld.PrepareOutpostPair(t, home, remote)
	home.Start()
	pair.Connect()
	t.Cleanup(func() {
		result := remote.Attn("daemon", "stop")
		if result.Code != 0 {
			t.Errorf("stop detached daemon: %+v", result)
		}
	})
	id := home.Spawn(pair.App, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	pair.App.TypeLine(id, "printf 'daemon-ssh-env-%s\\n' \"${SSH_CONNECTION-unset}\"")
	pair.App.AwaitScreen(id, "daemon-ssh-env-unset")
	pair.App.TypeLine(id, "printf 'before-master-exit-%s\\n' $$")
	pair.App.AwaitScreen(id, "before-master-exit-")
	beforeSince := home.App().Initial.Outposts[0].StateSince
	mark := pair.App.EventCount()
	pair.SSH.ExitMaster(home, pair.Host)
	outpostStateSince(pair.App, "outpost", protocol.OutpostStateLost, mark)
	home.Advance(bridge.ReconnectInterval)
	testworld.AwaitSince(pair.App, mark, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		for _, r := range m.Outposts {
			if r.ID == pair.Host && r.State == protocol.OutpostStateConnected && r.StateSince != beforeSince {
				return true
			}
		}
		return false
	})
	terminal := protocol.TerminalID(pair.App.Terminal(id))
	result := testworld.Request(pair.App, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: terminal}, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == terminal })
	if !result.Success {
		t.Fatal(protocol.Deref(result.Error))
	}
	pair.App.TypeLine(id, "printf 'after-master-exit-%s\\n' $$")
	pair.App.AwaitScreen(id, "after-master-exit-")
	t.Log("Add by destination started the remote daemon; a shell took typing before and after the master's exit; lost -> connected")
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
