package daemon_test

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAScheduledAutomationFiresOncePerDueInstantWhileEnabled(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		definitions := map[string]int{}
		schedules := map[string]string{
			"minutely-latest": automationScheduleSpec(w, "minutely-latest", "gone", "fresh", "latest"),
			"minutely-skip":   automationScheduleSpec(w, "minutely-skip", "gone", "fresh", "skip"),
			"paused":          automationScheduleSpec(w, "paused", "gone", "fresh", "latest"),
		}
		automationUnreachableFolder(t, w, "gone", func() {
			for name, spec := range schedules {
				definitions[name] = applyAutomation(t, cli, spec).ID
			}
		})
		setAutomationEnabled(t, cli, definitions["paused"], false)

		w.advance(3*time.Minute + 30*time.Second)

		for _, id := range []string{"minutely-latest", "minutely-skip"} {
			runs := automationRuns(t, cli, definitions[id])
			keys := automationOccurrenceKeys(runs)
			due := []string{"scheduled:2000-01-01T00:03:00Z", "scheduled:2000-01-01T00:02:00Z", "scheduled:2000-01-01T00:01:00Z"}
			if len(keys) < 2 || keys[0] != due[0] || !slices.Contains(keys, due[1]) || automationHasDuplicate(keys) ||
				slices.ContainsFunc(keys, func(key string) bool { return !slices.Contains(due, key) }) {
				t.Errorf("%s fired %v, want each due instant of %v at most once, through 00:02 and newest 00:03", id, keys, due)
			}
			var threads []string
			for _, run := range runs {
				threads = append(threads, "seed:"+protocol.Deref(run.SeedID), string("session:"+protocol.Deref(run.SessionID)))
			}
			if slices.Contains(threads, "seed:") || slices.Contains(threads, "session:") || automationHasDuplicate(threads) {
				t.Errorf("%s's fresh runs do not each start their own seed and session: %v", id, threads)
			}
		}
		if paused := automationRuns(t, cli, definitions["paused"]); len(paused) != 0 {
			t.Errorf("the paused automation fired %v, want nothing", automationOccurrenceKeys(paused))
		}
	})
}

func TestAScheduleWithoutRunsPreviewsTheTimesTheSchedulerWouldUse(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		preview := func(cron, timeZone string) protocol.AutomationSchedulePreviewResultMessage {
			t.Helper()
			id := cron + " " + timeZone
			return testworld.Request(app, protocol.AutomationSchedulePreviewMessage{Cmd: protocol.CmdAutomationSchedulePreview, Cron: cron, TimeZone: timeZone, RequestID: protocol.Ptr(id)},
				protocol.EventAutomationSchedulePreviewResult, automationAnswer[protocol.AutomationSchedulePreviewResultMessage](id))
		}

		hourly := preview("0 * * * *", "UTC")
		if want := []string{"2000-01-01T01:00:00Z", "2000-01-01T02:00:00Z", "2000-01-01T03:00:00Z", "2000-01-01T04:00:00Z", "2000-01-01T05:00:00Z"}; !hourly.Success || !slices.Equal(hourly.Times, want) {
			t.Errorf("hourly preview = %+v, want %v", hourly, want)
		}
		if zoned := preview("30 9 * * *", "Europe/Stockholm"); !zoned.Success || len(zoned.Times) != 5 || zoned.Times[0] != "2000-01-01T09:30:00+01:00" {
			t.Errorf("zoned preview = %+v, want five times starting at 09:30 Stockholm time", zoned)
		}
		for _, tc := range []struct{ name, cron, timeZone, want string }{
			{"an invalid cron", "99 99 99 99 99", "UTC", "must be a valid cron expression"},
			{"a cron that never occurs", "0 0 31 2 *", "UTC", "never occurs"},
			{"an unknown time zone", "0 * * * *", "Mars/Olympus", "must be an IANA timezone"},
		} {
			refused := preview(tc.cron, tc.timeZone)
			if refused.Success || len(refused.Times) != 0 || !strings.Contains(protocol.Deref(refused.Error), tc.want) {
				t.Errorf("%s previewed as %+v, want an error containing %q", tc.name, refused, tc.want)
			}
		}
	})
}

