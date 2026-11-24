package daemon_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func runSeedCmd[T any](t *testing.T, cli *client.Client, cmd string, session string, args map[string]any) (T, error) {
	t.Helper()
	var out T
	err := cli.RunCommand(cmd, protocol.SessionID(session), args, &out)
	return out, err
}

func mustRunSeedCmd[T any](t *testing.T, cli *client.Client, cmd string, session string, args map[string]any) T {
	t.Helper()
	out, err := runSeedCmd[T](t, cli, cmd, session, args)
	if err != nil {
		t.Fatalf("%s(%v): %v", cmd, args, err)
	}
	return out
}

func TestGardenV2BedsAndPulseRequireFlag(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	seed := plantSeedAs(t, cli, session, "Flag gated seed")

	for _, tc := range []struct {
		cmd  string
		args map[string]any
	}{
		{"seed bed ls", map[string]any{}},
		{"seed bed add", map[string]any{"name": "Payments"}},
		{"seed bed set", map[string]any{"bed": "Payments", "name": "Billing"}},
		{"seed bed rm", map[string]any{"bed": "Payments"}},
		{"seed bed assign", map[string]any{"seed": seed, "bed": "Payments"}},
		{"seed pulse", map[string]any{"seed": seed, "text": "Working on tests"}},
		{"seed plant", map[string]any{"title": "With bed", "bed": "Payments"}},
		{"seed plot", map[string]any{"plot": map[string]any{"title": "Plot", "bed": "Payments", "children": []any{map[string]any{"title": "Step 1"}}}}},
		{"seed ls", map[string]any{"bed": "Payments"}},
	} {
		err := cli.RunCommand(tc.cmd, protocol.SessionID(session), tc.args, &struct{}{})
		if err == nil || !strings.Contains(err.Error(), "garden_v2_enabled") {
			t.Errorf("%s while garden_v2_enabled is off = %v, want refusal naming garden_v2_enabled", tc.cmd, err)
		}
	}

	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"seed_bed_ls", map[string]any{}},
		{"seed_bed_add", map[string]any{"name": "Payments"}},
		{"seed_pulse", map[string]any{"seed": seed, "text": "Working"}},
	} {
		res := agent.mustCall(t, tc.tool, tc.args)
		if text := resultText(res); !res.IsError || !strings.HasPrefix(text, "setting_off: ") || !strings.Contains(text, "garden_v2_enabled") {
			t.Errorf("MCP %s while off = %q, want setting_off naming garden_v2_enabled", tc.tool, text)
		}
	}

	app.Send(protocol.GardenBedSaveMessage{Cmd: protocol.CmdGardenBedSave, RequestID: "save-off", Name: "Payments", Repos: []string{}})
	saveReply := testworld.Await(app, protocol.EventGardenBedSaveResult, func(msg protocol.GardenBedSaveResultMessage) bool {
		return msg.RequestID == "save-off"
	})
	if saveReply.Success || !strings.Contains(protocol.Deref(saveReply.Error), "garden_v2_enabled") {
		t.Errorf("WS garden_bed_save while off = %+v, want failure naming garden_v2_enabled", saveReply)
	}
}

