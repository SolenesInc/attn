package daemon

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const defaultNudgeCountdownWindow = 30 * time.Second

// firesAt is stored beside the timer because time.Timer has no deadline accessor.
type nudgeCountdown struct {
	timer   *time.Timer
	firesAt time.Time
}

func (d *Daemon) nudgeWindow() time.Duration {
	if d.nudgeWindowOverride > 0 {
		return d.nudgeWindowOverride
	}
	return defaultNudgeCountdownWindow
}

func (d *Daemon) armNudgeCountdownAt(sessionID string, deadline time.Time) {
	if sessionID == "" {
		return
	}
	if deadline.Before(time.Now()) {
		deadline = time.Now()
	}
	active := d.currentlySelectedSession() == sessionID

	// Checked before the lock: nudgeMu must never be held across a store read.
	if d.nudgeSuppressedFor(sessionID) {
		if d.nudgeSuppressionStillStands(sessionID) {
			d.nudgeMu.Lock()
			changed := d.setUnreadLocked(sessionID, true)
			changed = d.stopCountdownLocked(sessionID) || changed
			d.nudgeMu.Unlock()
			if changed {
				d.broadcastSessionStateChanged(sessionID)
			}
			return
		}
		d.clearNudgeSuppression(sessionID)
	}

	d.nudgeMu.Lock()
	changed := d.setUnreadLocked(sessionID, true)
	if active {
		changed = d.stopCountdownLocked(sessionID) || changed
	} else if existing, running := d.nudgeCountdowns[sessionID]; !running || deadline.Before(existing.firesAt) {
		d.startCountdownAtLocked(sessionID, deadline)
		changed = true
	}
	d.nudgeMu.Unlock()

	if changed {
		d.broadcastSessionStateChanged(sessionID)
	}
}

// The ready channel blocks the closure until `timer` is published, so the identity check in nudgeCountdownFire never reads a half-written value.
func (d *Daemon) startCountdownLocked(sessionID string, window time.Duration) {
	d.startCountdownAtLocked(sessionID, time.Now().Add(window))
}

func (d *Daemon) startCountdownAtLocked(sessionID string, firesAt time.Time) {
	if d.nudgeCountdowns == nil {
		d.nudgeCountdowns = make(map[string]*nudgeCountdown)
	}
	if existing, ok := d.nudgeCountdowns[sessionID]; ok {
		existing.timer.Stop()
	}
	ready := make(chan struct{})
	var timer *time.Timer
	delay := time.Until(firesAt)
	if delay < 0 {
		delay = 0
	}
	timer = time.AfterFunc(delay, func() {
		<-ready
		d.nudgeCountdownFire(sessionID, timer)
	})
	d.nudgeCountdowns[sessionID] = &nudgeCountdown{timer: timer, firesAt: firesAt}
	close(ready)
}

func (d *Daemon) stopCountdownLocked(sessionID string) bool {
	c, ok := d.nudgeCountdowns[sessionID]
	if !ok {
		return false
	}
	c.timer.Stop()
	delete(d.nudgeCountdowns, sessionID)
	return true
}

func (d *Daemon) setUnreadLocked(sessionID string, unread bool) bool {
	if d.unreadCache == nil {
		d.unreadCache = make(map[string]bool)
	}
	if d.unreadCache[sessionID] == unread {
		return false
	}
	if unread {
		d.unreadCache[sessionID] = true
	} else {
		delete(d.unreadCache, sessionID)
	}
	return true
}

func (d *Daemon) markTicketUnread(sessionID string, unread bool) {
	d.nudgeMu.Lock()
	changed := d.setUnreadLocked(sessionID, unread)
	if !unread {
		changed = d.stopCountdownLocked(sessionID) || changed
		delete(d.nudgeSuppressedThrough, sessionID)
	}
	d.nudgeMu.Unlock()
	if changed {
		d.broadcastSessionStateChanged(sessionID)
	}
}

func (d *Daemon) cancelNudgeCountdown(sessionID, reason string) {
	d.nudgeMu.Lock()
	changed := d.stopCountdownLocked(sessionID)
	d.nudgeMu.Unlock()
	if changed {
		if d.debugLogging {
			d.logf("nudge countdown canceled: session=%s reason=%s", sessionID, reason)
		}
		d.broadcastSessionStateChanged(sessionID)
	}
}

