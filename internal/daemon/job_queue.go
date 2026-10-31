package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/store"
)

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

func (d *Daemon) refreshJobQueueRole() {
	select {
	case d.jobQueueRoleChanged <- struct{}{}:
	default:
	}
}

func (d *Daemon) startJobQueue(home bool) error {
	var queueStore jobs.Store
	var sqlQueue *store.JobStore
	if d.store != nil {
		sqlQueue = d.newSQLJobStore()
		queueStore = sqlQueue
	}

	if home {
		d.importLegacyTasks()
	}
	opts := jobs.Options{Log: d.logf, Store: queueStore, Clock: d.clock}
	runner := jobs.New(opts)
	if !runner.Disabled() {
		if err := d.registerTaskWithFailureRenderer(
			runner,
			sessionTitleKind,
			d.sessionTitleHandler,
			jobs.HandlerConfig{Timeout: sessionTitleTimeout}, d.renderSessionTitleFailure,
		); err != nil {
			d.logf("session title: register session_title: %v", err)
		}
		d.registerWorktreeSweepCron(runner)
	}
	if !runner.Disabled() && home {

		if err := d.registerTaskWithFailureRenderer(
			runner,
			sessionActivityKind,
			d.sessionActivityHandler,
			jobs.HandlerConfig{
				Timeout:       sessionActivityTimeout,
				MaxConcurrent: sessionActivityConcurrency,
			}, d.renderSessionActivityFailure,
		); err != nil {
			d.logf("session activity: register session_activity: %v", err)
		}
		if err := d.registerTaskWithFailureRenderer(
			runner,
			gardenReviewClassifyKind,
			d.gardenReviewClassifyHandler,
			jobs.HandlerConfig{
				Timeout:       gardenReviewClassifyTimeout,
				MaxConcurrent: gardenReviewClassifyConcurrency,
			}, d.renderGardenReviewFailure,
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
		if err := runner.RegisterCron(
			analyticsPruneKind,
			analyticsPruneInterval,
			d.analyticsPruneHandler,
			jobs.HandlerConfig{Timeout: analyticsPruneTimeout},
		); err != nil {
			d.logf("analytics: register outbox prune tick: %v", err)
		}
		if err := runner.RegisterCron(
			analyticsShipKind,
			analyticsShipInterval,
			d.analyticsShipHandler,
			jobs.HandlerConfig{Timeout: analyticsShipTimeout},
		); err != nil {
			d.logf("analytics: register upload tick: %v", err)
		}
		if err := runner.RegisterCron(
			analyticsSampleKind,
			analyticsSampleInterval,
			d.analyticsSampleHandler,
			jobs.HandlerConfig{Timeout: analyticsSampleTimeout},
		); err != nil {
			d.logf("analytics: register resource sample tick: %v", err)
		}
		if err := runner.RegisterCron(
			askRepliesForgetKind,
			askRepliesForgetInterval,
			d.askRepliesForgetHandler,
			jobs.HandlerConfig{Timeout: askRepliesForgetTimeout},
		); err != nil {
			d.logf("asks: register reply forget tick: %v", err)
		}
		if err := runner.RegisterCron(
			ventsPruneKind,
			ventsPruneInterval,
			d.ventsPruneHandler,
			jobs.HandlerConfig{Timeout: ventsPruneTimeout},
		); err != nil {
			d.logf("vents: register prune tick: %v", err)
		}
		d.registerCrewLifecycleCron(runner)
		d.registerSessionPullRequestRefreshCron(runner)
		d.registerConversationKeepCron(runner)
		d.registerCodexUpdateCheckCron(runner)
		if err := runner.RegisterCron(
			automationScheduleKind,
			automationScheduleInterval(),
			d.automationScheduleHandler,
			jobs.HandlerConfig{Timeout: automationScheduleTickTimeout},
		); err != nil {
			d.logf("automation schedule: register tick: %v", err)
		}
	}
	runner.OnChange(func(jobID string) { d.publishFact(FactTaskChanged, jobID, nil) })
	runner.OnTerminalFailure(func(j *jobs.Job) {
		d.notifyTaskTerminalFailure(j)
		// The runner outlives the lifetime and reports a death once; nothing redoes it, so it lands inline.
		if !d.life.Go("failGardenReviewJob", func() { d.failGardenReviewJob(j) }) {
			d.failGardenReviewJob(j)
		}
	})
	if !home && sqlQueue != nil {
		sqlQueue.OnlyKinds(runner.RegisteredKinds())
	}
	if err := runner.Start(); err != nil {
		return fmt.Errorf("start background jobs: %w", err)
	}
	d.setJobQueue(runner)
	if !home {
		return nil
	}
	if _, err := runner.Enqueue(conversationKeepKind+"_tick", jobs.EnqueueOptions{UniqueKey: jobs.CronKey, RunNow: true}); err != nil {
		d.logf("conversation keep: enqueue startup: %v", err)
	}
	d.queueCodexUpdateCheck()
	d.resumeGardenReviews()
	d.settleHarvestConditions()
	return nil
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