func TestGardenV2BedsCurationAutoRoutingAndInheritance(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "garden_v2_enabled", "true")

	shopDir := w.Path("shop")
	coreDir := w.Path("core-repo")
	otherDir := w.Path("unrelated")
	for _, dir := range []string{shopDir, coreDir, otherDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	shopSession := w.Spawn(app, fakeagent.Claude, shopDir)
	coreSession := w.Spawn(app, fakeagent.Claude, coreDir)
	otherSession := w.Spawn(app, fakeagent.Claude, otherDir)

	payments := mustRunSeedCmd[protocol.SeedBedResult](t, cli, "seed bed add", shopSession, map[string]any{
		"name":  "Payments",
		"note":  "Checkout and billing",
		"repos": []string{shopDir, "billing"},
	})
	core := mustRunSeedCmd[protocol.SeedBedResult](t, cli, "seed bed add", shopSession, map[string]any{
		"name":  "Core",
		"repos": []string{"core-repo", "shop"},
	})
	if payments.Bed.Slug != "payments" || core.Bed.Slug != "core" {
		t.Fatalf("unexpected slugs: payments=%+v core=%+v", payments.Bed, core.Bed)
	}

	if _, err := runSeedCmd[protocol.SeedBedResult](t, cli, "seed bed add", shopSession, map[string]any{"name": "payments"}); err == nil {
		t.Fatal("duplicate bed name was not refused")
	}
	if _, err := runSeedCmd[protocol.SeedBedResult](t, cli, "seed bed add", shopSession, map[string]any{"name": "Overlap", "repos": []string{"BILLING"}}); err == nil || !strings.Contains(err.Error(), "Payments") {
		t.Fatalf("cross-bed repo collision = %v, want refusal naming Payments", err)
	}

	// Exact path (Payments -> shopDir) beats bare repo name (Core -> "shop").
	autoPayments := mustRunSeedCmd[protocol.SeedPlantResult](t, cli, "seed plant", shopSession, map[string]any{"title": "Fix checkout retry"})
	if protocol.Deref(autoPayments.BedID) != payments.Bed.ID || protocol.Deref(autoPayments.Bed) != "Payments" {
		t.Fatalf("exact path auto-route got bed=(%q,%q), want (%q,Payments)", protocol.Deref(autoPayments.BedID), protocol.Deref(autoPayments.Bed), payments.Bed.ID)
	}
	// Bare name matches Core.
	autoCore := mustRunSeedCmd[protocol.SeedPlantResult](t, cli, "seed plant", coreSession, map[string]any{"title": "Tighten core loop"})
	if protocol.Deref(autoCore.BedID) != core.Bed.ID || protocol.Deref(autoCore.Bed) != "Core" {
		t.Fatalf("basename auto-route got bed=(%q,%q), want (%q,Core)", protocol.Deref(autoCore.BedID), protocol.Deref(autoCore.Bed), core.Bed.ID)
	}
	// Explicit --bed overrides repo auto-routing.
	explicitCore := mustRunSeedCmd[protocol.SeedPlantResult](t, cli, "seed plant", shopSession, map[string]any{"title": "Explicit core seed", "bed": "core"})
	if protocol.Deref(explicitCore.BedID) != core.Bed.ID {
		t.Fatalf("explicit --bed got %q, want %q", protocol.Deref(explicitCore.BedID), core.Bed.ID)
	}
	// Unknown --bed is refused listing valid beds.
	if _, err := runSeedCmd[protocol.SeedPlantResult](t, cli, "seed plant", shopSession, map[string]any{"title": "Unknown bed", "bed": "Ghost"}); err == nil || !strings.Contains(err.Error(), "Core") || !strings.Contains(err.Error(), "Payments") {
		t.Fatalf("unknown bed refusal = %v, want refusal listing Core and Payments", err)
	}
	// Unrelated directory stays unassigned.
	unassigned := mustRunSeedCmd[protocol.SeedPlantResult](t, cli, "seed plant", otherSession, map[string]any{"title": "Loose task"})
	if protocol.Deref(unassigned.BedID) != "" {
		t.Fatalf("unrelated dir seed got bed %q, want unassigned", protocol.Deref(unassigned.BedID))
	}

	// Plot with explicit bed: crown holds bed, child inherits it.
	plotted := mustRunSeedCmd[protocol.SeedPlotResult](t, cli, "seed plot", otherSession, map[string]any{
		"plot": map[string]any{
			"title":    "Checkout v2",
			"bed":      "Payments",
			"children": []any{map[string]any{"title": "Build ledger"}},
		},
	})
	if protocol.Deref(plotted.Crown.BedID) != payments.Bed.ID || protocol.Deref(plotted.Children[0].BedID) != payments.Bed.ID || protocol.Deref(plotted.Children[0].Bed) != "Payments" {
		t.Fatalf("plot bed inheritance failed: crown=%q child=(%q,%q)", protocol.Deref(plotted.Crown.BedID), protocol.Deref(plotted.Children[0].BedID), protocol.Deref(plotted.Children[0].Bed))
	}
	childID := plotted.Children[0].ID

	// Child cannot pass --bed on plant or seed bed assign.
	if _, err := runSeedCmd[protocol.SeedPlantResult](t, cli, "seed plant", shopSession, map[string]any{"title": "Bad child", "part_of": plotted.Crown.ID, "bed": "Core"}); err == nil {
		t.Fatal("planting child with --bed was not refused")
	}
	if _, err := runSeedCmd[protocol.SeedBedAssignResult](t, cli, "seed bed assign", shopSession, map[string]any{"seed": childID, "bed": "Core"}); err == nil || !strings.Contains(err.Error(), plotted.Crown.ID) {
		t.Fatalf("assigning child bed = %v, want refusal naming root plot %s", err, plotted.Crown.ID)
	}

	// Reassigning the root plot updates the child's effective bed immediately.
	moved := mustRunSeedCmd[protocol.SeedBedAssignResult](t, cli, "seed bed assign", shopSession, map[string]any{"seed": plotted.Crown.ID, "bed": "Core"})
	if !moved.Changed || protocol.Deref(moved.Seed.BedID) != core.Bed.ID {
		t.Fatalf("seed bed assign on crown = %+v, want changed in Core", moved)
	}
	shownChild, err := cli.SeedShow("", childID)
	if err != nil {
		t.Fatal(err)
	}
	if protocol.Deref(shownChild.Seed.BedID) != core.Bed.ID || protocol.Deref(shownChild.Seed.Bed) != "Core" {
		t.Fatalf("child effective bed after moving crown = (%q,%q), want (%q,Core)", protocol.Deref(shownChild.Seed.BedID), protocol.Deref(shownChild.Seed.Bed), core.Bed.ID)
	}

	// Linking a seed with its own bed as part-of a plot clears its own bed and inherits the plot's bed.
	mustRunSeedCmd[protocol.SeedLinkResult](t, cli, "seed link", shopSession, map[string]any{"a": autoPayments.ID, "relation": "part-of", "b": plotted.Crown.ID})
	linkedShow, err := cli.SeedShow("", autoPayments.ID)
	if err != nil {
		t.Fatal(err)
	}
	if protocol.Deref(linkedShow.Seed.BedID) != core.Bed.ID {
		t.Fatalf("linked seed effective bed = %q, want plot bed %q", protocol.Deref(linkedShow.Seed.BedID), core.Bed.ID)
	}
	// Unlinking it leaves it unassigned rather than snapping back to its stale pre-link bed.
	mustRunSeedCmd[protocol.SeedLinkResult](t, cli, "seed unlink", shopSession, map[string]any{"a": autoPayments.ID, "relation": "part-of", "b": plotted.Crown.ID})
	unlinkedShow, err := cli.SeedShow("", autoPayments.ID)
	if err != nil {
		t.Fatal(err)
	}
	if protocol.Deref(unlinkedShow.Seed.BedID) != "" {
		t.Fatalf("unlinked seed kept stale bed %q, want empty", protocol.Deref(unlinkedShow.Seed.BedID))
	}

	// Filter seed ls by bed and by unassigned.
	coreList := mustRunSeedCmd[protocol.SeedListResult](t, cli, "seed ls", shopSession, map[string]any{"bed": "Core"})
	if len(coreList.Seeds) != 4 {
		t.Fatalf("seed ls --bed Core returned %d seeds, want 4 (autoCore, explicitCore, crown, child)", len(coreList.Seeds))
	}
	unassignedList := mustRunSeedCmd[protocol.SeedListResult](t, cli, "seed ls", shopSession, map[string]any{"bed": "unassigned"})
	if len(unassignedList.Seeds) != 2 {
		t.Fatalf("seed ls --bed unassigned returned %d seeds, want 2 (unassigned, unlinked autoPayments)", len(unassignedList.Seeds))
	}

	// seed bed ls reports alphabetical beds with accurate open counts.
	bedList := mustRunSeedCmd[protocol.SeedBedListResult](t, cli, "seed bed ls", shopSession, map[string]any{})
	if len(bedList.Beds) != 2 || bedList.Beds[0].Name != "Core" || bedList.Beds[0].OpenCount != 4 || bedList.Beds[1].Name != "Payments" || bedList.Beds[1].OpenCount != 0 {
		t.Fatalf("seed bed ls = %+v", bedList.Beds)
	}

	// Renaming Core updates resolved bed name on seeds.
	renamed := mustRunSeedCmd[protocol.SeedBedResult](t, cli, "seed bed set", shopSession, map[string]any{"bed": "Core", "name": "Platform"})
	if !protocol.Deref(renamed.Changed) || renamed.Bed.Name != "Platform" {
		t.Fatalf("seed bed set = %+v", renamed)
	}
	shownAfterRename, err := cli.SeedShow("", autoCore.ID)
	if err != nil {
		t.Fatal(err)
	}
	if protocol.Deref(shownAfterRename.Seed.Bed) != "Platform" {
		t.Fatalf("seed bed after rename = %q, want Platform", protocol.Deref(shownAfterRename.Seed.Bed))
	}

	// Removing Platform unassigns its root seeds (autoCore, explicitCore, crown = 3 root seeds).
	removed := mustRunSeedCmd[protocol.SeedBedDeleteResult](t, cli, "seed bed rm", shopSession, map[string]any{"bed": "Platform"})
	if removed.UnassignedSeeds != 3 {
		t.Fatalf("seed bed rm unassigned_seeds = %d, want 3", removed.UnassignedSeeds)
	}
	shownAfterRemove, err := cli.SeedShow("", childID)
	if err != nil {
		t.Fatal(err)
	}
	if protocol.Deref(shownAfterRemove.Seed.BedID) != "" {
		t.Fatalf("child bed after removing bed = %q, want empty", protocol.Deref(shownAfterRemove.Seed.BedID))
	}
}