func (d *Daemon) cancelNudgeCountdownByUser(sessionID string) bool {
	// Read before taking nudgeMu: a store read must never happen under it.
	newest, err := d.newestUnreadTicketSeq(sessionID)
	if err != nil {
		d.logf("nudge cancel unread scan %s: %v", sessionID, err)
		// Fail closed rather than re-arm what the user just cancelled.
		newest = nudgeSuppressAllSeq
	}

	d.nudgeMu.Lock()
	stopped := d.stopCountdownLocked(sessionID)
	if d.nudgeSuppressedThrough == nil {
		d.nudgeSuppressedThrough = make(map[string]int64)
	}
	if existing, ok := d.nudgeSuppressedThrough[sessionID]; !ok || newest > existing {
		d.nudgeSuppressedThrough[sessionID] = newest
	}
	d.nudgeMu.Unlock()

	if d.debugLogging {
		d.logf("nudge countdown canceled by user: session=%s had_countdown=%v through_seq=%d", sessionID, stopped, newest)
	}
	return stopped
}

const nudgeSuppressAllSeq = int64(1<<63 - 1)

func (d *Daemon) nudgeSuppressedFor(sessionID string) bool {
	d.nudgeMu.Lock()
	defer d.nudgeMu.Unlock()
	_, ok := d.nudgeSuppressedThrough[sessionID]
	return ok
}

func (d *Daemon) nudgeSuppressionStillStands(sessionID string) bool {
	newest, err := d.newestUnreadTicketSeq(sessionID)
	if err != nil {
		d.logf("nudge suppression scan %s: %v", sessionID, err)
		return true
	}
	d.nudgeMu.Lock()
	defer d.nudgeMu.Unlock()
	through, ok := d.nudgeSuppressedThrough[sessionID]
	return ok && newest <= through
}

func (d *Daemon) clearNudgeSuppression(sessionID string) {
	d.nudgeMu.Lock()
	delete(d.nudgeSuppressedThrough, sessionID)
	d.nudgeMu.Unlock()
}

func (d *Daemon) newestUnreadTicketSeq(sessionID string) (int64, error) {
	if d.store == nil {
		return 0, nil
	}
	var newest int64
	for _, observer := range d.ticketObserversForSession(sessionID) {
		events, err := d.store.UnreadTicketEventsFor(observer.ID, observer.AuthorID)
		if err != nil {
			return 0, err
		}
		for _, event := range events {
			if event.Seq > newest {
				newest = event.Seq
			}
		}
	}
	return newest, nil
}

func (d *Daemon) clearNudgeState(sessionID string) {
	d.nudgeMu.Lock()
	d.stopCountdownLocked(sessionID)
	delete(d.unreadCache, sessionID)
	delete(d.nudgeSuppressedThrough, sessionID)
	d.nudgeMu.Unlock()
	d.lastInputMu.Lock()
	delete(d.lastUserInputAt, sessionID)
	delete(d.lastAutoSettleActivityAt, sessionID)
	d.lastInputMu.Unlock()
	d.deliveryMu.Lock()
	delete(d.watchLeaseUntil, sessionID)
	d.deliveryMu.Unlock()
}

func (d *Daemon) stopNudgeCountdowns() {
	d.nudgeMu.Lock()
	defer d.nudgeMu.Unlock()
	for id, c := range d.nudgeCountdowns {
		c.timer.Stop()
		delete(d.nudgeCountdowns, id)
	}
}

// The identity check against the map entry keeps a countdown that lost a reschedule/cancel race from firing twice.
func (d *Daemon) nudgeCountdownFire(sessionID string, self *time.Timer) {
	d.nudgeMu.Lock()
	entry, ok := d.nudgeCountdowns[sessionID]
	current := ok && entry.timer == self
	if current {
		delete(d.nudgeCountdowns, sessionID)
	}
	d.nudgeMu.Unlock()
	if !current {
		return
	}
	d.deliverNudgeOrReArm(sessionID)
}

func (d *Daemon) deliverNudgeOrReArm(sessionID string) {
	action := d.runNudgeDelivery(sessionID)
	if d.debugLogging {
		d.logf("ticket delivery: observer=%s session=%s channel=countdown outcome=%s", d.ticketAttentionKey(sessionID), sessionID, action)
	}
	if d.nudgeFireHook != nil {
		d.nudgeFireHook(sessionID, action)
	}
	d.broadcastSessionStateChanged(sessionID)
}

