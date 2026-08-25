package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/statetrace"
)

func TestShellIgnoresTheWorkerPollsStateClaim(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "shell-veto.sock"))
	const id = "shell-worker-info"
	addCharacterizationSession(t, d, id, protocol.SessionAgentShell, protocol.SessionStateIdle)

	d.handlePTYState(id, pty.Observation{
		Source: pty.SourceWorkerInfo,
		Claim:  protocol.StateWorking,
		Detail: "watch subscribe replay",
		At:     time.Now(),
	})

	if got := d.store.Get(id).State; got != protocol.SessionStateIdle {
		t.Fatalf("state=%q: the worker poll moved a shell", got)
	}
	got := onlyObservation(t, d, id)
	if got.Outcome != statetrace.OutcomeVetoed || got.Reason != "resolver_owned" {
		t.Fatalf("trace = %+v, want vetoed/resolver_owned", got)
	}
}

func TestShellForegroundHeartbeatDrivesItsState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "shell-heartbeat.sock"))
	const id = "shell-heartbeat"
	addCharacterizationSession(t, d, id, protocol.SessionAgentShell, protocol.SessionStateIdle)

	d.handlePTYState(id, heartbeatObs("busy", "foreground command running", time.Now()))
	d.resolveAllSessions(time.Now())
	if got := d.store.Get(id).State; got != protocol.SessionStateWorking {
		t.Fatalf("state=%q, want working while a foreground command runs", got)
	}

	d.handlePTYState(id, heartbeatObs("not_busy", "shell at prompt", time.Now()))
	d.resolveAllSessions(time.Now())
	session := d.store.Get(id)
	if session.State != protocol.SessionStateIdle {
		t.Fatalf("state=%q, want idle at the prompt", session.State)
	}

	d.decorateSessionWithTurn(session)
	if session.TurnOwed != nil {
		t.Fatalf("TurnOwed=%v: a shell state change entered the queue", *session.TurnOwed)
	}
}
