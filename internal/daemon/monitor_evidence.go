package daemon

import (
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/statetrace"
)

func (d *Daemon) evidenceSnapshot(sessionID protocol.SessionID) (monitorEvidence, bool) {
	e := d.monitorEntry(sessionID)
	if e == nil {
		return monitorEvidence{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.monitor.mu.Lock()
	defer e.monitor.mu.Unlock()
	return monitorEvidence{e.monitor.terminal, e.evidence}, true
}

func (d *Daemon) recordEvidence(sessionID protocol.SessionID, at time.Time, mutate func(*monitorEvidence)) bool {
	return d.updateEvidence(sessionID, nil, movedAt(at, mutate))
}

func movedAt(at time.Time, mutate func(*monitorEvidence)) func(*monitorEvidence) {
	return func(e *monitorEvidence) {
		mutate(e)
		e.LastMovement = at
	}
}

func (d *Daemon) updateEvidence(
	sessionID protocol.SessionID,
	unchanged func(*monitorEvidence) bool,
	mutate func(*monitorEvidence),
) bool {
	e := d.monitorEntry(sessionID)
	if e == nil || d.store == nil || d.store.Get(sessionID) == nil {
		return false
	}
	return e.updateEvidence(d.monitors(), unchanged, mutate)
}

func (e *monitorEntry) recordEvidence(r *monitorRegistry, at time.Time, mutate func(*monitorEvidence)) bool {
	return e.updateEvidence(r, nil, movedAt(at, mutate))
}

func (e *monitorEntry) updateEvidence(r *monitorRegistry, unchanged func(*monitorEvidence) bool, mutate func(*monitorEvidence)) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.monitor.mu.Lock()
	defer e.monitor.mu.Unlock()
	evidence := monitorEvidence{e.monitor.terminal, e.evidence}
	if unchanged != nil && unchanged(&evidence) {
		return false
	}
	mutate(&evidence)
	e.monitor.terminal, e.evidence = evidence.TerminalEvidence, evidence.ConversationEvidence
	e.resolveSoonLocked(r)
	return true
}

func (d *Daemon) recordPTYEvidence(sessionID protocol.SessionID, obs pty.Observation) bool {
	at := obs.At
	if at.IsZero() {
		at = time.Now()
	}
	mutate, ok := ptyEvidence(obs, at)
	if !ok {
		return false
	}
	return d.updateEvidence(sessionID, func(e *monitorEvidence) bool {
		return holdsPTYEvidence(e, obs)
	}, movedAt(at, mutate))
}

func ptyEvidence(obs pty.Observation, at time.Time) (func(*monitorEvidence), bool) {
	if obs.Source == pty.SourceProgramStatus {
		return programStatusEvidence(obs, at), true
	}
	return heartbeatEvidence(obs, at)
}

func holdsPTYEvidence(e *monitorEvidence, obs pty.Observation) bool {
	if obs.Source == pty.SourceProgramStatus {
		return holdsProgramStatus(e, obs)
	}
	return e.ProgramStatus != nil || holdsSettledHeartbeat(e, obs)
}

func programStatusClaim(claim string) (sessionstate.Claim, bool) {
	switch claim {
	case pty.ProgramWorking:
		return sessionstate.ClaimBusy, true
	case pty.ProgramBlockedPermission:
		return sessionstate.ClaimApprovalPending, true
	case pty.ProgramBlocked, pty.ProgramBlockedQuestion, pty.ProgramBlockedAuth:
		return sessionstate.ClaimNeedsInput, true
	case pty.ProgramError:
		return sessionstate.ClaimStopFailed, true
	case pty.ProgramDone, pty.ProgramIdle:
		return sessionstate.ClaimSettled, true
	default:
		return "", false
	}
}

func holdsProgramStatus(e *monitorEvidence, obs pty.Observation) bool {
	claim, reported := programStatusClaim(obs.Claim)
	if !reported {
		return e.ProgramStatus == nil
	}
	return e.ProgramStatus != nil &&
		e.ProgramStatus.Claim == claim &&
		e.ProgramStatus.Detail == obs.Detail
}

func programStatusEvidence(obs pty.Observation, at time.Time) func(*monitorEvidence) {
	claim, reported := programStatusClaim(obs.Claim)
	return func(e *monitorEvidence) {
		if claim == sessionstate.ClaimBusy {
			e.LastBusyAt = at
		}
		e.Heartbeat = nil
		if !reported {
			e.ClearProgramStatus()
			return
		}
		e.ProgramStatus = &sessionstate.Observation{
			Source:     sessionstate.SourceProgramStatus,
			Claim:      claim,
			Detail:     obs.Detail,
			ObservedAt: at,
		}
	}
}

func holdsSettledHeartbeat(e *monitorEvidence, obs pty.Observation) bool {
	switch obs.Claim {
	case "approval", "unclassified", "busy":
		return false
	}
	return e.Heartbeat != nil &&
		e.Heartbeat.Claim == sessionstate.ClaimSettled &&
		e.Heartbeat.Detail == obs.Detail
}

func (d *Daemon) evidenceHoldsPTYEvidence(sessionID protocol.SessionID, obs pty.Observation) bool {
	evidence, ok := d.evidenceSnapshot(sessionID)
	return ok && holdsPTYEvidence(&evidence, obs)
}

func heartbeatEvidence(obs pty.Observation, at time.Time) (func(*monitorEvidence), bool) {
	if obs.Source != pty.SourceHeartbeat {
		return nil, false
	}
	switch obs.Claim {
	case "approval":
		return func(e *monitorEvidence) {
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
		return func(*monitorEvidence) {}, true
	}
	claim := sessionstate.ClaimSettled
	if obs.Claim == "busy" {
		claim = sessionstate.ClaimBusy
	}
	return func(e *monitorEvidence) {
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

func (d *Daemon) startEvidence(sessionID protocol.SessionID, fresh monitorEvidence) {
	entry := d.startSessionMonitor(sessionID)
	if entry != nil {
		if session := d.store.Get(sessionID); session != nil {
			entry.mu.Lock()
			entry.directory = session.Directory
			entry.branch = branchFromSession(session)
			entry.state = monitorStateView{Quiet: true, Sequence: entry.state.Sequence + 1, State: session.State,
				Since: protocol.Timestamp(session.StateSince).Time(), Updated: protocol.Timestamp(session.StateUpdatedAt).Time()}
			entry.firstReportAt = time.Time{}
			entry.reason = ""
			view := entry.viewLocked()
			entry.mu.Unlock()
			entry.sink.Applied(view)
		}
	}
	d.clearStateReason(sessionID)
	d.clearDwell(sessionID)
	d.updateEvidence(sessionID, nil, func(e *monitorEvidence) { *e = fresh })
}

func reportsTurnStarts(agent string) bool {
	return agentdriver.EffectiveCapabilities(agentdriver.Get(agent)).HasHooks
}

func (d *Daemon) recordBracketEvidence(sessionID protocol.SessionID, state string) {
	d.monitorEntry(sessionID).recordBracketEvidence(d.monitors(), state)
}

func (e *monitorEntry) recordBracketEvidence(r *monitorRegistry, state string) {
	at := time.Now()
	e.recordEvidence(r, at, func(e *monitorEvidence) {
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

func (e *monitorEntry) recordTranscriptEvidence(d *Daemon, sessionID protocol.SessionID, state, detail string, at time.Time) {
	d.recordMonitorObservation(e, sessionID, statetrace.Observation{Source: stateSourceTranscript, Detail: detail, Claim: state, ObservedAt: at, Outcome: statetrace.OutcomeObserved})
	e.recordBracketEvidence(d.monitors(), state)
}

func (e *monitorEntry) recordTurnAbortedEvidence(r *monitorRegistry, detail string, abortedAt, observedAt time.Time) {
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	at := abortedAt
	if at.IsZero() {
		at = observedAt
	}
	e.recordEvidence(r, observedAt, func(e *monitorEvidence) {
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

func (e *monitorEntry) recordTurnBracketClosedEvidence(r *monitorRegistry, at time.Time) {
	if at.IsZero() {
		at = time.Now()
	}
	e.recordEvidence(r, at, func(e *monitorEvidence) {
		e.TurnOpen = false
		e.ToolOpen = false
	})
}

func (d *Daemon) recordTurnEndedEvidence(sessionID protocol.SessionID, classifies bool) {
	at := time.Now()
	d.recordEvidence(sessionID, at, func(e *monitorEvidence) {
		e.TurnOpen = false
		e.ToolOpen = false
		if classifies {
			e.ClassifyingSince = at
		}
	})
}

func classifierVerdictMutation(state string, observedAt time.Time) func(*monitorEvidence) {
	claim := classifierClaim(state)
	if claim == "" {
		return nil
	}
	return func(e *monitorEvidence) {
		e.LastClassifier = &sessionstate.Observation{
			Source:     sessionstate.SourceClassifier,
			Claim:      claim,
			ObservedAt: observedAt,
		}
	}
}

func (d *Daemon) recordStopFacts(sessionID protocol.SessionID, backgroundWork, pendingCron bool) {
	d.recordEvidence(sessionID, time.Now(), func(e *monitorEvidence) {
		e.BackgroundWork = backgroundWork
		e.PendingCron = pendingCron
	})
}

func (d *Daemon) recordReviewerEvidence(sessionID protocol.SessionID, inLoop bool) {
	d.recordEvidence(sessionID, time.Now(), func(e *monitorEvidence) {
		e.ReviewerInLoop = inLoop
	})
}

func (d *Daemon) recordReviewerEvidenceFromPermissionMode(sessionID protocol.SessionID, permissionMode string) {
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

func (d *Daemon) sessionAgent(sessionID protocol.SessionID) protocol.SessionAgent {
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

func (d *Daemon) recordNotificationEvidence(sessionID protocol.SessionID, notificationType string, message string) {
	at := time.Now()
	switch strings.TrimSpace(notificationType) {
	case notifyPermissionPrompt:
		d.recordEvidence(sessionID, at, func(e *monitorEvidence) {
			e.LastHarnessEvent = &sessionstate.Observation{
				Source:     sessionstate.SourceHarnessEvent,
				Claim:      sessionstate.ClaimApprovalPending,
				Detail:     message,
				ObservedAt: at,
			}
		})
	case notifyIdlePrompt:
		d.recordEvidence(sessionID, at, func(e *monitorEvidence) {
			e.PromptIdleAt = at
		})
	}
}

func (d *Daemon) recordStopFailureEvidence(sessionID protocol.SessionID, errorType string, message string) {
	at := time.Now()
	detail := strings.TrimSpace(errorType)
	if message = strings.TrimSpace(message); message != "" {
		detail = detail + ": " + message
	}
	d.recordEvidence(sessionID, at, func(e *monitorEvidence) {
		e.LastHarnessEvent = &sessionstate.Observation{
			Source:     sessionstate.SourceHarnessEvent,
			Claim:      sessionstate.ClaimStopFailed,
			Detail:     detail,
			ObservedAt: at,
		}
	})
}

func (d *Daemon) recordCompactionEvidence(sessionID protocol.SessionID, active bool) {
	d.recordEvidence(sessionID, time.Now(), func(e *monitorEvidence) {
		e.Compacting = active
	})
}

func (d *Daemon) recordProcessEvidence(sessionID protocol.SessionID, exited bool) {
	if !exited {
		return
	}
	at := time.Now()
	d.recordEvidence(sessionID, at, func(e *monitorEvidence) {
		e.Process = &sessionstate.Observation{
			Source:     sessionstate.SourceProcess,
			Claim:      sessionstate.ClaimExited,
			ObservedAt: at,
		}
		e.PlacedInputOwed = false
	})
}

func (e *monitorEntry) recordClassifierStarted(d *Daemon, sessionID protocol.SessionID, at time.Time) {
	e.recordEvidence(d.monitors(), at, func(e *monitorEvidence) { e.ClassifyingSince = at })
	if d.monitorEntry(sessionID) == e {
		d.cancelAutoSettle(sessionID, "classification started")
	}
}

func (e *monitorEntry) concludeClassification(d *Daemon, sessionID protocol.SessionID, verdict func(*monitorEvidence)) {
	if d.monitorEntry(sessionID) == e {
		if session := d.store.Get(sessionID); session != nil {
			d.syncAutoSettle(sessionID, string(session.State))
		}
	}
	e.recordEvidence(d.monitors(), time.Now(), func(e *monitorEvidence) {
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

func (d *Daemon) publishResolution(sessionID protocol.SessionID, current protocol.SessionState, resolution sessionstate.Resolution, dwell time.Duration, now time.Time) (resolverOwnsState bool) {
	linkOwns := d.linkOwnsState(sessionID)
	resolverOwnsState = resolverOwnedStates[current] && !linkOwns
	if resolution.Hold {
		d.traceResolutionSkip(sessionID, resolution, string(resolution.Reason))
		return resolverOwnsState
	}
	if resolution.Reason == sessionstate.ReasonNoEvidence {
		return resolverOwnsState
	}
	if linkOwns {
		d.traceResolutionSkip(sessionID, resolution, "link_owns_state")
		return false
	}
	if !resolverOwnedStates[current] || resolution.State == current {
		d.clearDwell(sessionID)
		if d.recordStateReason(sessionID, resolution) && resolverOwnedStates[current] {
			if entry := d.monitorEntry(sessionID); entry != nil {
				entry.mu.Lock()
				view := entry.viewLocked()
				entry.mu.Unlock()
				entry.sink.Applied(view)
			}
		}
		return resolverOwnsState
	}
	if !d.dwellReady(sessionID, resolution.State, dwell, now) {
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

func resolutionDetail(resolution sessionstate.Resolution) string {
	if resolution.Detail == "" {
		return string(resolution.Reason)
	}
	return string(resolution.Reason) + ": " + resolution.Detail
}

func (d *Daemon) traceResolutionSkip(sessionID protocol.SessionID, resolution sessionstate.Resolution, reason string) {
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
