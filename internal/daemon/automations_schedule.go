package daemon

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/store"
)

const scheduleDueInstantCap = 1_000_000

const scheduleSkipGrace = 5 * time.Minute

const automationScheduleKind = "automation_schedule"

const defaultAutomationScheduleInterval = time.Minute

const automationScheduleTickTimeout = 2 * time.Minute

func automationScheduleInterval() time.Duration {
	if v := strings.TrimSpace(os.Getenv("ATTN_AUTOMATION_SCHEDULE_INTERVAL")); v != "" {
		if dur, err := time.ParseDuration(v); err == nil && dur > 0 {
			return dur
		}
	}
	return defaultAutomationScheduleInterval
}

func (d *Daemon) automationScheduleHandler(ctx context.Context, _ *jobs.Job) (any, error) {
	d.observeDueSchedules(ctx, time.Now())
	return nil, nil
}

func (d *Daemon) observeDueSchedules(ctx context.Context, now time.Time) {
	if d.isRecovering() {
		return
	}
	release, held := d.life.Hold("observeDueSchedules")
	if !held {
		return
	}
	defer release()
	d.automationMu.Lock()
	persistErr := d.persistAutomationLaunchOutcomes()
	d.automationMu.Unlock()
	if persistErr != nil {
		d.logf("automation launch persistence: %v", persistErr)
	}
	definitions, err := d.store.ListAutomationDefinitions()
	if err != nil {
		d.logf("automation schedule observation list definitions: %v", err)
		return
	}
	var due []*dueAutomationSchedule
	for i := range definitions {
		definition := definitions[i]
		if !definition.Enabled {
			continue
		}
		var spec automation.DefinitionSpec
		if err := json.Unmarshal([]byte(definition.SpecJSON), &spec); err != nil {
			d.logf("automation schedule observation parse %d: %v", definition.ID, err)
			continue
		}
		if spec.Trigger.Type != "scheduled" {
			continue
		}
		if item := d.dueAutomationSchedule(definition, spec, now); item != nil {
			due = append(due, item)
		}
	}
	var checks sync.WaitGroup
	var env []string
	for _, item := range due {
		if item.spec.Trigger.Guard == "" {
			continue
		}
		if env == nil {
			env = d.loginShellEnvironment()
		}
		checks.Add(1)
		if !d.life.Go("automationGuard", func() {
			defer checks.Done()
			item.result = automation.RunGuard(ctx, item.spec.Location.Path, env, item.spec.Trigger.Guard)
			item.checked = true
		}) {
			checks.Done()
		}
	}
	checks.Wait()
	if ctx.Err() != nil {
		return
	}
	for _, item := range due {
		if item.spec.Trigger.Guard != "" {
			if !item.checked {
				continue
			}
			launch, err := d.recordAutomationGuardDecision(item, now)
			if err != nil {
				d.logf("automation guard check %d: %v", item.definition.ID, err)
				continue
			}
			if !launch {
				d.advanceAutomationSchedule(item.definition.ID, now)
				continue
			}
		}
		if err := d.claimAndDeliverScheduledRun(item.definition, item.spec, item.intended, now, item.result.Output); err != nil {
			continue
		}
		d.advanceAutomationSchedule(item.definition.ID, now)
	}
}

type dueAutomationSchedule struct {
	definition store.AutomationDefinition
	spec       automation.DefinitionSpec
	intended   time.Time
	result     automation.GuardResult
	checked    bool
}

func (d *Daemon) recordAutomationGuardDecision(item *dueAutomationSchedule, now time.Time) (bool, error) {
	previous, err := d.store.GetAutomationGuardCheck(item.definition.ID)
	if err != nil {
		return false, err
	}
	handled := ""
	if previous != nil && previous.Guard == item.spec.Trigger.Guard && (previous.HandledRunState == store.AutomationRunStatePending || previous.HandledRunState == store.AutomationRunStateDelivered) {
		handled = previous.HandledOutput
	}
	outcome := ""
	switch {
	case item.result.Failure != "":
		outcome = "failed"
		d.logf("automation guard %d failed: %s", item.definition.ID, item.result.Failure)
	case item.result.Output == "":
		outcome = "empty"
	case item.result.Output == handled:
		outcome = "unchanged"
	default:
		return true, nil
	}
	if err := d.store.RecordAutomationGuardSkip(item.definition.ID, item.spec.Trigger.Guard, outcome, item.result.Failure, now); err != nil {
		return false, err
	}
	d.broadcastAutomationsChanged(item.definition.ID)
	return false, nil
}

func (d *Daemon) advanceAutomationSchedule(definitionID int, now time.Time) {
	if err := d.store.SetAutomationScheduleCursor(definitionID, now); err != nil {
		d.logf("automation schedule observation advance %d: %v", definitionID, err)
	}
}

