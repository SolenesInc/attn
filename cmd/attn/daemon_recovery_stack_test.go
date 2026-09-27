package main_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAppConnectingDuringRestartRecoveryWaitsForTheRecoveredSessions(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	app := s.App()
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
	attachWithPolicy(t, app, shell, protocol.AttachPolicyFreshSpawn)
	app.TypeLine(shell, "echo before-$((1+1))")
	app.AwaitScreen(shell, "before-2")
	s.Stop()

	recovery := s.PauseAt(pausepoint.DaemonStartupRecovery)
	s.StartHeldAt(recovery)
	app = s.ConnectApp()
	app.Send(protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: shell, AttachPolicy: protocol.Ptr(protocol.AttachPolicyRelaunchRestore)})
	refused := testworld.Refused(app)
	if protocol.Deref(refused.Cmd) != protocol.CmdAttachSession || protocol.Deref(refused.Error) != "daemon_recovering" {
		t.Fatalf("opening a terminal mid-recovery answered %s %q, want attach_session refused as daemon_recovering", protocol.Deref(refused.Cmd), protocol.Deref(refused.Error))
	}
	for _, event := range app.Received() {
		if event.Event == protocol.EventInitialState {
			t.Fatalf("the app got its initial state before the daemon recovered its sessions")
		}
	}

	recovery.Release()
	initial := testworld.Await[protocol.InitialStateMessage](app, protocol.EventInitialState, nil)
	var recovered bool
	for _, session := range initial.Sessions {
		recovered = recovered || session.ID == shell
	}
	if !recovered {
		t.Fatalf("the initial state after recovery lists %d sessions without the shell %s", len(initial.Sessions), shell)
	}
	attachWithPolicy(t, app, shell, protocol.AttachPolicyRelaunchRestore)
	app.AwaitScreen(shell, "before-2")
}
