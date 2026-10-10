package daemon_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestProfileDeletionRefusesARunningGardenReview(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	side := createProfile(app, "Side")
	selectProfile(app, side.ID)
	scoped := cli.WithRequester(side.ID, "")
	seed := gardenReviewAbandonedSeed(t, w, w.AppOn(side.ID), scoped, "side-review", "Side review candidate")
	setSetting(t, app, "garden.advisor", `{"agent":"claude"}`)
	t.Setenv("ATTN_HEADLESS_TASKS", "on")
	review := gardenReviewStart(t, scoped)
	task := w.HeadlessTask()
	lifeMove(t, scoped, "", seed, "harvest", "completed outside the review", "")
	for _, current := range w.AppOn(side.ID).Initial.Profiles {
		if current.ID == side.ID {
			side = current
		}
	}
	id := uuid.NewString()
	result := profileRequest(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: id, ProfileID: side.ID, ExpectedRevision: side.Revision}, id)
	if result.Success || !strings.Contains(protocol.Deref(result.Error), "1 running Garden reviews") {
		t.Fatalf("running review deletion: %+v", result)
	}
	shown, err := scoped.SeedReviewShow(review.Run.ID)
	if err != nil || shown.Review.Run.Status != "running" {
		t.Fatalf("refusal changed review: %+v %v", shown, err)
	}
	task.Fail("done checking deletion")
}

func TestFreeTenderNamesStayInsideTheirSeedsProfile(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		home := app.SelectedProfile()
		registerSessions(t, w, cli, "default-worker")
		original := plantSeedAs(t, cli, "default-worker", "free worker in Default")
		lifeMove(t, cli, "default-worker", original, "tend", "", "keel")
		if _, err := cli.WithRequester("", "default-worker").SeedEdit(original, "legacy alias remains editable"); err != nil {
			t.Fatal(err)
		}
		side := createProfile(app, "Side")
		w.advance(time.Second)
		selectProfile(app, side.ID)
		registerSessions(t, w, cli, "side-worker")
		other := plantSeedAs(t, cli, "side-worker", "free worker in Side")
		lifeMove(t, cli, "side-worker", other, "tend", "", "keel")
		lifeMove(t, cli, "side-worker", other, "harvest", "finished", "keel")

		writeCrewHomeFile(t, w, "keel", crew.CharterFileName, "# Keel\n\nNow a registered member.\n")
		w.restart()
		cli = w.Client()
		if err := w.InjectCrewSession("registered-keel", "Keel", w.Path("keel"), "keel"); err != nil {
			t.Fatal(err)
		}
		homeApp := w.App()
		w.advance(time.Second)
		selectProfile(homeApp, home)
		registerSessions(t, w, cli, "default-worker")
		if _, err := cli.WithRequester("", "default-worker").SeedEdit(original, "the existing free claim stays editable after registration"); err != nil {
			t.Fatal(err)
		}
		lifeMove(t, cli, "default-worker", original, "tend", "", "keel")
		if _, err := cli.SeedNote("default-worker", original, "this belongs in Default", "", "", true, nil); err != nil {
			t.Fatal(err)
		}
		w.advance(0)
		if items := readInbox(t, cli, "registered-keel", 0).Items; len(items) != 0 {
			t.Fatalf("a later foreign registration received the old alias's seed bell: %+v", items)
		}
		fresh := plantSeedAs(t, cli, "default-worker", "registered crew fence")
		_, err := cli.SeedTransition("default-worker", fresh, "tend", "", "keel", false, client.SeedTransitionOptions{})
		if err == nil || !strings.Contains(err.Error(), "Default") || !strings.Contains(err.Error(), "Side") {
			t.Fatalf("registered foreign member claim must name both profiles: %v", err)
		}

		if _, err := cli.WithRequester(side.ID, "").CrewRename("Keel", "Alfred"); err != nil {
			t.Fatal(err)
		}
		cwd := w.Path("delegation")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		_, err = cli.Delegate(delegateAtSeed("default-worker", cwd, original))
		if err == nil || !strings.Contains(err.Error(), "being tended by keel") || strings.Contains(err.Error(), "Alfred") {
			t.Fatalf("dispatch refusal must preserve the free tender's name: %v", err)
		}
		registerSessions(t, w, cli, "default-takeover")
		if _, err := cli.SeedTransition("default-takeover", original, "tend", "", "", true, client.SeedTransitionOptions{}); err != nil {
			t.Fatal(err)
		}
		notes, err := cli.SeedNotes("default-worker", original, 0)
		if err != nil {
			t.Fatal(err)
		}
		foundAudit := false
		for _, note := range notes.Notes {
			if strings.Contains(note.Body, "forced `attn seed tend") {
				foundAudit = true
				if !strings.Contains(note.Body, "; keel held the seed.") || strings.Contains(note.Body, "Alfred") {
					t.Fatalf("forced-move audit must preserve the free tender's name: %s", note.Body)
				}
			}
		}
		if !foundAudit {
			t.Fatal("forced takeover wrote no audit note")
		}

	})
}

