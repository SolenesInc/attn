package daemon

import (
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/statetrace"
)

const evidenceTickInterval = time.Second

type sessionEvidenceTable struct {
	mu       sync.Mutex
	sessions map[string]*sessionstate.Evidence
}

func newSessionEvidenceTable() *sessionEvidenceTable {
	return &sessionEvidenceTable{sessions: make(map[string]*sessionstate.Evidence)}
}

// updateIf runs admit INSIDE the table's lock: checked outside, a writer could
// pass liveness, lose to a removal, and recreate an orphan entry.
func (t *sessionEvidenceTable) updateIf(sessionID string, at time.Time, admit func() bool, mutate func(*sessionstate.Evidence)) {
	if t == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if admit != nil && !admit() {
		return
	}
	evidence := t.sessions[sessionID]
	if evidence == nil {
		evidence = &sessionstate.Evidence{}
		t.sessions[sessionID] = evidence
	}
	mutate(evidence)
	evidence.LastMovement = at
}

func (t *sessionEvidenceTable) snapshot(sessionID string) (sessionstate.Evidence, bool) {
	if t == nil {
		return sessionstate.Evidence{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	evidence := t.sessions[sessionID]
	if evidence == nil {
		return sessionstate.Evidence{}, false
	}
	return *evidence, true
}

func (t *sessionEvidenceTable) sessionIDs() []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	ids := make([]string, 0, len(t.sessions))
	for id := range t.sessions {
		ids = append(ids, id)
	}
	return ids
}

func (t *sessionEvidenceTable) forget(sessionID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.sessions, sessionID)
}

// Tests only: runs inside the table's lock, between the live-row check and the write.
var evidenceRecordGateHook func(sessionID string)

func (d *Daemon) recordEvidence(sessionID string, at time.Time, mutate func(*sessionstate.Evidence)) {
	d.evidenceTable().updateIf(sessionID, at, func() bool {
		live := d.store != nil && d.store.Get(sessionID) != nil
		if hook := evidenceRecordGateHook; hook != nil {
			hook(sessionID)
		}
		return live
	}, mutate)
}

func (d *Daemon) evidenceTable() *sessionEvidenceTable {
	d.sessionEvidenceOnce.Do(func() {
		d.sessionEvidence = newSessionEvidenceTable()
	})
	return d.sessionEvidence
}

func (d *Daemon) dwellGate() *dwellGate {
	d.sessionDwellOnce.Do(func() {
		d.sessionDwell = newDwellGate()
	})
	return d.sessionDwell
}

func (d *Daemon) recordPTYEvidence(sessionID string, obs pty.Observation) {
	at := obs.At
	if at.IsZero() {
		at = time.Now()
	}
	switch obs.Source {
	case pty.SourceHeartbeat:
		if obs.Claim == "approval" {
			d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
				e.LastHarnessEvent = &sessionstate.Observation{
					Source:     sessionstate.SourceHarnessEvent,
					Claim:      sessionstate.ClaimApprovalPending,
					Detail:     obs.Detail,
					ObservedAt: at,
				}
				e.Heartbeat = &sessionstate.Observation{
					Source:     sessionstate.SourceHeartbeat,
					Claim:      sessionstate.ClaimSettled,
					Detail:     obs.Detail,
					ObservedAt: at,
				}
			})
			return
		}
		// A title nobody can read is still someone painting: it stamps LastMovement
		// and leaves the level alone. Filed as settled, it would retire an open turn.
		if obs.Claim == "unclassified" {
			d.recordEvidence(sessionID, at, func(*sessionstate.Evidence) {})
			return
		}
		claim := sessionstate.ClaimSettled
		if obs.Claim == "busy" {
			claim = sessionstate.ClaimBusy
		}
		d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
			e.Heartbeat = &sessionstate.Observation{
				Source:     sessionstate.SourceHeartbeat,
				Claim:      claim,
				Detail:     obs.Detail,
				ObservedAt: at,
			}
			if claim == sessionstate.ClaimBusy {
				e.LastBusyAt = at
			}
		})
	}
}

