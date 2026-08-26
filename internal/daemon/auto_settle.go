package daemon

import (
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

const (
	defaultAutoSettleArmSeconds       = 30
	defaultAutoSettleCountdownSeconds = 15

	autoSettleHoldQuietWindow = 5 * time.Second

	// Arm floor sits past the resolver's own settle latency (HeartbeatSettleAfter, 5s).
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
		enabled:   parseBooleanSetting(d.store.GetSetting(SettingAutoSettleEnabled)),
		arm:       resolveAutoSettleSeconds(d.store.GetSetting(SettingAutoSettleArmSeconds), defaultAutoSettleArmSeconds),
		countdown: resolveAutoSettleSeconds(d.store.GetSetting(SettingAutoSettleCountdownSeconds), defaultAutoSettleCountdownSeconds),
	}
}

func resolveAutoSettleSeconds(stored string, fallbackSeconds int) time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(stored)); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return time.Duration(fallbackSeconds) * time.Second
}

func (d *Daemon) syncAutoSettle(sessionID, state string) {
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

func (d *Daemon) armAutoSettle(sessionID string) {
	if sessionID == "" || d.store == nil {
		return
	}
	cfg := d.autoSettleConfig()
	if !cfg.enabled {
		return
	}
	if d.autoSettleDismissalArmed(sessionID) {
		return
	}
	if !d.turnOwed(sessionID) {
		return
	}
	if _, ok := d.sessionInputs().currentUserRun(sessionID); !ok {
		return
	}

	d.autoSettleMu.Lock()
	_, pending := d.autoSettleTimers[sessionID]
	if !pending {
		d.startAutoSettleLocked(sessionID, autoSettleArming, cfg.arm)
	}
	d.autoSettleMu.Unlock()
}

func (d *Daemon) holdAutoSettle(sessionID string) {
	d.autoSettleMu.Lock()
	entry, ok := d.autoSettleTimers[sessionID]
	if !ok || entry.phase == autoSettleHeld {
		d.autoSettleMu.Unlock()
		return
	}
	resume := entry.phase
	d.startAutoSettleHeldLocked(sessionID, resume, autoSettleHoldQuietWindow)
	d.autoSettleMu.Unlock()

	if d.debugLogging {
		d.logf("auto-settle held: session=%s resume_phase=%d", sessionID, resume)
	}
	if resume == autoSettleCounting {
		d.broadcastSessionStateChanged(sessionID)
	}
}

func (d *Daemon) startAutoSettleHeldLocked(sessionID string, resume autoSettlePhase, window time.Duration) {
	d.startAutoSettleLocked(sessionID, autoSettleHeld, window)
	d.autoSettleTimers[sessionID].resume = resume
}

func (d *Daemon) startAutoSettleLocked(sessionID string, phase autoSettlePhase, window time.Duration) {
	if d.autoSettleTimers == nil {
		d.autoSettleTimers = make(map[string]*autoSettleTimer)
	}
	if existing, ok := d.autoSettleTimers[sessionID]; ok {
		existing.timer.Stop()
	}
	if window < 0 {
		window = 0
	}
	firesAt := time.Now().Add(window)
	run, _ := d.sessionInputs().currentUserRun(sessionID)
	opened := d.store.TurnStamps(sessionID).OpenedAt
	// ready blocks the closure until `timer` is published: on a zero-length window
	// it would otherwise read the identity check's variable before it is written.
	ready := make(chan struct{})
	var timer *time.Timer
	timer = time.AfterFunc(window, func() {
		<-ready
		d.autoSettleFire(sessionID, timer)
	})
	d.autoSettleTimers[sessionID] = &autoSettleTimer{timer: timer, phase: phase, firesAt: firesAt, run: run, opened: opened}
	close(ready)
}

func (d *Daemon) stopAutoSettleLocked(sessionID string) (removed, wasVisible bool) {
	entry, ok := d.autoSettleTimers[sessionID]
	if !ok {
		return false, false
	}
	entry.timer.Stop()
	delete(d.autoSettleTimers, sessionID)
	return true, entry.visible()
}

func (d *Daemon) cancelAutoSettle(sessionID, reason string) {
	d.autoSettleMu.Lock()
	removed, wasVisible := d.stopAutoSettleLocked(sessionID)
	d.autoSettleMu.Unlock()
	if removed && d.debugLogging {
		d.logf("auto-settle canceled: session=%s reason=%s", sessionID, reason)
	}
	if wasVisible {
		d.broadcastSessionStateChanged(sessionID)
	}
}

func (d *Daemon) clearAutoSettleState(sessionID string) {
	d.autoSettleMu.Lock()
	d.stopAutoSettleLocked(sessionID)
	delete(d.autoSettleDismissals, sessionID)
	d.autoSettleMu.Unlock()
}

func (d *Daemon) stopAutoSettleTimers() {
	d.autoSettleMu.Lock()
	defer d.autoSettleMu.Unlock()
	for id, entry := range d.autoSettleTimers {
		entry.timer.Stop()
		delete(d.autoSettleTimers, id)
	}
}

func (d *Daemon) autoSettleFire(sessionID string, self *time.Timer) {
	d.autoSettleMu.Lock()
	entry, ok := d.autoSettleTimers[sessionID]
	if !ok || entry.timer != self {
		d.autoSettleMu.Unlock()
		return
	}
	phase, resume, run, opened := entry.phase, entry.resume, entry.run, entry.opened
	delete(d.autoSettleTimers, sessionID)
	d.autoSettleMu.Unlock()

	action := d.runAutoSettleFor(sessionID, phase, resume, run, opened)
	if d.debugLogging {
		d.logf("auto-settle fire: session=%s phase=%d outcome=%s", sessionID, phase, action)
	}
	if d.autoSettleFireHook != nil {
		d.autoSettleFireHook(sessionID, action)
	}
	if action == "held" && phase != autoSettleCounting {
		return
	}
	d.broadcastSessionStateChanged(sessionID)
}

func (d *Daemon) runAutoSettleFor(sessionID string, phase, resume autoSettlePhase, armedRun sessionInputRunRef, armedTurn time.Time) string {
	// applyState takes this lock around its store write: without it the timer can
	// settle a turn a transition opened between the check and the settle.
	d.autoSettleFireMu.Lock()
	defer d.autoSettleFireMu.Unlock()

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
	if evidence, ok := d.evidenceTable().snapshot(sessionID); ok &&
		sessionstate.ClassifierVerdictPending(
			evidence,
			sessionstate.PolicyFor(string(session.Agent)),
			time.Now(),
		) {
		return "classifying"
	}
	if string(session.State) != protocol.StateWorking {
		return "not-working"
	}
	currentRun, userTaken := d.sessionInputs().currentUserRun(sessionID)
	if !userTaken || currentRun != armedRun {
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
		if quiet := d.autoSettleActivityQuietRemaining(sessionID, autoSettleHoldQuietWindow); quiet > 0 {
			d.holdFromFire(sessionID, hold, quiet)
			return "held"
		}
		if phase == autoSettleHeld {
			window := cfg.arm
			if resume == autoSettleCounting {
				window = cfg.countdown
			}
			d.autoSettleMu.Lock()
			d.startAutoSettleLocked(sessionID, resume, window)
			d.autoSettleMu.Unlock()
			return "resumed"
		}
		d.autoSettleMu.Lock()
		d.startAutoSettleLocked(sessionID, autoSettleCounting, cfg.countdown)
		d.autoSettleMu.Unlock()
		return "counting"
	}

	if d.autoSettlePreSettleHook != nil {
		d.autoSettlePreSettleHook()
	}

	// Asked again here because it must be indivisible from the write it guards:
	// settleIfAutoSettleQuiet holds the activity lock across both.
	quiet, settled := d.settleIfAutoSettleQuiet(sessionID, autoSettleHoldQuietWindow)
	if quiet > 0 {
		d.holdFromFire(sessionID, autoSettleCounting, quiet)
		return "held"
	}
	if !settled {
		return "settle-failed"
	}
	d.traceSettle(sessionID)
	return "settled"
}

func (d *Daemon) holdFromFire(sessionID string, resume autoSettlePhase, quiet time.Duration) {
	d.autoSettleMu.Lock()
	d.startAutoSettleHeldLocked(sessionID, resume, quiet)
	d.autoSettleMu.Unlock()
}

func (d *Daemon) answerAutoSettleByUser(sessionID string) bool {
	session := d.decoratedSession(sessionID)
	if !d.autoSettleAppliesTo(session) {
		return false
	}
	working := string(session.State) == protocol.StateWorking

	d.autoSettleMu.Lock()
	removed, _ := d.stopAutoSettleLocked(sessionID)
	_, armed := d.autoSettleDismissals[sessionID]
	disarm := armed && !removed
	if disarm {
		delete(d.autoSettleDismissals, sessionID)
	} else {
		if d.autoSettleDismissals == nil {
			d.autoSettleDismissals = make(map[string]bool)
		}
		d.autoSettleDismissals[sessionID] = working
	}
	d.autoSettleMu.Unlock()

	if disarm && working {
		d.armAutoSettle(sessionID)
	}
	if d.debugLogging {
		d.logf("auto-settle answered by user: session=%s had_pending=%v armed=%v", sessionID, removed, !disarm)
	}
	return true
}

func (d *Daemon) autoSettleDismissalArmed(sessionID string) bool {
	d.autoSettleMu.Lock()
	defer d.autoSettleMu.Unlock()
	_, armed := d.autoSettleDismissals[sessionID]
	return armed
}

func (d *Daemon) coverAutoSettleDismissal(sessionID string) {
	d.autoSettleMu.Lock()
	if _, armed := d.autoSettleDismissals[sessionID]; armed {
		d.autoSettleDismissals[sessionID] = true
	}
	d.autoSettleMu.Unlock()
}

func (d *Daemon) retireAutoSettleDismissal(sessionID string) {
	d.autoSettleMu.Lock()
	covered, armed := d.autoSettleDismissals[sessionID]
	retire := armed && covered
	if retire {
		delete(d.autoSettleDismissals, sessionID)
	}
	d.autoSettleMu.Unlock()
	if retire {
		d.broadcastSessionStateChanged(sessionID)
	}
}

func (d *Daemon) decorateSessionWithAutoSettle(clone *protocol.Session) {
	if clone == nil {
		return
	}
	d.autoSettleMu.Lock()
	entry, ok := d.autoSettleTimers[clone.ID]
	firesAt := ""
	held := false
	if ok {
		switch {
		case entry.phase == autoSettleCounting:
			firesAt = entry.firesAt.UTC().Format(time.RFC3339Nano)
		case entry.visible():
			held = true
		}
	}
	_, dismissArmed := d.autoSettleDismissals[clone.ID]
	d.autoSettleMu.Unlock()

	if dismissArmed {
		clone.AutoSettleDismissArmed = protocol.Ptr(true)
	} else {
		clone.AutoSettleDismissArmed = nil
	}

	if firesAt != "" {
		clone.AutoSettleFiresAt = protocol.Ptr(firesAt)
	} else {
		clone.AutoSettleFiresAt = nil
	}
	if held {
		clone.AutoSettleHeld = protocol.Ptr(true)
	} else {
		clone.AutoSettleHeld = nil
	}
}

func (d *Daemon) turnOwed(sessionID string) bool {
	clone := d.decoratedSession(sessionID)
	if clone == nil {
		return false
	}
	return protocol.Deref(clone.TurnOwed)
}

func (d *Daemon) autoSettleAppliesTo(session *protocol.Session) bool {
	return session != nil &&
		d.autoSettleConfig().enabled &&
		!attention.Excluded(d.attentionInputFor(session))
}

func (d *Daemon) decoratedSession(sessionID string) *protocol.Session {
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
	d.autoSettleMu.Lock()
	visible := make([]string, 0, len(d.autoSettleTimers))
	for id, entry := range d.autoSettleTimers {
		entry.timer.Stop()
		if entry.visible() {
			visible = append(visible, id)
		}
		delete(d.autoSettleTimers, id)
	}
	for id := range d.autoSettleDismissals {
		visible = append(visible, id)
	}
	d.autoSettleDismissals = nil
	d.autoSettleMu.Unlock()
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
