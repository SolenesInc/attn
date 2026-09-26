package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/pty"

	"github.com/victorarias/attn/internal/protocol"
)

func newTurnDaemon(t *testing.T) *Daemon {
	t.Helper()
	return NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
}

func addTurnSession(t *testing.T, d *Daemon, id string, agent protocol.SessionAgent, workspaceID string) {
	t.Helper()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             id,
		Agent:          agent,
		Label:          id,
		Directory:      "/tmp/" + id,
		WorkspaceID:    workspaceID,
		State:          protocol.StateLaunching,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
}

func moveTo(d *Daemon, id, state string) {
	d.applyState(sessionStateChange{sessionID: id, state: state, cause: liveSignal{}})
}

func owed(t *testing.T, d *Daemon, id string) bool {
	t.Helper()
	session := d.sessionForBroadcast(d.store.Get(id))
	if session == nil {
		t.Fatalf("session %s not found", id)
	}
	return protocol.Deref(session.TurnOwed)
}

func TestASettleSurvivesAnAgentRepaintingSlowerThanTheHeartbeatTTL(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentClaude, "ws1")

	d.recordBracketEvidence("s1", protocol.StateWorking)
	d.recordBracketEvidence("s1", protocol.StateIdle)
	moveTo(d, "s1", protocol.StateIdle)
	if !owed(t, d, "s1") {
		t.Fatal("a finished turn owes nothing; nothing to settle")
	}

	d.handleSettleTurn(&protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: "s1"})
	if owed(t, d, "s1") {
		t.Fatal("settle did not close the turn")
	}

	const repaint = 1920 * time.Millisecond
	base := time.Now()
	for tick := 1; tick <= 30; tick++ {
		at := base.Add(time.Duration(tick) * time.Second)
		d.recordPTYEvidence("s1", pty.Observation{
			Source: pty.SourceHeartbeat,
			Claim:  "busy",
			Detail: "⠐ compacting",
			At:     base.Add((at.Sub(base) / repaint) * repaint),
		})
		d.resolveDue(at)

		if owed(t, d, "s1") {
			t.Fatalf("tick %d (%s in): the settled turn re-opened while the agent was still working",
				tick, at.Sub(base))
		}
	}
	if state := d.store.Get("s1").State; state != protocol.SessionStateWorking {
		t.Fatalf("state %q, want working: the agent was painting busy frames throughout", state)
	}
}
