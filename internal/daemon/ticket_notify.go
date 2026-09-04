package daemon

import (
	"fmt"
	"strconv"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// A bounded "go look" trigger, never event content: the daemon signals, it never
// streams a ticket's content into the PTY.
var ticketNudgePrompt = prompts.RenderText("session", "legacy-ticket-nudge", prompts.Values{})

const legacyTicketMailboxCoalesceKey = "legacy-ticket"

// Busy delegation tickets in the production event history had a median inter-event gap
// of 9m49s (440 gaps across 67 tickets), so ten minutes sits just past the burst cadence.
const defaultTicketBundleWindow = 10 * time.Minute
const ticketWatchLeaseWindow = 5 * time.Second

func ticketWatchLeaseWindowFor(intervalMS *string) time.Duration {
	if intervalMS == nil {
		return ticketWatchLeaseWindow
	}
	milliseconds, err := strconv.ParseInt(*intervalMS, 10, 64)
	if err != nil || milliseconds <= 0 {
		return ticketWatchLeaseWindow
	}
	const maxDuration = time.Duration(1<<63 - 1)
	if milliseconds > int64(maxDuration/time.Millisecond) {
		return maxDuration
	}
	interval := time.Duration(milliseconds) * time.Millisecond
	grace := interval / 2
	if grace < time.Second {
		grace = time.Second
	}
	if interval > maxDuration-grace {
		return maxDuration
	}
	return interval + grace
}

func (d *Daemon) ticketBundleWindow() time.Duration {
	if d.ticketBundleWindowOverride > 0 {
		return d.ticketBundleWindowOverride
	}
	return defaultTicketBundleWindow
}

func (d *Daemon) ticketDeadline(sessionID string, newestPendingSeq int64, now time.Time) (time.Time, bool, error) {
	attention, found, err := d.store.TicketDeliveryAttention(d.ticketAttentionKey(sessionID))
	if err != nil {
		return time.Time{}, false, err
	}
	if found && newestPendingSeq <= attention.DeliveredThroughSeq {
		return time.Time{}, false, nil
	}
	deadline := now.Add(d.nudgeWindow())
	if !found || !attention.LastAttentionAt.Add(d.ticketBundleWindow()).After(now) {
		return deadline, true, nil
	}
	if bundled := attention.LastAttentionAt.Add(d.ticketBundleWindow()); bundled.After(deadline) {
		deadline = bundled
	}
	return deadline, false, nil
}

func (d *Daemon) notifyTicketObservers(ticketID string) {
	if d.ptyBackend == nil || d.store == nil {
		return
	}
	participants, err := d.store.TicketParticipants(ticketID)
	if err != nil {
		d.logf("ticket notify: participants for %s: %v", ticketID, err)
		return
	}
	now := time.Now()
	targets := make(map[string]bool, len(participants))
	var sleepingMembers []string
	for _, identity := range participants {
		if _, member := store.ParseTicketMemberIdentity(identity); member {
			if id := d.ticketSessionForIdentity(identity); id != "" {
				targets[id] = true
			} else {
				sleepingMembers = append(sleepingMembers, identity)
			}
			continue
		}
		if id := d.ticketSessionForIdentity(identity); id != "" {
			targets[id] = true
		}
	}
	for id := range targets {
		d.notifyTicketSession(id, now)
	}
	for _, identity := range sleepingMembers {
		d.notifySleepingTicketMember(identity, ticketID)
	}
}

// The unread event is the durable delivery: neither a failed wake nor its
// warning advances the cursor.
func (d *Daemon) notifySleepingTicketMember(identity, ticketID string) {
	memberID, ok := store.ParseTicketMemberIdentity(identity)
	if !ok {
		return
	}
	events, err := d.store.UnreadTicketEventsFor(identity, identity)
	if err != nil {
		d.logf("ticket notify: unread for %s: %v", identity, err)
		return
	}
	unread := false
	for _, event := range events {
		if event.TicketID == ticketID {
			unread = true
			break
		}
	}
	if !unread {
		return
	}

	result, err := d.crewWakeWithDelivery(memberID, "", true, nil)
	if err != nil {
		d.notifyTicketMemberWakeRefused(memberID, ticketID, err)
		return
	}
	d.notifyTicketSession(result.SessionID, time.Now())
}

const notificationKindCrewTicketWakeRefused = "crew_ticket_wake_refused"

func (d *Daemon) notifyTicketMemberWakeRefused(memberID, ticketID string, wakeErr error) {
	name := crew.DisplayName(memberID)
	d.logf("ticket notify: could not wake %s for %s: %v; activity remains unread", name, ticketID, wakeErr)
	record, err := d.store.AddNotification(store.NotificationRecord{
		Kind:       notificationKindCrewTicketWakeRefused,
		Severity:   store.NotificationWarning,
		Title:      fmt.Sprintf("Could not wake %s for ticket activity", name),
		Body:       fmt.Sprintf("Ticket %s is still unread. Wake %s from the sidebar or run `attn crew wake %s`.", ticketID, name, memberID),
		Detail:     wakeErr.Error(),
		SourceKind: "ticket",
		SourceID:   ticketID,
	}, time.Now())
	if err != nil {
		d.logf("notifications: add crew ticket wake refusal for %s: %v", memberID, err)
		return
	}
	d.publishFact(FactNotificationCreated, record.ID, nil)
}

func (d *Daemon) notifyTicketSession(sessionID string, now time.Time) {
	session := d.store.Get(sessionID)
	if session == nil {
		return
	}
	d.refreshTicketUnread(sessionID)
	d.notifyUnreadTicketSession(sessionID, now)
}

func (d *Daemon) syncNudgeForState(sessionID, state string) {
	if !sessionInputPhaseAllows(sessionInputAtTurnBoundary, protocol.SessionState(state)) {
		d.cancelNudgeCountdown(sessionID, "waiting for approval")
		return
	}
	go d.notifyUnreadTicketSession(sessionID, time.Now())
}

func (d *Daemon) notifyUnreadTicketSession(sessionID string, now time.Time) {
	d.deliveryMu.Lock()
	defer d.deliveryMu.Unlock()
	d.notifyUnreadTicketSessionLocked(sessionID, now)
}

// Unread scan, attention read, deadline calculation and timer arm stay in one critical
// section, or a stale calculation re-arms after a concurrent consume advanced the clock.
func (d *Daemon) notifyUnreadTicketSessionLocked(sessionID string, now time.Time) {
	if d.store == nil {
		return
	}
	session := d.store.Get(sessionID)
	if session == nil || !sessionInputPhaseAllows(sessionInputAtTurnBoundary, session.State) {
		return
	}
	pending := make(map[int64]struct{})
	for _, observer := range d.ticketObserversForSession(sessionID) {
		events, err := d.store.UnreadTicketEventsFor(observer.ID, observer.AuthorID)
		if err != nil {
			d.logf("ticket notify rebuild: %s: %v", sessionID, err)
			return
		}
		for _, event := range events {
			pending[event.Seq] = struct{}{}
		}
	}
	if len(pending) > 0 {
		var newestPendingSeq int64
		for seq := range pending {
			if seq > newestPendingSeq {
				newestPendingSeq = seq
			}
		}
		deadline, immediate, err := d.ticketDeadline(sessionID, newestPendingSeq, now)
		if err != nil {
			d.logf("ticket notify deadline: %s: %v", sessionID, err)
			return
		}
		if deadline.IsZero() {
			return
		}
		if d.ticketRebuildBeforeArmHook != nil {
			d.ticketRebuildBeforeArmHook(sessionID, deadline)
		}
		if d.debugLogging {
			d.logf("ticket delivery: observer=%s session=%s class=%s pending=%d deadline=%s channel=countdown outcome=armed", d.ticketAttentionKey(sessionID), sessionID, map[bool]string{true: "immediate", false: "bundled"}[immediate], len(pending), deadline.Format(time.RFC3339))
		}
		d.armNudgeCountdownAt(sessionID, deadline)
	}
}
