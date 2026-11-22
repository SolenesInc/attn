package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

// harnessEvents is the one sink of what harnesses report. A report that is not inferred is the harness's first
// report of its launch. A turn report with an epoch sets state outright, not through the resolver, because its
// epoch and sequence fence is part of the store's state write; every other report is evidence for the resolver.
type harnessEvents struct{ daemon *Daemon }

func (d *Daemon) events() harness.Events { return harnessEvents{daemon: d} }

var fencedTurnStates = map[harness.Turn]string{
	harness.TurnUnknown:  protocol.StateUnknown,
	harness.TurnRunning:  protocol.StateWorking,
	harness.TurnApproval: protocol.StatePendingApproval,
	harness.TurnQuestion: protocol.StateWaitingInput,
	harness.TurnEnded:    protocol.StateIdle,
}

var programStatusOfTurn = map[harness.Turn]string{
	harness.TurnRunning:  pty.ProgramWorking,
	harness.TurnApproval: pty.ProgramBlockedPermission,
	harness.TurnQuestion: pty.ProgramBlockedQuestion,
	harness.TurnEnded:    pty.ProgramIdle,
}

func (e harnessEvents) reported(session protocol.SessionID, inferred bool) {
	if inferred {
		return
	}
	if monitor := e.daemon.sessionMonitor(session); monitor != nil {
		monitor.Reported()
		return
	}
	e.daemon.noteLaunchStarted(session)
}

func (e harnessEvents) resolve(r harness.Reporter) (protocol.SessionID, *sessionmonitor.Monitor) {
	if terminal := r.Terminal(); terminal != "" {
		session := e.daemon.sessionInTerminal(terminal)
		return session, e.daemon.monitorInTerminal(terminal, session)
	}
	return r.Session(), e.daemon.sessionMonitor(r.Session())
}

func (e harnessEvents) Turn(r harness.Reporter, at time.Time, event harness.TurnEvent) {
	session, monitor := e.resolve(r)
	e.reported(session, event.Inferred)
	if event.Epoch != "" {
		e.fencedTurn(session, at, event)
		return
	}
	switch {
	case event.Status:
		monitor.RecordServerStatus(programStatusOfTurn[event.Turn], at)
	case event.Turn == harness.TurnRunning:
		monitor.RecordBracket(protocol.StateWorking)
	case event.Turn == harness.TurnApproval:
		monitor.RecordApproval(event.Detail)
	case event.Turn == harness.TurnQuestion:
		monitor.RecordBracket(protocol.StateWaitingInput)
	case event.Turn == harness.TurnPromptIdle:
		monitor.RecordPromptIdle()
	case event.Turn == harness.TurnAborted:
		monitor.RecordTurnAborted(event.Detail, at, at)
	case event.Turn == harness.TurnFailed:
		monitor.RecordStopFailure(event.Detail, "")
	case event.Turn == harness.TurnEnded && event.Stop == nil:
		monitor.RecordBracket(protocol.StateIdle)
	case event.Turn == harness.TurnEnded:
		if left := event.Stop.Left; left != nil {
			monitor.RecordStopFacts(left.BackgroundWork, left.PendingCron)
		}
		if !event.Stop.Yielded {
			monitor.RecordTurnEnded(event.Stop.Classifies)
		}
	}
	if event.Completion != 0 {
		monitor.RecordServerCompletion(event.Completion)
	}
	if said := event.Said; said != "" {
		e.daemon.life.Go("classifyServerAnswer", func() { monitor.ClassifyMessage(said) })
	}
}

func (e harnessEvents) Compacting(r harness.Reporter, active bool, trigger string) {
	session, monitor := e.resolve(r)
	e.reported(session, false)
	monitor.RecordCompaction(active, trigger)
}

func (e harnessEvents) fencedTurn(sessionID protocol.SessionID, at time.Time, event harness.TurnEvent) {
	d := e.daemon
	state := fencedTurnStates[event.Turn]
	if event.Restated {
		if session := d.store.Get(sessionID); session == nil || session.State != protocol.SessionStateUnknown {
			return
		}
		d.logf("link restates session=%s run=%s as %s after unknown", sessionID, event.Epoch, state)
	}
	var requestStartedAt time.Time
	if event.Turn == harness.TurnRunning {
		requestStartedAt = at
	}
	if !d.applyState(sessionID, sessionmonitor.Change{
		State:            state,
		RequestStartedAt: requestStartedAt,
		Cause:            sessionmonitor.LinkTurn{Run: event.Epoch, Seq: event.Seq},
		Origin:           sessionmonitor.Origin{Source: sessionmonitor.StateSourceLink, Detail: event.Epoch},
	}) {
		d.logf("link turn discarded: session=%s run=%s seq=%d state=%s", sessionID, event.Epoch, event.Seq, state)
	}
}
