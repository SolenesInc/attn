package daemon

import (
	"strings"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/statetrace"
)

// Snooze suppresses turns as they would OPEN, not at read like the
// shell/chief/pinned/muted exclusions.

// firesAt exists because time.Timer exposes no deadline accessor.
type snoozeTimer struct {
	timer   *time.Timer
	firesAt time.Time
}

// The client owns the arithmetic: "tomorrow" needs a timezone and locale a
// remote endpoint's daemon does not share.
func (d *Daemon) handleSnoozeTurn(msg *protocol.SnoozeTurnMessage) {
	if d == nil || d.store == nil || msg == nil {
		return
	}
	sessionID := strings.TrimSpace(msg.SessionID)
	if sessionID == "" {
		return
	}
	until, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(msg.Until))
	if err != nil {
		d.logf("snooze rejected: session=%s bad until=%q: %v", sessionID, msg.Until, err)
		return
	}
	if !d.store.SnoozeTurn(sessionID, until, time.Now()) {
		return
	}
	d.cancelAutoSettle(sessionID, "snoozed")
	d.traceSettle(sessionID)
	d.scheduleSnoozeWake(sessionID, until)
	d.broadcastSessionStateChanged(sessionID)
}

func (d *Daemon) handleWakeTurn(msg *protocol.WakeTurnMessage) {
	if d == nil || msg == nil {
		return
	}
	sessionID := strings.TrimSpace(msg.SessionID)
	if sessionID == "" {
		return
	}
	d.wakeSnooze(sessionID, time.Now(), "user")
}

func (d *Daemon) wakeSnooze(sessionID string, at time.Time, cause string) {
	if d == nil || d.store == nil || sessionID == "" {
		return
	}
	d.cancelSnoozeWake(sessionID)
	if !d.store.WakeTurn(sessionID) {
		return
	}
	d.finishSnoozeWake(sessionID, at, cause)
}

func (d *Daemon) finishSnoozeWake(sessionID string, at time.Time, cause string) {
	if session := d.store.Get(sessionID); session != nil && attention.OpensTurn(session.State) {
		d.store.OpenTurnIfClosed(sessionID, d.turnOpensAtOnWake(sessionID, at))
	}
	if d.debugLogging {
		d.logf("snooze woken: session=%s cause=%s", sessionID, cause)
	}
	d.recordStateObservation(sessionID, statetrace.Observation{
		Source:  "user",
		Claim:   d.currentStateClaim(sessionID),
		Detail:  cause,
		Cause:   "wake",
		Outcome: statetrace.OutcomeApplied,
	})
	d.broadcastSessionStateChanged(sessionID)
}

// Stamps a woken turn with the deadline — unless membership (`opened > settled`)
// would read it as already closed and silently lose the agent.
func (d *Daemon) turnOpensAtOnWake(sessionID string, deadline time.Time) time.Time {
	if deadline.After(d.store.TurnStamps(sessionID).SettledAt) {
		return deadline
	}
	return time.Now()
}

func (d *Daemon) currentStateClaim(sessionID string) string {
	session := d.store.Get(sessionID)
	if session == nil {
		return ""
	}
	return string(session.State)
}

func (d *Daemon) snoozeSuppressesTurn(sessionID string, state protocol.SessionState) bool {
	if d == nil || d.store == nil {
		return false
	}
	if d.store.TurnStamps(sessionID).SnoozedUntil.IsZero() {
		return false
	}
	reason := d.stateReasons().get(sessionID)
	if !attention.BreaksSnooze(state, reason) {
		return true
	}
	d.cancelSnoozeWake(sessionID)
	d.store.WakeTurn(sessionID)
	if d.debugLogging {
		d.logf("snooze broken: session=%s state=%s reason=%s", sessionID, state, reason)
	}
	return false
}

func (d *Daemon) scheduleSnoozeWake(sessionID string, until time.Time) {
	if d == nil || sessionID == "" {
		return
	}
	d.snoozeMu.Lock()
	defer d.snoozeMu.Unlock()
	d.scheduleSnoozeWakeLocked(sessionID, until)
}

func (d *Daemon) scheduleSnoozeWakeLocked(sessionID string, until time.Time) {
	if d.snoozeTimers == nil {
		d.snoozeTimers = make(map[string]*snoozeTimer)
	}
	if existing, ok := d.snoozeTimers[sessionID]; ok {
		existing.timer.Stop()
	}
	window := time.Until(until)
	if window < 0 {
		window = 0
	}
	// Same ready-channel handshake as auto_settle.go: the closure blocks until `timer`
	// is published, so a zero window firing immediately still reads a written value.
	ready := make(chan struct{})
	var timer *time.Timer
	timer = time.AfterFunc(window, func() {
		<-ready
		d.fireSnoozeWake(sessionID, timer, until)
	})
	d.snoozeTimers[sessionID] = &snoozeTimer{timer: timer, firesAt: until}
	close(ready)
}

// Two staleness checks: the identity check under the lock catches a lost
// cancel-or-replace race, and WakeTurnAt one made after the lock is dropped.
func (d *Daemon) fireSnoozeWake(sessionID string, self *time.Timer, deadline time.Time) {
	d.snoozeMu.Lock()
	entry, ok := d.snoozeTimers[sessionID]
	if !ok || entry.timer != self {
		d.snoozeMu.Unlock()
		return
	}
	delete(d.snoozeTimers, sessionID)
	d.snoozeMu.Unlock()

	if d.snoozeWakeHook != nil {
		defer d.snoozeWakeHook(sessionID)
	}
	if d.snoozeWakeGapHook != nil {
		d.snoozeWakeGapHook(sessionID)
	}
	if d.store == nil {
		return
	}
	if !d.store.WakeTurnAt(sessionID, deadline) {
		if d.debugLogging {
			d.logf("snooze wake superseded: session=%s deadline=%s", sessionID, deadline.UTC().Format(time.RFC3339Nano))
		}
		return
	}
	d.finishSnoozeWake(sessionID, deadline, "deadline")
}

func (d *Daemon) cancelSnoozeWake(sessionID string) {
	if d == nil {
		return
	}
	d.snoozeMu.Lock()
	defer d.snoozeMu.Unlock()
	if entry, ok := d.snoozeTimers[sessionID]; ok {
		entry.timer.Stop()
		delete(d.snoozeTimers, sessionID)
	}
}

func (d *Daemon) clearSnoozeState(sessionID string) {
	d.cancelSnoozeWake(sessionID)
}

func (d *Daemon) stopSnoozeTimers() {
	if d == nil {
		return
	}
	d.snoozeMu.Lock()
	defer d.snoozeMu.Unlock()
	for id, entry := range d.snoozeTimers {
		entry.timer.Stop()
		delete(d.snoozeTimers, id)
	}
}

func (d *Daemon) rescheduleSnoozeWakes() {
	if d == nil || d.store == nil {
		return
	}
	for sessionID, until := range d.store.SnoozedSessions() {
		d.scheduleSnoozeWake(sessionID, until)
	}
}

// Leaves a lapsed deadline off: the wake is racing this broadcast, and
// announcing it would park the row snoozed until the timer lands.
func (d *Daemon) decorateSessionWithSnooze(session *protocol.Session) {
	if session == nil || d.store == nil {
		return
	}
	session.TurnSnoozedUntil = nil
	until := d.store.TurnStamps(session.ID).SnoozedUntil
	if until.IsZero() || !until.After(time.Now()) {
		return
	}
	session.TurnSnoozedUntil = protocol.Ptr(until.UTC().Format(time.RFC3339Nano))
}
