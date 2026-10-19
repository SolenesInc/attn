package sessionmonitor

import (
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/statetrace"
)

const autoSettleHoldQuietWindow = 5 * time.Second

type autoSettlePhase int

const (
	autoSettleArming autoSettlePhase = iota
	autoSettleCounting
	autoSettleHeld
)

type autoSettleTimer struct {
	timer   *time.Timer
	phase   autoSettlePhase
	resume  autoSettlePhase
	firesAt time.Time
	run     runRef
	opened  time.Time
}

func (e *autoSettleTimer) visible() bool {
	switch e.phase {
	case autoSettleCounting:
		return true
	case autoSettleHeld:
		return e.resume == autoSettleCounting
	}
	return false
}

type AutoSettleSettings struct {
	Enabled   bool
	Arm       time.Duration
	Countdown time.Duration
}

type autoSettleDismissal uint8

const (
	autoSettleDismissalOff autoSettleDismissal = iota
	autoSettleDismissalArmed
	autoSettleDismissalCovered
)

func (e *Entry) SyncAutoSettle() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.syncAutoSettleLocked()
	view := e.viewLocked()
	e.mu.Unlock()
	e.home.Applied(view)
}

func (e *Entry) syncAutoSettleLocked() {
	state := e.state.State
	e.input.observePhaseLocked(state)
	if state != protocol.SessionStateWorking {
		if e.dismissal == autoSettleDismissalCovered {
			e.dismissal = autoSettleDismissalOff
		}
		e.stopAutoSettleLocked()
		return
	}
	if e.input.inNotificationRunLocked() && e.turnOwedLocked() && !e.turnByAgentLocked() && e.policy.QuietNotifications {
		e.settleForAgentLocked(time.Now())
		e.logf("turn settled by a notification run: session=%s", e.label)
	}
	if e.dismissal != autoSettleDismissalOff {
		e.dismissal = autoSettleDismissalCovered
	}
	e.armAutoSettleLocked()
}

func (e *Entry) armAutoSettleForUserInput(run runRef) {
	if !run.valid() {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	current, credited := e.input.currentUserRunLocked()
	if credited && current == run {
		e.armAutoSettleLocked()
	}
}

func (e *Entry) ArmAutoSettle() {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.armAutoSettleLocked()
}

func (e *Entry) armAutoSettleLocked() {
	cfg := e.policy.AutoSettle
	if !cfg.Enabled || e.state.State != protocol.SessionStateWorking || e.dismissal != autoSettleDismissalOff || !e.owesUserLocked() {
		return
	}
	if _, credited := e.input.currentUserRunLocked(); !credited {
		return
	}
	if e.autoSettle == nil {
		e.startAutoSettleLocked(autoSettleArming, cfg.Arm)
	}
}

func (e *Entry) owesUserLocked() bool {
	return e.turnOwedLocked() &&
		!attention.Excluded(attention.Input{IsShell: e.agent == protocol.SessionAgentShell, ChiefOfStaff: e.policy.ChiefOfStaff})
}

func (e *Entry) HoldAutoSettle() {
	if e == nil {
		return
	}
	if !e.mu.TryLock() {
		pausepoint.At(pausepoint.SessionInputLaneContended)
		e.mu.Lock()
	}
	if e.autoSettle == nil || e.autoSettle.phase == autoSettleHeld {
		e.mu.Unlock()
		return
	}
	resume := e.autoSettle.phase
	e.startAutoSettleHeldLocked(resume, autoSettleHoldQuietWindow)
	view := e.viewLocked()
	label := e.label
	e.mu.Unlock()
	e.home.Applied(view)
	if e.registry.host.Debug() {
		e.logf("auto-settle held: session=%s resume_phase=%d", label, resume)
	}
}

func (e *Entry) startAutoSettleHeldLocked(resume autoSettlePhase, window time.Duration) {
	e.startAutoSettleLocked(autoSettleHeld, window)
	e.autoSettle.resume = resume
}

func (e *Entry) startAutoSettleLocked(phase autoSettlePhase, window time.Duration) {
	e.stopAutoSettleLocked()
	if window < 0 {
		window = 0
	}
	firesAt := time.Now().Add(window)
	run, _ := e.input.currentUserRunLocked()
	timer := &autoSettleTimer{phase: phase, firesAt: firesAt, run: run, opened: e.turn.OpenedAt}
	timer.timer = e.registry.host.AfterFunc("autoSettle", window, func() { e.autoSettleFire(timer) })
	e.autoSettle = timer
}

func (e *Entry) stopAutoSettleLocked() (removed, wasVisible bool) {
	timer := e.autoSettle
	if timer == nil {
		return false, false
	}
	timer.timer.Stop()
	e.autoSettle = nil
	return true, timer.visible()
}

func (e *Entry) CancelAutoSettle(reason string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	removed, _ := e.stopAutoSettleLocked()
	var view View
	if removed {
		view = e.viewLocked()
	}
	label := e.label
	e.mu.Unlock()
	if !removed {
		return
	}
	e.home.Applied(view)
	if e.registry.host.Debug() {
		e.logf("auto-settle canceled: session=%s reason=%s", label, reason)
	}
}

func (e *Entry) StopAutoSettle() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.stopAutoSettleLocked()
	e.mu.Unlock()
}

