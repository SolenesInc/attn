package daemon_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestDeletingAProfileCancelsItsGardenReviewAndAdvisorJobs(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	home := app.SelectedProfile()
	side := createProfile(app, "Side")
	selectProfile(app, side.ID)
	scoped := cli.WithGardenProfile(side.ID, "")
	seed := gardenReviewAbandonedSeed(t, w, w.AppOn(side.ID), scoped, "side-review", "Side review candidate")
	setSetting(t, app, "garden.advisor", `{"agent":"claude"}`)
	t.Setenv("ATTN_HEADLESS_TASKS", "on")
	review := gardenReviewStart(t, scoped)
	if len(review.Items) != 1 || review.Run.Status != "running" {
		t.Fatalf("review must advise the Side candidate: %+v", review)
	}
	w.HeadlessTask()
	lifeMove(t, scoped, "", seed, "harvest", "completed outside the review", "")
	selectProfile(app, home)
	gardenReviewAbandonedSeed(t, w, app, cli, "home-review", "Default review candidate")
	other := gardenReviewStart(t, cli.WithGardenProfile(home, ""))
	w.HeadlessTask()
	for _, current := range w.AppOn(side.ID).Initial.Profiles {
		if current.ID == side.ID {
			side = current
		}
	}
	id := uuid.NewString()
	deleted := profileRequest(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: id, ProfileID: side.ID, ExpectedRevision: side.Revision, DestinationProfileID: home}, id)
	if !deleted.Success {
		t.Fatalf("delete profile while its review advises closed work: %+v", deleted)
	}
	check := func(app *testworld.Peer, cli *client.Client) {
		t.Helper()
		id := uuid.NewString()
		listed := testworld.Request(app, protocol.TaskListMessage{Cmd: protocol.CmdTaskList, RequestID: protocol.Ptr(id)}, protocol.EventTaskListResult, func(r protocol.TaskListResultMessage) bool { return r.RequestID == id })
		foundOther := false
		for _, task := range listed.Tasks {
			if task.Kind == "garden_review_classify" && task.Subject == review.Items[0].ID {
				t.Fatalf("deleted profile retains an advisor job: %+v", task)
			}
			foundOther = foundOther || task.Subject == other.Items[0].ID
		}
		shown, err := cli.WithGardenProfile(home, "").SeedReviewShow(other.Run.ID)
		if !foundOther || err != nil || shown.Review.Run.Status != "running" {
			t.Fatalf("the other profile's review must keep running: %+v %v, job present=%v", shown, err, foundOther)
		}
	}
	check(app, cli)
	w.restart()
	w.HeadlessTask()
	check(w.App(), w.Client())
}

func TestDeletingAProfileDropsMovedAgentsGardenWatchesAndBells(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		home := app.SelectedProfile()
		side := createProfile(app, "Side")
		selectProfile(app, side.ID)
		registerSessions(t, w, cli, "watcher", "worker")
		seed := plantSeedAs(t, cli, "worker", "Closed work in a deleted profile")
		if _, err := cli.SeedWatch("watcher", seed, false); err != nil {
			t.Fatal(err)
		}
		lifeMove(t, cli, "worker", seed, "harvest", "finished", "")
		if _, err := cli.SeedNote("worker", seed, "a final update", "", "", true, nil); err != nil {
			t.Fatal(err)
		}
		w.advance(0)
		id := uuid.NewString()
		deleted := profileRequest(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: id, ProfileID: side.ID, ExpectedRevision: side.Revision, DestinationProfileID: home}, id)
		if !deleted.Success {
			t.Fatalf("delete a profile containing closed work and user-launched agents: %+v", deleted)
		}
		if queriedSession(t, cli, "watcher").ProfileID != home {
			t.Fatal("deletion did not move the watcher to the destination profile")
		}
		shown, err := cli.SeedShow("watcher", seed)
		if err != nil || shown.Watching || len(shown.WatchingVia) != 0 {
			t.Fatalf("the moved agent still watches archived work: %+v %v", shown, err)
		}
		if items := readInbox(t, cli, "watcher", 0).Items; len(items) != 0 {
			t.Fatalf("the moved agent retains bells for its deleted profile: %+v", items)
		}
	})
}

