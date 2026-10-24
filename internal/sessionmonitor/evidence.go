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

func (m *Monitor) Evidence() (Evidence, bool) {
	if m == nil {
		return Evidence{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.watch.mu.Lock()
	defer m.watch.mu.Unlock()
	return Evidence{m.watch.terminal, m.evidence}, true
}

func movedAt(at time.Time, mutate func(*Evidence)) func(*Evidence) {
	return func(e *Evidence) {
		mutate(e)
		e.LastMovement = at
	}
}

func (m *Monitor) RecordEvidence(at time.Time, mutate func(*Evidence)) bool {
	if m == nil {
		return false
	}
	return m.UpdateEvidence(nil, movedAt(at, mutate))
}

func (m *Monitor) UpdateEvidence(unchanged func(*Evidence) bool, mutate func(*Evidence)) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.watch.mu.Lock()
	defer m.watch.mu.Unlock()
	evidence := Evidence{m.watch.terminal, m.evidence}
	if unchanged != nil && unchanged(&evidence) {
		return false
	}
	mutate(&evidence)
	m.watch.terminal, m.evidence = evidence.TerminalEvidence, evidence.ConversationEvidence
	m.resolveSoonLocked()
	return true
}

func (m *Monitor) RecordPTY(obs pty.Observation) bool {
	if m == nil {
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
	return m.UpdateEvidence(func(e *Evidence) bool {
		return holdsPTYEvidence(e, obs)
	}, movedAt(at, mutate))
}

func (m *Monitor) RecordServerStatus(status string, at time.Time) bool {
	if m == nil {
		return false
	}
	obs := pty.Observation{Source: pty.SourceProgramStatus, Claim: status, At: at}
	return m.UpdateEvidence(func(e *Evidence) bool {
		return holdsProgramStatus(e, obs, sessionstate.SourceAppServer)
	}, movedAt(at, programStatusEvidence(obs, at, sessionstate.SourceAppServer)))
}

func PTYEvidence(obs pty.Observation, at time.Time) (func(*Evidence), bool) {
	mutate, ok := terminalEvidence(obs, at)
	if !ok {
		return nil, false
	}
	return func(e *Evidence) {
		if reportedByServer(e) {
			e.ClearProgramStatus()
		}
		mutate(e)
	}, true
}

func terminalEvidence(obs pty.Observation, at time.Time) (func(*Evidence), bool) {
	if obs.Source == pty.SourceProgramStatus {
		return programStatusEvidence(obs, at, sessionstate.SourceProgramStatus), true
	}
	return heartbeatEvidence(obs, at)
}

func holdsPTYEvidence(e *Evidence, obs pty.Observation) bool {
	if reportedByServer(e) {
		return false
	}
	if obs.Source == pty.SourceProgramStatus {
		return holdsProgramStatus(e, obs, sessionstate.SourceProgramStatus)
	}
	return e.ProgramStatus != nil || holdsSettledHeartbeat(e, obs)
}

func reportedByServer(e *Evidence) bool {
	return e.ProgramStatus != nil && e.ProgramStatus.Source == sessionstate.SourceAppServer
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

func holdsProgramStatus(e *Evidence, obs pty.Observation, source sessionstate.Source) bool {
	claim, reported := ProgramStatusClaim(obs.Claim)
	if !reported {
		return e.ProgramStatus == nil
	}
	return e.ProgramStatus != nil &&
		e.ProgramStatus.Source == source &&
		e.ProgramStatus.Claim == claim &&
		e.ProgramStatus.Detail == obs.Detail
}

func programStatusEvidence(obs pty.Observation, at time.Time, source sessionstate.Source) func(*Evidence) {
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
			Source:     source,
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

func (m *Monitor) HoldsPTY(obs pty.Observation) bool {
	if m == nil {
		return false
	}
	evidence, ok := m.Evidence()
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

func (m *Monitor) Restart(launch Launch, fresh Evidence) {
	if m == nil {
		return
	}
	m.Begin(launch)
	m.UpdateEvidence(nil, func(e *Evidence) { *e = fresh })
}

func (m *Monitor) Begin(launch Launch) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.directory, m.branch, m.startedAt = launch.Directory, launch.Branch, launch.StartedAt
	m.state = StateView{Quiet: true, Sequence: m.state.Sequence + 1, State: launch.State, Since: launch.StateSince, Updated: launch.StateUpdated}
	if !launch.LastModelRequestAt.IsZero() {
		m.lastModelRequestAt = launch.LastModelRequestAt
	}
	if launch.Driver.Run != "" {
		m.driver = launch.Driver
	}
	if !launch.LastSeen.IsZero() {
		m.touchedAt = launch.LastSeen
	}
	if launch.Cost.Initialized {
		m.loadCostLocked(launch.Cost)
	}
	m.firstReportAt = time.Time{}
	m.reason = ""
	m.dwell = dwellPending{}
	view := m.viewLocked()
	m.mu.Unlock()
	m.publish(view)
}

func ReportsTurnStarts(agent string) bool {
	return agentdriver.EffectiveCapabilities(agentdriver.Get(agent)).HasHooks
}

func (m *Monitor) RecordBracket(state string) {
	if m == nil {
		return
	}
	at := time.Now()
	m.RecordEvidence(at, func(e *Evidence) {
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

func (m *Monitor) RecordTurnAborted(detail string, abortedAt, observedAt time.Time) {
	if m == nil {
		return
	}
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	at := abortedAt
	if at.IsZero() {
		at = observedAt
	}
	m.RecordEvidence(observedAt, func(e *Evidence) {
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

func (m *Monitor) RecordTurnEnded(classifies bool) {
	if m == nil {
		return
	}
	at := time.Now()
	m.RecordEvidence(at, func(e *Evidence) {
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

func (m *Monitor) RecordStopFacts(backgroundWork, pendingCron bool) {
	if m == nil {
		return
	}
	m.RecordEvidence(time.Now(), func(e *Evidence) {
		e.BackgroundWork = backgroundWork
		e.PendingCron = pendingCron
	})
}

func (m *Monitor) RecordReviewer(inLoop bool) {
	if m == nil {
		return
	}
	m.RecordEvidence(time.Now(), func(e *Evidence) {
		e.ReviewerInLoop = inLoop
	})
}

const (
	notifyPermissionPrompt = "permission_prompt"
	notifyIdlePrompt       = "idle_prompt"
)

func (m *Monitor) RecordNotification(notificationType string, message string) {
	if m == nil {
		return
	}
	at := time.Now()
	switch strings.TrimSpace(notificationType) {
	case notifyPermissionPrompt:
		m.RecordEvidence(at, func(e *Evidence) {
			e.LastHarnessEvent = &sessionstate.Observation{
				Source:     sessionstate.SourceHarnessEvent,
				Claim:      sessionstate.ClaimApprovalPending,
				Detail:     message,
				ObservedAt: at,
			}
		})
	case notifyIdlePrompt:
		m.RecordEvidence(at, func(e *Evidence) {
			e.PromptIdleAt = at
		})
	}
}

func (m *Monitor) RecordStopFailure(errorType string, message string) {
	if m == nil {
		return
	}
	at := time.Now()
	detail := strings.TrimSpace(errorType)
	if message = strings.TrimSpace(message); message != "" {
		detail = detail + ": " + message
	}
	m.RecordEvidence(at, func(e *Evidence) {
		e.LastHarnessEvent = &sessionstate.Observation{
			Source:     sessionstate.SourceHarnessEvent,
			Claim:      sessionstate.ClaimStopFailed,
			Detail:     detail,
			ObservedAt: at,
		}
	})
}

func (m *Monitor) RecordCompaction(active bool) {
	if m == nil {
		return
	}
	m.RecordEvidence(time.Now(), func(e *Evidence) {
		e.Compacting = active
	})
}

func (m *Monitor) RecordProcessExit() {
	if m == nil {
		return
	}
	at := time.Now()
	m.RecordEvidence(at, func(e *Evidence) {
		e.Process = &sessionstate.Observation{
			Source:     sessionstate.SourceProcess,
			Claim:      sessionstate.ClaimExited,
			ObservedAt: at,
		}
	})
}

func (m *Monitor) recordClassifierStarted(at time.Time) {
	m.RecordEvidence(at, func(e *Evidence) { e.ClassifyingSince = at })
	if m.current() {
		m.CancelAutoSettle("classification started")
	}
}

func (m *Monitor) concludeClassification(verdict func(*Evidence)) {
	if m.current() {
		m.SyncAutoSettle()
	}
	m.RecordEvidence(time.Now(), func(e *Evidence) {
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

func (m *Monitor) publishResolution(current protocol.SessionState, resolution sessionstate.Resolution, dwell time.Duration, now time.Time) (resolverOwnsState bool) {
	linkOwns := m.linkOwnsState()
	resolverOwnsState = ResolverOwnedStates[current] && !linkOwns
	if resolution.Hold {
		m.traceResolutionSkip(resolution, string(resolution.Reason))
		return resolverOwnsState
	}
	if resolution.Reason == sessionstate.ReasonNoEvidence {
		return resolverOwnsState
	}
	if linkOwns {
		m.traceResolutionSkip(resolution, "link_owns_state")
		return false
	}
	if !ResolverOwnedStates[current] || resolution.State == current {
		m.clearDwell()
		if m.recordStateReason(resolution) && ResolverOwnedStates[current] {
			m.mu.Lock()
			view := m.viewLocked()
			m.mu.Unlock()
			m.publish(view)
		}
		return resolverOwnsState
	}
	if !m.dwellReady(resolution.State, dwell, now) {
		m.traceResolutionSkip(resolution, "dwell")
		return true
	}
	m.recordStateReason(resolution)
	m.ApplyState(Change{
		State:  string(resolution.State),
		Cause:  resolverObservation{},
		Origin: Origin{Source: StateSourceResolver, Detail: resolutionDetail(resolution)},
	})
	return true
}

func (m *Monitor) linkOwnsState() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.driver.Run != "" && m.policy.DriverReportsState
}

func resolutionDetail(resolution sessionstate.Resolution) string {
	if resolution.Detail == "" {
		return string(resolution.Reason)
	}
	return string(resolution.Reason) + ": " + resolution.Detail
}

func (m *Monitor) traceResolutionSkip(resolution sessionstate.Resolution, reason string) {
	m.Trace(statetrace.Observation{
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
