package daemon_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

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
	if !claude.Success || len(claude.Models) != len(fakeagent.ClaudeModels) {
		t.Fatalf("Claude catalog = %+v, want the %d models its CLI reports", claude, len(fakeagent.ClaudeModels))
	}
	for index, reported := range fakeagent.ClaudeModels {
		offered := claude.Models[index]
		support := protocol.ModelCapabilitySupportUnsupported
		if reported.SupportsEffort {
			support = protocol.ModelCapabilitySupportSupported
		}
		if offered.ID != reported.Value || offered.EffortSupport != support || !slices.Equal(offered.EffortLevels, reported.SupportedEffortLevels) {
			t.Errorf("model at position %d = %+v, want %s with effort %s %v in harness reported order", index, offered, reported.Value, support, reported.SupportedEffortLevels)
		}
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
	if recovered := harnessCatalog(app, "claude", false); !recovered.Success {
		t.Errorf("read after repair = %+v, want recovered discovery", recovered)
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

func TestActivityUsesTheLightTierWhenModelDiscoveryFails(t *testing.T) {
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
	if task.Model != "haiku" {
		t.Errorf("activity with failed discovery used %q, want Claude Code's light tier", task.Model)
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

func TestATierCallUsesTheSavedOverrideOnceAMissingExecutableReturns(t *testing.T) {
	for name, configured := range map[string]bool{"found on PATH": false, "configured as a path": true} {
		t.Run(name, func(t *testing.T) { tierCallAfterTheExecutableReturns(t, configured) })
	}
}

func tierCallAfterTheExecutableReturns(t *testing.T, configured bool) {
	w := &world{World: prepareWorld(t, fakeagent.Claude)}
	bin := filepath.Join(w.Dir, "bin")
	executable := ""
	if configured {
		executable = filepath.Join(bin, "claude")
	}
	t.Setenv("ATTN_CLAUDE_EXECUTABLE", executable)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv("ATTN_HEADLESS_TASKS", "on")
	w.start()
	app := w.TrustedApp()
	token := pairSenderInApp(t, w.Client(), app, "Tier recovery")
	setSetting(t, app, "model_tier_overrides", `[{"harness":"claude","provider":"","model":"claude-sonnet-fake","tier":"light"}]`)
	const chosen = "claude-sonnet-fake"
	checkCatalog := func() {
		t.Helper()
		catalog := harnessCatalog(app, "claude", true)
		if !catalog.Success || protocol.Deref(catalog.TierDefaults.Light) != chosen {
			t.Fatalf("Settings refresh = %+v, want saved tier %q", catalog, chosen)
		}
	}
	call := func() string {
		t.Helper()
		conn, err := w.DialUnix()
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if err := conn.SetDeadline(time.Now().Add(fakeagent.HangGuard)); err != nil {
			t.Fatal(err)
		}
		msg := protocol.SenderModelCallMessage{
			Cmd: protocol.CmdSenderModelCall, Harness: protocol.Ptr("claude"),
			Input: "hello", LimitMs: int(fakeagent.HangGuard.Milliseconds()), Purpose: "test",
			SenderToken: token.Token, System: "Answer briefly.", Tier: protocol.Ptr(protocol.ModelTierLight),
		}
		if err := json.NewEncoder(conn).Encode(msg); err != nil {
			t.Fatal(err)
		}
		task := w.HeadlessTask()
		task.Answer("answered")
		var response protocol.Response
		if err := json.NewDecoder(conn).Decode(&response); err != nil {
			t.Fatal(err)
		}
		answer := response.SenderModelCallResult
		if !response.Ok || answer == nil || answer.Error != nil || protocol.Deref(answer.Text) != "answered" {
			t.Fatalf("paired call failed: %+v", response)
		}
		if task.Model != answer.Model {
			t.Fatalf("model selection in harness = %q, wire = %q", task.Model, answer.Model)
		}
		return answer.Model
	}
	checkCatalog()
	if model := call(); model != chosen {
		t.Fatalf("before failure, model = %q, want %q", model, chosen)
	}
	claude := filepath.Join(bin, "claude")
	if err := os.Rename(claude, claude+"-saved"); err != nil {
		t.Fatal(err)
	}
	if failed := harnessCatalog(app, "claude", true); failed.Success || failed.Error == nil {
		t.Fatalf("Settings refresh while Claude is absent = %+v, want failure", failed)
	}
	if err := os.Rename(claude+"-saved", claude); err != nil {
		t.Fatal(err)
	}
	for attempt := range 2 {
		if model := call(); model != chosen {
			t.Errorf("after executable recovery, call %d uses %q, want saved light override %q", attempt+1, model, chosen)
		}
	}
	checkCatalog()
	if model := call(); model != chosen {
		t.Errorf("after another Settings refresh, model = %q, want %q", model, chosen)
	}
}
