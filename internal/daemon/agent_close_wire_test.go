package daemon_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestTheChiefOfStaffClosesAnySessionButItself(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	stranger := spawnPanes(w, app, w.Path("stranger"))[0].session
	startConfiguredChief(t, w, cli)
	chief := string(chiefSession(t, cli))

	closed, err := cli.AgentClose(stranger, protocol.SessionID(chief), "abandoned, nobody is driving it")
	if err != nil {
		t.Fatalf("the chief closes a stranger: %v", err)
	}
	if closed.Rule != protocol.AgentCloseRuleChiefOfStaff || string(closed.TargetSessionID) != stranger {
		t.Errorf("close = %+v, want stranger closed under the chief_of_staff rule", closed)
	}
	if by := protocol.Deref(showSession(t, cli, stranger).ClosedBy); by != chief {
		t.Errorf("the ledger names %q as the closer, want the chief", by)
	}

	_, err = cli.AgentClose(chief, protocol.SessionID(chief), "done for the day")
	agentCloseRefused(t, err, "session_close_protected", "Chief is protected from closing; ask Chief to close first")
	if live := queriedIDs(t, cli, ""); !slices.Contains(live, chief) {
		t.Errorf("live sessions = %v, want the chief still there", live)
	}
}

func TestACrewMembersDayCannotBeClosedByAnAgent(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	day := startCrew(t, cli, "trellis", "")
	w.Launched(string(day.SessionID))
	startConfiguredChief(t, w, cli)
	chief := string(chiefSession(t, cli))

	_, err := cli.AgentClose(string(day.SessionID), protocol.SessionID(chief), "looks finished to me")
	agentCloseRefused(t, err, "session_close_protected", "Trellis is protected from closing; ask Trellis to close first")
	if live := queriedIDs(t, cli, ""); !slices.Contains(live, string(day.SessionID)) {
		t.Errorf("live sessions = %v, want the crew day still there", live)
	}
}

func TestAgentCloseBySeedClosesItsTender(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	orchestrator := spawnPanes(w, app, w.Path("orchestrator"))[0].session
	cwd := w.Path("orchestrator", "work")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	w.SetCodexFolderTrust(cwd, "trusted")
	seed := plantDelegationSeed(t, cli, orchestrator, "Reach it by seed")
	delegated, err := cli.Delegate(delegateAtSeed(orchestrator, cwd, seed))
	if err != nil {
		t.Fatalf("delegate at %s: %v", seed, err)
	}
	w.Launched(string(delegated.SessionID))

	closed, err := cli.AgentClose(seed, protocol.SessionID(orchestrator), "its report landed")
	if err != nil {
		t.Fatalf("close by seed: %v", err)
	}
	if closed.TargetSessionID != delegated.SessionID || closed.Rule != protocol.AgentCloseRuleDispatcher {
		t.Errorf("close = %+v, want the seed's tender %s closed by its dispatcher", closed, delegated.SessionID)
	}
	if entry := showSession(t, cli, string(delegated.SessionID)); protocol.Deref(entry.ClosedBy) != orchestrator || protocol.Deref(entry.CloseReason) != "its report landed" {
		t.Errorf("ledger entry = %+v, want it closed by the orchestrator for its reason", entry)
	}
}

func TestAgentCloseRefusalsCloseNothing(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	panes := spawnPanes(w, app, w.Path("worker"), w.Path("other"))
	worker, other := panes[0].session, panes[1].session
	dupes := []string{
		w.Spawn(app, fakeagent.Claude, w.Path("dupe"), withIDPrefix("dupe-")),
		w.Spawn(app, fakeagent.Claude, w.Path("dupe"), withIDPrefix("dupe-")),
	}
	for _, dupe := range dupes {
		w.Launched(dupe)
	}
	startConfiguredChief(t, w, cli)
	chief := string(chiefSession(t, cli))
	seedling := strings.Repeat("🌱", 400)

	for _, row := range []struct {
		name, target, source, reason string
		code                         string
		wants                        []string
	}{
		{name: "a blank reason", target: worker, source: worker, reason: "   ", code: "close_reason_required", wants: []string{"reason"}},
		{name: "a reason past the limit", target: worker, source: worker, reason: strings.Repeat("x", 401), code: "close_reason_required", wants: []string{"401", "400"}},
		{name: "a reason past the limit in characters", target: other, source: other, reason: seedling + "🌱", code: "close_reason_required", wants: []string{"401", "400"}},
		{name: "an ambiguous prefix", target: "dupe", source: chief, reason: "tidying up", code: "ambiguous_session", wants: []string{"more than one session"}},
		{name: "a caller that is not a session", target: worker, source: "ghost", reason: "cleaning up", code: "session_not_found", wants: []string{`the caller "ghost" is not a session`}},
	} {
		_, err := cli.AgentClose(row.target, protocol.SessionID(row.source), row.reason)
		if code := client.ErrorCode(err); code != row.code {
			t.Errorf("%s: close = %v, want code %s", row.name, err, row.code)
			continue
		}
		for _, want := range row.wants {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: refusal %q does not name %q", row.name, err, want)
			}
		}
	}
	if live := queriedIDs(t, cli, ""); !slices.Equal(agentCloseSortedIDs(live), agentCloseSortedIDs(append([]string{chief, worker, other}, dupes...))) {
		t.Fatalf("live sessions after the refusals = %v, want every session still there", live)
	}

	if _, err := cli.AgentClose(worker, protocol.SessionID(worker), seedling); err != nil {
		t.Fatalf("a reason of exactly 400 characters was refused: %v", err)
	}
}

