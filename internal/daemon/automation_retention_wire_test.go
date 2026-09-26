package daemon_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestRetentionPrunesOnlySettledRunsPastTheKeepCountAndMinimumAge(t *testing.T) {
	t.Setenv("ATTN_AUTOMATION_RETENTION_KEEP", "1")
	t.Setenv("ATTN_AUTOMATION_RETENTION_MIN_AGE", "3m")
	t.Setenv("ATTN_AUTOMATION_RETENTION_SWEEP_INTERVAL", "2m")
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		automationUnreachableFolder(t, w, "gone", func() {
			for _, id := range []string{"failing", "deleted"} {
				applyAutomation(t, cli, fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
id: %s
name: %s
trigger: {type: manual}
prompt: Check the folder.
launch: {driver: claude}
location: {type: directory, path: %q}
`, id, id, w.Path("gone")))
			}
		})
		for _, id := range []string{"cancelled", "pending"} {
			applyAutomation(t, cli, automationReviewSpec(id, "manual", ""))
		}
		runs := map[string][]string{}
		record := func(id string, n int) {
			for range n {
				request := fmt.Sprintf("%s-%d", id, len(runs[id]))
				input := ""
				if id == "cancelled" || id == "pending" {
					input = automationReviewInput(len(runs[id])+1, "0123456789abcdef0123456789abcdef01234567")
				}
				_, err := cli.AutomationRun(id, request, input)
				all := automationRuns(t, cli, id)
				if len(all) != len(runs[id])+1 {
					t.Fatalf("run request %s left runs %+v (err %v), want one new run", request, all, err)
				}
				for _, run := range all {
					if !slices.Contains(runs[id], run.ID) {
						runs[id] = append(runs[id], run.ID)
					}
				}
				w.advance(time.Second)
			}
		}
		record("failing", 3)
		record("cancelled", 2)
		record("pending", 2)
		record("deleted", 2)
		setAutomationEnabled(t, cli, "cancelled", false)
		if err := cli.AutomationDelete("deleted"); err != nil {
			t.Fatal(err)
		}
		w.advance(2*time.Minute + 20*time.Second)
		record("failing", 2)

		w.advance(100 * time.Second)

		for id, want := range map[string][]string{
			"failing":   {runs["failing"][4], runs["failing"][3]},
			"cancelled": {runs["cancelled"][1]},
			"pending":   {runs["pending"][1], runs["pending"][0]},
			"deleted":   {runs["deleted"][1]},
		} {
			if got := automationRunIDs(automationRuns(t, cli, id)); !slices.Equal(got, want) {
				t.Errorf("%s keeps runs %v, want %v", id, got, want)
			}
		}
	})
}

func TestRetentionKeepsAContinuingThreadsRunsWhilePruningFreshOnes(t *testing.T) {
	t.Setenv("ATTN_AUTOMATION_RETENTION_KEEP", "0")
	t.Setenv("ATTN_AUTOMATION_RETENTION_MIN_AGE", "0s")
	t.Setenv("ATTN_AUTOMATION_RETENTION_SWEEP_INTERVAL", "5m")
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		automationUnreachableFolder(t, w, "gone", func() {
			for _, continuity := range []string{"singleton", "fresh"} {
				applyAutomation(t, cli, fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
id: %s
name: %s
trigger: {type: scheduled, schedule: {cron: "*/2 * * * *", time_zone: UTC}, continuity: %s, catch_up: latest}
prompt: Continue the thread.
launch: {driver: claude}
location: {type: directory, path: %q}
`, continuity, continuity, continuity, w.Path("gone")))
			}
		})

		w.advance(6*time.Minute + 30*time.Second)

		if got, want := automationOccurrenceKeys(automationRuns(t, cli, "fresh")), []string{"scheduled:2000-01-01T00:06:00Z"}; !slices.Equal(got, want) {
			t.Errorf("the fresh automation keeps %v after the sweep, want only the run since %v", got, want)
		}
		thread := automationRuns(t, cli, "singleton")
		if got, want := automationOccurrenceKeys(thread), []string{"scheduled:2000-01-01T00:06:00Z", "scheduled:2000-01-01T00:04:00Z", "scheduled:2000-01-01T00:02:00Z"}; !slices.Equal(got, want) {
			t.Fatalf("the continuing thread keeps %v after the sweep, want every run %v", got, want)
		}
		for _, run := range thread[:2] {
			if *run.SeedID != *thread[2].SeedID || *run.SessionID != *thread[2].SessionID {
				t.Errorf("run %s = %+v, want it on its origin's seed and session", protocol.Deref(run.OccurrenceKey), run)
			}
		}
	})
}

func automationRunIDs(runs []protocol.AutomationRunSummary) []string {
	ids := make([]string, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.ID)
	}
	return ids
}
