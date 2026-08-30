package daemon

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/statetrace"
)

// Snooze suppresses turns as they would OPEN, not at read like the
// shell/chief/pinned/muted exclusions.

const snoozeWakeKind = "session_snooze_wake"

type snoozeWakePayload struct {
	Deadline time.Time `json:"deadline"`
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
	d.snoozeMu.Lock()
	defer d.snoozeMu.Unlock()
	if !d.store.SnoozeTurn(sessionID, until, time.Now()) {
		return
	}
	d.cancelAutoSettle(sessionID, "snoozed")
	d.traceSettle(sessionID)
	if !d.scheduleSnoozeWake(sessionID, until) {
		return
	}
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
	d.snoozeMu.Lock()
	defer d.snoozeMu.Unlock()
	deadline := d.store.TurnStamps(sessionID).SnoozedUntil
	if deadline.IsZero() {
		return
	}
	d.removeSnoozeWake(sessionID)
	if !d.applySnoozeWakeAt(sessionID, deadline, at) {
		return
	}
	d.finishSnoozeWake(sessionID, at, cause)
}

func (d *Daemon) applySnoozeWakeAt(sessionID string, deadline, at time.Time) bool {
	session := d.store.Get(sessionID)
	if session == nil {
		return false
	}
	if attention.OpensTurn(session.State) {
		return d.store.WakeTurnAtAndOpenIfClosed(sessionID, deadline, d.turnOpensAtOnWake(sessionID, at))
	}
	return d.store.WakeTurnAt(sessionID, deadline)
}

func (d *Daemon) finishSnoozeWake(sessionID string, at time.Time, cause string) {
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
	d.snoozeMu.Lock()
	defer d.snoozeMu.Unlock()
	deadline := d.store.TurnStamps(sessionID).SnoozedUntil
	if deadline.IsZero() {
		return false
	}
	reason := d.stateReasons().get(sessionID)
	expired := !deadline.After(time.Now())
	if !expired && !attention.BreaksSnooze(state, reason) {
		return true
	}
	if !d.store.WakeTurnAt(sessionID, deadline) {
		return !d.store.TurnStamps(sessionID).SnoozedUntil.IsZero()
	}
	d.removeSnoozeWake(sessionID)
	if d.debugLogging {
		cause := "broken"
		if expired {
			cause = "expired"
		}
		d.logf("snooze %s: session=%s state=%s reason=%s", cause, sessionID, state, reason)
	}
	return false
}

func (d *Daemon) registerSnoozeWakeHandler(runner *jobs.Runner) error {
	return runner.Register(snoozeWakeKind, d.snoozeWakeHandler)
}

func (d *Daemon) enqueueSnoozeWake(sessionID string, deadline time.Time) error {
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return jobs.ErrDisabled
	}
	delay := time.Until(deadline)
	if delay < 0 {
		delay = 0
	}
	_, err := runner.Enqueue(snoozeWakeKind, jobs.EnqueueOptions{
		UniqueKey: sessionID,
		Payload:   snoozeWakePayload{Deadline: deadline.UTC()},
		Delay:     delay,
	})
	return err
}

func (d *Daemon) scheduleSnoozeWake(sessionID string, deadline time.Time) bool {
	if err := d.enqueueSnoozeWake(sessionID, deadline); err != nil {
		d.logf("snooze wake schedule failed: session=%s: %v", sessionID, err)
		at := time.Now()
		if d.applySnoozeWakeAt(sessionID, deadline, at) {
			d.finishSnoozeWake(sessionID, at, "schedule_failed")
		} else {
			d.broadcastSessionStateChanged(sessionID)
		}
		return false
	}
	return true
}

func (d *Daemon) snoozeWakeHandler(ctx context.Context, job *jobs.Job) (any, error) {
	if d == nil || d.store == nil {
		return nil, nil
	}
	sessionID := strings.TrimSpace(jobSubject(job))
	if sessionID == "" {
		return nil, errors.New("session_snooze_wake requires a session id")
	}
	var payload snoozeWakePayload
	if err := job.DecodePayload(&payload); err != nil {
		return nil, err
	}
	if payload.Deadline.IsZero() {
		return nil, errors.New("session_snooze_wake requires a deadline")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	changed, err := func() (bool, error) {
		if job.CommitGuard != nil {
			if !job.CommitGuard.Enter() {
				return false, context.Canceled
			}
			defer job.CommitGuard.Leave()
		}
		return d.applySnoozeWakeAt(sessionID, payload.Deadline, payload.Deadline), nil
	}()
	if err != nil {
		return nil, err
	}
	if !changed {
		if d.debugLogging {
			d.logf("snooze wake superseded: session=%s deadline=%s", sessionID,
				payload.Deadline.UTC().Format(time.RFC3339Nano))
		}
		return nil, nil
	}
	d.finishSnoozeWake(sessionID, payload.Deadline, "deadline")
	return nil, nil
}

func (d *Daemon) removeSnoozeWake(sessionID string) {
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return
	}
	runner.RemoveByKey(snoozeWakeKind, sessionID)
}

func (d *Daemon) clearSnoozeState(sessionID string) {
	d.snoozeMu.Lock()
	defer d.snoozeMu.Unlock()
	d.removeSnoozeWake(sessionID)
}

func (d *Daemon) reconcileSnoozeWakeJobs() {
	if d == nil || d.store == nil {
		return
	}
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return
	}
	d.snoozeMu.Lock()
	defer d.snoozeMu.Unlock()

	snoozed := d.store.SnoozedSessions()
	queued, err := runner.List()
	if err != nil {
		d.logf("snooze wake reconcile: list jobs: %v", err)
	} else {
		for _, job := range queued {
			if job.Kind == snoozeWakeKind {
				if _, live := snoozed[job.UniqueKey]; !live {
					runner.Remove(job.ID)
				}
			}
		}
	}
	for sessionID, deadline := range snoozed {
		d.scheduleSnoozeWake(sessionID, deadline)
	}
}

// Leaves a lapsed deadline off while the overdue wake job catches up.
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
