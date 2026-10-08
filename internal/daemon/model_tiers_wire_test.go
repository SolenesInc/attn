package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func harnessCatalog(app *testworld.Peer, harness string, refresh bool) protocol.HarnessModelsResultMessage {
	id := uuid.NewString()
	return testworld.Request(app, protocol.HarnessModelsMessage{Cmd: protocol.CmdHarnessModels, Harness: harness, RequestID: id, Refresh: protocol.Ptr(refresh)},
		protocol.EventHarnessModelsResult, func(result protocol.HarnessModelsResultMessage) bool { return result.RequestID == id })
}

func TestModelTiersAndDefaultsFollowReportedOrderAndOverridesWithoutRediscovery(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	app := w.App()
	claude := harnessCatalog(app, "claude", false)
	if !claude.Success || len(claude.Models) != 4 {
		t.Fatalf("Claude catalog = %+v", claude)
	}
	if claude.Models[0].Tier != nil || claude.Models[0].TierSource != protocol.ModelTierSourceAlias {
		t.Errorf("default alias = %+v, want no tier", claude.Models[0])
	}
	for index, tier := range []protocol.ModelTier{protocol.ModelTierDeep, protocol.ModelTierStandard, protocol.ModelTierLight} {
		model := claude.Models[index+1]
		if protocol.Deref(model.Tier) != tier || protocol.Deref(model.ShippedTier) != tier || model.TierSource != protocol.ModelTierSourceShipped {
			t.Errorf("model %s = %+v, want shipped %s", model.ID, model, tier)
		}
	}
	if protocol.Deref(claude.TierDefaults.Light) != "claude-haiku-fake" || protocol.Deref(claude.TierDefaults.Deep) != "claude-opus-fake" {
		t.Errorf("Claude defaults = %+v", claude.TierDefaults)
	}
	codex := harnessCatalog(app, "codex", false)
	if !codex.Success || protocol.Deref(codex.TierDefaults.Light) != "gpt-6-luna" || protocol.Deref(codex.TierDefaults.Standard) != "gpt-5.6-terra" || protocol.Deref(codex.TierDefaults.Deep) != "gpt-6.1-sol" {
		t.Errorf("Codex catalog = %+v", codex)
	}
	executable := filepath.Join(w.Dir, "bin", "claude")
	if err := os.Rename(executable, executable+"-saved"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(executable+"-saved", executable) })
	setSetting(t, app, "model_tier_overrides", `[{"harness":"claude","provider":"","model":"claude-sonnet-fake","tier":"light"}]`)
	changed := harnessCatalog(app, "claude", false)
	if !changed.Success || protocol.Deref(changed.TierDefaults.Light) != "claude-sonnet-fake" || changed.Models[2].TierSource != protocol.ModelTierSourceOverride || protocol.Deref(changed.Models[2].ShippedTier) != protocol.ModelTierStandard {
		t.Fatalf("override without a discovery executable = %+v", changed)
	}
	setSetting(t, app, "model_tier_overrides", `[]`)
	reset := harnessCatalog(app, "claude", false)
	if !reset.Success || protocol.Deref(reset.TierDefaults.Light) != "claude-haiku-fake" || reset.Models[2].TierSource != protocol.ModelTierSourceShipped {
		t.Errorf("reset = %+v, want shipped tiers without rediscovery", reset)
	}
	if refreshed := harnessCatalog(app, "claude", true); refreshed.Success || refreshed.Error == nil {
		t.Errorf("refresh with missing executable = %+v, want discovery failure", refreshed)
	}
}

func TestAnUnsetChiefRunsTheDeepDefaultAtLowEffort(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	chief := w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("chief"), func(message *protocol.SpawnSessionMessage) { message.ChiefOfStaff = protocol.Ptr(true) }))
	model, _ := flagValue(chief.Argv, "--model")
	if model != "gpt-6.1-sol" || !hasArgs(chief.Argv, "-c", `model_reasoning_effort="low"`) {
		t.Errorf("unset chief launched with %q, want the reported deep default at low effort", chief.Argv)
	}
}

func TestSessionTitlesUseTheirHarnessLightDefault(t *testing.T) {
	for _, row := range []struct {
		harness fakeagent.Harness
		model   string
	}{{fakeagent.Claude, "claude-haiku-fake"}, {fakeagent.Codex, "gpt-6-luna"}} {
		t.Run(string(row.harness), func(t *testing.T) {
			w := newTitlingWorld(t, row.harness)
			task := titleTaskFor(t, w, row.harness)
			if task.Model != row.model {
				t.Errorf("title used %q, want %s", task.Model, row.model)
			}
		})
	}
}

func TestInvalidTierOverridesAreRefusedAndKeepTheSavedMapping(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	const saved = `[{"harness":"claude","provider":"","model":"claude-sonnet-fake","tier":"light"}]`
	setSetting(t, app, "model_tier_overrides", saved)
	for _, raw := range []string{`{}`, `[{"harness":"claude","model":"sonnet","tier":"fast"}]`, `[{"harness":"claude","model":"sonnet","tier":"light","typo":true}]`, `[] []`} {
		refused := gardenAdvisorSetSetting(app, "model_tier_overrides", raw)
		if protocol.Deref(refused.Success) || refused.Error == nil {
			t.Errorf("accepted %s: %+v", raw, refused)
		}
	}
	if actual := w.App().Initial.Settings["model_tier_overrides"]; actual != saved {
		t.Errorf("saved overrides = %v, want %s", actual, saved)
	}
}

func TestActivityUsesItsFallbackWhenModelDiscoveryFails(t *testing.T) {
	w, app, _, session, agent := watchedActivityWorld(t)
	t.Setenv("ATTN_CLAUDE_EXECUTABLE", "")
	executable := filepath.Join(t.TempDir(), "failing-discovery")
	script := fmt.Sprintf("#!/bin/sh\nfor arg do\nif [ \"$arg\" = --input-format ]; then exit 1; fi\ndone\nexec '%s' \"$@\"\n", filepath.Join(w.Dir, "bin", "claude"))
	if err := os.WriteFile(executable, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	setSetting(t, app, "claude_executable", executable)
	activityTurn(t, w, app, agent, session, "continue the tests", "The tests are running.")
	task := w.HeadlessTask()
	if task.Model != "claude-haiku-4-5" {
		t.Errorf("activity with failed discovery used %q, want the existing fallback", task.Model)
	}
	task.Answer("Running tests")
	awaitActivity(app, session, "Running tests")
}
