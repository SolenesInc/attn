package daemon

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

func newSnoozeDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := newTurnDaemon(t)
	runner := jobs.New(jobs.Options{Store: d.newSQLJobStore()})
	if err := d.registerSnoozeWakeHandler(runner); err != nil {
		t.Fatalf("register snooze wake handler: %v", err)
	}
	d.setJobQueue(runner)
	return d
}

func queuedSnoozeJob(t *testing.T, d *Daemon, sessionID string) *jobs.Job {
	t.Helper()
	job, err := d.jobQueueRef().GetByKey(snoozeWakeKind, sessionID)
	if err != nil {
		t.Fatalf("get snooze wake job: %v", err)
	}
	if job == nil {
		t.Fatalf("no snooze wake job for %s", sessionID)
	}
	job.CommitGuard = &jobs.CommitGuard{}
	return job
}

func runSnoozeJob(t *testing.T, d *Daemon, job *jobs.Job) {
	t.Helper()
	if _, err := d.snoozeWakeHandler(context.Background(), job); err != nil {
		t.Fatalf("run snooze wake job: %v", err)
	}
}

func snoozeUntil(d *Daemon, id string, until time.Time) {
	d.handleSnoozeTurn(&protocol.SnoozeTurnMessage{
		SessionID: id,
		Until:     until.Format(time.RFC3339Nano),
	})
}

func snoozedUntil(t *testing.T, d *Daemon, id string) string {
	t.Helper()
	session := d.sessionForBroadcast(d.store.Get(id))
	if session == nil {
		t.Fatalf("session %s not found", id)
	}
	return protocol.Deref(session.TurnSnoozedUntil)
}

func TestSnoozeSuppressesTurnsUntilItsDeadline(t *testing.T) {
	d := newSnoozeDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateWaitingInput)
	if !owed(t, d, "s1") {
		t.Fatal("setup: a waiting session owes no turn")
	}

	snoozeUntil(d, "s1", time.Now().Add(time.Hour))
	if owed(t, d, "s1") {
		t.Fatal("the session still owes a turn immediately after snoozing")
	}
	if snoozedUntil(t, d, "s1") == "" {
		t.Fatal("no deadline on the wire, so the row has nothing to park under")
	}

	for _, state := range []string{
		protocol.StateWorking,
		protocol.StateWaitingInput,
		protocol.StatePendingApproval,
		protocol.StateIdle,
	} {
		moveTo(d, "s1", state)
		if owed(t, d, "s1") {
			t.Fatalf("state %s opened a turn on a snoozed session", state)
		}
	}
}

func TestWakeOpensTheTurnAtTheWakeInstant(t *testing.T) {
	d := newSnoozeDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateWaitingInput)
	original := protocol.Deref(d.sessionForBroadcast(d.store.Get("s1")).TurnOpenedAt)

	snoozeUntil(d, "s1", time.Now().Add(time.Hour))
	moveTo(d, "s1", protocol.StateIdle)

	wakeAt := time.Now().Add(time.Hour)
	d.wakeSnooze("s1", wakeAt, "test")

	if !owed(t, d, "s1") {
		t.Fatal("the woken session owes no turn although it is sitting idle")
	}
	opened := protocol.Deref(d.sessionForBroadcast(d.store.Get("s1")).TurnOpenedAt)
	if opened == original {
		t.Error("the turn kept its pre-snooze age, so it wakes to the head of the queue")
	}
	parsed, err := time.Parse(time.RFC3339Nano, opened)
	if err != nil {
		t.Fatalf("turn_opened_at %q does not parse: %v", opened, err)
	}
	if !parsed.Equal(wakeAt.UTC()) {
		t.Errorf("turn opened at %s, want the wake instant %s", parsed, wakeAt.UTC())
	}
	if snoozedUntil(t, d, "s1") != "" {
		t.Error("the deadline survived the wake")
	}
}

func TestWakeOpensNoTurnWhileTheAgentIsWorking(t *testing.T) {
	d := newSnoozeDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateWorking)
	snoozeUntil(d, "s1", time.Now().Add(time.Hour))
	d.wakeSnooze("s1", time.Now(), "test")

	if owed(t, d, "s1") {
		t.Fatal("waking a working agent opened a turn")
	}
	moveTo(d, "s1", protocol.StateWaitingInput)
	if !owed(t, d, "s1") {
		t.Error("the session stayed suppressed after the snooze was cleared")
	}
}