func (d *Daemon) runNudgeDelivery(sessionID string) string {
	if d.store == nil {
		return "noop"
	}
	if d.initialPromptPending(sessionID) {
		return "priming"
	}
	session := d.store.Get(sessionID)
	if session == nil || !sessionInputPhaseAllows(sessionInputAtTurnBoundary, session.State) {
		return "blocked"
	}
	if d.currentlySelectedSession() == sessionID {
		return "active"
	}
	d.deliveryMu.Lock()
	defer d.deliveryMu.Unlock()
	if until := d.watchLeaseUntil[sessionID]; until.After(time.Now()) {
		d.nudgeMu.Lock()
		d.startCountdownAtLocked(sessionID, until)
		d.nudgeMu.Unlock()
		return "watch"
	}
	unread, err := d.ticketUnreadForSession(sessionID)
	if err != nil {
		d.logf("nudge countdown unread check %s: %v", sessionID, err)
		return "error"
	}
	if unread == 0 {
		d.markTicketUnread(sessionID, false)
		return "drained"
	}
	if d.recentUserInput(sessionID, sessionInputQuietWindow) {
		d.nudgeMu.Lock()
		d.startCountdownLocked(sessionID, d.nudgeWindow())
		d.nudgeMu.Unlock()
		return "rearm"
	}
	deliveredThroughSeq, err := d.newestUnreadTicketSeq(sessionID)
	if err != nil {
		d.logf("nudge delivered-through scan %s: %v", sessionID, err)
		return "error"
	}
	delivery := maintenanceSessionInput(
		"ticket-nudge",
		fmt.Sprintf("%s/%d", sessionID, deliveredThroughSeq),
		sessionID,
		ticketNudgePrompt,
		sessionInputAtTurnBoundary,
	)
	delivery.resend = func() { d.deliverNudgeOrReArm(sessionID) }
	attempt := d.sessionInputs().try(context.Background(), delivery)
	if attempt.err != nil {
		if sessionInputQuietDeferral(attempt.err) {
			return "rearm"
		}
		d.logf("nudge countdown input %s: %v", sessionID, attempt.err)
		return "doorbell-error"
	}
	if err := d.store.SetTicketDeliveryAttentionThrough(d.ticketAttentionKey(sessionID), time.Now(), deliveredThroughSeq); err != nil {
		d.logf("nudge attention update %s: %v", sessionID, err)
		return "error"
	}
	d.sessionInputs().release(sessionID, delivery.id)
	return "doorbell"
}

// The approval store read precedes nudgeMu: lock order is one-way.
func (d *Daemon) updateNudgeSelection(oldID, newID string) {
	resumeOld := false
	if oldID != "" && oldID != newID && d.store != nil {
		if s := d.store.Get(oldID); s != nil && sessionInputPhaseAllows(sessionInputAtTurnBoundary, s.State) {
			resumeOld = true
		}
	}

	var changed []string
	d.nudgeMu.Lock()
	if newID != "" && d.stopCountdownLocked(newID) {
		changed = append(changed, newID)
	}
	resumeUnread := resumeOld && d.unreadCache[oldID]
	d.nudgeMu.Unlock()

	for _, id := range changed {
		d.broadcastSessionStateChanged(id)
	}
	if resumeUnread {
		// Re-derive the deadline from durable unread events so switching away cannot collapse an active bundle window to the short countdown.
		go d.notifyUnreadTicketSession(oldID, time.Now())
	}
}

func (d *Daemon) refreshTicketUnread(sessionID string) {
	if d.store == nil {
		return
	}
	unread, err := d.ticketUnreadForSession(sessionID)
	if err != nil {
		d.logf("ticket unread refresh %s: %v", sessionID, err)
		return
	}
	d.markTicketUnread(sessionID, unread > 0)
}

func (d *Daemon) handleTriggerNudge(msg *protocol.TriggerNudgeMessage) {
	sessionID := strings.TrimSpace(msg.SessionID)
	if sessionID == "" {
		return
	}
	if d.initialPromptPending(sessionID) {
		return
	}
	d.cancelNudgeCountdown(sessionID, "user triggered")
	if d.store == nil {
		return
	}
	session := d.store.Get(sessionID)
	if session == nil || !sessionInputPhaseAllows(sessionInputAtTurnBoundary, session.State) {
		return
	}
	d.deliveryMu.Lock()
	defer d.deliveryMu.Unlock()
	unread, err := d.ticketUnreadForSession(sessionID)
	if err != nil || unread == 0 {
		d.markTicketUnread(sessionID, false)
		return
	}
	deliveredThroughSeq, scanErr := d.newestUnreadTicketSeq(sessionID)
	if scanErr != nil {
		d.logf("trigger_nudge delivered-through scan %s: %v", sessionID, scanErr)
		d.broadcastSessionStateChanged(sessionID)
		return
	}
	delivery := maintenanceSessionInput(
		"ticket-nudge",
		fmt.Sprintf("%s/%d", sessionID, deliveredThroughSeq),
		sessionID,
		ticketNudgePrompt,
		sessionInputAtTurnBoundary,
	)
	attempt := d.sessionInputs().try(context.Background(), delivery)
	if attempt.err != nil {
		d.logf("trigger_nudge input %s: %v", sessionID, attempt.err)
	} else if err := d.store.SetTicketDeliveryAttentionThrough(d.ticketAttentionKey(sessionID), time.Now(), deliveredThroughSeq); err != nil {
		d.logf("trigger_nudge attention update %s: %v", sessionID, err)
	} else {
		d.sessionInputs().release(sessionID, delivery.id)
	}
	d.broadcastSessionStateChanged(sessionID)
}