func TestGardenV2PulseLifecycleAndStanding(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "garden_v2_enabled", "true")

	panes := spawnPanes(w, app, w.Path("owner"), w.Path("stranger"))
	owner, stranger := panes[0].session, panes[1].session
	seed := plantSeedAs(t, cli, owner, "Pulse test seed")
	lifeMove(t, cli, owner, seed, "tend", "", "")

	notesBefore, err := cli.SeedNotes("", seed, client.SeedNotesQuery{})
	if err != nil {
		t.Fatal(err)
	}
	pulsed := mustRunSeedCmd[protocol.SeedPulseResult](t, cli, "seed pulse", owner, map[string]any{"seed": seed, "text": "Wire tests green"})
	if !pulsed.Changed || protocol.Deref(pulsed.Seed.Pulse) != "Wire tests green" || protocol.Deref(pulsed.Seed.PulseAt) == "" {
		t.Fatalf("seed pulse = %+v", pulsed)
	}
	// Pulse does not add a log note.
	notesAfter, err := cli.SeedNotes("", seed, client.SeedNotesQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if notesAfter.Total != notesBefore.Total {
		t.Fatalf("pulse added %d log notes, want 0", notesAfter.Total-notesBefore.Total)
	}
	// Identical pulse is idempotent.
	same := mustRunSeedCmd[protocol.SeedPulseResult](t, cli, "seed pulse", owner, map[string]any{"seed": seed, "text": "Wire tests green"})
	if same.Changed {
		t.Fatalf("identical pulse reported changed=true")
	}
	// Stranger cannot overwrite tender's pulse.
	if _, err := runSeedCmd[protocol.SeedPulseResult](t, cli, "seed pulse", stranger, map[string]any{"seed": seed, "text": "Hijacked"}); err == nil {
		t.Fatal("stranger pulse was not refused")
	}
	// Multiline and >160-char pulses are refused.
	if _, err := runSeedCmd[protocol.SeedPulseResult](t, cli, "seed pulse", owner, map[string]any{"seed": seed, "text": "line1\nline2"}); err == nil {
		t.Fatal("multiline pulse was not refused")
	}
	if _, err := runSeedCmd[protocol.SeedPulseResult](t, cli, "seed pulse", owner, map[string]any{"seed": seed, "text": strings.Repeat("a", 161)}); err == nil {
		t.Fatal("overlong pulse was not refused")
	}

	// Parking preserves pulse; harvesting clears it.
	lifeMove(t, cli, owner, seed, "park", "waiting on upstream", "")
	parkedShow, err := cli.SeedShow("", seed)
	if err != nil {
		t.Fatal(err)
	}
	if protocol.Deref(parkedShow.Seed.Pulse) != "Wire tests green" {
		t.Fatalf("parked seed pulse = %q, want preserved", protocol.Deref(parkedShow.Seed.Pulse))
	}
	lifeMove(t, cli, owner, seed, "harvest", "done", "")
	harvestedShow, err := cli.SeedShow("", seed)
	if err != nil {
		t.Fatal(err)
	}
	if protocol.Deref(harvestedShow.Seed.Pulse) != "" {
		t.Fatalf("harvested seed still carried pulse %q", protocol.Deref(harvestedShow.Seed.Pulse))
	}
	if _, err := runSeedCmd[protocol.SeedPulseResult](t, cli, "seed pulse", owner, map[string]any{"seed": seed, "text": "After harvest"}); err == nil {
		t.Fatal("pulse on closed seed was not refused")
	}

	// Replant clears pulse too.
	lifeMove(t, cli, owner, seed, "replant", "", "")
	mustRunSeedCmd[protocol.SeedPulseResult](t, cli, "seed pulse", owner, map[string]any{"seed": seed, "text": "Second pass"})
	lifeMove(t, cli, owner, seed, "tend", "", "")
	lifeMove(t, cli, owner, seed, "replant", "", "")
	replantedShow, err := cli.SeedShow("", seed)
	if err != nil {
		t.Fatal(err)
	}
	if protocol.Deref(replantedShow.Seed.Pulse) != "" {
		t.Fatalf("replanted seed still carried pulse %q", protocol.Deref(replantedShow.Seed.Pulse))
	}
	// Clear pulse explicitly.
	mustRunSeedCmd[protocol.SeedPulseResult](t, cli, "seed pulse", owner, map[string]any{"seed": seed, "text": "Temporary"})
	cleared := mustRunSeedCmd[protocol.SeedPulseResult](t, cli, "seed pulse", owner, map[string]any{"seed": seed, "clear": true})
	if !cleared.Changed || protocol.Deref(cleared.Seed.Pulse) != "" {
		t.Fatalf("seed pulse --clear = %+v", cleared)
	}
}

