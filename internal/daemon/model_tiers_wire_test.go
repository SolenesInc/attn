package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	if err := os.Rename(executable+"-saved", executable); err != nil {
		t.Fatal(err)
	}
	if failed := harnessCatalog(app, "claude", false); failed.Success || failed.Error == nil {
		t.Errorf("cached failure after repair = %+v, want the failure until Refresh", failed)
	}
	if recovered := harnessCatalog(app, "claude", true); !recovered.Success {
		t.Errorf("Refresh after repair = %+v, want recovered discovery", recovered)
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

func TestChiefDiscoversDefaultsWithItsLaunchExecutable(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%v", legacy), func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex)
			t.Setenv("ATTN_CODEX_EXECUTABLE", "")
			app := w.App()
			global := harnessCatalog(app, "codex", false)
			if !global.Success || protocol.Deref(global.TierDefaults.Deep) != "gpt-6.1-sol" {
				t.Fatalf("daemon catalog = %+v", global)
			}
			executable := filepath.Join(t.TempDir(), "alternate-codex")
			runtime := strings.ReplaceAll(filepath.Join(w.Dir, "bin", "codex"), "'", "'\\''")
			script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = app-server ]; then
 IFS= read -r initialize
 printf '%%s\n' '{"id":1,"result":{}}'
 IFS= read -r initialized
 IFS= read -r models
 printf '%%s\n' '{"id":2,"result":{"data":[{"id":"alternate-sol","model":"alternate-sol","displayName":"Alternate Sol","supportedReasoningEfforts":[{"reasoningEffort":"low"}]}],"nextCursor":null}}'
 cat >/dev/null
else
 exec '%s' "$@"
fi
`, runtime)
			if err := os.WriteFile(executable, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			chief := w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("chief"), func(message *protocol.SpawnSessionMessage) {
				message.ChiefOfStaff = protocol.Ptr(true)
				if legacy {
					message.CodexExecutable = protocol.Ptr(executable)
				} else {
					message.Executable = protocol.Ptr(executable)
				}
			}))
			model, _ := flagValue(chief.Argv, "--model")
			if model != "alternate-sol" {
				t.Errorf("Chief launched %q, want its executable's alternate-sol", chief.Argv)
			}
			after := harnessCatalog(app, "codex", false)
			if protocol.Deref(after.TierDefaults.Deep) != "gpt-6.1-sol" {
				t.Errorf("alternate launch replaced the daemon catalog: %+v", after)
			}
		})
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
	for _, raw := range []string{`null`, `{}`, `[{"harness":"claude","model":"sonnet","tier":"fast"}]`, `[{"harness":"claude","model":"sonnet","tier":"light","typo":true}]`, `[] []`} {
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

func TestUnsetAdvisorModelAndEffortStayUnsetInEditableSettings(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "garden.advisor", `{"agent":"claude","model":"","effort":""}`)
	if got := gardenAdvisorRecipe(t, w.App().Initial.Settings); got.Model != "" || got.Effort != "" {
		t.Errorf("editable advisor setting = %+v, want both pins unset", got)
	}
}
