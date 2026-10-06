package daemon_test

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestProfileDeletionRefusesAnAutomationUntilItIsDeleted(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		side := createProfile(app, "Side")
		cli = cli.WithGardenProfile(side.ID, "")
		selectProfile(app, side.ID)
		folder := w.Path("sweep")
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
		id := uuid.NewString()
		applied := testworld.Request(app, protocol.AutomationApplyMessage{Cmd: protocol.CmdAutomationApply, RequestID: protocol.Ptr(id), ProfileID: protocol.Ptr(side.ID), DefinitionYaml: automationSingletonSpec(w, "Sweep", "latest", "sweep")}, protocol.EventAutomationApplyResult, automationAnswer[protocol.AutomationApplyResultMessage](id))
		if !applied.Success {
			t.Fatalf("apply Side automation: %+v", applied)
		}
		w.advance(2*time.Minute + 30*time.Second)
		runs := automationRuns(t, cli, 1)
		if len(runs) != 1 || runs[0].State != "delivered" {
			t.Fatalf("first occurrence: %+v", runs)
		}
		first := runs[0]
		worker, seed := protocol.Deref(first.SessionID), protocol.Deref(first.SeedID)
		lifeMove(t, cli, string(worker), seed, "harvest", "finished", "")
		if _, err := cli.AgentClose(string(worker), worker, "finished this automation run"); err != nil {
			t.Fatal(err)
		}
		for _, current := range w.AppOn(side.ID).Initial.Profiles {
			if current.ID == side.ID {
				side = current
			}
		}
		id = uuid.NewString()
		deleted := profileRequest(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: id, ProfileID: side.ID, ExpectedRevision: side.Revision}, id)
		if deleted.Success {
			t.Fatal("profile deletion must refuse its automation")
		}
		definition, err := cli.AutomationDefinition(applied.Definition.ID)
		if err != nil || definition.Definition.ID != applied.Definition.ID {
			t.Fatalf("refusal changed automation owner: %+v %v", definition, err)
		}
		if err := cli.AutomationDelete(applied.Definition.ID); err != nil {
			t.Fatal(err)
		}
		id = uuid.NewString()
		deleted = profileRequest(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: id, ProfileID: side.ID, ExpectedRevision: side.Revision}, id)
		if !deleted.Success {
			t.Fatalf("delete cleaned profile: %+v", deleted)
		}

	})
}

func TestProfileDeletionLeavesPendingAutomationRunsUntouched(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "false")
	r := newAutomationReviewWorld(t)
	side := createProfile(r.app, "Side")
	r.cli = r.cli.WithGardenProfile(side.ID, "")
	selectProfile(r.app, side.ID)
	id := uuid.NewString()
	applied := testworld.Request(r.app, protocol.AutomationApplyMessage{
		Cmd: protocol.CmdAutomationApply, RequestID: protocol.Ptr(id), ProfileID: protocol.Ptr(side.ID),
		DefinitionYaml: automationReviewSpec("held-review", "manual", automationReviewOverride(r.clone)),
	}, protocol.EventAutomationApplyResult, automationAnswer[protocol.AutomationApplyResultMessage](id))
	if !applied.Success {
		t.Fatalf("apply Side automation: %+v", applied)
	}
	upstream := newRepo(t, "upstream")
	unfetched := commitFile(t, upstream, "later.go", "package later\n")
	if _, err := r.cli.AutomationRun(applied.Definition.ID, "held", automationReviewInput(46, unfetched)); err == nil {
		t.Fatal("a run whose head cannot be fetched started")
	}
	held := automationRuns(t, r.cli, applied.Definition.ID)
	if len(held) != 1 || held[0].State != "pending" {
		t.Fatalf("run must be held pending: %+v", held)
	}
	lifeMove(t, r.cli.WithGardenProfile(side.ID, ""), "", protocol.Deref(held[0].SeedID), "wither", "ending this profile's work", "")
	id = uuid.NewString()
	deleted := profileRequest(r.app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: id, ProfileID: side.ID, ExpectedRevision: side.Revision}, id)
	if deleted.Success {
		t.Fatalf("profile deletion must refuse its automation: %+v", deleted)
	}
	runs := automationRuns(t, r.cli, applied.Definition.ID)
	if len(runs) != 1 || runs[0].ID != held[0].ID || runs[0].State != "pending" {
		t.Fatalf("refusal must leave the occurrence pending: %+v", runs)
	}
}

