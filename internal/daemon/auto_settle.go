package daemon

import (
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

const (
	defaultAutoSettleArmSeconds       = 30
	defaultAutoSettleCountdownSeconds = 15

	autoSettleHoldQuietWindow = 5 * time.Second

	autoSettleArmMinSeconds       = 5
	autoSettleArmMaxSeconds       = 3600
	autoSettleCountdownMinSeconds = 3
	autoSettleCountdownMaxSeconds = 600
)

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
	run     sessionInputRunRef
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

type autoSettleConfig struct {
	enabled   bool
	arm       time.Duration
	countdown time.Duration
}

func (d *Daemon) autoSettleConfig() autoSettleConfig {
	if d.store == nil {
		return autoSettleConfig{}
	}
	return autoSettleConfig{
		enabled:   parseBooleanSetting(d.daemonSetting(SettingAutoSettleEnabled)),
		arm:       resolveAutoSettleSeconds(d.daemonSetting(SettingAutoSettleArmSeconds), defaultAutoSettleArmSeconds),
		countdown: resolveAutoSettleSeconds(d.daemonSetting(SettingAutoSettleCountdownSeconds), defaultAutoSettleCountdownSeconds),
	}
}

func resolveAutoSettleSeconds(stored string, fallbackSeconds int) time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(stored)); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return time.Duration(fallbackSeconds) * time.Second
}

type autoSettleDismissal uint8

const (
	autoSettleDismissalOff autoSettleDismissal = iota
	autoSettleDismissalArmed
	autoSettleDismissalCovered
)

func (d *Daemon) syncAutoSettle(sessionID protocol.SessionID, _ string) {
	e := d.monitorEntry(sessionID)
	if e == nil {
		return
	}
	e.mu.Lock()
	d.syncAutoSettleLocked(sessionID, e)
	view := e.viewLocked()
	e.mu.Unlock()
	e.sink.Applied(view)
}

func (d *Daemon) syncAutoSettleLocked(sessionID protocol.SessionID, e *monitorEntry) {
	state := e.state.State
	e.input.observePhaseLocked(sessionID, state)
	if state != protocol.SessionStateWorking {
		if e.dismissal == autoSettleDismissalCovered {
			e.dismissal = autoSettleDismissalOff
		}
		e.stopAutoSettleLocked()
		return
	}
	if e.input.inNotificationRunLocked() && e.turnOwedLocked() && !e.turnByAgentLocked() && d.quietNotifications() {
		e.settleForAgentLocked(time.Now())
		d.logf("turn settled by a notification run: session=%s", sessionID)
	}
	if e.dismissal != autoSettleDismissalOff {
		e.dismissal = autoSettleDismissalCovered
	}
	d.armAutoSettleLocked(sessionID, e)
}

func (d *Daemon) quietNotifications() bool {
	return parseBooleanSetting(d.daemonSetting(SettingQueueQuietNotifications)) && d.queueFlowOn()
}

func (d *Daemon) queueFlowOn() bool {
	return parseBooleanSetting(d.daemonSetting(SettingQueueModeEnabled))
}

func (d *Daemon) armAutoSettleForUserInput(run sessionInputRunRef) {
	if !run.valid() {
		return
	}
	e := d.monitorEntry(run.sessionID)
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	current, credited := e.input.currentUserRunLocked()
	if credited && current == run {
		d.armAutoSettleLocked(run.sessionID, e)
	}
}

func (d *Daemon) armAutoSettle(sessionID protocol.SessionID) {
	e := d.monitorEntry(sessionID)
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	d.armAutoSettleLocked(sessionID, e)
}

func (d *Daemon) armAutoSettleLocked(sessionID protocol.SessionID, e *monitorEntry) {
	cfg := d.autoSettleConfig()
	if !cfg.enabled || e.state.State != protocol.SessionStateWorking || e.dismissal != autoSettleDismissalOff || !d.monitorTurnOwed(sessionID, e) {
		return
	}
	if _, credited := e.input.currentUserRunLocked(); !credited {
		return
	}
	if e.autoSettle == nil {
		d.startAutoSettleLocked(sessionID, e, autoSettleArming, cfg.arm)
	}
}