func (d *Daemon) dueAutomationSchedule(definition store.AutomationDefinition, spec automation.DefinitionSpec, now time.Time) *dueAutomationSchedule {
	if spec.Trigger.Schedule == nil {
		d.logf("automation schedule observation %d: scheduled trigger has no schedule", definition.ID)
		return nil
	}
	compiled, err := automation.CompileSchedule(*spec.Trigger.Schedule)
	if err != nil {
		d.logf("automation schedule observation compile %d: %v", definition.ID, err)
		return nil
	}
	cursor, ok, err := d.store.GetAutomationScheduleCursor(definition.ID)
	if err != nil {
		d.logf("automation schedule observation cursor %d: %v", definition.ID, err)
		return nil
	}
	if !ok {
		if err := d.store.SetAutomationScheduleCursor(definition.ID, now); err != nil {
			d.logf("automation schedule observation anchor %d: %v", definition.ID, err)
		}
		return nil
	}
	instants, ok := compiled.DueInstants(cursor, now, scheduleDueInstantCap)
	if !ok {
		d.logf("automation schedule observation %d: replay storm guard hit, cursor advanced to now", definition.ID)
		if err := d.store.SetAutomationScheduleCursor(definition.ID, now); err != nil {
			d.logf("automation schedule observation advance %d: %v", definition.ID, err)
		}
		return nil
	}
	if len(instants) == 0 {
		return nil
	}
	intended := instants[len(instants)-1]
	fire := true
	if spec.Trigger.CatchUp == "skip" {
		fire = now.Sub(intended) <= scheduleSkipGrace
	}
	if !fire {
		d.advanceAutomationSchedule(definition.ID, now)
		return nil
	}
	return &dueAutomationSchedule{definition: definition, spec: spec, intended: intended}
}

func (d *Daemon) claimAndDeliverScheduledRun(definition store.AutomationDefinition, spec automation.DefinitionSpec, intended, observedAt time.Time, guardOutput string) error {
	observationLock := d.automationObservationLock(definition.ID, "schedule", 0)
	observationLock.Lock()
	input := automation.NewScheduledInput(intended, observedAt)
	input.GuardOutput = guardOutput
	payload, err := json.Marshal(input)
	if err != nil {
		observationLock.Unlock()
		d.logf("automation schedule observation payload %d: %v", definition.ID, err)
		return err
	}
	effective, err := automation.Effective(spec, definition.Revision)
	if err != nil {
		observationLock.Unlock()
		d.logf("automation schedule observation snapshot %d: %v", definition.ID, err)
		return err
	}
	snapshotJSON, err := json.Marshal(effective)
	if err != nil {
		observationLock.Unlock()
		d.logf("automation schedule observation snapshot marshal %d: %v", definition.ID, err)
		return err
	}
	continuityKey := ""
	continuity, _ := automation.ResolvedTriggerPolicy(spec)
	if continuity == "singleton" {
		continuityKey = "singleton"
		if err := d.releaseUnresumableSingleton(definition.ID, effective.Launch.Agent); err != nil {
			observationLock.Unlock()
			d.logf("automation schedule observation release %d: %v", definition.ID, err)
			return err
		}
	}
	reservation, claimErr := d.newAutomationRunReservation(&definition)
	if claimErr != nil {
		observationLock.Unlock()
		return claimErr
	}
	var guardCheck *store.AutomationGuardCheck
	if spec.Trigger.Guard != "" {
		guardCheck = &store.AutomationGuardCheck{Guard: spec.Trigger.Guard, HandledOutput: guardOutput}
	}
	run, _, claimErr := d.store.ClaimScheduledAutomationRun(definition.ID, automation.ScheduledOccurrenceKey(intended), continuityKey, definition.Revision, string(payload), string(snapshotJSON), observedAt, reservation, guardCheck)
	observationLock.Unlock()
	if claimErr != nil {
		d.logf("automation schedule observation claim %d: %v", definition.ID, claimErr)
		return claimErr
	}
	d.broadcastAutomationsChanged(definition.ID)
	d.automationMu.Lock()
	current, loadErr := d.store.GetAutomationRun(run.ID)
	if loadErr == nil && current != nil && current.State == store.AutomationRunStatePending {
		if deliverErr := d.deliverObservedAutomationRun(current); deliverErr != nil {
			_, deliverErr = d.handleAutomationDeliveryError(current, deliverErr)
			loadErr = deliverErr
		}
	}
	d.automationMu.Unlock()
	if loadErr != nil {
		d.logf("automation schedule observation deliver %d: %v", definition.ID, loadErr)
	}
	return nil
}

func (d *Daemon) releaseUnresumableSingleton(definitionID int, agent string) error {
	binding, err := d.store.GetActiveAutomationContinuityBinding(definitionID, "singleton")
	if err != nil || binding == nil {
		return err
	}
	origin, err := d.store.GetAutomationRun(binding.OriginRunID)
	if err != nil || origin == nil || origin.State != store.AutomationRunStateDelivered {
		return err
	}
	if d.automationSessionIsLive(binding.SessionID) || d.conversationReady(agentdriver.Get(agent), strings.TrimSpace(d.store.GetResumeSessionID(binding.SessionID))) {
		return nil
	}
	return d.store.ReleaseAutomationContinuityBindings(definitionID, store.AutomationBindingReleasedUnresumable, time.Now())
}