func TestGardenBelongsToTheCallingProfile(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		original := app.SelectedProfile()
		registerSessions(t, w, cli, "default-worker")
		a, err := cli.SeedPlant("default-worker", "profile boundary alpha", "", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		side := createProfile(app, "Side")
		selectProfile(app, side.ID)
		registerSessions(t, w, cli, "side-worker")
		b, err := cli.SeedPlant("side-worker", "profile boundary beta", "", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if a.Seed.ProfileID != original || b.Seed.ProfileID != side.ID {
			t.Fatalf("seed ownership: alpha=%s beta=%s", a.Seed.ProfileID, b.Seed.ProfileID)
		}
		for _, row := range []struct{ session, seed, profile string }{{"default-worker", a.Seed.ID, original}, {"side-worker", b.Seed.ID, side.ID}} {
			listed, err := cli.SeedList(protocol.SessionID(row.session), false, 0)
			if err != nil || listed.Total != 1 || len(listed.Seeds) != 1 || listed.Seeds[0].ID != row.seed {
				t.Fatalf("%s list: %+v, %v", row.session, listed, err)
			}
			for _, all := range []bool{false, true} {
				ready, err := cli.SeedReady(protocol.SessionID(row.session), "", all)
				if err != nil || len(ready.Seeds) != 1 || ready.Seeds[0].ID != row.seed {
					t.Fatalf("%s ready all=%v: %+v, %v", row.session, all, ready, err)
				}
			}
			hits, err := cli.SeedSearch(protocol.SessionID(row.session), "profile boundary", 0)
			if err != nil || hits.Searched != 1 || len(hits.Hits) != 1 || hits.Hits[0].Seed.ID != row.seed {
				t.Fatalf("%s search: %+v, %v", row.session, hits, err)
			}
		}
		cross := cli.WithRequester("", "default-worker")
		checks := []struct {
			name string
			run  func() error
		}{
			{"notes", func() error { _, err := cross.SeedNotes("default-worker", b.Seed.ID, 0); return err }},
			{"open", func() error { return cross.OpenSeed(b.Seed.ID, "default-worker") }},
			{"watch", func() error { _, err := cross.SeedWatch("default-worker", b.Seed.ID, false); return err }},
			{"show", func() error { _, err := cross.SeedShow("default-worker", b.Seed.ID); return err }},
			{"tend", func() error {
				_, err := cross.SeedTransition("default-worker", b.Seed.ID, "tend", "", "", true, client.SeedTransitionOptions{})
				return err
			}},
			{"edit", func() error { _, err := cross.SeedEdit(b.Seed.ID, "cross-profile edit"); return err }},
			{"link", func() error { _, err := cross.SeedLink(a.Seed.ID, "blocks", b.Seed.ID, false); return err }},
			{"note", func() error {
				_, err := cross.SeedNote("default-worker", b.Seed.ID, "cross-profile note", "", "", false, nil)
				return err
			}},
			{"child", func() error {
				_, err := cross.SeedPlant("default-worker", "cross-profile child", "", b.Seed.ID, "", "")
				return err
			}},
			{"delegate", func() error {
				_, err := cross.StartDelegation(protocol.DelegateMessage{Cmd: protocol.CmdDelegate, RequestID: uuid.NewString(), SourceSessionID: protocol.Ptr(protocol.SessionID("default-worker")), Cwd: w.Path(), Agent: protocol.Ptr("codex"), Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindSeed, SeedID: protocol.Ptr(b.Seed.ID)}})
				return err
			}},
		}
		for _, verb := range []string{"park", "harvest", "wither", "replant"} {
			checks = append(checks, struct {
				name string
				run  func() error
			}{verb, func() error {
				_, err := cross.SeedTransition("default-worker", b.Seed.ID, verb, "cross-profile transition", "", true, client.SeedTransitionOptions{})
				return err
			}})
		}
		for _, check := range checks {
			err := check.run()
			if err == nil || !strings.Contains(err.Error(), "Default") || !strings.Contains(err.Error(), "Side") {
				t.Errorf("%s must refuse naming both profiles: %v", check.name, err)
			}
		}
		if _, err := cli.SeedList("", false, 0); err == nil || !strings.Contains(err.Error(), "--profile") || !strings.Contains(err.Error(), "Default") || !strings.Contains(err.Error(), "Side") {
			t.Fatalf("ambiguous terminal: %v", err)
		}
		if err := cli.OpenSeed(b.Seed.ID, ""); err == nil || !strings.Contains(err.Error(), "--profile") {
			t.Fatalf("ambiguous seed open: %v", err)
		}
		listed, err := cli.WithRequester("Side", "").SeedList("", false, 0)
		if err != nil || len(listed.Seeds) != 1 || listed.Seeds[0].ID != b.Seed.ID {
			t.Fatalf("explicit profile: %+v %v", listed, err)
		}
		for _, row := range []struct {
			name  string
			query func() error
		}{
			{"get", func() error { _, err := cli.DocGet(garden.Namespace, garden.CollectionSeeds, b.Seed.ID); return err }},
			{"query", func() error {
				_, err := cli.DocQuery(protocol.DocumentQuery{Namespace: garden.Namespace, Collection: garden.CollectionSeeds})
				return err
			}},
			{"count", func() error {
				_, err := cli.DocCount(protocol.DocumentQuery{Namespace: garden.Namespace, Collection: garden.CollectionSeeds})
				return err
			}},
		} {
			if err := row.query(); err == nil || !strings.Contains(err.Error(), "attn seed") {
				t.Errorf("generic garden %s: %v", row.name, err)
			}
		}
		selectProfile(app, original)
		snapshot := testworld.Await(app, protocol.EventGardenSeedsUpdated, func(m protocol.GardenSeedsUpdatedMessage) bool { return m.ProfileID == original && len(m.Seeds) == 1 })
		if snapshot.Seeds[0].ID != a.Seed.ID || snapshot.Total != 1 {
			t.Fatalf("profile switch snapshot: %+v", snapshot)
		}
		other := w.AppOn(side.ID)
		if len(other.Initial.Seeds) != 1 || other.Initial.Seeds[0].ID != b.Seed.ID {
			t.Fatalf("second profile initial garden: %+v", other.Initial.Seeds)
		}
	})
}