func TestFreeTenderNamesStayInsideTheirSeedsProfile(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		home := app.SelectedProfile()
		registerSessions(t, w, cli, "default-worker")
		original := plantSeedAs(t, cli, "default-worker", "free worker in Default")
		lifeMove(t, cli, "default-worker", original, "tend", "", "keel")
		if _, err := cli.WithGardenProfile("", "default-worker").SeedEdit(original, "legacy alias remains editable"); err != nil {
			t.Fatal(err)
		}
		side := createProfile(app, "Side")
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
		selectProfile(w.App(), home)
		registerSessions(t, w, cli, "default-worker")
		id := uuid.NewString()
		moved := profileRequest(w.App(), protocol.SessionMoveMessage{Cmd: protocol.CmdSessionMove, RequestID: id, SessionID: "default-worker", ExpectedProfileID: side.ID, DestinationProfileID: home}, id)
		if !moved.Success {
			t.Fatalf("return the newly registered worker to Default: %+v", moved)
		}
		if _, err := cli.WithGardenProfile("", "default-worker").SeedEdit(original, "the existing free claim stays editable after registration"); err != nil {
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
		id = uuid.NewString()
		moved = profileRequest(w.App(), protocol.SessionMoveMessage{Cmd: protocol.CmdSessionMove, RequestID: id, SessionID: "registered-keel", ExpectedProfileID: side.ID, DestinationProfileID: home}, id)
		if !moved.Success {
			t.Fatalf("a foreign free alias must not hold the registered member in Side: %+v", moved)
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
			listed, err := cli.SeedList(row.session, false, 0)
			if err != nil || listed.Total != 1 || len(listed.Seeds) != 1 || listed.Seeds[0].ID != row.seed {
				t.Fatalf("%s list: %+v, %v", row.session, listed, err)
			}
			for _, all := range []bool{false, true} {
				ready, err := cli.SeedReady(row.session, "", all)
				if err != nil || len(ready.Seeds) != 1 || ready.Seeds[0].ID != row.seed {
					t.Fatalf("%s ready all=%v: %+v, %v", row.session, all, ready, err)
				}
			}
			hits, err := cli.SeedSearch(row.session, "profile boundary", 0)
			if err != nil || hits.Searched != 1 || len(hits.Hits) != 1 || hits.Hits[0].Seed.ID != row.seed {
				t.Fatalf("%s search: %+v, %v", row.session, hits, err)
			}
		}
		cross := cli.WithGardenProfile("", "default-worker")
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
				_, err := cross.StartDelegation(protocol.DelegateMessage{Cmd: protocol.CmdDelegate, RequestID: uuid.NewString(), SourceSessionID: protocol.Ptr("default-worker"), Cwd: w.Path(), Agent: protocol.Ptr("codex"), Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindSeed, SeedID: protocol.Ptr(b.Seed.ID)}})
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
		listed, err := cli.WithGardenProfile("Side", "").SeedList("", false, 0)
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
			return profileRequest(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: id, ProfileID: side.ID, ExpectedRevision: side.Revision, DestinationProfileID: original}, id)
		}
		refused := request()
		if refused.Success || !strings.Contains(protocol.Deref(refused.Error), "1 open seeds") || !strings.Contains(protocol.Deref(refused.Error), "harvest or wither") {
			t.Fatalf("delete with open seed: %+v", refused)
		}
		child, err := cli.SeedPlant("side-worker", "completed archived child", "", seed, "", "")
		if err != nil {
			t.Fatal(err)
		}
		lifeMove(t, cli, "side-worker", child.Seed.ID, "harvest", "finished", "")
		lifeMove(t, cli, "side-worker", seed, "wither", "work abandoned", "")
		if deleted := request(); !deleted.Success {
			t.Fatalf("delete closed garden: %+v", deleted)
		}
		archived, err := cli.WithGardenProfile(original, "").SeedShow("", seed)
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