func (d *Daemon) monitorTurnOwed(sessionID protocol.SessionID, e *monitorEntry) bool {
	if !e.turn.OpenedAt.After(e.turn.SettledAt) {
		return false
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return false
	}
	d.decorateChiefOfStaff(session, d.profileChiefs())
	return !attention.Excluded(d.attentionInputFor(session))
}

func (d *Daemon) holdAutoSettle(sessionID protocol.SessionID) {
	e := d.monitorEntry(sessionID)
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
	d.startAutoSettleHeldLocked(sessionID, e, resume, autoSettleHoldQuietWindow)
	view := e.viewLocked()
	e.mu.Unlock()
	e.sink.Applied(view)
	if d.debugLogging {
		d.logf("auto-settle held: session=%s resume_phase=%d", sessionID, resume)
	}
}

func (d *Daemon) startAutoSettleHeldLocked(sessionID protocol.SessionID, e *monitorEntry, resume autoSettlePhase, window time.Duration) {
	d.startAutoSettleLocked(sessionID, e, autoSettleHeld, window)
	e.autoSettle.resume = resume
}

func (d *Daemon) startAutoSettleLocked(sessionID protocol.SessionID, e *monitorEntry, phase autoSettlePhase, window time.Duration) {
	e.stopAutoSettleLocked()
	if window < 0 {
		window = 0
	}
	firesAt := time.Now().Add(window)
	run, _ := e.input.currentUserRunLocked()
	opened := e.turn.OpenedAt
	timer := &autoSettleTimer{phase: phase, firesAt: firesAt, run: run, opened: opened}
	timer.timer = d.life.AfterFunc("autoSettle", window, func() { d.autoSettleFire(sessionID, e, timer) })
	e.autoSettle = timer
}

func (e *monitorEntry) stopAutoSettleLocked() (removed, wasVisible bool) {
	timer := e.autoSettle
	if timer == nil {
		return false, false
	}
	timer.timer.Stop()
	e.autoSettle = nil
	return true, timer.visible()
}

func (d *Daemon) cancelAutoSettle(sessionID protocol.SessionID, reason string) {
	e := d.monitorEntry(sessionID)
	if e == nil {
		return
	}
	e.mu.Lock()
	removed, _ := e.stopAutoSettleLocked()
	var view monitorView
	if removed {
		view = e.viewLocked()
	}
	e.mu.Unlock()
	if removed {
		e.sink.Applied(view)
	}
	if removed && d.debugLogging {
		d.logf("auto-settle canceled: session=%s reason=%s", sessionID, reason)
	}
}

func (d *Daemon) stopAutoSettleTimers() {
	for _, e := range d.monitors().each() {
		e.mu.Lock()
		e.stopAutoSettleLocked()
		e.mu.Unlock()
	}
}

func (d *Daemon) autoSettleFire(sessionID protocol.SessionID, e *monitorEntry, self *autoSettleTimer) {
	e.mu.Lock()
	timer := e.autoSettle
	if timer == nil || timer != self {
		e.mu.Unlock()
		return
	}
	e.autoSettle = nil
	action := d.runAutoSettleForLocked(sessionID, e, timer.phase, timer.resume, timer.run, timer.opened)
	view := e.viewLocked()
	e.mu.Unlock()
	e.sink.Applied(view)
	if action == "settled" {
		d.traceSettle(sessionID)
	}
	if d.debugLogging {
		d.logf("auto-settle fire: session=%s phase=%d outcome=%s", sessionID, timer.phase, action)
	}
}

