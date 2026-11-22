package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/sessioncost"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/store"
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
	if said := event.Said; said != nil {
		e.daemon.life.Go("classifyServerAnswer", func() { monitor.ClassifyMessage(*said) })
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

func (e harnessEvents) Prompt(r harness.Reporter, at time.Time, p harness.Prompt) {
	session, monitor := e.resolve(r)
	typed := false
	if p.Live {
		e.reported(session, false)
		typed = monitor.ObservePromptSubmitted(at)
	}
	if p.Titles {
		text, user := p.Text, typed || p.History
		e.daemon.life.Go("maybeGenerateSessionTitleFromPrompt", func() { monitor.TitleFromPrompt(text, user) })
	}
}

func (e harnessEvents) Tool(session protocol.SessionID, use harness.ToolUse) error {
	e.reported(session, false)
	d := e.daemon
	var failed []error
	if len(use.EditedFiles) > 0 && !d.recordEditedFiles(session, use.EditedFiles) {
		failed = append(failed, errors.New("edited file monitor view was not applied"))
	}
	for _, url := range use.PullRequests {
		rec, err := d.sessionPullRequestIdentity(session, session, url)
		if err == nil {
			err = d.recordSessionPullRequest(rec)
		}
		if err != nil {
			failed = append(failed, err)
		}
	}
	return errors.Join(failed...)
}

func (d *Daemon) recordEditedFiles(sessionID protocol.SessionID, paths []string) bool {
	now := time.Now()
	if monitor := d.sessionMonitor(sessionID); monitor != nil {
		return monitor.ReportEditedFiles(paths, now, d.awayFromHome(sessionID))
	}
	for _, path := range paths {
		d.store.RecordFileActivity(store.FileActivity{Path: path, Source: store.FileActivitySourceEdited, SessionID: sessionID, At: now})
	}
	return true
}

func (e harnessEvents) Usage(session protocol.SessionID, source string, observations []sessioncost.Observation) {
	e.daemon.sessionMonitor(session).ReportServerUsage(source, observations)
}

func (e harnessEvents) Replied(session protocol.SessionID) {
	e.reported(session, false)
	e.daemon.publishFact(FactSessionAssistantWindowChanged, string(session), nil)
}

func (e harnessEvents) Named(session protocol.SessionID, title string) {
	e.daemon.sessionMonitor(session).ReportHarnessTitle(title)
}

func (e harnessEvents) Moved(r harness.Reporter, m harness.Move) func(context.Context) error {
	if terminal := r.Terminal(); terminal != "" {
		return e.daemon.conversationMoved(terminal, m)
	}
	receipt, err := e.daemon.persistResumeSessionID(r.Session(), m.Conversation)
	if err != nil {
		return refused(err)
	}
	if receipt == nil {
		return nil
	}
	return receipt.Wait
}