func TestMovingAnAgentToAnotherProfileRequiresDetachedGardenWork(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		original := app.SelectedProfile()
		registerSessions(t, w, cli, "worker")
		planted := plantSeedAs(t, cli, "worker", "leave work in its profile")
		lifeMove(t, cli, "worker", planted, "tend", "", "")
		if _, err := cli.SeedWatch("worker", planted, false); err != nil {
			t.Fatal(err)
		}
		side := createProfile(app, "Side")
		move := func() protocol.ProfileActionResultMessage {
			id := uuid.NewString()
			return profileRequest(app, protocol.SessionMoveMessage{Cmd: protocol.CmdSessionMove, RequestID: id, SessionID: "worker", ExpectedProfileID: original, DestinationProfileID: side.ID}, id)
		}
		if result := move(); result.Success || !strings.Contains(protocol.Deref(result.Error), planted) || !strings.Contains(protocol.Deref(result.Error), "Default") || !strings.Contains(protocol.Deref(result.Error), "Side") {
			t.Fatalf("move while tending: %+v", result)
		}
		lifeMove(t, cli, "worker", planted, "park", "paused", "")
		if result := move(); !result.Success {
			t.Fatalf("move parked worker: %+v", result)
		}
		shown, err := cli.SeedShow("worker", planted)
		if err == nil {
			t.Fatalf("moved worker still sees old work: %+v", shown)
		}
		ready, err := cli.SeedReady("worker", "", false)
		if err != nil || len(ready.Seeds) != 0 {
			t.Fatalf("moved worker prime: %+v %v", ready, err)
		}
		session := queriedSession(t, cli, "worker")
		if protocol.Deref(session.SeedID) != "" {
			t.Fatalf("moved worker displays old seed: %+v", session)
		}
		next := plantSeedAs(t, cli, "worker", "new profile work")
		lifeMove(t, cli, "worker", next, "tend", "", "")
		former, err := cli.WithGardenProfile(original, "").SeedShow("", planted)
		if err != nil || former.Seed.ProfileID != original {
			t.Fatalf("former seed after new work: %+v %v", former, err)
		}
		if continuation := former.Seed.Continuation; continuation != nil && (continuation.Cwd != "" || continuation.ResumeAvailable) {
			t.Fatalf("former seed exposes the moved agent's execution: %+v", continuation)
		}
	})
}

func TestNamedInstanceCanDeleteAProfileWithOpenSeeds(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "scope-wire")
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		original := app.SelectedProfile()
		side := createProfile(app, "Side")
		selectProfile(app, side.ID)
		registerSessions(t, w, cli, "worker")
		planted := plantSeedAs(t, cli, "worker", "throwaway instance work")
		id := uuid.NewString()
		result := profileRequest(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: id, ProfileID: side.ID, ExpectedRevision: side.Revision, DestinationProfileID: original}, id)
		if !result.Success {
			t.Fatalf("named instance deletion: %+v %s", result, protocol.Deref(result.Error))
		}
		if _, err := cli.SeedShow("worker", planted); err == nil {
			t.Fatal("deleted profile open seed entered destination garden")
		}
	})
}

func TestDispatchedAgentsStayInTheirOriginalProfileAfterTheirSeedCloses(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	original := app.SelectedProfile()
	cwd := registerDelegationCaller(t, w, cli, "caller")
	worker, err := cli.Delegate(delegateFrom("caller", cwd, "Work stays in its profile", fakeagent.Codex))
	if err != nil {
		t.Fatal(err)
	}
	side := createProfile(app, "Side")
	for _, verb := range []string{"park", "harvest"} {
		if verb == "harvest" {
			lifeMove(t, cli, worker.SessionID, worker.SeedID, "tend", "", "")
		}
		lifeMove(t, cli, worker.SessionID, worker.SeedID, verb, "finished this turn", "")
		id := uuid.NewString()
		result := profileRequest(app, protocol.SessionMoveMessage{Cmd: protocol.CmdSessionMove, RequestID: id, SessionID: worker.SessionID, ExpectedProfileID: original, DestinationProfileID: side.ID}, id)
		if result.Success || !strings.Contains(protocol.Deref(result.Error), worker.SeedID) || !strings.Contains(protocol.Deref(result.Error), "Default") || !strings.Contains(protocol.Deref(result.Error), "Side") || !strings.Contains(protocol.Deref(result.Error), "delegate afresh") {
			t.Fatalf("move dispatched agent after %s: %+v", verb, result)
		}
	}
	sideWorker, err := cli.WithGardenProfile("Side", "").Delegate(delegateFrom("", cwd, "Fresh work in Side", fakeagent.Codex))
	if err != nil {
		t.Fatalf("delegate through an explicitly scoped client: %v", err)
	}
	shown, err := cli.WithGardenProfile(side.ID, "").SeedShow("", sideWorker.SeedID)
	if err != nil || shown.Seed.ProfileID != side.ID || queriedSession(t, cli, sideWorker.SessionID).ProfileID != side.ID {
		t.Fatalf("fresh Side delegation: %+v %v", shown, err)
	}
}