func TestAutomationSummariesNameTheNextRunOnlyWhileAScheduleIsEnabled(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		if err := os.MkdirAll(w.Path("check"), 0o755); err != nil {
			t.Fatal(err)
		}
		next := func(name string) string {
			t.Helper()
			result, err := cli.AutomationDefinitions()
			if err != nil {
				t.Fatal(err)
			}
			for _, definition := range result.Definitions {
				if definition.Name == name {
					return protocol.Deref(definition.NextRunAt)
				}
			}
			t.Fatalf("no automation named %s in %+v", name, result.Definitions)
			return ""
		}
		spec := func(name, cron string) string {
			return fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
name: %s
trigger: {type: scheduled, schedule: {cron: %q, time_zone: UTC}, continuity: fresh, catch_up: latest}
prompt: Check.
launch: {driver: codex}
location: {type: directory, path: %q}
`, name, cron, w.Path("gone"))
		}
		var hourly protocol.AutomationDefinitionSummary
		var interval int
		automationUnreachableFolder(t, w, "gone", func() {
			hourly = applyAutomation(t, cli, spec("hourly", "0 * * * *"))
			interval = applyAutomation(t, cli, spec("interval", "@every 90m")).ID
		})
		if got := protocol.Deref(hourly.NextRunAt); got != "2000-01-01T01:00:00Z" {
			t.Errorf("apply reported next run %q, want 01:00", got)
		}
		applyAutomation(t, cli, manualAutomation(w, "Check locally."))

		w.advance(30 * time.Minute)

		predicted := next("interval")
		if got := next("hourly"); got != "2000-01-01T01:00:00Z" {
			t.Errorf("hourly next run = %q, want 01:00", got)
		}
		w.advance(time.Hour + 2*time.Minute)
		if got := automationOccurrenceKeys(automationRuns(t, cli, interval)); !slices.Equal(got, []string{"scheduled:" + predicted}) {
			t.Errorf("@every fired %v, but its next run read %s an hour earlier", got, predicted)
		}
		if got := next("Manual check"); got != "" {
			t.Errorf("a manual automation has next run %q", got)
		}
		if disabled := setAutomationEnabled(t, cli, hourly.ID, false); disabled.NextRunAt != nil {
			t.Errorf("a disabled schedule has next run %q", *disabled.NextRunAt)
		}
		if got := next("hourly"); got != "" {
			t.Errorf("a disabled schedule lists next run %q", got)
		}
	})
}

func automationHasDuplicate(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}

func TestAfterDowntimeAScheduleCatchesUpOnlyAsItsPolicyAllows(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cron     string
		catchUp  string
		downtime time.Duration
		want     []string
	}{
		{"latest fires the newest missed instant once", "0 * * * *", "latest", 2*time.Hour + 33*time.Minute, []string{"scheduled:2000-01-01T02:00:00Z"}},
		{"skip fires nothing long past its instant", "0 * * * *", "skip", 2*time.Hour + 33*time.Minute, nil},
		{"skip fires an instant a minute or two old", "0 * * * *", "skip", time.Hour, []string{"scheduled:2000-01-01T01:00:00Z"}},
		{"skip lets an instant several minutes past its grace go", "0 * * * *", "skip", time.Hour + 7*time.Minute, nil},
		{"a gap of more instants than the cap fires nothing", "@every 1s", "latest", 12 * 24 * time.Hour, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inBubble(t, func(t *testing.T, w *world) {
				automationUnreachableFolder(t, w, "gone", func() {
					applyAutomation(t, w.Client(), fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
name: Sweep
trigger: {type: scheduled, schedule: {cron: %q, time_zone: UTC}, continuity: fresh, catch_up: %s}
prompt: Sweep.
launch: {driver: claude}
location: {type: directory, path: %q}
`, tc.cron, tc.catchUp, w.Path("gone")))
				})
				w.advance(time.Minute)
				setAutomationEnabled(t, w.Client(), 1, false)
				w.stop()
				w.advance(tc.downtime)
				w.start()
				w.advance(30 * time.Second)
				setAutomationEnabled(t, w.Client(), 1, true)
				w.advance(30 * time.Second)

				if got := automationOccurrenceKeys(automationRuns(t, w.Client(), 1)); !slices.Equal(got, tc.want) {
					t.Errorf("after %s down the schedule fired %v, want %v", tc.downtime, got, tc.want)
				}
			})
		})
	}
}