func TestGardenV2WebSocketAndLaunchPrompt(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "garden_v2_enabled", "true")

	// Save bed over WS.
	app.Send(protocol.GardenBedSaveMessage{
		Cmd:       protocol.CmdGardenBedSave,
		RequestID: "bed-add-1",
		Name:      "Payments",
		Note:      protocol.Ptr("Checkout work"),
		Repos:     []string{"payments-api"},
	})
	saved := testworld.Await(app, protocol.EventGardenBedSaveResult, func(msg protocol.GardenBedSaveResultMessage) bool {
		return msg.RequestID == "bed-add-1"
	})
	if !saved.Success || saved.Bed == nil {
		t.Fatalf("garden_bed_save_result = %+v", saved)
	}
	testworld.Await(app, protocol.EventGardenBedsUpdated, func(msg protocol.GardenBedsUpdatedMessage) bool {
		return len(msg.Beds) == 1 && msg.Beds[0].ID == saved.Bed.ID
	})

	// Fresh client receives garden_beds in initial_state.
	joined := w.App()
	if len(joined.Initial.GardenBeds) != 1 || joined.Initial.GardenBeds[0].Name != "Payments" {
		t.Fatalf("joined initial_state.garden_beds = %+v, want [Payments]", joined.Initial.GardenBeds)
	}

	// Assign a seed to the bed over WS.
	session := w.Spawn(app, fakeagent.Claude, w.Path("workspace"))
	seed := plantSeedAs(t, cli, session, "WS bed assignment")
	app.Send(protocol.SeedBedSetMessage{
		Cmd:       protocol.CmdSeedBedSet,
		RequestID: "bed-set-1",
		SeedID:    seed,
		BedID:     protocol.Ptr(saved.Bed.ID),
	})
	assigned := testworld.Await(app, protocol.EventSeedBedSetResult, func(msg protocol.SeedBedSetResultMessage) bool {
		return msg.RequestID == "bed-set-1"
	})
	if !assigned.Success || assigned.Seed == nil || protocol.Deref(assigned.Seed.BedID) != saved.Bed.ID {
		t.Fatalf("seed_bed_set_result = %+v", assigned)
	}

	// Launch instructions for spawned session include Garden v2 guidance + profile beds.
	launchArgs := strings.Join(w.Launched(session).Argv, " ")
	if !strings.Contains(launchArgs, "Profile beds: Payments.") || !strings.Contains(launchArgs, "one-line pulse") {
		t.Errorf("launch instructions missing Garden v2 beds/pulse guidance: %s", launchArgs)
	}

	// Delete bed over WS.
	app.Send(protocol.GardenBedDeleteMessage{
		Cmd:       protocol.CmdGardenBedDelete,
		RequestID: "bed-del-1",
		ID:        saved.Bed.ID,
	})
	deleted := testworld.Await(app, protocol.EventGardenBedDeleteResult, func(msg protocol.GardenBedDeleteResultMessage) bool {
		return msg.RequestID == "bed-del-1"
	})
	if !deleted.Success || protocol.Deref(deleted.UnassignedSeeds) != 1 {
		t.Fatalf("garden_bed_delete_result = %+v", deleted)
	}
	testworld.Await(app, protocol.EventGardenBedsUpdated, func(msg protocol.GardenBedsUpdatedMessage) bool {
		return len(msg.Beds) == 0
	})
}