func TestAnotherProfilesAutomationIsUnknownToAProfile(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		home := app.SelectedProfile()
		side := createProfile(app, "Side")
		sideApp := w.AppOn(side.ID)
		if err := os.MkdirAll(w.Path("sweep"), 0o755); err != nil {
			t.Fatal(err)
		}
		id := uuid.NewString()
		applied := testworld.Request(sideApp, protocol.AutomationApplyMessage{Cmd: protocol.CmdAutomationApply, RequestID: protocol.Ptr(id), DefinitionYaml: automationSingletonSpec(w, "Sweep", "latest", "sweep")}, protocol.EventAutomationApplyResult, automationAnswer[protocol.AutomationApplyResultMessage](id))
		if !applied.Success {
			t.Fatalf("apply Side automation: %+v", applied)
		}
		sweep := applied.Definition.ID

		homeApp := w.AppOn(home)
		id = uuid.NewString()
		listed := testworld.Request(homeApp, protocol.AutomationDefinitionsGetMessage{Cmd: protocol.CmdAutomationDefinitionsGet, RequestID: protocol.Ptr(id)}, protocol.EventAutomationDefinitionsResult, automationAnswer[protocol.AutomationDefinitionsResultMessage](id))
		for _, definition := range listed.Definitions {
			if definition.ID == sweep {
				t.Fatalf("Default's automations list Side's %d: %+v", sweep, listed.Definitions)
			}
		}
		id = uuid.NewString()
		ran := testworld.Request(homeApp, protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, RequestID: id, DefinitionID: sweep}, protocol.EventAutomationRunResult, func(r protocol.AutomationRunResultMessage) bool { return protocol.Deref(r.RequestID) == id })
		if ran.Success || protocol.Deref(ran.Error) != fmt.Sprintf("automation %d not found", sweep) {
			t.Errorf("running Side's automation from Default = %+v, want it not found", ran)
		}

		w.advance(time.Second)
		selectProfile(app, home)
		registerSessions(t, w, cli, "home-agent")
		fromAgent := cli.WithGardenProfile("", "home-agent")
		if err := fromAgent.AutomationDelete(sweep); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("automation %d not found", sweep)) {
			t.Errorf("deleting Side's automation from a Default agent = %v, want it not found", err)
		}
		if definitions, err := fromAgent.AutomationDefinitions(); err != nil || slices.ContainsFunc(definitions.Definitions, func(d protocol.AutomationDefinitionSummary) bool { return d.ID == sweep }) {
			t.Errorf("a Default agent's automation list = %+v, %v; want Side's %d absent", definitions, err, sweep)
		}
		if _, err := cli.AutomationDefinition(sweep); err == nil || !strings.Contains(err.Error(), "choose --profile") {
			t.Errorf("a plain terminal must choose its profile: %v", err)
		}
		plain := cli.WithGardenProfile(home, "")
		if definitions, err := plain.AutomationDefinitions(); err != nil || len(definitions.Definitions) != 0 {
			t.Fatalf("plain terminal list: %+v, %v", definitions, err)
		}
		checks := map[string]func(int) error{
			"show":    func(id int) error { _, err := plain.AutomationDefinition(id); return err },
			"run":     func(id int) error { _, err := plain.AutomationRun(id, "plain-run", "{}"); return err },
			"delete":  plain.AutomationDelete,
			"enable":  func(id int) error { _, err := plain.AutomationSetEnabled(id, true); return err },
			"cleanup": func(id int) error { _, err := plain.AutomationCleanup(id); return err },
			"set":     func(id int) error { _, err := plain.SetAutomationLaunchDesktop(id, "own", nil); return err },
		}
		for name, check := range checks {
			foreign, unknown := check(sweep), check(sweep+1)
			if foreign == nil || unknown == nil || foreign.Error() != strings.ReplaceAll(unknown.Error(), strconv.Itoa(sweep+1), strconv.Itoa(sweep)) {
				t.Errorf("%s foreign=%v, unknown=%v", name, foreign, unknown)
			}
		}
		if runs, err := plain.AutomationRuns(sweep); err != nil || len(runs.Runs) != 0 {
			t.Errorf("foreign runs: %+v, %v", runs, err)
		}
		if _, err := plain.AutomationApply(automationEditSpec(sweep, automationSingletonSpec(w, "Edited", "latest", "sweep"))); err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Errorf("foreign edit: %v", err)
		}
		for _, profile := range []string{side.ID, "side"} {
			chosen := cli.WithGardenProfile(profile, "")
			if definitions, err := chosen.AutomationDefinitions(); err != nil || len(definitions.Definitions) != 1 || definitions.Definitions[0].ID != sweep {
				t.Errorf("chosen profile %s: %+v, %v", profile, definitions, err)
			}
		}
		if _, err := cli.WithGardenProfile(side.ID, "home-agent").AutomationDefinition(sweep); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("explicit profile overrode source session: %v", err)
		}
		if _, err := cli.WithGardenProfile(side.ID, "missing-session").AutomationDefinition(sweep); err == nil {
			t.Error("unknown source session exposed an automation")
		}
	})
}