func (d *Daemon) noteUserInput(sessionID, source string, data []byte) bool {
	if sessionID == "" || !isComposerKeystroke(source, data) {
		return false
	}
	now := time.Now()
	d.lastInputMu.Lock()
	if d.lastUserInputAt == nil {
		d.lastUserInputAt = make(map[string]time.Time)
	}
	if d.lastAutoSettleActivityAt == nil {
		d.lastAutoSettleActivityAt = make(map[string]time.Time)
	}
	d.lastUserInputAt[sessionID] = now
	d.lastAutoSettleActivityAt[sessionID] = now
	d.lastInputMu.Unlock()
	return true
}

// Shares lastInputMu with settleIfAutoSettleQuiet so activity wins.
func (d *Daemon) noteAutoSettleActivity(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	d.lastInputMu.Lock()
	if d.lastAutoSettleActivityAt == nil {
		d.lastAutoSettleActivityAt = make(map[string]time.Time)
	}
	d.lastAutoSettleActivityAt[sessionID] = time.Now()
	d.lastInputMu.Unlock()
	return true
}

// The composer is empty once a prompt is taken, whatever the clock says.
func (d *Daemon) forgetUserInput(sessionID string) {
	d.lastInputMu.Lock()
	delete(d.lastUserInputAt, sessionID)
	d.lastInputMu.Unlock()
}

func (d *Daemon) recentUserInput(sessionID string, within time.Duration) bool {
	return d.userInputQuietRemaining(sessionID, within) > 0
}

func (d *Daemon) userInputQuietRemaining(sessionID string, within time.Duration) time.Duration {
	d.lastInputMu.Lock()
	defer d.lastInputMu.Unlock()
	return d.userInputQuietRemainingLocked(sessionID, within)
}

func (d *Daemon) userInputQuietRemainingLocked(sessionID string, within time.Duration) time.Duration {
	last, ok := d.lastUserInputAt[sessionID]
	if !ok {
		return 0
	}
	remaining := within - time.Since(last)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (d *Daemon) autoSettleActivityQuietRemaining(sessionID string, within time.Duration) time.Duration {
	d.lastInputMu.Lock()
	defer d.lastInputMu.Unlock()
	return d.autoSettleActivityQuietRemainingLocked(sessionID, within)
}

func (d *Daemon) autoSettleActivityQuietRemainingLocked(sessionID string, within time.Duration) time.Duration {
	last, ok := d.lastAutoSettleActivityAt[sessionID]
	if !ok {
		return 0
	}
	remaining := within - time.Since(last)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// The quiet check and the store write must share one critical section under the lock activity stamps use, or a real interaction lands in the gap and the turn closes with the user's hands on the session.
func (d *Daemon) settleIfAutoSettleQuiet(sessionID string, within time.Duration) (quiet time.Duration, settled bool) {
	d.lastInputMu.Lock()
	defer d.lastInputMu.Unlock()
	if remaining := d.autoSettleActivityQuietRemainingLocked(sessionID, within); remaining > 0 {
		return remaining, false
	}
	return 0, d.store.SettleTurn(sessionID, time.Now())
}

func isUserKeystrokeSource(source string) bool {
	switch source {
	case "automation", "attach_replay", "pointer", "response":
		return false
	default:
		return true
	}
}

func isComposerKeystroke(source string, data []byte) bool {
	return isUserKeystrokeSource(source) && userInputEditsComposer(data)
}

// Backstop for clients that predate the "pointer" tag: SGR mouse reports and focus reports never edit the composer.
func userInputEditsComposer(data []byte) bool {
	rest := data
	for len(rest) > 0 {
		switch {
		case bytes.HasPrefix(rest, []byte("\x1b[<")):
			end := bytes.IndexAny(rest, "Mm")
			if end < 0 {
				return true
			}
			rest = rest[end+1:]
		case bytes.HasPrefix(rest, []byte("\x1b[I")), bytes.HasPrefix(rest, []byte("\x1b[O")):
			rest = rest[3:]
		default:
			return true
		}
	}
	return false
}

// Takes nudgeMu; callers must not already hold it.
func (d *Daemon) decorateSessionWithNudge(clone *protocol.Session) {
	if clone == nil {
		return
	}
	d.nudgeMu.Lock()
	unread := d.unreadCache[clone.ID]
	var firesAt string
	if c, ok := d.nudgeCountdowns[clone.ID]; ok {
		firesAt = c.firesAt.UTC().Format(time.RFC3339Nano)
	}
	d.nudgeMu.Unlock()

	if unread {
		clone.TicketUnread = protocol.Ptr(true)
	} else {
		clone.TicketUnread = nil
	}
	if firesAt != "" {
		clone.NudgeFiresAt = protocol.Ptr(firesAt)
	} else {
		clone.NudgeFiresAt = nil
	}
}
