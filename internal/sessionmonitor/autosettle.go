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

func (m *Monitor) SyncAutoSettle() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.syncAutoSettleLocked()
	view := m.viewLocked()
	m.mu.Unlock()
	m.publish(view)
}

func (m *Monitor) syncAutoSettleLocked() {
	state := m.state.State
	m.input.observePhaseLocked(state)
	if state != protocol.SessionStateWorking {
		if m.dismissal == autoSettleDismissalCovered {
			m.dismissal = autoSettleDismissalOff
		}
		m.stopAutoSettleLocked()
		return
	}
	if m.input.inNotificationRunLocked() && m.turnOwedLocked() && !m.turnByAgentLocked() && m.policy.QuietNotifications {
		m.settleForAgentLocked(time.Now())
		m.logf("turn settled by a notification run: session=%s", m.label)
	}
	if m.dismissal != autoSettleDismissalOff {
		m.dismissal = autoSettleDismissalCovered
	}
	m.armAutoSettleLocked()
}

func (m *Monitor) armAutoSettleForUserInput(run runRef) {
	if !run.valid() {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, credited := m.input.currentUserRunLocked()
	if credited && current == run {
		m.armAutoSettleLocked()
	}
}

func (m *Monitor) ArmAutoSettle() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.armAutoSettleLocked()
}

func (m *Monitor) armAutoSettleLocked() {
	cfg := m.policy.AutoSettle
	if !cfg.Enabled || m.state.State != protocol.SessionStateWorking || m.dismissal != autoSettleDismissalOff || !m.owesUserLocked() {
		return
	}
	if _, credited := m.input.currentUserRunLocked(); !credited {
		return
	}
	if m.autoSettle == nil {
		m.startAutoSettleLocked(autoSettleArming, cfg.Arm)
	}
}

func (m *Monitor) owesUserLocked() bool {
	return m.turnOwedLocked() &&
		!attention.Excluded(attention.Input{IsShell: m.agent == protocol.SessionAgentShell, Chief: m.policy.Chief})
}

func (m *Monitor) HoldAutoSettle() {
	if m == nil {
		return
	}
	if !m.mu.TryLock() {
		pausepoint.At(pausepoint.SessionInputLaneContended)
		m.mu.Lock()
	}
	if m.autoSettle == nil || m.autoSettle.phase == autoSettleHeld {
		m.mu.Unlock()
		return
	}
	resume := m.autoSettle.phase
	m.startAutoSettleHeldLocked(resume, autoSettleHoldQuietWindow)
	view := m.viewLocked()
	label := m.label
	m.mu.Unlock()
	m.publish(view)
	if m.registry.host.Debug() {
		m.logf("auto-settle held: session=%s resume_phase=%d", label, resume)
	}
}

func (m *Monitor) startAutoSettleHeldLocked(resume autoSettlePhase, window time.Duration) {
	m.startAutoSettleLocked(autoSettleHeld, window)
	m.autoSettle.resume = resume
}

func (m *Monitor) startAutoSettleLocked(phase autoSettlePhase, window time.Duration) {
	m.stopAutoSettleLocked()
	if window < 0 {
		window = 0
	}
	firesAt := time.Now().Add(window)
	run, _ := m.input.currentUserRunLocked()
	timer := &autoSettleTimer{phase: phase, firesAt: firesAt, run: run, opened: m.turn.OpenedAt}
	timer.timer = m.registry.host.AfterFunc("autoSettle", window, func() { m.autoSettleFire(timer) })
	m.autoSettle = timer
}

func (m *Monitor) stopAutoSettleLocked() (removed, wasVisible bool) {
	timer := m.autoSettle
	if timer == nil {
		return false, false
	}
	timer.timer.Stop()
	m.autoSettle = nil
	return true, timer.visible()
}

func (m *Monitor) CancelAutoSettle(reason string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	removed, _ := m.stopAutoSettleLocked()
	var view View
	if removed {
		view = m.viewLocked()
	}
	label := m.label
	m.mu.Unlock()
	if !removed {
		return
	}
	m.publish(view)
	if m.registry.host.Debug() {
		m.logf("auto-settle canceled: session=%s reason=%s", label, reason)
	}
}