func TestBreakThroughStatesEndTheSnooze(t *testing.T) {
	tests := []struct {
		name   string
		state  string
		reason sessionstate.Reason
		breaks bool
	}{
		{"stuck", protocol.StateUnknown, sessionstate.ReasonStuck, true},
		{"process exited", protocol.StateIdle, sessionstate.ReasonProcessExited, true},
		{"an ordinary finished run", protocol.StateIdle, sessionstate.ReasonClassifierVerdict, false},
		{"a question", protocol.StateWaitingInput, sessionstate.ReasonQuestionOpen, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newSnoozeDaemon(t)
			addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

			moveTo(d, "s1", protocol.StateWorking)
			snoozeUntil(d, "s1", time.Now().Add(time.Hour))

			d.recordStateReason("s1", sessionstate.Resolution{
				State:  protocol.SessionState(tt.state),
				Reason: tt.reason,
			})
			moveTo(d, "s1", tt.state)

			if got := owed(t, d, "s1"); got != tt.breaks {
				t.Errorf("owed = %v, want %v", got, tt.breaks)
			}
			stillSnoozed := snoozedUntil(t, d, "s1") != ""
			if stillSnoozed == tt.breaks {
				t.Errorf("snooze live = %v after a state that breaks=%v; a break-through must consume the deferral",
					stillSnoozed, tt.breaks)
			}
		})
	}
}

func TestSnoozingAWorkingAgentSuppressesTheTurnItWouldOpen(t *testing.T) {
	d := newSnoozeDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateWorking)
	if owed(t, d, "s1") {
		t.Fatal("setup: a working session owes a turn")
	}
	snoozeUntil(d, "s1", time.Now().Add(time.Hour))

	moveTo(d, "s1", protocol.StateIdle)
	if owed(t, d, "s1") {
		t.Error("the finished run opened a turn although the agent was deferred")
	}
}

func TestSnoozeWakeJobIsReconciledAfterRestart(t *testing.T) {
	d := newSnoozeDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")
	moveTo(d, "s1", protocol.StateIdle)

	now := time.Now()
	deadline := now.Add(-time.Minute)
	if !d.store.SnoozeTurn("s1", deadline, now) {
		t.Fatal("setup: the snooze was not stored")
	}
	d.reconcileSnoozeWakeJobs()
	runSnoozeJob(t, d, queuedSnoozeJob(t, d, "s1"))

	if !owed(t, d, "s1") {
		t.Error("the overdue restart job left an idle session settled")
	}
	if snoozedUntil(t, d, "s1") != "" {
		t.Error("the overdue restart job left the deadline stored")
	}
}

func TestSnoozeWakeReconciliationFailsOpenWhenSchedulingFails(t *testing.T) {
	d := newTurnDaemon(t)
	d.setJobQueue(jobs.New(jobs.Options{Store: d.newSQLJobStore()}))
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")
	moveTo(d, "s1", protocol.StateIdle)

	now := time.Now()
	if !d.store.SnoozeTurn("s1", now.Add(time.Hour), now) {
		t.Fatal("setup: the snooze was not stored")
	}
	d.reconcileSnoozeWakeJobs()

	if !owed(t, d, "s1") {
		t.Error("the unscheduled snooze left the idle session settled")
	}
	if got := d.store.TurnStamps("s1").SnoozedUntil; !got.IsZero() {
		t.Errorf("the unscheduled snooze left its deadline stored: %s", got)
	}
}

func TestSnoozeFailsOpenWhenJobQueueFailsToStart(t *testing.T) {
	d := newTurnDaemon(t)
	lockHolder := jobs.New(jobs.Options{Store: d.newSQLJobStore()})
	if err := lockHolder.Start(); err != nil {
		t.Fatalf("start lock holder: %v", err)
	}
	t.Cleanup(lockHolder.Stop)

	d.startJobQueue()
	if runner := d.jobQueueRef(); runner != nil {
		t.Fatal("the failed job queue remained available")
	}

	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")
	moveTo(d, "s1", protocol.StateIdle)
	snoozeUntil(d, "s1", time.Now().Add(time.Hour))

	if !owed(t, d, "s1") {
		t.Error("the unscheduled snooze left the idle session settled")
	}
	if got := d.store.TurnStamps("s1").SnoozedUntil; !got.IsZero() {
		t.Errorf("the unscheduled snooze left its deadline stored: %s", got)
	}
}

