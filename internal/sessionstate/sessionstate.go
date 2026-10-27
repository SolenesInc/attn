package sessionstate

import (
	"slices"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

type Source string

const (
	SourceHeartbeat     Source = "heartbeat"
	SourceProgramStatus Source = "program_status"
	SourceAppServer     Source = "app_server"
	SourceBracket       Source = "hook_bracket"
	SourceHarnessEvent  Source = "harness_event"
	SourceClassifier    Source = "classifier"
	SourceProcess       Source = "process"
)

type Claim string

const (
	ClaimBusy            Claim = "busy"
	ClaimSettled         Claim = "settled"
	ClaimApprovalPending Claim = "approval_pending"
	ClaimNeedsInput      Claim = "needs_input"
	ClaimIdle            Claim = "idle"
	ClaimParked          Claim = "parked"
	ClaimExited          Claim = "exited"
	ClaimStopFailed      Claim = "stop_failed"
	ClaimTurnAborted     Claim = "turn_aborted"
)

type Observation struct {
	Source     Source
	Claim      Claim
	Detail     string
	ObservedAt time.Time
}

type TerminalEvidence struct {
	Heartbeat      *Observation
	ProgramStatus  *Observation
	Process        *Observation
	ReviewerInLoop bool
	LastBusyAt     time.Time
	LastMovement   time.Time
}

type ConversationEvidence struct {
	LastHarnessEvent  *Observation
	LastClassifier    *Observation
	TurnOpen          bool
	TurnEverOpened    bool
	ToolOpen          bool
	BackgroundWork    bool
	PendingCron       bool
	Compacting        bool
	InitialPromptOwed bool
	PromptIdleAt      time.Time
	ClassifyingSince  time.Time
}

type evidence struct {
	TerminalEvidence
	ConversationEvidence
}

type Policy struct {
	HeartbeatTTL         time.Duration
	HeartbeatSettleAfter time.Duration
	StaleAfter           time.Duration
	StuckAfter           time.Duration
	SettleGrace          time.Duration
	ClassifierTimeout    time.Duration
	GuardianDwell        time.Duration
	ParkedAfter          time.Duration
}

const (
	claudeHeartbeatTTL          = 1500 * time.Millisecond
	codexHeartbeatTTL           = 500 * time.Millisecond
	defaultHeartbeatSettleAfter = 5 * time.Second
	defaultStaleAfter           = 60 * time.Second
	defaultStuckAfter           = 90 * time.Second
	guardianDwell               = 60 * time.Second
	defaultSettleGrace          = 4 * time.Second
	defaultClassifierTimeout    = 30 * time.Second
	defaultParkedAfter          = 30 * time.Minute
	shellHeartbeatTTL           = 2500 * time.Millisecond
)

func PolicyFor(agent string) Policy {
	policy := Policy{
		HeartbeatTTL:         codexHeartbeatTTL,
		HeartbeatSettleAfter: defaultHeartbeatSettleAfter,

		StaleAfter:        defaultStaleAfter,
		StuckAfter:        defaultStuckAfter,
		GuardianDwell:     guardianDwell,
		SettleGrace:       defaultSettleGrace,
		ClassifierTimeout: defaultClassifierTimeout,
		ParkedAfter:       defaultParkedAfter,
	}
	if agent == string(protocol.SessionAgentClaude) {
		policy.HeartbeatTTL = claudeHeartbeatTTL
		policy.GuardianDwell = 0
	}
	if agent == string(protocol.SessionAgentShell) {
		policy.HeartbeatTTL = shellHeartbeatTTL
	}
	return policy
}

type Reason string

const (
	ReasonProcessExited     Reason = "process_exited"
	ReasonHeartbeatBusy     Reason = "heartbeat_busy"
	ReasonProgramWorking    Reason = "program_working"
	ReasonProgramBlocked    Reason = "program_blocked"
	ReasonProgramError      Reason = "program_error"
	ReasonProgramSettled    Reason = "program_settled"
	ReasonApprovalOpen      Reason = "approval_open"
	ReasonQuestionOpen      Reason = "question_open"
	ReasonCronPending       Reason = "cron_pending"
	ReasonBracketOpen       Reason = "bracket_open"
	ReasonPromptIdle        Reason = "prompt_idle"
	ReasonBracketStale      Reason = "bracket_stale"
	ReasonHeartbeatSettled  Reason = "heartbeat_settled"
	ReasonSettleGrace       Reason = "settle_grace"
	ReasonAwaitingVerdict   Reason = "awaiting_verdict"
	ReasonBackgroundWork    Reason = "background_work"
	ReasonBackgroundParked  Reason = "background_parked"
	ReasonParkedExpired     Reason = "parked_expired"
	ReasonCompacting        Reason = "compacting"
	ReasonStopFailed        Reason = "stop_failed"
	ReasonTurnAborted       Reason = "turn_aborted"
	ReasonClassifierVerdict Reason = "classifier_verdict"
	ReasonAtPrompt          Reason = "at_prompt"
	ReasonPromptOwed        Reason = "prompt_owed"
	ReasonStuck             Reason = "stuck"
	ReasonNoEvidence        Reason = "no_evidence"
)

type Resolution struct {
	State  protocol.SessionState
	Reason Reason
	Detail string
	Hold   bool
}

func Resolve(terminal TerminalEvidence, conversation ConversationEvidence, policy Policy, now time.Time) Resolution {
	e := evidence{terminal, conversation}
	if e.Process != nil && e.Process.Claim == ClaimExited {
		return Resolution{State: protocol.SessionStateIdle, Reason: ReasonProcessExited, Detail: e.Process.Detail}
	}

	if e.ProgramStatus != nil {
		if r, ok := harnessEdge(e, policy); ok {
			return r
		}
		if r, ok := programStatusEdge(*e.ProgramStatus); ok {
			return r
		}
	} else if fresh(e.Heartbeat, ClaimBusy, now, policy.HeartbeatTTL) {
		return running(e)
	}

	if r, ok := harnessEdge(e, policy); ok {
		return r
	}

	if turnAborted(e) {
		return Resolution{
			State:  protocol.SessionStateIdle,
			Reason: ReasonTurnAborted,
			Detail: e.LastHarnessEvent.Detail,
		}
	}

	if e.Compacting {
		if evidenceStoppedMoving(e, now, policy.StuckAfter) {
			return Resolution{State: protocol.SessionStateUnknown, Reason: ReasonStuck}
		}
		return Resolution{State: protocol.SessionStateWorking, Reason: ReasonCompacting}
	}

	if e.BackgroundWork {
		if r, ok := classifierVerdict(e); ok {
			return r
		}
		if parkedVerdict(e) {
			if now.Sub(e.LastClassifier.ObservedAt) <= policy.ParkedAfter {
				return Resolution{State: protocol.SessionStateWorking, Reason: ReasonBackgroundParked}
			}
			if promptIdleConfirmed(e) {
				return settled(e, ReasonParkedExpired, policy, now)
			}
		}
		if ClassifierVerdictPending(e.ConversationEvidence, policy, now) {
			return Resolution{State: protocol.SessionStateWorking, Reason: ReasonBackgroundWork}
		}
		if promptIdleConfirmed(e) {
			return settled(e, ReasonPromptIdle, policy, now)
		}
		if evidenceStoppedMoving(e, now, policy.StuckAfter) {
			return Resolution{State: protocol.SessionStateUnknown, Reason: ReasonStuck}
		}
		return Resolution{State: protocol.SessionStateWorking, Reason: ReasonBackgroundWork}
	}

	if promptIdleConfirmed(e) {
		return settled(e, ReasonPromptIdle, policy, now)
	}

	if e.TurnOpen || e.ToolOpen {
		if evidenceStoppedMoving(e, now, policy.StuckAfter) {
			return Resolution{State: protocol.SessionStateUnknown, Reason: ReasonStuck}
		}
		if !heartbeatSilentFor(e, now, policy.StaleAfter) {
			return running(e)
		}
		if r, ok := classifierVerdict(e); ok {
			return r
		}
		if !heartbeatSilentFor(e, now, policy.StaleAfter+policy.SettleGrace) {
			return Resolution{Hold: true, Reason: ReasonSettleGrace}
		}
		return settled(e, ReasonBracketStale, policy, now)
	}

	signal := liveSignal(e)
	if signal != nil && TookATurn(e.ConversationEvidence) && !e.TurnOpen && !e.ToolOpen {
		if signal.Claim == ClaimBusy && !heartbeatSilentFor(e, now, policy.HeartbeatSettleAfter) {
			return running(e)
		}
		if e.ProgramStatus != nil {
			return settled(e, ReasonProgramSettled, policy, now)
		}
		return settled(e, ReasonHeartbeatSettled, policy, now)
	}

	if e.PendingCron && !e.TurnOpen && !e.ToolOpen {
		return settled(e, ReasonCronPending, policy, now)
	}

	if signal != nil && signal.Claim == ClaimSettled && !TookATurn(e.ConversationEvidence) {
		if e.InitialPromptOwed {
			return Resolution{Hold: true, Reason: ReasonPromptOwed}
		}
		return Resolution{State: protocol.SessionStateIdle, Reason: ReasonAtPrompt}
	}

	if r, ok := classifierVerdict(e); ok {
		return r
	}

	if e.TurnEverOpened && evidenceStoppedMoving(e, now, policy.StuckAfter) {
		return Resolution{State: protocol.SessionStateUnknown, Reason: ReasonStuck}
	}

	return Resolution{State: protocol.SessionStateUnknown, Reason: ReasonNoEvidence}
}

func NextChange(terminal TerminalEvidence, conversation ConversationEvidence, policy Policy, now time.Time) (time.Time, bool) {
	e := evidence{terminal, conversation}
	current := Resolve(e.TerminalEvidence, e.ConversationEvidence, policy, now)
	for _, at := range expiryInstants(e, policy, now) {
		if Resolve(e.TerminalEvidence, e.ConversationEvidence, policy, at) != current {
			return at, true
		}
	}
	return time.Time{}, false
}

func expiryInstants(e evidence, policy Policy, now time.Time) []time.Time {
	var instants []time.Time
	expires := func(base time.Time, lifetime time.Duration) {
		if base.IsZero() {
			return
		}
		if at := base.Add(lifetime + time.Nanosecond); at.After(now) {
			instants = append(instants, at)
		}
	}
	if e.Heartbeat != nil {
		expires(e.Heartbeat.ObservedAt, policy.HeartbeatTTL)
	}
	lastActive := lastActiveAt(e)
	expires(lastActive, policy.HeartbeatSettleAfter)
	expires(lastActive, policy.StaleAfter)
	expires(lastActive, policy.StaleAfter+policy.SettleGrace)
	expires(e.LastMovement, policy.StuckAfter)
	expires(e.ClassifyingSince, policy.ClassifierTimeout)
	if e.LastClassifier != nil {
		expires(e.LastClassifier.ObservedAt, policy.ParkedAfter)
	}
	slices.SortFunc(instants, time.Time.Compare)
	return instants
}

func running(e evidence) Resolution {
	if e.TurnOpen || e.ToolOpen {
		return Resolution{State: protocol.SessionStateWorking, Reason: ReasonBracketOpen}
	}
	detail := ""
	if signal := liveSignal(e); signal != nil {
		detail = signal.Detail
	}
	return Resolution{State: protocol.SessionStateWorking, Reason: ReasonHeartbeatBusy, Detail: detail}
}

func liveSignal(e evidence) *Observation {
	if e.ProgramStatus != nil {
		return e.ProgramStatus
	}
	return e.Heartbeat
}

func programStatusEdge(o Observation) (Resolution, bool) {
	switch o.Claim {
	case ClaimBusy:
		return Resolution{State: protocol.SessionStateWorking, Reason: ReasonProgramWorking, Detail: o.Detail}, true
	case ClaimApprovalPending:
		return Resolution{State: protocol.SessionStatePendingApproval, Reason: ReasonProgramBlocked, Detail: o.Detail}, true
	case ClaimNeedsInput:
		return Resolution{State: protocol.SessionStatePendingApproval, Reason: ReasonProgramBlocked, Detail: o.Detail}, true
	case ClaimStopFailed:
		return Resolution{State: protocol.SessionStateWaitingInput, Reason: ReasonProgramError, Detail: o.Detail}, true
	default:
		return Resolution{}, false
	}
}

func settled(e evidence, fallback Reason, policy Policy, now time.Time) Resolution {
	if r, ok := classifierVerdict(e); ok {
		return r
	}
	if ClassifierVerdictPending(e.ConversationEvidence, policy, now) {
		return Resolution{Hold: true, Reason: ReasonAwaitingVerdict}
	}
	return Resolution{State: protocol.SessionStateIdle, Reason: fallback}
}

func ClassifierVerdictPending(e ConversationEvidence, policy Policy, now time.Time) bool {
	if e.ClassifyingSince.IsZero() {
		return false
	}
	return now.Sub(e.ClassifyingSince) <= policy.ClassifierTimeout
}

func harnessEdge(e evidence, policy Policy) (Resolution, bool) {
	if e.LastHarnessEvent == nil || supersededByBusy(e.LastHarnessEvent, e) || supersededByProgramStatus(e.LastHarnessEvent, e) {
		return Resolution{}, false
	}
	switch e.LastHarnessEvent.Claim {
	case ClaimApprovalPending:
		if reviewedApprovalsShowOnTheTerminal(e, policy) {
			return Resolution{}, false
		}
		return Resolution{
			State:  protocol.SessionStatePendingApproval,
			Reason: ReasonApprovalOpen,
			Detail: e.LastHarnessEvent.Detail,
		}, true
	case ClaimNeedsInput:
		return Resolution{
			State:  protocol.SessionStateWaitingInput,
			Reason: ReasonQuestionOpen,
			Detail: e.LastHarnessEvent.Detail,
		}, true
	case ClaimStopFailed:
		return Resolution{
			State:  protocol.SessionStateWaitingInput,
			Reason: ReasonStopFailed,
			Detail: e.LastHarnessEvent.Detail,
		}, true
	default:
		return Resolution{}, false
	}
}

func reviewedApprovalsShowOnTheTerminal(e evidence, policy Policy) bool {
	reviewerAnswers := e.ReviewerInLoop && policy.GuardianDwell > 0
	terminalReports := e.ProgramStatus != nil && e.ProgramStatus.Source == SourceProgramStatus
	return reviewerAnswers && terminalReports
}

func turnAborted(e evidence) bool {
	return e.LastHarnessEvent != nil &&
		e.LastHarnessEvent.Claim == ClaimTurnAborted &&
		!supersededByBusy(e.LastHarnessEvent, e)
}

func parkedVerdict(e evidence) bool {
	return e.LastClassifier != nil &&
		e.LastClassifier.Claim == ClaimParked &&
		!supersededByBusy(e.LastClassifier, e)
}

func classifierVerdict(e evidence) (Resolution, bool) {
	if e.LastClassifier == nil {
		return Resolution{}, false
	}
	if supersededByBusy(e.LastClassifier, e) {
		return Resolution{}, false
	}
	switch e.LastClassifier.Claim {
	case ClaimNeedsInput:
		return Resolution{
			State:  protocol.SessionStateWaitingInput,
			Reason: ReasonClassifierVerdict,
			Detail: e.LastClassifier.Detail,
		}, true
	case ClaimIdle:
		return Resolution{
			State:  protocol.SessionStateIdle,
			Reason: ReasonClassifierVerdict,
			Detail: e.LastClassifier.Detail,
		}, true
	default:
		return Resolution{}, false
	}
}

func DwellFor(state protocol.SessionState, terminal TerminalEvidence, conversation ConversationEvidence, policy Policy) time.Duration {
	e := evidence{terminal, conversation}
	if state == protocol.SessionStatePendingApproval && e.ReviewerInLoop && !waitsOnUser(e) {
		return policy.GuardianDwell
	}
	return 0
}

func waitsOnUser(e evidence) bool {
	status := e.ProgramStatus
	if status == nil {
		return false
	}
	terminalShowsApproval := status.Source == SourceProgramStatus && status.Claim == ClaimApprovalPending
	return status.Claim == ClaimNeedsInput || terminalShowsApproval
}

func supersededByBusy(o *Observation, e evidence) bool {
	if o == nil || e.LastBusyAt.IsZero() {
		return false
	}
	return e.LastBusyAt.After(o.ObservedAt)
}

func ClearProgramStatus(terminal *TerminalEvidence, conversation *ConversationEvidence) {
	if conversation.LastHarnessEvent != nil && supersededByProgramStatus(conversation.LastHarnessEvent, evidence{*terminal, *conversation}) {
		conversation.LastHarnessEvent = nil
	}
	terminal.ProgramStatus = nil
}

func supersededByProgramStatus(o *Observation, e evidence) bool {
	return e.ProgramStatus != nil && !o.ObservedAt.After(e.ProgramStatus.ObservedAt)
}

func fresh(o *Observation, claim Claim, now time.Time, ttl time.Duration) bool {
	return o != nil && o.Claim == claim && now.Sub(o.ObservedAt) <= ttl
}

func TookATurn(e ConversationEvidence) bool {
	if e.TurnOpen || e.ToolOpen || e.TurnEverOpened {
		return true
	}
	return e.LastClassifier != nil || !e.ClassifyingSince.IsZero()
}

func evidenceStoppedMoving(e evidence, now time.Time, d time.Duration) bool {
	if e.LastMovement.IsZero() {
		return false
	}
	return now.Sub(e.LastMovement) > d
}

func promptIdleConfirmed(e evidence) bool {
	return !e.PromptIdleAt.IsZero() && e.PromptIdleAt.After(e.LastBusyAt)
}

func heartbeatSilentFor(e evidence, now time.Time, d time.Duration) bool {
	lastActive := lastActiveAt(e)
	if lastActive.IsZero() {
		return false
	}
	return now.Sub(lastActive) > d
}

func lastActiveAt(e evidence) time.Time {
	if e.LastBusyAt.IsZero() || e.ProgramStatus == nil || e.ProgramStatus.ObservedAt.Before(e.LastBusyAt) {
		return e.LastBusyAt
	}
	return e.ProgramStatus.ObservedAt
}
