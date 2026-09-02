package sessionstate

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

type Source string

const (
	SourceHeartbeat    Source = "heartbeat"
	SourceBracket      Source = "hook_bracket"
	SourceHarnessEvent Source = "harness_event"
	SourceClassifier   Source = "classifier"
	SourceProcess      Source = "process"
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

type Evidence struct {
	Heartbeat        *Observation
	LastHarnessEvent *Observation
	LastClassifier   *Observation
	Process          *Observation

	TurnOpen bool
	// codex paints busy title frames before its prompt is ready, so a busy frame
	// alone does not mean a turn happened.
	TurnEverOpened bool
	ToolOpen       bool
	BackgroundWork bool
	PendingCron    bool
	// Compaction paints no frames and opens no turn, so nothing else here sees
	// it. Measured at 26s.
	Compacting bool
	// ReviewerInLoop changes only how long an approval holds before it is shown,
	// never whether the state is published.
	ReviewerInLoop bool

	// claude blips its not-busy glyph mid-turn; reading that as a settle flips a healthy
	// open turn to idle, so staleness is measured from here, not the latest heartbeat.
	LastBusyAt time.Time

	PromptIdleAt time.Time

	ClassifyingSince time.Time

	LastMovement time.Time
}

type Policy struct {
	// A precedence window, not a liveness one: too long and a busy frame
	// suppresses the approval/question edges announced when painting stops.
	HeartbeatTTL         time.Duration
	HeartbeatSettleAfter time.Duration
	StaleAfter           time.Duration
	StuckAfter           time.Duration
	SettleGrace          time.Duration
	ClassifierTimeout    time.Duration
	GuardianDwell        time.Duration
	ParkedAfter          time.Duration
}

// Measured on claude 2.1.220 and codex 0.145.0 through a real PTY: claude repaints ~1/s
// and goes silent up to ~3.5s in a blocking tool call; codex repaints ~10/s.
const (
	claudeHeartbeatTTL = 1500 * time.Millisecond
	codexHeartbeatTTL  = 500 * time.Millisecond
	// Measured: claude repaints every ~1.92s during a `/compact`, past the 1.5s
	// TTL; 5s clears that with margin for PTY read batching.
	defaultHeartbeatSettleAfter = 5 * time.Second
	// Far past any measured mid-turn silence (claude's worst ~3.5s).
	defaultStaleAfter = 60 * time.Second
	defaultStuckAfter = 90 * time.Second
	// Measured 90ms for claude's permission classifier, low seconds for codex's
	// auto_review.
	guardianDwell            = 60 * time.Second
	defaultSettleGrace       = 4 * time.Second
	defaultClassifierTimeout = 30 * time.Second
	// Tripwire. Measured in one day of production: 21 parked waits that resumed,
	// the longest 3.7 minutes; the three that did not resume were held for days.
	defaultParkedAfter = 30 * time.Minute
	// A shell pane's heartbeat is the foreground process group on the 1s
	// keepalive; 2.5s covers one missed poll plus worker RPC latency.
	shellHeartbeatTTL = 2500 * time.Millisecond
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
	ReasonStuck             Reason = "stuck"
	ReasonNoEvidence        Reason = "no_evidence"
)

type Resolution struct {
	State  protocol.SessionState
	Reason Reason
	Detail string
	// Hold means "keep whatever the session already shows"; State is empty.
	Hold bool
}

// Clauses are ordered and the first match wins: a fresh heartbeat outranks an
// open approval because an agent visibly running cannot be blocked on the user.
func Resolve(e Evidence, policy Policy, now time.Time) Resolution {
	if e.Process != nil && e.Process.Claim == ClaimExited {
		return Resolution{State: protocol.SessionStateIdle, Reason: ReasonProcessExited, Detail: e.Process.Detail}
	}

	if fresh(e.Heartbeat, ClaimBusy, now, policy.HeartbeatTTL) {
		return running(e)
	}

	// Nothing announces the answer to these edges: they retire only by the agent
	// going busy past them.
	if r, ok := harnessEdge(e); ok {
		return r
	}

	if turnAborted(e) {
		return Resolution{
			State:  protocol.SessionStateIdle,
			Reason: ReasonTurnAborted,
			Detail: e.LastHarnessEvent.Detail,
		}
	}

	// Expires on total silence: a lost PostCompact must not pin the session green
	// for good.
	if e.Compacting {
		if evidenceStoppedMoving(e, now, policy.StuckAfter) {
			return Resolution{State: protocol.SessionStateUnknown, Reason: ReasonStuck}
		}
		return Resolution{State: protocol.SessionStateWorking, Reason: ReasonCompacting}
	}

	// A parked verdict holds working WITHOUT decaying to unknown, which would
	// open a turn. Past ParkedAfter the work it waited on never woke the agent.
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
		if ClassifierVerdictPending(e, policy, now) {
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
		// For an agent with hooks but no heartbeat, heartbeatSilentFor answers
		// "not silent" forever; without this check stuck is unreachable.
		if evidenceStoppedMoving(e, now, policy.StuckAfter) {
			return Resolution{State: protocol.SessionStateUnknown, Reason: ReasonStuck}
		}
		if !heartbeatSilentFor(e, now, policy.StaleAfter) {
			return running(e)
		}
		// A finished turn and an unannounced approval look the same, so hold for
		// SettleGrace rather than assert idle into a late explanation.
		if r, ok := classifierVerdict(e); ok {
			return r
		}
		if !heartbeatSilentFor(e, now, policy.StaleAfter+policy.SettleGrace) {
			return Resolution{Hold: true, Reason: ReasonSettleGrace}
		}
		return settled(e, ReasonBracketStale, policy, now)
	}

	if e.Heartbeat != nil && everTookATurn(e) && !e.TurnOpen && !e.ToolOpen {
		// A gap only longer than the TTL is a repaint gap, not a settle; without
		// HeartbeatSettleAfter every wide gap costs one owed turn.
		if e.Heartbeat.Claim == ClaimBusy && !heartbeatSilentFor(e, now, policy.HeartbeatSettleAfter) {
			return running(e)
		}
		return settled(e, ReasonHeartbeatSettled, policy, now)
	}

	// Needs no heartbeat: a session reporting hooks without a title (headless,
	// remote) would otherwise read as never having spoken.
	if e.PendingCron && !e.TurnOpen && !e.ToolOpen {
		return settled(e, ReasonCronPending, policy, now)
	}

	if e.Heartbeat != nil && e.Heartbeat.Claim == ClaimSettled && !everTookATurn(e) {
		return Resolution{State: protocol.SessionStateIdle, Reason: ReasonAtPrompt}
	}

	if r, ok := classifierVerdict(e); ok {
		return r
	}

	// Needs a turn to have opened first: a launched-and-left-alone agent is
	// silent because there is nothing to report.
	if e.TurnEverOpened && evidenceStoppedMoving(e, now, policy.StuckAfter) {
		return Resolution{State: protocol.SessionStateUnknown, Reason: ReasonStuck}
	}

	return Resolution{State: protocol.SessionStateUnknown, Reason: ReasonNoEvidence}
}

func running(e Evidence) Resolution {
	if e.TurnOpen || e.ToolOpen {
		return Resolution{State: protocol.SessionStateWorking, Reason: ReasonBracketOpen}
	}
	detail := ""
	if e.Heartbeat != nil {
		detail = e.Heartbeat.Detail
	}
	return Resolution{State: protocol.SessionStateWorking, Reason: ReasonHeartbeatBusy, Detail: detail}
}

// Holds while a verdict is computed: publishing idle first and correcting on
// arrival flickers green-then-yellow.
func settled(e Evidence, fallback Reason, policy Policy, now time.Time) Resolution {
	if r, ok := classifierVerdict(e); ok {
		return r
	}
	if ClassifierVerdictPending(e, policy, now) {
		return Resolution{Hold: true, Reason: ReasonAwaitingVerdict}
	}
	return Resolution{State: protocol.SessionStateIdle, Reason: fallback}
}

func ClassifierVerdictPending(e Evidence, policy Policy, now time.Time) bool {
	if e.ClassifyingSince.IsZero() {
		return false
	}
	return now.Sub(e.ClassifyingSince) <= policy.ClassifierTimeout
}

func harnessEdge(e Evidence) (Resolution, bool) {
	if e.LastHarnessEvent == nil || supersededByBusy(e.LastHarnessEvent, e) {
		return Resolution{}, false
	}
	switch e.LastHarnessEvent.Claim {
	case ClaimApprovalPending:
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

func turnAborted(e Evidence) bool {
	return e.LastHarnessEvent != nil &&
		e.LastHarnessEvent.Claim == ClaimTurnAborted &&
		!supersededByBusy(e.LastHarnessEvent, e)
}

func parkedVerdict(e Evidence) bool {
	return e.LastClassifier != nil &&
		e.LastClassifier.Claim == ClaimParked &&
		!supersededByBusy(e.LastClassifier, e)
}

// A verdict the agent has gone busy past is dropped, or a turn settling
// mid-classification would take the previous turn's answer.
func classifierVerdict(e Evidence) (Resolution, bool) {
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

func DwellFor(state protocol.SessionState, e Evidence, policy Policy) time.Duration {
	if state == protocol.SessionStatePendingApproval && e.ReviewerInLoop {
		return policy.GuardianDwell
	}
	return 0
}

func supersededByBusy(o *Observation, e Evidence) bool {
	if o == nil || e.LastBusyAt.IsZero() {
		return false
	}
	return e.LastBusyAt.After(o.ObservedAt)
}

func fresh(o *Observation, claim Claim, now time.Time, ttl time.Duration) bool {
	return o != nil && o.Claim == claim && now.Sub(o.ObservedAt) <= ttl
}

// Counts the classifier alongside the brackets: a daemon restarted mid-turn is
// judged with no bracket to show for it.
func everTookATurn(e Evidence) bool {
	if e.TurnOpen || e.ToolOpen || e.TurnEverOpened {
		return true
	}
	return e.LastClassifier != nil || !e.ClassifyingSince.IsZero()
}

func evidenceStoppedMoving(e Evidence, now time.Time, d time.Duration) bool {
	if e.LastMovement.IsZero() {
		return false
	}
	return now.Sub(e.LastMovement) > d
}

func promptIdleConfirmed(e Evidence) bool {
	return !e.PromptIdleAt.IsZero() && e.PromptIdleAt.After(e.LastBusyAt)
}

// An agent that never reported busy is not silent: one with no harness signals
// must not have its brackets closed out from under it.
func heartbeatSilentFor(e Evidence, now time.Time, d time.Duration) bool {
	if e.LastBusyAt.IsZero() {
		return false
	}
	return now.Sub(e.LastBusyAt) > d
}