func TestSnoozeWakeJobOpensAnAttentionTurn(t *testing.T) {
	d := newSnoozeDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")
	moveTo(d, "s1", protocol.StateWaitingInput)

	snoozeUntil(d, "s1", time.Now().Add(-time.Minute))
	if owed(t, d, "s1") {
		t.Fatal("the session still owes a turn immediately after snoozing")
	}
	runSnoozeJob(t, d, queuedSnoozeJob(t, d, "s1"))

	if !owed(t, d, "s1") {
		t.Error("the due snooze job did not reopen the waiting session's turn")
	}
}

func TestResnoozingReplacesTheQueuedWake(t *testing.T) {
	d := newSnoozeDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")
	moveTo(d, "s1", protocol.StateWaitingInput)

	first := time.Now().Add(time.Minute)
	later := time.Now().Add(time.Hour)
	snoozeUntil(d, "s1", first)
	stale := queuedSnoozeJob(t, d, "s1")
	snoozeUntil(d, "s1", later)
	current := queuedSnoozeJob(t, d, "s1")

	var payload snoozeWakePayload
	if err := current.DecodePayload(&payload); err != nil {
		t.Fatalf("decode current wake: %v", err)
	}
	if !payload.Deadline.Equal(later) {
		t.Fatalf("queued deadline = %s, want %s", payload.Deadline, later)
	}
	runSnoozeJob(t, d, stale)

	if owed(t, d, "s1") {
		t.Error("the stale job reopened a session with a later snooze")
	}
	if got := d.store.TurnStamps("s1").SnoozedUntil; !got.Equal(later) {
		t.Errorf("live deadline = %s, want %s", got, later)
	}
}

func TestSnoozeCancelsAPendingAutoSettle(t *testing.T) {
	d := newSnoozeDaemon(t)
	d.store.SetSetting(SettingAutoSettleEnabled, "true")
	d.store.SetSetting(SettingAutoSettleArmSeconds, "5")
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateWaitingInput)
	creditUserInputForNextWorking(t, d, "s1")
	moveTo(d, "s1", protocol.StateWorking)

	d.autoSettleMu.Lock()
	pending := len(d.autoSettleTimers)
	d.autoSettleMu.Unlock()
	if pending == 0 {
		t.Fatal("setup: no auto-settle armed")
	}

	snoozeUntil(d, "s1", time.Now().Add(time.Hour))

	d.autoSettleMu.Lock()
	pending = len(d.autoSettleTimers)
	d.autoSettleMu.Unlock()
	if pending != 0 {
		t.Error("the auto-settle timer survived the snooze")
	}
}

func TestALapsedDeadlineCannotSuppressANewTurnBeforeTheJobRuns(t *testing.T) {
	d := newSnoozeDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")
		moveTo(d, "s1", protocol.StateWorking)

		snoozeUntil(d, "s1", time.Now().Add(time.Minute))
		time.Sleep(time.Hour)
		moveTo(d, "s1", protocol.StateIdle)

		if !owed(t, d, "s1") {
			t.Error("the lapsed stamp suppressed the idle turn while its job was delayed")
		}
		if got := d.store.TurnStamps("s1").SnoozedUntil; !got.IsZero() {
			t.Errorf("lapsed deadline survived the attention transition: %s", got)
		}
		job, err := d.jobQueueRef().GetByKey(snoozeWakeKind, "s1")
		if err != nil {
			t.Fatalf("get lapsed snooze job: %v", err)
		}
		if job != nil {
			t.Errorf("lapsed snooze job still queued in state %s", job.State)
		}
	})
}

func TestSnoozeRejectsAnUnparseableDeadline(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")
	moveTo(d, "s1", protocol.StateWaitingInput)

	d.handleSnoozeTurn(&protocol.SnoozeTurnMessage{SessionID: "s1", Until: "next tuesday"})

	if !owed(t, d, "s1") {
		t.Error("a malformed deadline settled the turn anyway")
	}
	if snoozedUntil(t, d, "s1") != "" {
		t.Error("a malformed deadline was stored")
	}
}
