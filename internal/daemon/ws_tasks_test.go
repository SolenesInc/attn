package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
)

const testTaskKind = "test_task"

func installInstrumentedTaskRunner(t *testing.T, d *Daemon) (*jobs.Runner, *atomic.Bool) {
	t.Helper()
	shouldFail := &atomic.Bool{}
	runner := jobs.New(jobs.Options{
		Store:        newTestJobStore(t, d),
		Log:          func(string, ...interface{}) {},
		PollInterval: 2 * time.Millisecond,
		MaxAttempts:  1,
		BackoffBase:  time.Hour,
		BackoffCap:   time.Hour,
	})
	if err := runner.Register(testTaskKind, func(context.Context, *jobs.Job) (any, error) {
		if shouldFail.Load() {
			return nil, context.DeadlineExceeded
		}
		return nil, nil
	}); err != nil {
		t.Fatalf("register %s: %v", testTaskKind, err)
	}
	runner.OnChange(func(taskID string) { d.publishFact(FactTaskChanged, taskID, nil) })
	if err := runner.Start(); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(runner.Stop)
	d.jobQueue = runner
	return runner, shouldFail
}

func TestSendTaskRetryWSResultRequeuesDeadTask(t *testing.T) {
	d := newNotebookDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		runner, shouldFail := installInstrumentedTaskRunner(t, d)
		shouldFail.Store(true)

		if _, err := runner.Enqueue(testTaskKind, jobs.EnqueueOptions{UniqueKey: "ws-fail"}); err != nil {
			t.Fatalf("enqueue ws-fail: %v", err)
		}
		dead := requireTaskState(t, d, testTaskKind, "ws-fail", jobs.StateDead)
		if dead.LastError == "" {
			t.Fatalf("dead task has no last_error: %+v", dead)
		}

		before := time.Now()
		client := &wsClient{send: make(chan outboundMessage, 4)}
		d.sendTaskRetryWSResult(client, "retry-1", dead.ID)

		var msg protocol.TaskRetryResultMessage
		readNotebookWSEvent(t, client.send, &msg)
		if msg.Event != protocol.EventTaskRetryResult || msg.RequestID != "retry-1" || !msg.Success {
			t.Fatalf("retry result = %+v, want success task_retry_result for retry-1", msg)
		}
		if msg.Task == nil || msg.Task.State != string(jobs.StateQueued) {
			t.Fatalf("retry result task = %+v, want state queued", msg.Task)
		}
		if msg.Task.Attempts != 0 {
			t.Fatalf("retry result attempts = %d, want 0 (reset)", msg.Task.Attempts)
		}
		nextAt, err := time.Parse(time.RFC3339, msg.Task.NextAttemptAt)
		if err != nil {
			t.Fatalf("parse next_attempt_at %q: %v", msg.Task.NextAttemptAt, err)
		}
		if nextAt.Before(before.Add(-2*time.Second)) || nextAt.After(time.Now().Add(2*time.Second)) {
			t.Fatalf("next_attempt_at = %s, want ~now (between %s and %s)", nextAt, before, time.Now())
		}
	})
}

func requireTaskState(t *testing.T, d *Daemon, kind, subject string, want jobs.State) *jobs.Job {
	t.Helper()
	synctest.Wait()
	task, err := d.jobQueue.GetByKey(kind, subject)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task == nil || task.State != want {
		t.Fatalf("task %s:%s settled at %s, want %s (last=%+v)", kind, subject, taskState(task), want, task)
	}
	return task
}

func taskState(task *jobs.Job) jobs.State {
	if task == nil {
		return "<absent>"
	}
	return task.State
}
