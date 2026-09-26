package daemon

import (
	"strings"
	"sync"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/statetrace"
)

type sessionEvidenceTable struct {
	mu       sync.Mutex
	sessions map[string]*sessionstate.Evidence
}

func newSessionEvidenceTable() *sessionEvidenceTable {
	return &sessionEvidenceTable{sessions: make(map[string]*sessionstate.Evidence)}
}

func (t *sessionEvidenceTable) updateIf(
	sessionID string,
	admit func() bool,
	unchanged func(*sessionstate.Evidence) bool,
	mutate func(*sessionstate.Evidence),
) bool {
	if t == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	evidence := t.sessions[sessionID]
	if unchanged != nil {
		current := evidence
		if current == nil {
			current = &sessionstate.Evidence{}
		}
		if unchanged(current) {
			return false
		}
	}
	if admit != nil && !admit() {
		return false
	}
	if evidence == nil {
		evidence = &sessionstate.Evidence{}
		t.sessions[sessionID] = evidence
	}
	mutate(evidence)
	return true
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

func (t *sessionEvidenceTable) forget(sessionID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.sessions, sessionID)
}

func (d *Daemon) recordEvidence(sessionID string, at time.Time, mutate func(*sessionstate.Evidence)) bool {
	return d.updateEvidence(sessionID, nil, movedAt(at, mutate))
}

func movedAt(at time.Time, mutate func(*sessionstate.Evidence)) func(*sessionstate.Evidence) {
	return func(e *sessionstate.Evidence) {
		mutate(e)
		e.LastMovement = at
	}
}

func (d *Daemon) updateEvidence(
	sessionID string,
	unchanged func(*sessionstate.Evidence) bool,
	mutate func(*sessionstate.Evidence),
) bool {
	changed := d.evidenceTable().updateIf(sessionID, func() bool {
		return d.store != nil && d.store.Get(sessionID) != nil
	}, unchanged, mutate)
	if changed {
		d.resolveSoon(sessionID)
	}
	return changed
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

func (d *Daemon) recordPTYEvidence(sessionID string, obs pty.Observation) bool {
	at := obs.At
	if at.IsZero() {
		at = time.Now()
	}
	mutate, ok := heartbeatEvidence(obs, at)
	if !ok {
		return false
	}
	return d.updateEvidence(sessionID, func(e *sessionstate.Evidence) bool {
		return holdsSettledHeartbeat(e, obs)
	}, movedAt(at, mutate))
}

func holdsSettledHeartbeat(e *sessionstate.Evidence, obs pty.Observation) bool {
	switch obs.Claim {
	case "approval", "unclassified", "busy":
		return false
	}
	return e.Heartbeat != nil &&
		e.Heartbeat.Claim == sessionstate.ClaimSettled &&
		e.Heartbeat.Detail == obs.Detail
}

func (d *Daemon) evidenceHoldsSettledHeartbeat(sessionID string, obs pty.Observation) bool {
	evidence, ok := d.evidenceTable().snapshot(sessionID)
	return ok && holdsSettledHeartbeat(&evidence, obs)
}

func heartbeatEvidence(obs pty.Observation, at time.Time) (func(*sessionstate.Evidence), bool) {
	if obs.Source != pty.SourceHeartbeat {
		return nil, false
	}
	switch obs.Claim {
	case "approval":
		return func(e *sessionstate.Evidence) {
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
		}, true
	case "unclassified":
		return func(*sessionstate.Evidence) {}, true
	}
	claim := sessionstate.ClaimSettled
	if obs.Claim == "busy" {
		claim = sessionstate.ClaimBusy
	}
	return func(e *sessionstate.Evidence) {
		e.Heartbeat = &sessionstate.Observation{
			Source:     sessionstate.SourceHeartbeat,
			Claim:      claim,
			Detail:     obs.Detail,
			ObservedAt: at,
		}
		if claim == sessionstate.ClaimBusy {
			e.LastBusyAt = at
		}
	}, true
}

func (d *Daemon) startEvidence(sessionID string, fresh sessionstate.Evidence) {
	d.dwellGate().clear(sessionID)
	d.updateEvidence(sessionID, nil, func(e *sessionstate.Evidence) { *e = fresh })
}

func (d *Daemon) recordPlacedInputOwed(sessionID string, owed bool) {
	d.updateEvidence(sessionID, func(e *sessionstate.Evidence) bool {
		return e.PlacedInputOwed == owed || (owed && sessionstate.TookATurn(*e))
	}, func(e *sessionstate.Evidence) {
		e.PlacedInputOwed = owed
	})
}

func reportsPromptsTaken(agent string) bool {
	return agentdriver.EffectiveCapabilities(agentdriver.Get(agent)).HasHooks
}

func (d *Daemon) initialPromptPending(sessionID string) bool {
	evidence, _ := d.evidenceTable().snapshot(sessionID)
	return evidence.InitialPromptOwed
}

func (d *Daemon) recordBracketEvidence(sessionID, state string) {
	at := time.Now()
	d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
		switch state {
		case protocol.StateWorking:
			e.TurnOpen = true
			e.TurnEverOpened = true
			e.InitialPromptOwed = false
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
			e.BackgroundWork = false
			e.PendingCron = false
		case protocol.StateIdle:
			e.TurnOpen = false
			e.ToolOpen = false
		case protocol.StateWaitingInput:
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
	d.traceStateEvidence(
		sessionID,
		stateOrigin{source: stateSourceTranscript, detail: detail, observedAt: at},
		state,
	)
	d.recordBracketEvidence(sessionID, state)
}

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

func (d *Daemon) recordTurnEndedEvidence(sessionID string, classifies bool) {
	at := time.Now()
	d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
		e.TurnOpen = false
		e.ToolOpen = false
		if classifies {
			e.ClassifyingSince = at
		}
	})
}

