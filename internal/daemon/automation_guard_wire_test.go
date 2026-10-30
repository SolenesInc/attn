package daemon_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAGuardedScheduleOnlyRunsForNewOutput(t *testing.T) {
	t.Setenv("ATTN_AUTOMATION_SCHEDULE_INTERVAL", "1s")
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	dir := w.Path("guarded")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "state.txt"), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(" red \n")
	spec := fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
name: Guarded
trigger:
  type: scheduled
  schedule: {cron: "@every 3s", time_zone: UTC}
  guard: 'if [ -f fail ]; then echo broken-guard >&2; exit 17; fi; cat state.txt'
  continuity: singleton
  catch_up: latest
prompt: Inspect the guard output.
launch: {driver: claude}
location: {type: directory, path: %q}
`, dir)
	id := applyAutomation(t, cli, spec).ID
	await := func(outcome string, minChecks int) protocol.AutomationDefinitionSummary {
		t.Helper()
		for {
			awaitAutomationChanged(app, id)
			result, err := cli.AutomationDefinitions()
			if err != nil {
				t.Fatal(err)
			}
			for _, def := range result.Definitions {
				if def.ID == id && def.GuardCheck != nil && def.GuardCheck.Outcome == outcome && def.GuardCheck.ChecksSinceRun >= minChecks && (outcome != "launched" || def.LastRun != nil && def.LastRun.State == "delivered") {
					return def
				}
			}
		}
	}
	first := await("launched", 0)
	runs := automationRuns(t, cli, id)
	if len(runs) != 1 {
		t.Fatalf("first output made %d runs, want one", len(runs))
	}
	agent := w.Launched(string(protocol.Deref(first.LastRun.SessionID)))
	inputPath := filepath.Join(w.Dir, "automation", "occurrences", runs[0].ID+".json")
	if prompt := agent.Prompted(); !strings.Contains(prompt, inputPath) {
		t.Fatalf("opening %q does not name input %s", prompt, inputPath)
	}
	checkInput := func(run protocol.AutomationRunSummary, want string) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(w.Dir, "automation", "occurrences", run.ID+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var input struct {
			GuardOutput string `json:"guard_output"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			t.Fatal(err)
		}
		if input.GuardOutput != want {
			t.Fatalf("guard_output=%q, want %q", input.GuardOutput, want)
		}
	}
	checkInput(runs[0], "red")
	agent.Reply("Handled. <!-- attn:state=done -->")
	unchanged := await("unchanged", 1)
	if len(automationRuns(t, cli, id)) != 1 {
		t.Fatal("unchanged guard created a run")
	}
	write(" \n")
	empty := await("empty", unchanged.GuardCheck.ChecksSinceRun+1)
	if len(automationRuns(t, cli, id)) != 1 {
		t.Fatal("empty guard created a run")
	}
	write("red")
	await("launched", 0)
	runs = automationRuns(t, cli, id)
	if len(runs) != 2 {
		t.Fatalf("same output after empty made %d total runs, want two", len(runs))
	}
	checkInput(runs[0], "red")
	if err := os.WriteFile(filepath.Join(dir, "fail"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	failed := await("failed", 1)
	if detail := protocol.Deref(failed.GuardCheck.Detail); !strings.Contains(detail, "exit 17") || !strings.Contains(detail, "broken-guard") {
		t.Fatalf("failed check detail=%q", detail)
	}
	if len(automationRuns(t, cli, id)) != 2 {
		t.Fatal("failed guard created a run")
	}
	if err := os.Remove(filepath.Join(dir, "fail")); err != nil {
		t.Fatal(err)
	}
	await("unchanged", 2)
	write("blue")
	await("launched", 0)
	runs = automationRuns(t, cli, id)
	if len(runs) != 3 {
		t.Fatalf("new output made %d total runs, want three", len(runs))
	}
	checkInput(runs[0], "blue")
	if *runs[0].SessionID != *runs[1].SessionID || *runs[0].SessionID != *runs[2].SessionID {
		t.Fatal("guarded singleton did not keep its session")
	}
	t.Logf("red launched once; unchanged=%+v; empty=%+v; red launched again; failed=%+v; failure preserved red; blue launched with guard_output", *unchanged.GuardCheck, *empty.GuardCheck, *failed.GuardCheck)
	setAutomationEnabled(t, cli, id, false)
}

func TestAGuardedScheduleRetriesOutputWhoseRunFailed(t *testing.T) {
	t.Setenv("ATTN_AUTOMATION_SCHEDULE_INTERVAL", "1s")
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	spec := fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
name: %s
trigger:
  type: scheduled
  schedule: {cron: "@every 3s", time_zone: UTC}
  guard: printf retry-me
  continuity: singleton
  catch_up: latest
prompt: Inspect.
launch: {driver: claude}
location: {type: directory, path: %q}
`, strings.Repeat("x", 401), w.Dir)
	id := applyAutomation(t, cli, spec).ID
	for {
		awaitAutomationChanged(app, id)
		runs := automationRuns(t, cli, id)
		if len(runs) < 2 || runs[0].State == "pending" {
			continue
		}
		if runs[0].State != "failed" || runs[1].State != "failed" || runs[0].ID == runs[1].ID {
			t.Fatalf("same output after failed launch = %+v, want two failed attempts", runs)
		}
		t.Logf("same output retried after failed run: %s and %s", runs[1].ID, runs[0].ID)
		break
	}
	setAutomationEnabled(t, cli, id, false)
}