func (d *Daemon) recordBracketEvidence(sessionID, state string) {
	at := time.Now()
	d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
		switch state {
		case protocol.StateWorking:
			e.TurnOpen = true
			e.TurnEverOpened = true
			// A stale verdict judged the previous turn; left in the table the
			// resolver would report it the moment this turn settles.
			e.LastClassifier = nil
			if e.LastHarnessEvent != nil {
				switch e.LastHarnessEvent.Claim {
				case sessionstate.ClaimApprovalPending,
					sessionstate.ClaimNeedsInput,
					sessionstate.ClaimStopFailed,
					sessionstate.ClaimTurnAborted:
					e.LastHarnessEvent = nil
				}
			}
			e.Compacting = false
			// These describe how the LAST turn yielded; left behind, background work
			// pins the session working with only silence to unpin it.
			e.BackgroundWork = false
			e.PendingCron = false
		case protocol.StateIdle:
			e.TurnOpen = false
			e.ToolOpen = false
		case protocol.StateWaitingInput:
			// A question is filed like an approval request and retired the same way:
			// closing the brackets alone resolves to idle and loses the question.
			e.TurnOpen = false
			e.ToolOpen = false
			e.LastHarnessEvent = &sessionstate.Observation{
				Source:     sessionstate.SourceHarnessEvent,
				Claim:      sessionstate.ClaimNeedsInput,
				ObservedAt: at,
			}
		case protocol.StatePendingApproval:
			e.LastHarnessEvent = &sessionstate.Observation{
				Source:     sessionstate.SourceHarnessEvent,
				Claim:      sessionstate.ClaimApprovalPending,
				ObservedAt: at,
			}
		}
	})
}

func (d *Daemon) recordTranscriptEvidence(sessionID, state, detail string, at time.Time) {
	d.recordBracketEvidence(sessionID, state)
	d.traceStateEvidence(
		sessionID,
		stateOrigin{source: stateSourceTranscript, detail: detail, observedAt: at},
		state,
	)
}

// abortedAt (agent-dated) and observedAt (read time) stay separate so a late-read
// halt loses to later busy frames.
func (d *Daemon) recordTurnAbortedEvidence(sessionID, detail string, abortedAt, observedAt time.Time) {
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	at := abortedAt
	if at.IsZero() {
		at = observedAt
	}
	d.recordEvidence(sessionID, observedAt, func(e *sessionstate.Evidence) {
		e.TurnOpen = false
		e.ToolOpen = false
		e.LastHarnessEvent = &sessionstate.Observation{
			Source:     sessionstate.SourceHarnessEvent,
			Claim:      sessionstate.ClaimTurnAborted,
			Detail:     detail,
			ObservedAt: at,
		}
	})
}

func (d *Daemon) recordTurnBracketClosedEvidence(sessionID string, at time.Time) {
	if at.IsZero() {
		at = time.Now()
	}
	d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
		e.TurnOpen = false
		e.ToolOpen = false
	})
}

func (d *Daemon) recordClassifierEvidence(sessionID, state string, observedAt time.Time) {
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	claim := classifierClaim(state)
	if claim == "" {
		return
	}
	d.recordEvidence(sessionID, observedAt, func(e *sessionstate.Evidence) {
		e.LastClassifier = &sessionstate.Observation{
			Source:     sessionstate.SourceClassifier,
			Claim:      claim,
			ObservedAt: observedAt,
		}
	})
}

func (d *Daemon) recordStopFacts(sessionID string, backgroundWork, pendingCron bool) {
	d.recordEvidence(sessionID, time.Now(), func(e *sessionstate.Evidence) {
		e.BackgroundWork = backgroundWork
		e.PendingCron = pendingCron
	})
}