func classifierVerdictMutation(state string, observedAt time.Time) func(*sessionstate.Evidence) {
	claim := classifierClaim(state)
	if claim == "" {
		return nil
	}
	return func(e *sessionstate.Evidence) {
		e.LastClassifier = &sessionstate.Observation{
			Source:     sessionstate.SourceClassifier,
			Claim:      claim,
			ObservedAt: observedAt,
		}
	}
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
		e.PlacedInputOwed = false
	})
}

func (d *Daemon) recordClassifierStarted(sessionID string, at time.Time) {
	d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
		e.ClassifyingSince = at
	})
	d.cancelAutoSettle(sessionID, "classification started")
}

func (d *Daemon) concludeClassification(sessionID string, verdict func(*sessionstate.Evidence)) {
	if session := d.store.Get(sessionID); session != nil {
		d.syncAutoSettle(sessionID, string(session.State))
	}
	d.recordEvidence(sessionID, time.Now(), func(e *sessionstate.Evidence) {
		if verdict != nil {
			verdict(e)
		}
		e.ClassifyingSince = time.Time{}
	})
}

var resolverOwnedStates = map[protocol.SessionState]bool{
	protocol.SessionStateLaunching:       true,
	protocol.SessionStateWorking:         true,
	protocol.SessionStatePendingApproval: true,
	protocol.SessionStateWaitingInput:    true,
	protocol.SessionStateIdle:            true,
	protocol.SessionStateScheduled:       true,
	protocol.SessionStateUnknown:         true,
}

func (d *Daemon) publishResolution(sessionID string, current protocol.SessionState, resolution sessionstate.Resolution, dwell time.Duration, now time.Time) (resolverOwnsState bool) {
	pluginOwnsState := d.pluginDriverOwnsState(sessionID)
	resolverOwnsState = resolverOwnedStates[current] && !pluginOwnsState
	if resolution.Hold {
		d.traceResolutionSkip(sessionID, resolution, string(resolution.Reason))
		return resolverOwnsState
	}
	if resolution.Reason == sessionstate.ReasonNoEvidence {
		return resolverOwnsState
	}
	if pluginOwnsState {
		d.traceResolutionSkip(sessionID, resolution, "plugin_driver_owns_state")
		return false
	}
	if !resolverOwnedStates[current] || resolution.State == current {
		d.dwellGate().clear(sessionID)
		if d.recordStateReason(sessionID, resolution) && resolverOwnedStates[current] {
			d.broadcastSessionStateChanged(sessionID)
		}
		return resolverOwnsState
	}
	if !d.dwellGate().ready(sessionID, resolution.State, dwell, now) {
		d.traceResolutionSkip(sessionID, resolution, "dwell")
		return true
	}
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
	return true
}

func (d *Daemon) pluginDriverOwnsState(sessionID string) bool {
	if run := d.store.GetAgentDriverRun(sessionID); run.RunID == "" {
		return false
	}
	session := d.store.Get(sessionID)
	return session != nil && d.pluginDriverReportsState(session.Agent)
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