func agentCloseRefused(t *testing.T, err error, code, message string) {
	t.Helper()
	if got := client.ErrorCode(err); got != code || !strings.Contains(err.Error(), message) {
		t.Fatalf("close = %v (code %q), want %s saying %q", err, got, code, message)
	}
}

func agentCloseSortedIDs(ids []string) []string {
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	return sorted
}

func TestAChiefCannotCloseAnAgentOfAnotherProfile(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	startConfiguredChief(t, w, cli)
	chief := string(chiefSession(t, cli))
	profile := createProfile(app, "Work")
	work := w.AppOn(profile.ID)
	target := spawnPanes(w, work, w.Path("work-agent"))[0].session
	_, err := cli.AgentClose(target, protocol.SessionID(chief), "Outside my profile")
	agentCloseRefused(t, err, "close_not_authorized", "unknown live session")
	if live := queriedIDs(t, cli, ""); !slices.Contains(live, target) {
		t.Fatalf("foreign agent disappeared: %v", live)
	}
}

func TestGardenV2GuardsSelfCloseOnGrowingSeedsAndAutoParksOnDispatcherClose(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "agent_tools_enabled", "true")

	panes := spawnPanes(w, app, w.Path("legacy"), w.Path("worker"), w.Path("dispatcher"))
	legacy, worker, dispatcher := panes[0].session, panes[1].session, panes[2].session

	legacySeed := plantSeedAs(t, cli, legacy, "Legacy self-close while growing")
	lifeMove(t, cli, legacy, legacySeed, "tend", "", "")
	if _, err := cli.AgentClose(legacy, protocol.SessionID(legacy), "flag is off"); err != nil {
		t.Fatalf("self-close with garden_v2_enabled off refused: %v", err)
	}
	if shown := lifeShow(t, cli, legacySeed).Seed; shown.Status != "growing" {
		t.Fatalf("seed after legacy self-close = %q, want growing", shown.Status)
	}

	setSetting(t, app, "garden_v2_enabled", "true")

	seedA := plantSeedAs(t, cli, worker, "Finish widget migration")
	seedB := plantSeedAs(t, cli, worker, "Clean up widget tests")
	lifeMove(t, cli, worker, seedA, "tend", "", "")
	lifeMove(t, cli, worker, seedB, "tend", "", "")

	_, err := cli.AgentClose(worker, protocol.SessionID(worker), "trying to leave early")
	agentCloseRefused(t, err, "growing_seeds_held", seedA)
	if !strings.Contains(err.Error(), seedB) || !strings.Contains(err.Error(), "attn seed park") || !strings.Contains(err.Error(), "attn seed standby <id> --stay") {
		t.Fatalf("CLI self-close refusal = %q, want both seeds, park hint, and --stay standby hint", err.Error())
	}

	workerTools := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(worker))}
	mcpRefusal := workerTools.mustCall(t, "agent_close", map[string]any{"self": true, "reason": "trying via mcp"})
	if !mcpRefusal.IsError {
		t.Fatal("MCP agent_close --self succeeded while tending growing seeds")
	}
	mcpText := resultText(mcpRefusal)
	if !strings.HasPrefix(mcpText, "growing_seeds_held: ") || !strings.Contains(mcpText, seedA) || !strings.Contains(mcpText, "`seed_park({seed: ") || !strings.Contains(mcpText, "`seed_standby({seed: \"<id>\", stay: true, ") || !strings.Contains(mcpText, "Fix: Harvest, park, wither, or put each growing seed on standby, then close again.") {
		t.Fatalf("MCP self-close refusal = %q, want growing_seeds_held with park and stay: true calls, and catalog fix", mcpText)
	}

	_, standbyErr := cli.WithGardenProfile("", protocol.SessionID(worker)).SeedStandby(seedA, client.SeedStandbyOptions{Note: "wait for CI"})
	if standbyErr == nil || !strings.Contains(standbyErr.Error(), "was not placed on standby") || !strings.Contains(standbyErr.Error(), "--stay") {
		t.Fatalf("seed standby without --stay while a second growing seed was held = %v, want refusal naming --stay and unchanged seed", standbyErr)
	}
	if shown := lifeShow(t, cli, seedA).Seed; shown.Standby != nil {
		t.Fatalf("refused standby half-committed standby on %s: %+v", seedA, shown.Standby)
	}

	if _, err := cli.WithGardenProfile("", protocol.SessionID(worker)).SeedStandby(seedA, client.SeedStandbyOptions{Note: "wait for CI", Stay: true}); err != nil {
		t.Fatalf("seed standby --stay on %s: %v", seedA, err)
	}
	lifeMove(t, cli, worker, seedB, "park", "pausing before close", "")
	if _, err := cli.AgentClose(worker, protocol.SessionID(worker), "seedA on standby, seedB parked"); err != nil {
		t.Fatalf("self-close after standby+park refused: %v", err)
	}
	if shown := lifeShow(t, cli, seedA).Seed; shown.Status != "growing" || shown.Standby == nil {
		t.Fatalf("standby seed after self-close = status %q standby %+v, want growing on standby", shown.Status, shown.Standby)
	}

	cwd := w.Path("dispatcher", "sub")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	w.SetCodexFolderTrust(cwd, "trusted")
	activeSeed := plantDelegationSeed(t, cli, dispatcher, "Auto-parked on dispatcher close")
	delegated, err := cli.Delegate(delegateAtSeed(dispatcher, cwd, activeSeed))
	if err != nil {
		t.Fatalf("delegate at %s: %v", activeSeed, err)
	}
	w.Launched(string(delegated.SessionID))
	standbySeed := plantSeedAs(t, cli, string(delegated.SessionID), "Kept on standby across dispatcher close")
	lifeMove(t, cli, string(delegated.SessionID), standbySeed, "tend", "", "")
	if _, err := cli.WithGardenProfile("", delegated.SessionID).SeedStandby(standbySeed, client.SeedStandbyOptions{Note: "waiting on PR", Stay: true}); err != nil {
		t.Fatalf("standby on %s: %v", standbySeed, err)
	}

	dispatcherTools := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(dispatcher))}
	mcpClose := dispatcherTools.mustCall(t, "agent_close", map[string]any{"target": string(delegated.SessionID), "reason": "switching approach"})
	if mcpClose.IsError {
		t.Fatalf("MCP dispatcher agent_close failed: %s", resultText(mcpClose))
	}

	parked := lifeShow(t, cli, activeSeed)
	if parked.Seed.Status != "parked" || parked.Seed.Tender != nil {
		t.Fatalf("active seed after dispatcher close = status %q tender %+v, want parked with no tender", parked.Seed.Status, parked.Seed.Tender)
	}
	if len(parked.Notes) == 0 || !strings.Contains(parked.Notes[0].Body, "switching approach") || !strings.Contains(parked.Notes[0].Body, "parked automatically") {
		t.Fatalf("auto-parked seed notes = %+v, want close reason and auto-park explanation", parked.Notes)
	}
	kept := lifeShow(t, cli, standbySeed).Seed
	if kept.Status != "growing" || kept.Standby == nil {
		t.Fatalf("standby seed after dispatcher close = status %q standby %+v, want growing on standby", kept.Status, kept.Standby)
	}

	setSetting(t, app, "garden_v2_enabled", "false")
	offWorker := spawnPanes(w, app, w.Path("off-again"))[0].session
	offSeed := plantSeedAs(t, cli, offWorker, "Self-close allowed once flag toggles back off")
	lifeMove(t, cli, offWorker, offSeed, "tend", "", "")
	if _, err := cli.AgentClose(offWorker, protocol.SessionID(offWorker), "flag toggled off"); err != nil {
		t.Fatalf("self-close after toggling garden_v2_enabled off refused: %v", err)
	}
}
