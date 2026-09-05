package daemon

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/jobs"
)

// Stable, not per-run temp: Claude spills tool outputs under ~/.claude/projects/<cwd-hash>, so unique cwds accumulate orphaned dirs attn must never reach in to clean.
func headlessScratchCwd() (string, error) {
	dir := filepath.Join(config.DataDir(), "headless-cwd")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func (d *Daemon) jobQueueRef() *jobs.Runner {
	d.jobQueueMu.RLock()
	defer d.jobQueueMu.RUnlock()
	return d.jobQueue
}

func (d *Daemon) setJobQueue(runner *jobs.Runner) {
	d.jobQueueMu.Lock()
	d.jobQueue = runner
	d.jobQueueMu.Unlock()
}

func (d *Daemon) startJobQueue() {
	var queueStore jobs.Store
	if d.store != nil {
		queueStore = d.newSQLJobStore()
	}
	d.startJobQueueWithStore(queueStore)
}

func (d *Daemon) startJobQueueWithStore(queueStore jobs.Store) {
	d.importLegacyTasks()
	opts := jobs.Options{Log: d.logf, Store: queueStore}
	runner := jobs.New(opts)
	if !runner.Disabled() {
		if err := d.registerSnoozeWakeHandler(runner); err != nil {
			d.logf("snooze wake: register session_snooze_wake: %v", err)
		}
		if err := runner.RegisterWith(
			sessionActivityKind,
			d.sessionActivityHandler,
			jobs.HandlerConfig{
				Timeout:       sessionActivityTimeout,
				MaxConcurrent: sessionActivityConcurrency,
			},
		); err != nil {
			d.logf("session activity: register session_activity: %v", err)
		}
		if err := runner.RegisterWith(
			reconcileKind,
			d.reconcileJobHandler,
			jobs.HandlerConfig{
				Timeout:       ticketReconcileTimeout(),
				MaxConcurrent: ticketReconcileConcurrency,
			},
		); err != nil {
			d.logf("ticket reconcile: register reconcile: %v", err)
		}
		if err := runner.RegisterWith(
			legacyTicketRecoveryKind,
			d.legacyTicketRecoveryHandler,
			jobs.HandlerConfig{Timeout: legacyTicketRecoveryTimeout},
		); err != nil {
			d.logf("legacy ticket recovery: register: %v", err)
		}
		if err := runner.RegisterWith(
			sessionTitleKind,
			d.sessionTitleHandler,
			jobs.HandlerConfig{Timeout: sessionTitleTimeout},
		); err != nil {
			d.logf("session title: register session_title: %v", err)
		}
		if err := runner.RegisterWith(
			gardenReviewClassifyKind,
			d.gardenReviewClassifyHandler,
			jobs.HandlerConfig{
				Timeout:       gardenReviewClassifyTimeout,
				MaxConcurrent: gardenReviewClassifyConcurrency,
			},
		); err != nil {
			d.logf("garden review: register classification: %v", err)
		}
		if err := runner.RegisterCron(
			sessionActivityScanKind,
			sessionActivityScanInterval,
			d.sessionActivityScanHandler,
			jobs.HandlerConfig{Timeout: sessionActivityScanTimeout},
		); err != nil {
			d.logf("session activity: register scan tick: %v", err)
		}
		if err := runner.RegisterCron(
			appInvocationRetentionKind,
			appInvocationRetentionInterval,
			d.appInvocationRetentionHandler,
			jobs.HandlerConfig{Timeout: appInvocationRetentionTimeout},
		); err != nil {
			d.logf("apps: register invocation retention tick: %v", err)
		}
		if age := d.busPinAlarmAge(); age > 0 {
			if err := runner.RegisterCron(
				busPinAlarmKind,
				busPinAlarmInterval(age),
				d.busPinAlarmHandler,
				jobs.HandlerConfig{Timeout: busPinAlarmTimeout},
			); err != nil {
				d.logf("bus: register retention-pin alarm tick: %v", err)
			}
		}
		d.registerCrewLifecycleCron(runner)
		d.registerSessionPullRequestRefreshCron(runner)
		if err := runner.RegisterCron(
			automationScheduleKind,
			automationScheduleInterval(),
			d.automationScheduleHandler,
			jobs.HandlerConfig{Timeout: automationScheduleTickTimeout},
		); err != nil {
			d.logf("automation schedule: register tick: %v", err)
		}
	}
	// OnChange may fire CONCURRENTLY from dispatch and in-flight runs; the handler
	// must be cheap, concurrency-safe and non-blocking.
	runner.OnChange(func(jobID string) { d.publishFact(FactTaskChanged, jobID, nil) })
	// OnTerminalFailure fires on the queue's goroutine; it must stay non-blocking.
	runner.OnTerminalFailure(func(j *jobs.Job) {
		d.notifyTaskTerminalFailure(j)
		go d.failGardenReviewJob(j)
	})
	if err := runner.Start(); err != nil {
		d.logf("jobs: THE JOB QUEUE DID NOT START: %v — no background work and no periodic ticks will run until the daemon is restarted", err)
		return
	}
	d.setJobQueue(runner)
	d.reconcileSnoozeWakeJobs()
	d.resumeGardenReviews()
	d.settleHarvestConditions()
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("unexpected trailing JSON")
}