func TestDeletingAProfileWithAClosedSeedAndALiveDelegate(t *testing.T) {
	for _, named := range []bool{false, true} {
		t.Run(map[bool]string{false: "production", true: "named"}[named], func(t *testing.T) {
			if named {
				t.Setenv("ATTN_INSTANCE", "scope-delete")
			}
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app, cli := w.App(), w.Client()
				home := app.SelectedProfile()
				side := createProfile(app, "Side")
				selectProfile(app, side.ID)
				cwd := registerDelegationCaller(t, w, cli, "caller")
				worker, err := cli.Delegate(delegateFrom("caller", cwd, "Close before deleting this profile", fakeagent.Codex))
				if err != nil {
					t.Fatal(err)
				}
				lifeMove(t, cli, worker.SessionID, worker.SeedID, "harvest", "finished", "")
				remove := func() protocol.ProfileActionResultMessage {
					for _, current := range w.AppOn(side.ID).Initial.Profiles {
						if current.ID == side.ID {
							side = current
						}
					}
					id := uuid.NewString()
					return profileRequest(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: id, ProfileID: side.ID, ExpectedRevision: side.Revision, DestinationProfileID: home}, id)
				}
				deleted := remove()
				if named {
					if !deleted.Success {
						t.Fatalf("named deletion with a live delegate: %+v", deleted)
					}
				} else if deleted.Success || !strings.Contains(protocol.Deref(deleted.Error), "0 open seeds and 1 live dispatched sessions") || !strings.Contains(protocol.Deref(deleted.Error), "close the delegated agents") {
					t.Fatalf("production deletion must count the live delegate: %+v", deleted)
				}
				if _, err := cli.AgentClose(worker.SessionID, worker.SessionID, "finished my work"); err != nil {
					t.Fatalf("the delegate must always be able to close itself: %v", err)
				}
				if !named {
					if deleted := remove(); !deleted.Success {
						t.Fatalf("deletion after closing the delegate: %+v", deleted)
					}
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
		first, err := cli.WithGardenProfile(original, "").SeedReviewStart()
		if err != nil || first.Review == nil || first.Review.Run.ProfileID != original {
			t.Fatalf("Default review: %+v %v", first, err)
		}
		second, err := cli.WithGardenProfile(side.ID, "").SeedReviewStart()
		if err != nil || second.Review == nil || second.Review.Run.ProfileID != side.ID || second.Review.Run.ID == first.Review.Run.ID {
			t.Fatalf("Side review: %+v %v", second, err)
		}
		if _, err := cli.WithGardenProfile(original, "").SeedReviewShow(second.Review.Run.ID); err == nil || !strings.Contains(err.Error(), "Side") || !strings.Contains(err.Error(), "Default") {
			t.Fatalf("cross-profile review: %v", err)
		}
		shown, err := cli.WithGardenProfile(original, "").SeedReviewShow(first.Review.Run.ID)
		if err != nil || shown.Review == nil || shown.Review.Run.ID != first.Review.Run.ID {
			t.Fatalf("Default latest: %+v %v", shown, err)
		}
	})
}

func TestMovingADispatcherCannotCloseItsFormerProfilesWorkerBySessionID(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	original := app.SelectedProfile()
	cwd := registerDelegationCaller(t, w, cli, "caller")
	worker, err := cli.Delegate(delegateFrom("caller", cwd, "Stay inside Default", fakeagent.Codex))
	if err != nil {
		t.Fatal(err)
	}
	w.Launched(worker.SessionID)
	before, err := cli.SeedNotes("caller", worker.SeedID, 0)
	if err != nil {
		t.Fatal(err)
	}
	side := createProfile(app, "Side")
	id := uuid.NewString()
	moved := profileRequest(app, protocol.SessionMoveMessage{Cmd: protocol.CmdSessionMove, RequestID: id, SessionID: "caller", ExpectedProfileID: original, DestinationProfileID: side.ID}, id)
	if !moved.Success {
		t.Fatalf("move dispatcher: %+v", moved)
	}
	for _, target := range []string{worker.SeedID, worker.SessionID} {
		_, err := cli.AgentClose(target, "caller", "finished")
		if err == nil || !strings.Contains(err.Error(), "Default") || !strings.Contains(err.Error(), "Side") {
			t.Fatalf("cross-profile close by %s: %v", target, err)
		}
	}
	if shown := showSession(t, cli, worker.SessionID); protocol.Deref(shown.ClosedAt) != "" {
		t.Fatalf("refused close ended the worker: %+v", shown)
	}
	after, err := cli.WithGardenProfile(original, "").SeedNotes("", worker.SeedID, 0)
	if err != nil || len(after.Notes) != len(before.Notes) {
		t.Fatalf("refused close wrote a seed note: %+v %v", after, err)
	}
}