func (e *Entry) ClearAutoSettle() {
	if e == nil {
		return
	}
	e.mu.Lock()
	_, wasVisible := e.stopAutoSettleLocked()
	changed := wasVisible || e.dismissal != autoSettleDismissalOff
	e.dismissal = autoSettleDismissalOff
	var view View
	if changed {
		view = e.viewLocked()
	}
	e.mu.Unlock()
	if changed {
		e.home.Applied(view)
	}
}

func (e *Entry) autoSettleFire(self *autoSettleTimer) {
	e.mu.Lock()
	timer := e.autoSettle
	if timer == nil || timer != self {
		e.mu.Unlock()
		return
	}
	e.autoSettle = nil
	action := e.runAutoSettleLocked(timer.phase, timer.resume, timer.run, timer.opened)
	view := e.viewLocked()
	label := e.label
	e.mu.Unlock()
	e.home.Applied(view)
	if action == "settled" {
		e.TraceSettle()
	}
	if e.registry.host.Debug() {
		e.logf("auto-settle fire: session=%s phase=%d outcome=%s", label, timer.phase, action)
	}
}

func (e *Entry) TraceSettle() {
	if e == nil {
		return
	}
	e.mu.Lock()
	state := e.state.State
	e.mu.Unlock()
	e.Trace(statetrace.Observation{Source: "user", Claim: string(state), Cause: "settle", Outcome: statetrace.OutcomeApplied})
}

func (e *Entry) runAutoSettleLocked(phase, resume autoSettlePhase, armedRun runRef, armedTurn time.Time) string {
	cfg := e.policy.AutoSettle
	if !cfg.Enabled {
		return "disabled"
	}
	if sessionstate.ClassifierVerdictPending(e.evidence, sessionstate.PolicyFor(e.agent), time.Now()) {
		return "classifying"
	}
	if e.state.State != protocol.SessionStateWorking {
		return "not-working"
	}
	current, credited := e.input.currentUserRunLocked()
	if !credited || current != armedRun {
		return "wrong-run"
	}
	if opened := e.turn.OpenedAt; opened.IsZero() || !opened.Equal(armedTurn) {
		return "wrong-turn"
	}
	if !e.owesUserLocked() {
		return "not-owed"
	}
	if phase == autoSettleHeld || phase == autoSettleArming {
		hold := phase
		if phase == autoSettleHeld {
			hold = resume
		}
		if quiet := quietRemaining(e.activityAt, autoSettleHoldQuietWindow); quiet > 0 {
			e.startAutoSettleHeldLocked(hold, quiet)
			return "held"
		}
		if phase == autoSettleHeld {
			window := cfg.Arm
			if resume == autoSettleCounting {
				window = cfg.Countdown
			}
			e.startAutoSettleLocked(resume, window)
			return "resumed"
		}
		e.startAutoSettleLocked(autoSettleCounting, cfg.Countdown)
		return "counting"
	}
	if quiet := quietRemaining(e.activityAt, autoSettleHoldQuietWindow); quiet > 0 {
		e.startAutoSettleHeldLocked(autoSettleCounting, quiet)
		return "held"
	}
	if !e.settleLocked(armedTurn, time.Now()) {
		return "settle-failed"
	}
	return "settled"
}

func (e *Entry) AnswerCountdown() (answered, hadPending, rearmed bool) {
	if e == nil {
		return false, false, false
	}
	e.mu.Lock()
	hadPending = e.autoSettle != nil
	disarm := e.dismissal != autoSettleDismissalOff && !hadPending
	working := e.state.State == protocol.SessionStateWorking
	key := e.keyLocked()
	e.mu.Unlock()
	view, applied := e.Do(Command{Key: key, Kind: CancelCountdown}, time.Now())
	if !applied || !e.home.Applied(view) {
		return false, hadPending, false
	}
	if disarm && working {
		e.ArmAutoSettle()
	}
	return true, hadPending, !disarm
}
