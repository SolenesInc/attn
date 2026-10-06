package daemon_test

import (
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
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

func TestEditingAScheduledAutomationKeepsItsThreadUntilItsContractChanges(t *testing.T) {
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

		runs := automationRuns(t, cli, 1)
		if got, want := automationOccurrenceKeys(runs), []string{"scheduled:2000-01-01T00:06:00Z", "scheduled:2000-01-01T00:04:00Z", "scheduled:2000-01-01T00:02:00Z"}; !slices.Equal(got, want) {
			t.Fatalf("the automation fired %v, want %v", got, want)
		}
		moved, renamed, original := runs[0], runs[1], runs[2]
		if *renamed.SeedID != *original.SeedID || *renamed.SessionID != *original.SessionID {
			t.Errorf("after a rename the run is on %s/%s, want the thread %s/%s", *renamed.SeedID, *renamed.SessionID, *original.SeedID, *original.SessionID)
		}
		if *moved.SeedID == *original.SeedID || *moved.SessionID == *original.SessionID {
			t.Errorf("after a location change the run stayed on the thread %s/%s", *moved.SeedID, *moved.SessionID)
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