func (m *Monitor) StopAutoSettle() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.stopAutoSettleLocked()
	m.mu.Unlock()
}

func (m *Monitor) ClearAutoSettle() {
	if m == nil {
		return
	}
	m.mu.Lock()
	_, wasVisible := m.stopAutoSettleLocked()
	changed := wasVisible || m.dismissal != autoSettleDismissalOff
	m.dismissal = autoSettleDismissalOff
	var view View
	if changed {
		view = m.viewLocked()
	}
	m.mu.Unlock()
	if changed {
		m.publish(view)
	}
}

func (m *Monitor) autoSettleFire(self *autoSettleTimer) {
	m.mu.Lock()
	timer := m.autoSettle
	if timer == nil || timer != self {
		m.mu.Unlock()
		return
	}
	m.autoSettle = nil
	action := m.runAutoSettleLocked(timer.phase, timer.resume, timer.run, timer.opened)
	view := m.viewLocked()
	label := m.label
	m.mu.Unlock()
	m.publish(view)
	if action == "settled" {
		m.TraceSettle()
	}
	if m.registry.host.Debug() {
		m.logf("auto-settle fire: session=%s phase=%d outcome=%s", label, timer.phase, action)
	}
}

func (m *Monitor) TraceSettle() {
	if m == nil {
		return
	}
	m.mu.Lock()
	state := m.state.State
	m.mu.Unlock()
	m.Trace(statetrace.Observation{Source: "user", Claim: string(state), Cause: "settle", Outcome: statetrace.OutcomeApplied})
}

func (m *Monitor) runAutoSettleLocked(phase, resume autoSettlePhase, armedRun runRef, armedTurn time.Time) string {
	cfg := m.policy.AutoSettle
	if !cfg.Enabled {
		return "disabled"
	}
	if sessionstate.ClassifierVerdictPending(m.evidence, sessionstate.PolicyFor(m.agent), time.Now()) {
		return "classifying"
	}
	if m.state.State != protocol.SessionStateWorking {
		return "not-working"
	}
	current, credited := m.input.currentUserRunLocked()
	if !credited || current != armedRun {
		return "wrong-run"
	}
	if opened := m.turn.OpenedAt; opened.IsZero() || !opened.Equal(armedTurn) {
		return "wrong-turn"
	}
	if !m.owesUserLocked() {
		return "not-owed"
	}
	if phase == autoSettleHeld || phase == autoSettleArming {
		hold := phase
		if phase == autoSettleHeld {
			hold = resume
		}
		if quiet := quietRemaining(m.activityAt, autoSettleHoldQuietWindow); quiet > 0 {
			m.startAutoSettleHeldLocked(hold, quiet)
			return "held"
		}
		if phase == autoSettleHeld {
			window := cfg.Arm
			if resume == autoSettleCounting {
				window = cfg.Countdown
			}
			m.startAutoSettleLocked(resume, window)
			return "resumed"
		}
		m.startAutoSettleLocked(autoSettleCounting, cfg.Countdown)
		return "counting"
	}
	if quiet := quietRemaining(m.activityAt, autoSettleHoldQuietWindow); quiet > 0 {
		m.startAutoSettleHeldLocked(autoSettleCounting, quiet)
		return "held"
	}
	if !m.settleLocked(armedTurn, time.Now()) {
		return "settle-failed"
	}
	return "settled"
}

func (m *Monitor) AnswerCountdown() (answered, hadPending, rearmed bool) {
	if m == nil {
		return false, false, false
	}
	m.mu.Lock()
	hadPending = m.autoSettle != nil
	disarm := m.dismissal != autoSettleDismissalOff && !hadPending
	working := m.state.State == protocol.SessionStateWorking
	key := m.keyLocked()
	m.mu.Unlock()
	view, applied := m.Do(Command{Key: key, Kind: CancelCountdown}, time.Now())
	if !applied || !m.publish(view) {
		return false, hadPending, false
	}
	if disarm && working {
		m.ArmAutoSettle()
	}
	return true, hadPending, !disarm
}