func TestProfileDeletionKeepsSeedsInTheirOriginalProfile(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		original := app.SelectedProfile()
		side := createProfile(app, "Side")
		selectProfile(app, side.ID)
		registerSessions(t, w, cli, "side-worker")
		seed := plantSeedAs(t, cli, "side-worker", "unfinished profile work")
		request := func() protocol.ProfileActionResultMessage {
			id := uuid.NewString()
			return profileRequest(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: id, ProfileID: side.ID, ExpectedRevision: side.Revision}, id)
		}
		refused := request()
		if refused.Success || !strings.Contains(protocol.Deref(refused.Error), "1 open seeds") || !strings.Contains(protocol.Deref(refused.Error), "clean up") {
			t.Fatalf("delete with open seed: %+v", refused)
		}
		child, err := cli.SeedPlant("side-worker", "completed archived child", "", seed, "", "")
		if err != nil {
			t.Fatal(err)
		}
		lifeMove(t, cli, "side-worker", child.Seed.ID, "harvest", "finished", "")
		lifeMove(t, cli, "side-worker", seed, "wither", "work abandoned", "")
		if _, err := cli.AgentClose("side-worker", "side-worker", "finished"); err != nil {
			t.Fatal(err)
		}

		if deleted := request(); !deleted.Success {
			t.Fatalf("delete closed garden: %+v", deleted)
		}
		archived, err := cli.WithRequester(original, "").SeedShow("", seed)
		if err != nil || archived.Seed.ProfileID != side.ID {
			t.Fatalf("closed seed archival inspection: %+v %v", archived, err)
		}
		if len(archived.Relations) != 1 || archived.Relations[0].SeedID != child.Seed.ID || archived.Seed.PlotProgress == nil || archived.Seed.PlotProgress.Total != 1 || archived.Seed.PlotProgress.Done != 1 {
			t.Fatalf("archived plot lost its relationships or progress: %+v", archived)
		}
		id := uuid.NewString()
		document := testworld.Request(app, protocol.SeedDocumentGetMessage{Cmd: protocol.CmdSeedDocumentGet, RequestID: id, SeedID: seed}, protocol.EventSeedDocumentGetResult, func(result protocol.SeedDocumentGetResultMessage) bool { return result.RequestID == id })
		if !document.Success || document.Document == nil || len(document.Document.Children) != 1 || document.Document.Children[0].ID != child.Seed.ID {
			t.Fatalf("archived document lost its child: %+v", document)
		}
		listed, err := cli.SeedList("", false, 0)
		if err != nil || len(listed.Seeds) != 0 {
			t.Fatalf("closed archive leaked into live garden: %+v %v", listed, err)
		}
		if _, err := cli.SeedTransition("", seed, "replant", "", "", false, client.SeedTransitionOptions{}); err == nil {
			t.Fatal("replanted work of a deleted profile")
		}
		if resumed := seedResumeRequest(app, seed); resumed.Success || !strings.Contains(protocol.Deref(resumed.Error), "Side") || !strings.Contains(protocol.Deref(resumed.Error), "Default") {
			t.Fatalf("resume archived work into another profile: %+v", resumed)
		}
	})
}

