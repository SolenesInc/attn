package sessionmonitor

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

func (e *Entry) Evidence() (Evidence, bool) {
	if e == nil {
		return Evidence{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.monitor.mu.Lock()
	defer e.monitor.mu.Unlock()
	return Evidence{e.monitor.terminal, e.evidence}, true
}

func movedAt(at time.Time, mutate func(*Evidence)) func(*Evidence) {
	return func(e *Evidence) {
		mutate(e)
		e.LastMovement = at
	}
}

func (e *Entry) RecordEvidence(at time.Time, mutate func(*Evidence)) bool {
	if e == nil {
		return false
	}
	return e.UpdateEvidence(nil, movedAt(at, mutate))
}

func (e *Entry) UpdateEvidence(unchanged func(*Evidence) bool, mutate func(*Evidence)) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.monitor.mu.Lock()
	defer e.monitor.mu.Unlock()
	evidence := Evidence{e.monitor.terminal, e.evidence}
	if unchanged != nil && unchanged(&evidence) {
		return false
	}
	mutate(&evidence)
	e.monitor.terminal, e.evidence = evidence.TerminalEvidence, evidence.ConversationEvidence
	e.resolveSoonLocked()
	return true
}

func (e *Entry) RecordPTY(obs pty.Observation) bool {
	if e == nil {
		return false
	}
	at := obs.At
	if at.IsZero() {
		at = time.Now()
	}
	mutate, ok := PTYEvidence(obs, at)
	if !ok {
		return false
	}
	return e.UpdateEvidence(func(e *Evidence) bool {
		return holdsPTYEvidence(e, obs)
	}, movedAt(at, mutate))
}

func PTYEvidence(obs pty.Observation, at time.Time) (func(*Evidence), bool) {
	if obs.Source == pty.SourceProgramStatus {
		return programStatusEvidence(obs, at), true
	}
	return heartbeatEvidence(obs, at)
}

func holdsPTYEvidence(e *Evidence, obs pty.Observation) bool {
	if obs.Source == pty.SourceProgramStatus {
		return holdsProgramStatus(e, obs)
	}
	return e.ProgramStatus != nil || holdsSettledHeartbeat(e, obs)
}

func ProgramStatusClaim(claim string) (sessionstate.Claim, bool) {
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

func holdsProgramStatus(e *Evidence, obs pty.Observation) bool {
	claim, reported := ProgramStatusClaim(obs.Claim)
	if !reported {
		return e.ProgramStatus == nil
	}
	return e.ProgramStatus != nil &&
		e.ProgramStatus.Claim == claim &&
		e.ProgramStatus.Detail == obs.Detail
}

func programStatusEvidence(obs pty.Observation, at time.Time) func(*Evidence) {
	claim, reported := ProgramStatusClaim(obs.Claim)
	return func(e *Evidence) {
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

func holdsSettledHeartbeat(e *Evidence, obs pty.Observation) bool {
	switch obs.Claim {
	case "approval", "unclassified", "busy":
		return false
	}
	return e.Heartbeat != nil &&
		e.Heartbeat.Claim == sessionstate.ClaimSettled &&
		e.Heartbeat.Detail == obs.Detail
}

func (e *Entry) HoldsPTY(obs pty.Observation) bool {
	if e == nil {
		return false
	}
	evidence, ok := e.Evidence()
	return ok && holdsPTYEvidence(&evidence, obs)
}

func heartbeatEvidence(obs pty.Observation, at time.Time) (func(*Evidence), bool) {
	if obs.Source != pty.SourceHeartbeat {
		return nil, false
	}
	switch obs.Claim {
	case "approval":
		return func(e *Evidence) {
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
		return func(*Evidence) {}, true
	}
	claim := sessionstate.ClaimSettled
	if obs.Claim == "busy" {
		claim = sessionstate.ClaimBusy
	}
	return func(e *Evidence) {
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

func (e *Entry) Restart(launch Launch, fresh Evidence) {
	if e == nil {
		return
	}
	e.Begin(launch)
	e.UpdateEvidence(nil, func(e *Evidence) { *e = fresh })
}

func (e *Entry) Begin(launch Launch) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.directory, e.branch, e.startedAt = launch.Directory, launch.Branch, launch.StartedAt
	e.state = StateView{Quiet: true, Sequence: e.state.Sequence + 1, State: launch.State, Since: launch.StateSince, Updated: launch.StateUpdated}
	if !launch.LastModelRequestAt.IsZero() {
		e.lastModelRequestAt = launch.LastModelRequestAt
	}
	if launch.Driver.Run != "" {
		e.driver = launch.Driver
	}
	if !launch.LastSeen.IsZero() {
		e.touchedAt = launch.LastSeen
	}
	if launch.Cost.Initialized {
		e.loadCostLocked(launch.Cost)
	}
	e.firstReportAt = time.Time{}
	e.reason = ""
	e.dwell = dwellPending{}
	view := e.viewLocked()
	e.mu.Unlock()
	e.publish(view)
}

func ReportsTurnStarts(agent string) bool {
	return agentdriver.EffectiveCapabilities(agentdriver.Get(agent)).HasHooks
}

func (e *Entry) RecordBracket(state string) {
	if e == nil {
		return
	}
	at := time.Now()
	e.RecordEvidence(at, func(e *Evidence) {
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

func (e *Entry) recordTranscriptEvidence(state, detail string, at time.Time) {
	e.Trace(statetrace.Observation{Source: StateSourceTranscript, Detail: detail, Claim: state, ObservedAt: at, Outcome: statetrace.OutcomeObserved})
	e.RecordBracket(state)
}

func (e *Entry) RecordTurnAborted(detail string, abortedAt, observedAt time.Time) {
	if e == nil {
		return
	}
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	at := abortedAt
	if at.IsZero() {
		at = observedAt
	}
	e.RecordEvidence(observedAt, func(e *Evidence) {
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

func (e *Entry) recordTurnBracketClosedEvidence(at time.Time) {
	if at.IsZero() {
		at = time.Now()
	}
	e.RecordEvidence(at, func(e *Evidence) {
		e.TurnOpen = false
		e.ToolOpen = false
	})
}

func (e *Entry) RecordTurnEnded(classifies bool) {
	if e == nil {
		return
	}
	at := time.Now()
	e.RecordEvidence(at, func(e *Evidence) {
		e.TurnOpen = false
		e.ToolOpen = false
		if classifies {
			e.ClassifyingSince = at
		}
	})
}

func classifierVerdictMutation(state string, observedAt time.Time) func(*Evidence) {
	claim := classifierClaim(state)
	if claim == "" {
		return nil
	}
	return func(e *Evidence) {
		e.LastClassifier = &sessionstate.Observation{
			Source:     sessionstate.SourceClassifier,
			Claim:      claim,
			ObservedAt: observedAt,
		}
	}
}

func (e *Entry) RecordStopFacts(backgroundWork, pendingCron bool) {
	if e == nil {
		return
	}
	e.RecordEvidence(time.Now(), func(e *Evidence) {
		e.BackgroundWork = backgroundWork
		e.PendingCron = pendingCron
	})
}

func (e *Entry) RecordReviewer(inLoop bool) {
	if e == nil {
		return
	}
	e.RecordEvidence(time.Now(), func(e *Evidence) {
		e.ReviewerInLoop = inLoop
	})
}

const (
	notifyPermissionPrompt = "permission_prompt"
	notifyIdlePrompt       = "idle_prompt"
)

func (e *Entry) RecordNotification(notificationType string, message string) {
	if e == nil {
		return
	}
	at := time.Now()
	switch strings.TrimSpace(notificationType) {
	case notifyPermissionPrompt:
		e.RecordEvidence(at, func(e *Evidence) {
			e.LastHarnessEvent = &sessionstate.Observation{
				Source:     sessionstate.SourceHarnessEvent,
				Claim:      sessionstate.ClaimApprovalPending,
				Detail:     message,
				ObservedAt: at,
			}
		})
	case notifyIdlePrompt:
		e.RecordEvidence(at, func(e *Evidence) {
			e.PromptIdleAt = at
		})
	}
}

func (e *Entry) RecordStopFailure(errorType string, message string) {
	if e == nil {
		return
	}
	at := time.Now()
	detail := strings.TrimSpace(errorType)
	if message = strings.TrimSpace(message); message != "" {
		detail = detail + ": " + message
	}
	e.RecordEvidence(at, func(e *Evidence) {
		e.LastHarnessEvent = &sessionstate.Observation{
			Source:     sessionstate.SourceHarnessEvent,
			Claim:      sessionstate.ClaimStopFailed,
			Detail:     detail,
			ObservedAt: at,
		}
	})
}

func (e *Entry) RecordCompaction(active bool) {
	if e == nil {
		return
	}
	e.RecordEvidence(time.Now(), func(e *Evidence) {
		e.Compacting = active
	})
}

func (e *Entry) RecordProcessExit() {
	if e == nil {
		return
	}
	at := time.Now()
	e.RecordEvidence(at, func(e *Evidence) {
		e.Process = &sessionstate.Observation{
			Source:     sessionstate.SourceProcess,
			Claim:      sessionstate.ClaimExited,
			ObservedAt: at,
		}
	})
}

func (e *Entry) recordClassifierStarted(at time.Time) {
	e.RecordEvidence(at, func(e *Evidence) { e.ClassifyingSince = at })
	if e.current() {
		e.CancelAutoSettle("classification started")
	}
}

func (e *Entry) concludeClassification(verdict func(*Evidence)) {
	if e.current() {
		e.SyncAutoSettle()
	}
	e.RecordEvidence(time.Now(), func(e *Evidence) {
		if verdict != nil {
			verdict(e)
		}
		e.ClassifyingSince = time.Time{}
	})
}

var ResolverOwnedStates = map[protocol.SessionState]bool{
	protocol.SessionStateLaunching:       true,
	protocol.SessionStateWorking:         true,
	protocol.SessionStatePendingApproval: true,
	protocol.SessionStateWaitingInput:    true,
	protocol.SessionStateIdle:            true,
	protocol.SessionStateScheduled:       true,
	protocol.SessionStateUnknown:         true,
}

func (e *Entry) publishResolution(current protocol.SessionState, resolution sessionstate.Resolution, dwell time.Duration, now time.Time) (resolverOwnsState bool) {
	linkOwns := e.linkOwnsState()
	resolverOwnsState = ResolverOwnedStates[current] && !linkOwns
	if resolution.Hold {
		e.traceResolutionSkip(resolution, string(resolution.Reason))
		return resolverOwnsState
	}
	if resolution.Reason == sessionstate.ReasonNoEvidence {
		return resolverOwnsState
	}
	if linkOwns {
		e.traceResolutionSkip(resolution, "link_owns_state")
		return false
	}
	if !ResolverOwnedStates[current] || resolution.State == current {
		e.clearDwell()
		if e.recordStateReason(resolution) && ResolverOwnedStates[current] {
			e.mu.Lock()
			view := e.viewLocked()
			e.mu.Unlock()
			e.publish(view)
		}
		return resolverOwnsState
	}
	if !e.dwellReady(resolution.State, dwell, now) {
		e.traceResolutionSkip(resolution, "dwell")
		return true
	}
	e.recordStateReason(resolution)
	e.ApplyState(Change{
		State:  string(resolution.State),
		Cause:  resolverObservation{},
		Origin: Origin{Source: StateSourceResolver, Detail: resolutionDetail(resolution)},
	})
	return true
}

func (e *Entry) linkOwnsState() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.driver.Run != "" && e.policy.DriverReportsState
}

func resolutionDetail(resolution sessionstate.Resolution) string {
	if resolution.Detail == "" {
		return string(resolution.Reason)
	}
	return string(resolution.Reason) + ": " + resolution.Detail
}

func (e *Entry) traceResolutionSkip(resolution sessionstate.Resolution, reason string) {
	e.Trace(statetrace.Observation{
		Source:  StateSourceResolver,
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
