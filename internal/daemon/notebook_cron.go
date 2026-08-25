package daemon

import (
	"context"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/notebook"
)

const (
	notebookCronKind = "notebook_cron"

	defaultNotebookCronFrequency = "0 3 * * *"

	defaultNotebookCronInterval = time.Minute

	// A tripwire, not a budget: the tick is sub-millisecond work, so one still
	// running here is wedged.
	notebookCronTickTimeout = 30 * time.Second
)

// legacyNotebookDreaming*Key exist ONLY for migrateNotebookCronSettingKeys to
// copy forward / reap. Never read them anywhere else.
const (
	legacyNotebookDreamingFrequencyKey = "notebook.dreaming.frequency"
	legacyNotebookDreamingTimezoneKey  = "notebook.dreaming.timezone"
	legacyNotebookDreamingEnabledKey   = "notebook.dreaming.enabled"
)

// migrateNotebookCronSettingKeys renames the persisted notebook.dreaming.* keys to
// notebook.cron.*. It runs at every daemon start, so it MUST stay idempotent.
func (d *Daemon) migrateNotebookCronSettingKeys() {
	if d.store == nil {
		return
	}
	d.renameSettingKey(legacyNotebookDreamingFrequencyKey, SettingNotebookCronFrequency)
	d.renameSettingKey(legacyNotebookDreamingTimezoneKey, SettingNotebookCronTimezone)
	d.store.DeleteSetting(legacyNotebookDreamingEnabledKey)
}

func (d *Daemon) notebookCronFrequency() string {
	if d.store != nil {
		if f := strings.TrimSpace(d.store.GetSetting(SettingNotebookCronFrequency)); f != "" {
			return f
		}
	}
	return defaultNotebookCronFrequency
}

func (d *Daemon) notebookCronSchedule() (cron.Schedule, string, error) {
	raw := d.notebookCronFrequency()
	sched, err := cron.ParseStandard(raw)
	if err != nil {
		return nil, raw, err
	}
	return sched, raw, nil
}

func (d *Daemon) notebookCronLocation() *time.Location {
	if d.store == nil {
		return time.Local
	}
	tz := strings.TrimSpace(d.store.GetSetting(SettingNotebookCronTimezone))
	if tz == "" {
		return time.Local
	}
	if loc, err := time.LoadLocation(tz); err == nil {
		return loc
	}
	d.logf("notebook cron: invalid timezone %q, using local time", tz)
	return time.Local
}

func parseNotebookCronTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func (d *Daemon) notebookCronHandler(_ context.Context, _ *jobs.Job) (any, error) {
	d.notebookCronTick(time.Now())
	return nil, nil
}

func (d *Daemon) notebookCronTick(now time.Time) {
	d.enqueueDueDailyNarrates(now)
}

// Anchor-FIRST ordering: the persisted anchor advances BEFORE enqueueing, so an
// enqueue failure skips one idempotent day rather than re-firing every tick.
func (d *Daemon) enqueueDueDailyNarrates(now time.Time) {
	sched, raw, err := d.notebookCronSchedule()
	if err != nil {
		d.logf("daily narrate: invalid frequency %q: %v", raw, err)
		return
	}
	root, err := d.notebookRoot()
	if err != nil {
		d.logf("daily narrate: resolve root: %v", err)
		return
	}

	// Resolve the runner BEFORE touching state: a missing/disabled runner must not
	// advance the anchor and silently skip the day.
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return
	}

	state, err := notebook.LoadNarrateCronState(root)
	if err != nil {
		d.logf("daily narrate: load state: %v", err)
		return
	}

	anchor, ok := parseNotebookCronTime(state.ScheduledFrom)
	if !ok {
		state.ScheduledFrom = now.UTC().Format(time.RFC3339)
		if err := notebook.SaveNarrateCronState(root, state); err != nil {
			d.logf("daily narrate: anchor schedule: %v", err)
		}
		return
	}

	loc := d.notebookCronLocation()
	next := sched.Next(anchor.In(loc))
	if next.IsZero() {
		// Unsatisfiable schedule: never-due, since always-due re-narrates every tick.
		d.logf("daily narrate: frequency %q never occurs; skipping", raw)
		return
	}
	if next.After(now) {
		return
	}

	state.ScheduledFrom = now.UTC().Format(time.RFC3339)
	if err := notebook.SaveNarrateCronState(root, state); err != nil {
		d.logf("daily narrate: advance anchor: %v", err)
	}

	for _, workspaceID := range d.drainNotebookNarrateActivity() {
		if d.store.GetWorkspace(workspaceID) == nil {
			continue
		}
		d.enqueueDailyNarrateWorkspace(workspaceID)
	}
}

// Clearing on drain is what makes a no-activity day enqueue nothing.
func (d *Daemon) drainNotebookNarrateActivity() []string {
	d.notebookNarrateActivityMu.Lock()
	defer d.notebookNarrateActivityMu.Unlock()
	if len(d.notebookNarrateActivity) == 0 {
		return nil
	}
	ids := make([]string, 0, len(d.notebookNarrateActivity))
	for id := range d.notebookNarrateActivity {
		ids = append(ids, id)
	}
	d.notebookNarrateActivity = make(map[string]struct{})
	return ids
}