func TestNamedInstanceAlsoRefusesToDeleteAProfileWithOpenSeeds(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "scope-wire")
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		side := createProfile(app, "Side")
		selectProfile(app, side.ID)
		registerSessions(t, w, cli, "worker")
		planted := plantSeedAs(t, cli, "worker", "throwaway instance work")
		id := uuid.NewString()
		result := profileRequest(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: id, ProfileID: side.ID, ExpectedRevision: side.Revision}, id)
		if result.Success {
			t.Fatalf("named instance deletion: %+v %s", result, protocol.Deref(result.Error))
		}
		if _, err := cli.SeedShow("worker", planted); err != nil {
			t.Fatal("refused deletion lost its seed")
		}
	})
}

func TestDeletingAProfileWithAClosedSeedAndALiveDelegate(t *testing.T) {
	for _, named := range []bool{false, true} {
		t.Run(map[bool]string{false: "production", true: "named"}[named], func(t *testing.T) {
			if named {
				t.Setenv("ATTN_INSTANCE", "scope-delete")
			}
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app, cli := w.App(), w.Client()
				side := createProfile(app, "Side")
				selectProfile(app, side.ID)
				cwd := registerDelegationCaller(t, w, cli, "caller")
				worker, err := cli.Delegate(delegateFrom("caller", cwd, "Close before deleting this profile", fakeagent.Codex))
				if err != nil {
					t.Fatal(err)
				}
				lifeMove(t, cli, string(worker.SessionID), worker.SeedID, "harvest", "finished", "")
				remove := func() protocol.ProfileActionResultMessage {
					for _, current := range w.AppOn(side.ID).Initial.Profiles {
						if current.ID == side.ID {
							side = current
						}
					}
					id := uuid.NewString()
					return profileRequest(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: id, ProfileID: side.ID, ExpectedRevision: side.Revision}, id)
				}
				deleted := remove()
				if deleted.Success || !strings.Contains(protocol.Deref(deleted.Error), "live agents") {
					t.Fatalf("live delegate must prevent deletion: %+v", deleted)
				}
				if _, err := cli.AgentClose(string(worker.SessionID), worker.SessionID, "finished my work"); err != nil {
					t.Fatalf("the delegate must always be able to close itself: %v", err)
				}
				if _, err := cli.AgentClose("caller", "caller", "finished dispatching"); err != nil {
					t.Fatal(err)
				}
				if deleted := remove(); !deleted.Success {
					t.Fatalf("deletion after cleanup: %+v", deleted)
				}
			})
		})
	}
}

func TestGardenReviewsBelongToTheRequestingProfile(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		original := app.SelectedProfile()
		side := createProfile(app, "Side")
		first, err := cli.WithRequester(original, "").SeedReviewStart()
		if err != nil || first.Review == nil || first.Review.Run.ProfileID != original {
			t.Fatalf("Default review: %+v %v", first, err)
		}
		second, err := cli.WithRequester(side.ID, "").SeedReviewStart()
		if err != nil || second.Review == nil || second.Review.Run.ProfileID != side.ID || second.Review.Run.ID == first.Review.Run.ID {
			t.Fatalf("Side review: %+v %v", second, err)
		}
		if _, err := cli.WithRequester(original, "").SeedReviewShow(second.Review.Run.ID); err == nil || !strings.Contains(err.Error(), "Side") || !strings.Contains(err.Error(), "Default") {
			t.Fatalf("cross-profile review: %v", err)
		}
		shown, err := cli.WithRequester(original, "").SeedReviewShow(first.Review.Run.ID)
		if err != nil || shown.Review == nil || shown.Review.Run.ID != first.Review.Run.ID {
			t.Fatalf("Default latest: %+v %v", shown, err)
		}
	})
}
