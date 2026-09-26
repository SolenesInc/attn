package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
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

type blockingSnoozeJobStore struct {
	jobs.Store
	acquiring chan struct{}
	release   chan struct{}
}

func (s *blockingSnoozeJobStore) AcquireLock() (string, error) {
	close(s.acquiring)
	<-s.release
	return "", jobs.ErrAlreadyRunning
}

func TestSnoozeFailsOpenWhileJobQueueStartFails(t *testing.T) {
	d := newTurnDaemon(t)
	store := &blockingSnoozeJobStore{
		Store:     d.newSQLJobStore(),
		acquiring: make(chan struct{}),
		release:   make(chan struct{}),
	}
	startDone := make(chan error, 1)
	go func() {
		startDone <- d.startJobQueueWithStore(store)
	}()
	<-store.acquiring
	if runner := d.jobQueueRef(); runner == nil || !runner.Disabled() {
		t.Fatal("the starting job queue was published before it acquired its lock")
	}

	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")
	moveTo(d, "s1", protocol.StateIdle)
	snoozeUntil(d, "s1", time.Now().Add(time.Hour))
	close(store.release)
	if err := <-startDone; !errors.Is(err, jobs.ErrAlreadyRunning) {
		t.Fatalf("startJobQueueWithStore() error = %v, want ErrAlreadyRunning", err)
	}

	if !owed(t, d, "s1") {
		t.Error("the unscheduled snooze left the idle session settled")
	}
	if got := d.store.TurnStamps("s1").SnoozedUntil; !got.IsZero() {
		t.Errorf("the unscheduled snooze left its deadline stored: %s", got)
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