func automationUnreachableFolder(t *testing.T, w *world, dir string, apply func()) {
	t.Helper()
	if err := os.MkdirAll(w.Path(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	apply()
	if err := os.Remove(w.Path(dir)); err != nil {
		t.Fatal(err)
	}
}

func automationOccurrenceKeys(runs []protocol.AutomationRunSummary) []string {
	var keys []string
	for _, run := range runs {
		keys = append(keys, protocol.Deref(run.OccurrenceKey))
	}
	return keys
}

func TestEditingAScheduledAutomationKeepsItsSeedAndReplacesItsSessionOnlyForAContractChange(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		definitionID := 0
		edit := func(name, catchUp, dir string) {
			automationUnreachableFolder(t, w, dir, func() {
				spec := automationSingletonSpec(w, name, catchUp, dir)
				if definitionID != 0 {
					spec = automationEditSpec(definitionID, spec)
				}
				definitionID = applyAutomation(t, cli, spec).ID
			})
		}
		edit("Sweep", "latest", "first")
		w.advance(2*time.Minute + 30*time.Second)
		edit("Sweep, renamed", "skip", "first")
		w.advance(2 * time.Minute)
		edit("Sweep, renamed", "skip", "second")
		w.advance(2 * time.Minute)
		edit("Sweep, renamed", "skip", "first")
		w.advance(2 * time.Minute)

		runs := automationRuns(t, cli, 1)
		if got, want := automationOccurrenceKeys(runs), []string{"scheduled:2000-01-01T00:08:00Z", "scheduled:2000-01-01T00:06:00Z", "scheduled:2000-01-01T00:04:00Z", "scheduled:2000-01-01T00:02:00Z"}; !slices.Equal(got, want) {
			t.Fatalf("the automation fired %v, want %v", got, want)
		}
		reverted, moved, renamed, original := runs[0], runs[1], runs[2], runs[3]
		if *renamed.SeedID != *original.SeedID || *renamed.SessionID != *original.SessionID {
			t.Errorf("after a rename the run is on %s/%s, want the thread %s/%s", *renamed.SeedID, *renamed.SessionID, *original.SeedID, *original.SessionID)
		}
		if *moved.SeedID != *original.SeedID || *moved.SessionID == *original.SessionID {
			t.Errorf("after a location change the run is on %s/%s, want a new session continuing seed %s", *moved.SeedID, *moved.SessionID, *original.SeedID)
		}
		if *reverted.SeedID != *original.SeedID || *reverted.SessionID == *original.SessionID || *reverted.SessionID == *moved.SessionID {
			t.Errorf("after moving back to the first location the run is on %s/%s, want a third session continuing seed %s", *reverted.SeedID, *reverted.SessionID, *original.SeedID)
		}
	})
}

func automationSingletonSpec(w *world, name, catchUp, dir string) string {
	return fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
name: %s
trigger: {type: scheduled, schedule: {cron: "*/2 * * * *", time_zone: UTC}, continuity: singleton, catch_up: %s}
prompt: Sweep.
launch: {driver: claude}
location: {type: directory, path: %q}
`, name, catchUp, w.Path(dir))
}