func (d *Daemon) recordReviewerEvidence(sessionID string, inLoop bool) {
	d.recordEvidence(sessionID, time.Now(), func(e *sessionstate.Evidence) {
		e.ReviewerInLoop = inLoop
	})
}

// An absent mode (older CLI) is not a report, and codex sends `default` as filler,
// which would retire the spawn-time fact on turn one.
func (d *Daemon) recordReviewerEvidenceFromPermissionMode(sessionID, permissionMode string) {
	mode := strings.TrimSpace(permissionMode)
	if mode == "" {
		return
	}
	if !permissionModeGovernsApprovals(d.sessionAgent(sessionID)) {
		return
	}
	d.recordReviewerEvidence(sessionID, mode != "default")
}

func permissionModeGovernsApprovals(agent protocol.SessionAgent) bool {
	return agent == protocol.SessionAgentClaude
}

func (d *Daemon) sessionAgent(sessionID string) protocol.SessionAgent {
	if d.store == nil {
		return ""
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return ""
	}
	return session.Agent
}

const (
	notifyPermissionPrompt = "permission_prompt"
	notifyIdlePrompt       = "idle_prompt"
)

func (d *Daemon) recordNotificationEvidence(sessionID, notificationType, message string) {
	at := time.Now()
	switch strings.TrimSpace(notificationType) {
	case notifyPermissionPrompt:
		d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
			e.LastHarnessEvent = &sessionstate.Observation{
				Source:     sessionstate.SourceHarnessEvent,
				Claim:      sessionstate.ClaimApprovalPending,
				Detail:     message,
				ObservedAt: at,
			}
		})
	case notifyIdlePrompt:
		d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
			e.PromptIdleAt = at
		})
	}
}

func (d *Daemon) recordStopFailureEvidence(sessionID, errorType, message string) {
	at := time.Now()
	detail := strings.TrimSpace(errorType)
	if message = strings.TrimSpace(message); message != "" {
		detail = detail + ": " + message
	}
	d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
		e.LastHarnessEvent = &sessionstate.Observation{
			Source:     sessionstate.SourceHarnessEvent,
			Claim:      sessionstate.ClaimStopFailed,
			Detail:     detail,
			ObservedAt: at,
		}
	})
}

func (d *Daemon) recordCompactionEvidence(sessionID string, active bool) {
	d.recordEvidence(sessionID, time.Now(), func(e *sessionstate.Evidence) {
		e.Compacting = active
	})
}

func (d *Daemon) recordProcessEvidence(sessionID string, exited bool) {
	if !exited {
		return
	}
	at := time.Now()
	d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
		e.Process = &sessionstate.Observation{
			Source:     sessionstate.SourceProcess,
			Claim:      sessionstate.ClaimExited,
			ObservedAt: at,
		}
	})
}

func (d *Daemon) recordClassifierStarted(sessionID string, at time.Time) {
	d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
		e.ClassifyingSince = at
	})
	// Suspend auto-settle until the verdict lands; the fire path repeats this
	// check to close the race with a timer that already left the map.
	d.cancelAutoSettle(sessionID, "classification started")
}

// Must run on EVERY exit from a classification: one that applies nothing is exactly
// when the session has to settle on its own.
func (d *Daemon) recordClassifierFinished(sessionID string) {
	d.recordEvidence(sessionID, time.Now(), func(e *sessionstate.Evidence) {
		e.ClassifyingSince = time.Time{}
	})
	// No transition is guaranteed here (a background-working verdict can leave
	// `working` persisted), so re-evaluate auto-settle explicitly.
	if session := d.store.Get(sessionID); session != nil {
		d.syncAutoSettle(sessionID, string(session.State))
	}
}

func (d *Daemon) runEvidenceResolveLoop() {
	ticker := time.NewTicker(evidenceTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.resolveAllSessions(time.Now())
		}
	}
}