func TestGardenV2ToggleOffHidesAndRestoresBedsAndPulse(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "garden_v2_enabled", "true")

	bed := mustRunSeedCmd[protocol.SeedBedResult](t, cli, "seed bed add", "", map[string]any{"name": "Payments"}).Bed
	seed := mustRunSeedCmd[protocol.SeedPlantResult](t, cli, "seed plant", "", map[string]any{
		"title": "Toggle seed",
		"bed":   bed.ID,
	}).ID
	mustRunSeedCmd[protocol.SeedPulseResult](t, cli, "seed pulse", "", map[string]any{
		"seed": seed,
		"text": "Halfway through rollout",
	})

	setSetting(t, app, "garden_v2_enabled", "false")
	testworld.Await(app, protocol.EventGardenBedsUpdated, func(msg protocol.GardenBedsUpdatedMessage) bool {
		return len(msg.Beds) == 0
	})
	testworld.Await(app, protocol.EventGardenSeedsChanged, func(msg protocol.GardenSeedsChangedMessage) bool {
		for _, s := range msg.Seeds {
			if s.ID == seed {
				return protocol.Deref(s.BedID) == "" && protocol.Deref(s.Pulse) == ""
			}
		}
		return false
	})

	shownOff, err := cli.SeedShow("", seed)
	if err != nil {
		t.Fatal(err)
	}
	if protocol.Deref(shownOff.Seed.BedID) != "" || protocol.Deref(shownOff.Seed.Bed) != "" || protocol.Deref(shownOff.Seed.Pulse) != "" {
		t.Fatalf("seed show with garden_v2_enabled=false leaked bed/pulse: %+v", shownOff.Seed)
	}

	setSetting(t, app, "garden_v2_enabled", "true")
	testworld.Await(app, protocol.EventGardenBedsUpdated, func(msg protocol.GardenBedsUpdatedMessage) bool {
		return len(msg.Beds) == 1 && msg.Beds[0].ID == bed.ID
	})
	shownBack, err := cli.SeedShow("", seed)
	if err != nil {
		t.Fatal(err)
	}
	if protocol.Deref(shownBack.Seed.BedID) != bed.ID || protocol.Deref(shownBack.Seed.Pulse) != "Halfway through rollout" {
		t.Fatalf("seed show after re-enabling lost bed/pulse: %+v", shownBack.Seed)
	}
}

