package main_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyhost/ptyhosttest"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAReloadWithNoUsableHostIsRefusedAndTheAgentKeepsAnswering(t *testing.T) {
	t.Parallel()
	u, app, live, _ := anAgentOnTheHostAfterAnUpdateWithoutTheHost(t, "")

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
	u, app, live, reloaded := anAgentOnTheHostAfterAnUpdateWithoutTheHost(t, pausepoint.SessionAgentReloadAttempted)

	pin := testworld.Request(app, protocol.SetSessionContextWindowCapMessage{Cmd: protocol.CmdSetSessionContextWindowCap, SessionID: protocol.SessionID(live.session), Cap: 500000},
		protocol.EventSessionContextWindowCapResult, func(r protocol.SessionContextWindowCapResultMessage) bool { return string(r.SessionID) == live.session })
	if !pin.Success {
		t.Fatalf("pin the agent to 500000: %s", protocol.Deref(pin.Error))
	}
	reloaded.Await()
	reloaded.Release()
	u.turn(app, live, "after the cap change")
}

// anAgentOnTheHostAfterAnUpdateWithoutTheHost starts an agent in the shared host, then updates to a build whose
// snapshot format no stored host passed, installed without attn-pty-host, so no host can start. A non-empty pauseAt
// arms that pause point for the updated daemon.
func anAgentOnTheHostAfterAnUpdateWithoutTheHost(t *testing.T, pauseAt string) (*releaseUpdate, *testworld.Peer, releaseAgent, *testworld.Pause) {
	t.Helper()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Vars = append(s.Vars, "ATTN_PTY_BACKEND=migrating")
	s.StartBinary(testworld.AttnBinaryWithSnapshotFormat(t, "current"), "ATTN_PTY_HOST_BINARY="+ptyhosttest.Variant(t, "current"))
	u := &releaseUpdate{Stack: s, t: t}
	live := u.spawnAgent(s.App(), fakeagent.Claude)
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