func (d *Daemon) resolveAllSessions(now time.Time) {
	for _, sessionID := range d.evidenceTable().sessionIDs() {
		session := d.store.Get(sessionID)
		if session == nil {
			d.evidenceTable().forget(sessionID)
			d.dwellGate().clear(sessionID)
			continue
		}
		evidence, ok := d.evidenceTable().snapshot(sessionID)
		if !ok {
			continue
		}
		policy := sessionstate.PolicyFor(string(session.Agent))
		resolution := sessionstate.Resolve(evidence, policy, now)
		d.publishResolution(sessionID, session.State, resolution, sessionstate.DwellFor(resolution.State, evidence, policy), now)
	}
}

// Resolving `recoverable` would let a stale process observation stomp the revive
// path; `launching` must stay owned, or the session strands.
var resolverOwnedStates = map[protocol.SessionState]bool{
	protocol.SessionStateLaunching:       true,
	protocol.SessionStateWorking:         true,
	protocol.SessionStatePendingApproval: true,
	protocol.SessionStateWaitingInput:    true,
	protocol.SessionStateIdle:            true,
	protocol.SessionStateScheduled:       true,
	protocol.SessionStateUnknown:         true,
}

func (d *Daemon) publishResolution(sessionID string, current protocol.SessionState, resolution sessionstate.Resolution, dwell time.Duration, now time.Time) {
	if resolution.Hold {
		d.traceResolutionSkip(sessionID, resolution, string(resolution.Reason))
		return
	}
	if resolution.Reason == sessionstate.ReasonNoEvidence {
		return
	}
	// An external driver owns its session's state through sequenced report_*
	// calls; without this veto the tick would overwrite a current report.
	if run := d.store.GetAgentDriverRun(sessionID); run.RunID != "" {
		if session := d.store.Get(sessionID); session != nil && d.pluginDriverReportsState(session.Agent) {
			d.traceResolutionSkip(sessionID, resolution, "plugin_driver_owns_state")
			return
		}
	}
	if !resolverOwnedStates[current] || resolution.State == current {
		// No transition: drop the dwell wait so a later one cannot inherit a clock
		// that started before an unrelated transition.
		d.dwellGate().clear(sessionID)
		if d.recordStateReason(sessionID, resolution) && resolverOwnedStates[current] {
			d.broadcastSessionStateChanged(sessionID)
		}
		return
	}
	if !d.dwellGate().ready(sessionID, resolution.State, dwell, now) {
		d.traceResolutionSkip(sessionID, resolution, "dwell")
		return
	}
	// Below the dwell, not above: recording the reason for a transition still serving
	// its dwell publishes a self-contradicting pair, witnessed on a live session.
	d.recordStateReason(sessionID, resolution)
	d.applyState(sessionStateChange{
		sessionID: sessionID,
		state:     string(resolution.State),
		cause:     resolverObservation{},
		origin: stateOrigin{
			source: stateSourceResolver,
			detail: resolutionDetail(resolution),
		},
	})
}

func resolutionDetail(resolution sessionstate.Resolution) string {
	if resolution.Detail == "" {
		return string(resolution.Reason)
	}
	return string(resolution.Reason) + ": " + resolution.Detail
}

func (d *Daemon) traceResolutionSkip(sessionID string, resolution sessionstate.Resolution, reason string) {
	d.recordStateObservation(sessionID, statetrace.Observation{
		Source:  stateSourceResolver,
		Claim:   string(resolution.State),
		Detail:  resolution.Detail,
		Outcome: statetrace.OutcomeSkipped,
		Reason:  reason,
	})
}

func classifierClaim(state string) sessionstate.Claim {
	switch state {
	case protocol.StateWaitingInput:
		return sessionstate.ClaimNeedsInput
	case protocol.StateIdle:
		return sessionstate.ClaimIdle
	case classifier.VerdictParked:
		return sessionstate.ClaimParked
	default:
		return ""
	}
}
