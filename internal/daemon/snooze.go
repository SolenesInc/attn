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
	"github.com/victorarias/attn/internal/store"
)

const snoozeWakeKind = "session_snooze_wake"

type snoozeWakePayload struct {
	Deadline time.Time `json:"deadline"`
}

func (d *Daemon) handleSnoozeTurn(msg *protocol.SnoozeTurnMessage) {
	if d == nil || d.store == nil || msg == nil {
		return
	}
	sessionID := protocol.TrimID(msg.SessionID)
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
	sessionID := protocol.TrimID(msg.SessionID)
	if sessionID == "" {
		return
	}
	d.wakeSnooze(sessionID, time.Now(), "user")
}

func (d *Daemon) wakeSnooze(sessionID protocol.SessionID, at time.Time, cause string) {
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

func (d *Daemon) applySnoozeWakeAt(sessionID protocol.SessionID, deadline, at time.Time) bool {
	session := d.store.Get(sessionID)
	if session == nil {
		return false
	}
	if attention.OpensTurn(session.State) {
		return d.store.WakeTurnAtAndOpenIfClosed(sessionID, deadline, d.turnOpensAtOnWake(sessionID, at))
	}
	return d.store.WakeTurnAt(sessionID, deadline)
}

func (d *Daemon) finishSnoozeWake(sessionID protocol.SessionID, at time.Time, cause string) {
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

func (d *Daemon) turnOpensAtOnWake(sessionID protocol.SessionID, deadline time.Time) time.Time {
	if deadline.After(d.store.TurnStamps(sessionID).SettledAt) {
		return deadline
	}
	return time.Now()
}

func (d *Daemon) currentStateClaim(sessionID protocol.SessionID) string {
	session := d.store.Get(sessionID)
	if session == nil {
		return ""
	}
	return string(session.State)
}

func (d *Daemon) turnOpeningFor(sessionID protocol.SessionID, state protocol.SessionState) store.TurnOpening {
	if !attention.OpensTurn(state) {
		return store.TurnOpening{}
	}
	return store.TurnOpening{Opens: true, BreaksSnooze: attention.BreaksSnooze(state, d.stateReasons().get(sessionID))}
}

func (d *Daemon) dropEndedSnoozeWake(sessionID protocol.SessionID, state string, ended time.Time) {
	if ended.IsZero() {
		return
	}
	d.snoozeMu.Lock()
	defer d.snoozeMu.Unlock()
	d.removeSnoozeWakeFor(sessionID, ended)
	if d.debugLogging {
		cause := "broken"
		if !ended.After(time.Now()) {
			cause = "expired"
		}
		d.logf("snooze %s: session=%s state=%s reason=%s", cause, sessionID, state, d.stateReasons().get(sessionID))
	}
}

func (d *Daemon) enqueueSnoozeWake(sessionID protocol.SessionID, deadline time.Time) error {
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return jobs.ErrDisabled
	}
	delay := time.Until(deadline)
	if delay < 0 {
		delay = 0
	}
	_, err := runner.Enqueue(snoozeWakeKind, jobs.EnqueueOptions{
		UniqueKey: string(sessionID),
		Payload:   snoozeWakePayload{Deadline: deadline.UTC()},
		Delay:     delay,
	})
	return err
}

func (d *Daemon) scheduleSnoozeWake(sessionID protocol.SessionID, deadline time.Time) bool {
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
	sessionID := protocol.SessionID(strings.TrimSpace(jobSubject(job)))
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
	crashAt(crashWhileWakingASnooze)
	if !d.applySnoozeWakeAt(sessionID, payload.Deadline, payload.Deadline) {
		if d.debugLogging {
			d.logf("snooze wake superseded: session=%s deadline=%s", sessionID,
				payload.Deadline.UTC().Format(time.RFC3339Nano))
		}
		return nil, nil
	}
	d.finishSnoozeWake(sessionID, payload.Deadline, "deadline")
	return nil, nil
}

func (d *Daemon) removeSnoozeWake(sessionID protocol.SessionID) {
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return
	}
	runner.RemoveByKey(snoozeWakeKind, string(sessionID))
}

func (d *Daemon) removeSnoozeWakeFor(sessionID protocol.SessionID, deadline time.Time) {
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return
	}
	job, err := runner.GetByKey(snoozeWakeKind, string(sessionID))
	if err != nil || job == nil {
		return
	}
	var payload snoozeWakePayload
	if job.DecodePayload(&payload) == nil && payload.Deadline.Equal(deadline) {
		runner.Remove(job.ID)
	}
}

func (d *Daemon) clearSnoozeState(sessionID protocol.SessionID) {
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
				if _, live := snoozed[protocol.SessionID(job.UniqueKey)]; !live {
					runner.Remove(job.ID)
				}
			}
		}
	}
	for sessionID, deadline := range snoozed {
		d.scheduleSnoozeWake(sessionID, deadline)
	}
}

func (d *Daemon) decorateSessionWithSnooze(session *protocol.Session) {
	if session == nil || session.TurnSnoozedUntil == nil {
		return
	}
	if until, err := time.Parse(time.RFC3339Nano, *session.TurnSnoozedUntil); err != nil || !until.After(time.Now()) {
		session.TurnSnoozedUntil = nil
	}
}
