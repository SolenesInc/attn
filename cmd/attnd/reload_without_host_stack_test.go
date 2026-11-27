package main_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyhost/ptyhosttest"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAReloadWithNoUsableHostIsRefusedAndTheAgentKeepsAnswering(t *testing.T) {
	t.Parallel()
	u, app, live, _ := anAgentOnTheHostAfterAnUpdateWithoutTheHost(t, fakeagent.Claude, "")

	result := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(live.session), Cols: 100, Rows: 30},
		protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == live.session })
	if refusal := protocol.Deref(result.Error); result.Success || !strings.Contains(refusal, "shared terminal host is unavailable") || !strings.Contains(refusal, "Reinstall attn") {
		t.Fatalf("a reload with no usable host answered %+v, want a refusal that says how to get the host", result)
	}
	t.Logf("reload refused: %s", protocol.Deref(result.Error))
	u.turn(app, live, "after the refused reload")
}

func TestAContextWindowCapWithNoUsableHostKeepsTheAgentAnswering(t *testing.T) {
	t.Parallel()
	u, app, live, reloaded := anAgentOnTheHostAfterAnUpdateWithoutTheHost(t, fakeagent.Claude, pausepoint.SessionAgentReloadAttempted)

	pin := testworld.Request(app, protocol.SetSessionContextWindowCapMessage{Cmd: protocol.CmdSetSessionContextWindowCap, SessionID: protocol.SessionID(live.session), Cap: 500000},
		protocol.EventSessionContextWindowCapResult, func(r protocol.SessionContextWindowCapResultMessage) bool { return string(r.SessionID) == live.session })
	if !pin.Success {
		t.Fatalf("pin the agent to 500000: %s", protocol.Deref(pin.Error))
	}
	reloaded.Await()
	reloaded.Release()
	u.turn(app, live, "after the cap change")
}

func TestASharedCodexRestartWithNoUsableHostIsRefusedAndItsSessionKeepsAnswering(t *testing.T) {
	t.Parallel()
	u, app, live, _ := anAgentOnTheHostAfterAnUpdateWithoutTheHost(t, fakeagent.Codex, "")

	requestID := uuid.NewString()
	restarted := testworld.Request(app, protocol.RestartSharedCodexMessage{Cmd: protocol.CmdRestartSharedCodex, ProfileID: app.SelectedProfile(), RequestID: protocol.Ptr(requestID)},
		protocol.EventRestartSharedCodexResult, func(m protocol.RestartSharedCodexResultMessage) bool { return protocol.Deref(m.RequestID) == requestID })
	if !restarted.Success {
		t.Fatalf("restart shared Codex: %s", protocol.Deref(restarted.Error))
	}
	failed := testworld.Await(app, protocol.EventSharedCodexChanged, func(m protocol.SharedCodexChangedMessage) bool {
		return m.SharedCodex != nil && m.SharedCodex.Restart == protocol.SharedCodexRestartFailed
	})
	if refusal := protocol.Deref(failed.SharedCodex.Error); !strings.Contains(refusal, "Reinstall attn") {
		t.Fatalf("the restart failed with %q, want the remedy for the missing host", refusal)
	}
	t.Logf("restart refused: %s", protocol.Deref(failed.SharedCodex.Error))
	u.turn(app, live, "after the refused restart")
}

// anAgentOnTheHostAfterAnUpdateWithoutTheHost starts an agent in the shared host, then updates to a build whose
// snapshot format no stored host passed, installed without attn-pty-host, so no host can start. A non-empty pauseAt
// arms that pause point for the updated daemon.
func anAgentOnTheHostAfterAnUpdateWithoutTheHost(t *testing.T, h fakeagent.Harness, pauseAt string) (*releaseUpdate, *testworld.Peer, releaseAgent, *testworld.Pause) {
	t.Helper()
	s := testworld.NewStack(t, testworld.WithAgents(h))
	s.Vars = append(s.Vars, "ATTN_PTY_BACKEND=migrating")
	s.StartBinary(testworld.AttnBinaryWithSnapshotFormat(t, "current"), "ATTN_PTY_HOST_BINARY="+ptyhosttest.Variant(t, "current"))
	u := &releaseUpdate{Stack: s, t: t}
	live := u.spawnAgent(s.App(), h)
	u.assertParent(live.run, "attn-pty-host")
	s.Stop()

	var pause *testworld.Pause
	if pauseAt != "" {
		pause = s.PauseAt(pauseAt)
	}
	s.StartBinary(testworld.AttnBinaryWithSnapshotFormat(t, "next"), "ATTN_PTY_HOST_BINARY="+filepath.Join(s.Dir, "no-host"))
	app := s.App()
	u.assertSpawnRefused(app, "Reinstall attn")
	u.turn(app, live, "after the update")
	return u, app, live, pause
}
