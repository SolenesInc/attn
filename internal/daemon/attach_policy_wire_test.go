package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAFreshSpawnAttachGetsNoReplayWhileARemountDoes(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	shell := w.Spawn(app, shellHarness, w.Path("shop"))
	app.TypeLine(shell, `printf 'mark%s\n' er-painted`)
	app.AwaitScreen(shell, "marker-painted")

	fresh := attachWithPolicy(w.App(), shell, protocol.AttachPolicyFreshSpawn)
	if !fresh.Success || fresh.Snapshot != nil {
		t.Errorf("a fresh-spawn attach succeeded=%v with snapshot=%v, want success and no replay", fresh.Success, fresh.Snapshot != nil)
	}
	remount := attachWithPolicy(w.App(), shell, protocol.AttachPolicySameAppRemount)
	if !remount.Success || remount.Snapshot == nil {
		t.Errorf("a remount attach succeeded=%v with snapshot=%v, want the painted screen replayed", remount.Success, remount.Snapshot != nil)
	}
}

func attachWithPolicy(p *testworld.Peer, session string, policy protocol.AttachPolicy) protocol.AttachResultMessage {
	p.T.Helper()
	terminal := p.Terminal(session)
	return testworld.Request(p, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: terminal, AttachPolicy: protocol.Ptr(policy)},
		protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == terminal })
}