func TestGardenV2AutoRoutesWorktreesSubdirsAndSymlinks(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "garden_v2_enabled", "true")

	realRoot := filepath.Join(t.TempDir(), "payments-api")
	if err := os.MkdirAll(filepath.Join(realRoot, "services", "billing"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit(realRoot, "init")
	runGit(realRoot, "commit", "--allow-empty", "-m", "init")
	wtDir := filepath.Join(t.TempDir(), "wt-branch")
	runGit(realRoot, "worktree", "add", "-b", "feature", wtDir)

	symlinkRoot := filepath.Join(t.TempDir(), "payments-link")
	if err := os.Symlink(realRoot, symlinkRoot); err != nil {
		t.Fatal(err)
	}

	attnBin := testworld.AttnBinary(t)
	cliEnv := append(os.Environ(), "ATTN_SOCKET_PATH="+w.Socket, "ATTN_DATA_DIR="+filepath.Dir(w.Socket), "ATTN_SESSION_ID=")
	addCmd := exec.Command(attnBin, "seed", "bed", "add", "Payments", "--repo", "./payments-link", "--json")
	addCmd.Dir = filepath.Dir(symlinkRoot)
	addCmd.Env = cliEnv
	created, err := addCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("attn seed bed add with relative repo: %v\n%s", err, created)
	}
	var added protocol.SeedBedResult
	if err := json.Unmarshal(created, &added); err != nil {
		t.Fatalf("decode bed add result: %v\n%s", err, created)
	}
	bed := added.Bed
	canonicalRepo, err := filepath.EvalSymlinks(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(bed.Repos) != 1 || bed.Repos[0] != canonicalRepo {
		t.Fatalf("relative local repo saved as %v, want %q", bed.Repos, canonicalRepo)
	}
	if _, err := runSeedCmd[protocol.SeedBedResult](t, cli, "seed bed add", "", map[string]any{
		"name": "Duplicate", "repos": []string{symlinkRoot},
	}); err == nil || !strings.Contains(err.Error(), bed.Name) {
		t.Fatalf("duplicate local repo alias = %v, want refusal naming %s", err, bed.Name)
	}
	boundAlias := mustRunSeedCmd[protocol.SeedBedResult](t, cli, "seed bed set", "", map[string]any{
		"bed": bed.ID, "repos": []string{symlinkRoot},
	}).Bed
	if len(boundAlias.Repos) != 1 || boundAlias.Repos[0] != symlinkRoot {
		t.Fatalf("absolute local alias saved as %v, want %q", boundAlias.Repos, symlinkRoot)
	}

	plantCmd := exec.Command(attnBin, "seed", "plant", "--json", "Planted from linked worktree")
	plantCmd.Dir = wtDir
	plantCmd.Env = cliEnv
	plantOut, err := plantCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("attn seed plant in worktree: %v\n%s", err, plantOut)
	}
	var planted protocol.SeedPlantResult
	if err := json.Unmarshal(plantOut, &planted); err != nil {
		t.Fatalf("decode plant result: %v\n%s", err, plantOut)
	}
	wtShow, err := cli.SeedShow("", planted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if protocol.Deref(wtShow.Seed.BedID) != bed.ID {
		t.Fatalf("worktree seed bed_id = %q, want %q", protocol.Deref(wtShow.Seed.BedID), bed.ID)
	}

	assignErrCmd := exec.Command(attnBin, "seed", "bed", "assign", planted.ID)
	assignErrCmd.Env = cliEnv
	assignErrOut, err := assignErrCmd.CombinedOutput()
	if err == nil {
		t.Fatalf("attn seed bed assign without target succeeded: %s", assignErrOut)
	}
	if strings.Contains(string(assignErrOut), "{bed}") {
		t.Fatalf("seed bed assign error leaked literal placeholder: %s", assignErrOut)
	}

	bareBed := mustRunSeedCmd[protocol.SeedBedResult](t, cli, "seed bed add", "", map[string]any{
		"name":  "Billing",
		"repos": []string{"checkout-svc"},
	}).Bed
	otherRepo := filepath.Join(t.TempDir(), "checkout-svc")
	subDir := filepath.Join(otherRepo, "cmd", "api")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(otherRepo, "init")
	runGit(otherRepo, "commit", "--allow-empty", "-m", "init")

	subSession := w.Spawn(app, fakeagent.Claude, subDir)
	fromSubdir := mustRunSeedCmd[protocol.SeedPlantResult](t, cli, "seed plant", subSession, map[string]any{
		"title": "Planted from repo subdir session",
	}).ID
	subShow, err := cli.SeedShow("", fromSubdir)
	if err != nil {
		t.Fatal(err)
	}
	if protocol.Deref(subShow.Seed.BedID) != bareBed.ID {
		t.Fatalf("subdir session seed bed_id = %q, want %q", protocol.Deref(subShow.Seed.BedID), bareBed.ID)
	}
}

func TestGardenV2KeepsTheOutpostsRepositoryWhenTheHomeHasASymlink(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "garden_v2_enabled", "true")
	homeRepo := newRepo(t, "billing")
	outpostRepo := filepath.Join(t.TempDir(), "shop")
	if err := os.Symlink(homeRepo, outpostRepo); err != nil {
		t.Fatal(err)
	}
	outpostSubdir := filepath.Join(outpostRepo, "web")
	host, peer := connectFarOutpost(t, w, app, func(_ *bridge.Conn, incoming ssh.NewChannel) {
		bridge.Answer(incoming, func(_ context.Context, payload []byte) (any, error) {
			var inspect protocol.CheckoutInspectMessage
			if err := json.Unmarshal(payload, &inspect); err != nil {
				return nil, err
			}
			if inspect.Cmd == protocol.CmdCheckoutInspect {
				return protocol.CheckoutInspection{Exists: true, IsDirectory: true, Cwd: inspect.Cwd, MainRepo: outpostRepo}, nil
			}
			return struct{}{}, nil
		})
	})
	absolute := mustRunSeedCmd[protocol.SeedBedResult](t, cli, "seed bed add", "", map[string]any{
		"name": "Shop path", "repos": []string{outpostRepo},
	}).Bed
	mustRunSeedCmd[protocol.SeedBedResult](t, cli, "seed bed add", "", map[string]any{
		"name": "Billing", "repos": []string{"billing"},
	})
	saved := testworld.Request(app, protocol.GardenBedSaveMessage{
		Cmd: protocol.CmdGardenBedSave, RequestID: "edit-shop-note", ID: &absolute.ID, Name: absolute.Name,
		Note: protocol.Ptr("Project note"), Repos: absolute.Repos,
	}, protocol.EventGardenBedSaveResult, func(msg protocol.GardenBedSaveResultMessage) bool {
		return msg.RequestID == "edit-shop-note"
	})
	if !saved.Success {
		t.Fatalf("saving shop bed note: %s", protocol.Deref(saved.Error))
	}
	const session = "outpost-shop"
	if err := w.InjectSessionOn(host, session, "Shop on the outpost", outpostRepo, "claude"); err != nil {
		t.Fatal(err)
	}
	checkBed := func(id string) {
		t.Helper()
		shown, err := cli.SeedShow("", id)
		if err != nil {
			t.Fatal(err)
		}
		if got := protocol.Deref(shown.Seed.Bed); got != absolute.Name {
			t.Fatalf("outpost seed belongs to bed %q, want %q", got, absolute.Name)
		}
	}
	checkBed(plantSeedAs(t, cli, session, "Work in the outpost shop"))
	mustRunSeedCmd[protocol.SeedBedResult](t, cli, "seed bed set", "", map[string]any{
		"bed": absolute.ID, "repos": []string{"shop"},
	})

	ctx, cancel := context.WithTimeout(t.Context(), fakeagent.HangGuard)
	defer cancel()
	channel, requests, err := peer.Open(ctx, "forward", []byte(frozenForwardHeader))
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	defer context.AfterFunc(ctx, func() { _ = channel.Close() })()
	go ssh.DiscardRequests(requests)
	request := protocol.RunCommandMessage{
		Cmd: protocol.CmdRunCommand, Command: "seed plant", Directory: &outpostSubdir,
		Args: json.RawMessage(`{"title":"Work from a sessionless outpost shell"}`),
	}
	if err := json.NewEncoder(channel).Encode(request); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Ok     bool                     `json:"ok"`
		Result protocol.SeedPlantResult `json:"command_result"`
	}
	if err := json.NewDecoder(channel).Decode(&response); err != nil || !response.Ok {
		t.Fatalf("outpost plant response = %+v, %v", response, err)
	}
	checkBed(response.Result.ID)
}