func (d *Daemon) runAutoSettleForLocked(sessionID protocol.SessionID, e *monitorEntry, phase, resume autoSettlePhase, armedRun sessionInputRunRef, armedTurn time.Time) string {
	cfg := d.autoSettleConfig()
	if !cfg.enabled {
		return "disabled"
	}
	if d.store == nil {
		return "noop"
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return "gone"
	}
	if sessionstate.ClassifierVerdictPending(e.evidence, sessionstate.PolicyFor(session.Agent), time.Now()) {
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
	if !d.monitorTurnOwed(sessionID, e) {
		return "not-owed"
	}
	if phase == autoSettleHeld || phase == autoSettleArming {
		hold := phase
		if phase == autoSettleHeld {
			hold = resume
		}
		if quiet := quietRemaining(e.activityAt, autoSettleHoldQuietWindow); quiet > 0 {
			d.startAutoSettleHeldLocked(sessionID, e, hold, quiet)
			return "held"
		}
		if phase == autoSettleHeld {
			window := cfg.arm
			if resume == autoSettleCounting {
				window = cfg.countdown
			}
			d.startAutoSettleLocked(sessionID, e, resume, window)
			return "resumed"
		}
		d.startAutoSettleLocked(sessionID, e, autoSettleCounting, cfg.countdown)
		return "counting"
	}
	if quiet := quietRemaining(e.activityAt, autoSettleHoldQuietWindow); quiet > 0 {
		d.startAutoSettleHeldLocked(sessionID, e, autoSettleCounting, quiet)
		return "held"
	}
	if !e.settleLocked(armedTurn, time.Now()) {
		return "settle-failed"
	}
	return "settled"
}

func (d *Daemon) answerAutoSettleByUser(sessionID protocol.SessionID) bool {
	session := d.decoratedSession(sessionID)
	if !d.autoSettleAppliesTo(session) {
		return false
	}
	e := d.monitorEntry(sessionID)
	if e == nil {
		return false
	}
	working := string(session.State) == protocol.StateWorking
	e.mu.Lock()
	removed := e.autoSettle != nil
	disarm := e.dismissal != autoSettleDismissalOff && !removed
	e.mu.Unlock()
	view, applied := e.command(monitorCommand{Key: e.key(), Kind: monitorCancelCountdown}, time.Now())
	if !applied || !e.sink.Applied(view) {
		return false
	}
	if disarm && working {
		d.armAutoSettle(sessionID)
	}
	if d.debugLogging {
		d.logf("auto-settle answered by user: session=%s had_pending=%v armed=%v", sessionID, removed, !disarm)
	}
	return true
}

func (d *Daemon) decorateSessionWithAutoSettle(clone *protocol.Session) {
	if clone == nil {
		return
	}
	firesAt := ""
	held, armed := false, false
	if view, ok := d.monitorViewFor(clone.ID); ok {
		auto := view.AutoSettle.V
		if !auto.FiresAt.IsZero() {
			firesAt = auto.FiresAt.UTC().Format(time.RFC3339Nano)
		}
		held, armed = auto.Held, auto.DismissArmed
	}
	clone.AutoSettleDismissArmed = nil
	if armed {
		clone.AutoSettleDismissArmed = protocol.Ptr(true)
	}
	clone.AutoSettleFiresAt = nil
	if firesAt != "" {
		clone.AutoSettleFiresAt = protocol.Ptr(firesAt)
	}
	clone.AutoSettleHeld = nil
	if held {
		clone.AutoSettleHeld = protocol.Ptr(true)
	}
}

func (d *Daemon) autoSettleAppliesTo(session *protocol.Session) bool {
	return session != nil && d.autoSettleConfig().enabled && !attention.Excluded(d.attentionInputFor(session))
}

func (d *Daemon) decoratedSession(sessionID protocol.SessionID) *protocol.Session {
	if d.store == nil {
		return nil
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return nil
	}
	return d.sessionForBroadcast(session)
}

func (d *Daemon) cancelAllAutoSettle() {
	for _, e := range d.monitors().each() {
		e.mu.Lock()
		_, wasVisible := e.stopAutoSettleLocked()
		changed := wasVisible || e.dismissal != autoSettleDismissalOff
		e.dismissal = autoSettleDismissalOff
		var view monitorView
		if changed {
			view = e.viewLocked()
		}
		e.mu.Unlock()
		if changed {
			e.sink.Applied(view)
		}
	}
}

func (d *Daemon) armAutoSettleForRunningSessions() {
	if d.store == nil {
		return
	}
	for _, session := range d.store.List("") {
		if session == nil || string(session.State) != protocol.StateWorking {
			continue
		}
		d.armAutoSettle(session.ID)
	}
}
