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

func (d *Daemon) syncAutoSettle(sessionID protocol.SessionID, state string) {
	if state != protocol.StateWorking {
		d.sessionInputs().observePhase(sessionID, protocol.SessionState(state))
		d.retireAutoSettleDismissal(sessionID)
		d.cancelAutoSettle(sessionID, "left working")
		return
	}
	d.sessionInputs().observePhase(sessionID, protocol.SessionStateWorking)
	d.coverAutoSettleDismissal(sessionID)
	if run, ok := d.sessionInputs().currentUserRun(sessionID); ok {
		d.armAutoSettleForUserInput(run)
	}
}

func (d *Daemon) armAutoSettleForUserInput(run sessionInputRunRef) {
	if !run.valid() || d.store == nil {
		return
	}
	current, credited := d.sessionInputs().currentUserRun(run.sessionID)
	if !credited || current != run {
		return
	}
	session := d.store.Get(run.sessionID)
	if session == nil || session.State != protocol.SessionStateWorking {
		return
	}
	d.armAutoSettle(run.sessionID)
}

func (d *Daemon) armAutoSettle(sessionID protocol.SessionID) {
	if sessionID == "" || d.store == nil {
		return
	}
	cfg := d.autoSettleConfig()
	if !cfg.enabled {
		return
	}
	e := d.monitorEntry(sessionID)
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dismissal != autoSettleDismissalOff || !d.turnOwed(sessionID) {
		return
	}
	if _, credited := e.input.currentUserRunLocked(); !credited {
		return
	}
	if e.autoSettle == nil {
		d.startAutoSettleLocked(sessionID, e, autoSettleArming, cfg.arm)
	}
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
	e.mu.Unlock()
	if d.debugLogging {
		d.logf("auto-settle held: session=%s resume_phase=%d", sessionID, resume)
	}
	if resume == autoSettleCounting {
		d.broadcastSessionStateChanged(sessionID)
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
	opened := d.store.TurnStamps(sessionID).OpenedAt
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
	removed, visible := e.stopAutoSettleLocked()
	e.mu.Unlock()
	if removed && d.debugLogging {
		d.logf("auto-settle canceled: session=%s reason=%s", sessionID, reason)
	}
	if visible {
		d.broadcastSessionStateChanged(sessionID)
	}
}

func (d *Daemon) clearAutoSettleState(sessionID protocol.SessionID) {
	if e := d.monitorEntry(sessionID); e != nil {
		e.mu.Lock()
		e.stopAutoSettleLocked()
		e.dismissal = autoSettleDismissalOff
		e.mu.Unlock()
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
	e.mu.Unlock()
	if action == "settled" {
		d.traceSettle(sessionID)
	}
	if d.debugLogging {
		d.logf("auto-settle fire: session=%s phase=%d outcome=%s", sessionID, timer.phase, action)
	}
	if action == "held" && timer.phase != autoSettleCounting {
		return
	}
	d.broadcastSessionStateChanged(sessionID)
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
	if string(session.State) != protocol.StateWorking {
		return "not-working"
	}
	current, credited := e.input.currentUserRunLocked()
	if !credited || current != armedRun {
		return "wrong-run"
	}
	if opened := d.store.TurnStamps(sessionID).OpenedAt; opened.IsZero() || !opened.Equal(armedTurn) {
		return "wrong-turn"
	}
	if !d.turnOwed(sessionID) {
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
	if !d.store.SettleTurn(sessionID, time.Now()) {
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
	removed, _ := e.stopAutoSettleLocked()
	armed := e.dismissal != autoSettleDismissalOff
	disarm := armed && !removed
	if disarm {
		e.dismissal = autoSettleDismissalOff
	} else if working {
		e.dismissal = autoSettleDismissalCovered
	} else {
		e.dismissal = autoSettleDismissalArmed
	}
	e.mu.Unlock()
	if disarm && working {
		d.armAutoSettle(sessionID)
	}
	if d.debugLogging {
		d.logf("auto-settle answered by user: session=%s had_pending=%v armed=%v", sessionID, removed, !disarm)
	}
	return true
}

func (d *Daemon) coverAutoSettleDismissal(sessionID protocol.SessionID) {
	if e := d.monitorEntry(sessionID); e != nil {
		e.mu.Lock()
		if e.dismissal != autoSettleDismissalOff {
			e.dismissal = autoSettleDismissalCovered
		}
		e.mu.Unlock()
	}
}

func (d *Daemon) retireAutoSettleDismissal(sessionID protocol.SessionID) {
	e := d.monitorEntry(sessionID)
	if e == nil {
		return
	}
	e.mu.Lock()
	retire := e.dismissal == autoSettleDismissalCovered
	if retire {
		e.dismissal = autoSettleDismissalOff
	}
	e.mu.Unlock()
	if retire {
		d.broadcastSessionStateChanged(sessionID)
	}
}

func (d *Daemon) decorateSessionWithAutoSettle(clone *protocol.Session) {
	if clone == nil {
		return
	}
	firesAt := ""
	held, armed := false, false
	if e := d.monitorEntry(clone.ID); e != nil {
		e.mu.Lock()
		if timer := e.autoSettle; timer != nil {
			switch {
			case timer.phase == autoSettleCounting:
				firesAt = timer.firesAt.UTC().Format(time.RFC3339Nano)
			case timer.visible():
				held = true
			}
		}
		armed = e.dismissal != autoSettleDismissalOff
		e.mu.Unlock()
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

func (d *Daemon) turnOwed(sessionID protocol.SessionID) bool {
	session := cloneSession(d.store.Get(sessionID))
	if session == nil {
		return false
	}
	d.decorateChiefOfStaff(session, d.profileChiefs())
	d.decorateSessionWithTurn(session)
	return protocol.Deref(session.TurnOwed)
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
	var visible []protocol.SessionID
	for id, e := range d.monitors().each() {
		e.mu.Lock()
		_, wasVisible := e.stopAutoSettleLocked()
		if wasVisible || e.dismissal != autoSettleDismissalOff {
			visible = append(visible, id)
		}
		e.dismissal = autoSettleDismissalOff
		e.mu.Unlock()
	}
	for _, id := range visible {
		d.broadcastSessionStateChanged(id)
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
