package daemon_test

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAGardenReviewFreezesItsCandidatesAndRecipe(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	writeCrewCharter(t, w, "trellis")
	app, cli := w.App(), w.Client()
	tender := gardenReviewConversationTending(t, w, app, cli, "interrupted work")
	w.restart()
	app, cli = w.App(), w.Client()
	if state := queriedSession(t, cli, tender).State; state != protocol.SessionStateRecoverable {
		t.Fatalf("the tender came back %q, want recoverable", state)
	}
	abandoned := gardenReviewAbandonedSeed(t, w, app, cli, "gardener", "old work")
	keeper := spawnPanes(w, app, w.Path("keeper"))[0]
	claimed := plantSeedAs(t, cli, keeper.session, "member-owned work")
	if _, err := cli.SeedTransition("", claimed, "tend", "", "trellis", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatalf("trellis tends: %v", err)
	}
	closePane(app, keeper)

	if shown := gardenReviewShow(t, cli, ""); shown.CandidateCount != 1 || shown.Review != nil {
		t.Fatalf("show before any review = %d candidates and review %+v, want only the abandoned seed counted and nothing started", shown.CandidateCount, shown.Review)
	}
	if again := gardenReviewShow(t, cli, ""); again.Review != nil {
		t.Fatalf("showing twice started review %+v", again.Review)
	}

	setSetting(t, app, "garden.advisor", `{"agent":"claude","model":"sonnet","effort":"medium"}`)
	first := gardenReviewStart(t, cli)
	frozen := protocol.GardenReviewRecipe{Agent: "claude", Model: "sonnet", Effort: protocol.Ptr("medium")}
	if first.Run.Status != "running" || !slices.Equal(first.Run.CandidateIds, []string{abandoned}) || !reflect.DeepEqual(first.Run.Recipe, frozen) {
		t.Fatalf("the started review = %+v, want it running over %s with the saved recipe", first.Run, abandoned)
	}
	if len(first.Items) != 1 || !slices.Equal(first.Items[0].Actions, []string{"keep_growing", "park", "harvest", "wither"}) {
		t.Fatalf("the review items = %+v, want the abandoned seed with the growing seed's actions", first.Items)
	}

	setSetting(t, app, "garden.advisor", `{"agent":"codex","model":"later","effort":"low"}`)
	late := gardenReviewAbandonedSeed(t, w, app, cli, "latecomer", "late work")
	second := gardenReviewStart(t, cli)
	if second.Run.ID != first.Run.ID || !reflect.DeepEqual(second.Run.Recipe, frozen) || !slices.Equal(second.Run.CandidateIds, []string{abandoned}) {
		t.Fatalf("starting again = %+v, want the running review unchanged", second.Run)
	}

	if canceled, err := cli.SeedReviewCancel(first.Run.ID); err != nil || canceled.Review == nil || canceled.Review.Run.Status != "canceled" {
		t.Fatalf("cancel = %+v, %v; want the review canceled", canceled, err)
	}
	w.restart()
	app, cli = w.App(), w.Client()
	if after := gardenReviewShow(t, cli, first.Run.ID); after.Review == nil || after.Review.Run.Status != "canceled" {
		t.Fatalf("after a restart the canceled review is %+v", after.Review)
	}

	chief := spawnPanes(w, app, w.Path("chief"))[0].session
	if made := testworld.Request(app, protocol.SetChiefOfStaffMessage{Cmd: protocol.CmdSetChiefOfStaff, SessionID: chief, ChiefOfStaff: true},
		protocol.EventChiefOfStaffResult, func(m protocol.ChiefOfStaffResultMessage) bool { return m.SessionID == chief }); !made.Success {
		t.Fatalf("make chief the chief of staff: %s", protocol.Deref(made.Error))
	}
	withChief := gardenReviewStart(t, cli)
	if withChief.Run.ID == first.Run.ID || len(withChief.Items) != 2 {
		t.Fatalf("a review after the cancel = %+v, want a new one over both abandoned seeds", withChief.Run)
	}
	for _, item := range withChief.Items {
		if item.SeedID != abandoned && item.SeedID != late {
			t.Errorf("the review offers %s, want only the abandoned seeds", item.SeedID)
		}
		if !slices.Equal(item.Actions, []string{"send_to_chief", "keep_growing", "park", "harvest", "wither"}) {
			t.Errorf("%s offers %v, want send_to_chief first", item.SeedID, item.Actions)
		}
	}
}

func TestReviewActionsResolveTheirItems(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	harvested := gardenReviewAbandonedSeed(t, w, app, cli, "first", "harvest me")
	kept := gardenReviewAbandonedSeed(t, w, app, cli, "second", "keep me")
	changed := gardenReviewAbandonedSeed(t, w, app, cli, "third", "changed under the review")
	reviewer := spawnPanes(w, app, w.Path("reviewer"))[0].session

	stale := gardenReviewStart(t, cli)
	gardenReviewAwaitFailedFirstAdvice(app, stale.Run.ID)
	if _, err := cli.SeedNote(reviewer, changed, "new evidence", "", "", false, nil); err != nil {
		t.Fatal(err)
	}
	if refused := gardenReviewMoveFromApp(app, changed, "park", gardenReviewReceipts(stale)[changed]); refused.Success || !strings.Contains(protocol.Deref(refused.Error), "changed since this review item was loaded; refresh the garden") {
		t.Fatalf("parking with the stale receipt = %+v, want a refusal", refused)
	}
	if seed := gardenReviewSeed(t, cli, changed); seed.Status != "growing" {
		t.Errorf("the refused park left the seed %s", seed.Status)
	}
	if item := gardenReviewItem(t, gardenReviewShow(t, cli, stale.Run.ID).Review, changed); item.Resolution != "unresolved" {
		t.Errorf("the refused item = %s, want unresolved", item.Resolution)
	}
	if _, err := cli.SeedReviewCancel(stale.Run.ID); err != nil {
		t.Fatal(err)
	}

	review := gardenReviewStart(t, cli)
	gardenReviewAwaitFailedFirstAdvice(app, review.Run.ID)
	receipts := gardenReviewReceipts(review)
	if moved := gardenReviewMoveFromApp(app, harvested, "harvest", receipts[harvested]); !moved.Success {
		t.Fatalf("harvest from the review: %s", protocol.Deref(moved.Error))
	}
	keptAt := time.Now()
	if _, err := cli.SeedReviewKeep(kept, receipts[kept]); err != nil {
		t.Fatalf("keep from the review: %v", err)
	}
	if seed := gardenReviewSeed(t, cli, kept); seed.Status != "growing" {
		t.Errorf("keeping left the seed %s, want it growing", seed.Status)
	}
	shown := gardenReviewShow(t, cli, review.Run.ID).Review
	if item := gardenReviewItem(t, shown, harvested); item.Resolution != "resolved" || protocol.Deref(item.ResolvedAction) != "harvest" {
		t.Errorf("the harvested item = %s by %q, want resolved by the harvest before any advice", item.Resolution, protocol.Deref(item.ResolvedAction))
	}
	item := gardenReviewItem(t, shown, kept)
	again, err := time.Parse(time.RFC3339Nano, protocol.Deref(item.ReviewAgainAt))
	if item.Resolution != "resolved" || protocol.Deref(item.ResolvedAction) != "keep_growing" || err != nil || again.Before(keptAt.Add(7*24*time.Hour-time.Minute)) {
		t.Errorf("the kept item = %s by %q, review again at %q; want kept growing for seven days", item.Resolution, protocol.Deref(item.ResolvedAction), protocol.Deref(item.ReviewAgainAt))
	}
	if shown.Run.Status != "running" {
		t.Errorf("with an item unresolved the review is %s", shown.Run.Status)
	}

	if moved := gardenReviewMoveFromApp(app, changed, "park", receipts[changed]); !moved.Success {
		t.Fatalf("park from the review: %s", protocol.Deref(moved.Error))
	}
	done := gardenReviewShow(t, cli, review.Run.ID)
	if done.Review.Run.Status != "complete" || done.CandidateCount != 0 {
		t.Errorf("after every item was settled the review is %s with %d candidates left, want complete with none", done.Review.Run.Status, done.CandidateCount)
	}
}

func TestTheCLIAndTheAppShowTheSameReviewWithItsAdvisorProgress(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	seed := gardenReviewAbandonedSeed(t, w, app, cli, "gardener", "old work")
	review := gardenReviewStart(t, cli)

	retrying := testworld.Await(app, protocol.EventGardenReviewUpdated, func(m protocol.GardenReviewUpdatedMessage) bool {
		return m.Review.Run.ID == review.Run.ID && len(m.Review.Items) == 1 && protocol.Deref(m.Review.Items[0].AdvisorState) == "retrying"
	}).Review.Items[0]
	if retrying.SeedID != seed || protocol.Deref(retrying.AdvisorAttempt) != 1 || protocol.Deref(retrying.AdvisorMaxAttempts) != 3 ||
		retrying.AdvisorRetryAt == nil || protocol.Deref(retrying.AdvisorError) == "" {
		t.Fatalf("the app sees the failed first advice as %+v, want attempt 1 of 3 with its retry time and error", retrying)
	}

	fromCLI := gardenReviewShow(t, cli, review.Run.ID)
	requestID := uuid.NewString()
	fromApp := testworld.Request(app, protocol.SeedReviewShowMessage{Cmd: protocol.CmdSeedReviewShow, RequestID: protocol.Ptr(requestID), ReviewID: protocol.Ptr(review.Run.ID)},
		protocol.EventSeedReviewResult, func(m protocol.SeedReviewResultMessage) bool { return m.RequestID == requestID })
	if !fromApp.Success || fromApp.CandidateCount != fromCLI.CandidateCount || !reflect.DeepEqual(fromApp.Review, fromCLI.Review) {
		t.Fatalf("the app shows %+v (%d candidates), the CLI %+v (%d); want the same review", fromApp.Review, fromApp.CandidateCount, fromCLI.Review, fromCLI.CandidateCount)
	}
	if got := fromCLI.Review.Items[0]; !reflect.DeepEqual(got, retrying) {
		t.Errorf("show reports %+v, want the progress the app was sent %+v", got, retrying)
	}
}

func gardenReviewAbandonedSeed(t *testing.T, w *world, app *testworld.Peer, cli *client.Client, name, title string) string {
	t.Helper()
	cwd := w.Path(name)
	pane := spawnPanes(w, app, cwd)[0]
	seed := gardenReviewPlantTended(t, cli, pane.session, title)
	closePane(app, pane)
	if err := os.RemoveAll(cwd); err != nil {
		t.Fatal(err)
	}
	return seed
}

func gardenReviewRegisteredAbandonedSeed(t *testing.T, w *world, cli *client.Client, session, title string) string {
	t.Helper()
	registerSessions(t, w, cli, session)
	seed := gardenReviewPlantTended(t, cli, session, title)
	if err := cli.Unregister(session); err != nil {
		t.Fatal(err)
	}
	return seed
}

func gardenReviewPlantTended(t *testing.T, cli *client.Client, session, title string) string {
	t.Helper()
	planted, err := cli.SeedPlant(session, title, "Carry "+title+" to the end.", "", "", "")
	if err != nil {
		t.Fatalf("plant %q: %v", title, err)
	}
	if _, err := cli.SeedTransition(session, planted.Seed.ID, "tend", "", "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatalf("tend %q: %v", title, err)
	}
	return planted.Seed.ID
}

func gardenReviewConversationTending(t *testing.T, w *world, app *testworld.Peer, cli *client.Client, title string) string {
	t.Helper()
	session := w.Spawn(app, fakeagent.Claude, w.Path("interrupted"))
	run := w.Launched(session)
	app.TypeLine(session, "carry "+title)
	if got := run.Prompted(); got != "carry "+title {
		t.Fatalf("claude received %q", got)
	}
	run.Reply("On it. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	gardenReviewPlantTended(t, cli, session, title)
	return session
}

func gardenReviewStart(t *testing.T, cli *client.Client) protocol.GardenReview {
	t.Helper()
	started, err := cli.SeedReviewStart()
	if err != nil || started.Review == nil {
		t.Fatalf("start the Garden review = %+v, %v", started, err)
	}
	return *started.Review
}

func gardenReviewShow(t *testing.T, cli *client.Client, reviewID string) *protocol.SeedReviewResult {
	t.Helper()
	shown, err := cli.SeedReviewShow(reviewID)
	if err != nil {
		t.Fatalf("show the Garden review %q: %v", reviewID, err)
	}
	return shown
}

func gardenReviewSeed(t *testing.T, cli *client.Client, seedID string) protocol.Seed {
	t.Helper()
	shown, err := cli.SeedShow("", seedID)
	if err != nil {
		t.Fatalf("show %s: %v", seedID, err)
	}
	return shown.Seed
}

func gardenReviewMoveFromApp(app *testworld.Peer, seedID, verb string, receipt protocol.SeedReviewActionContext) protocol.SeedTransitionResultMessage {
	requestID := uuid.NewString()
	move := protocol.SeedTransitionMessage{Cmd: protocol.CmdSeedTransition, RequestID: protocol.Ptr(requestID), SeedID: seedID, Verb: verb}
	if receipt.ReviewID != "" {
		move.Review = &receipt
	}
	if verb == "harvest" || verb == "wither" {
		move.Reason = protocol.Ptr("settled in review")
	}
	return testworld.Request(app, move, protocol.EventSeedTransitionResult, func(m protocol.SeedTransitionResultMessage) bool { return m.RequestID == requestID })
}

func gardenReviewReceipts(review protocol.GardenReview) map[string]protocol.SeedReviewActionContext {
	receipts := map[string]protocol.SeedReviewActionContext{}
	for _, item := range review.Items {
		receipts[item.SeedID] = protocol.SeedReviewActionContext{ReviewID: review.Run.ID, EvidenceVersion: item.EvidenceVersion}
	}
	return receipts
}

func gardenReviewItem(t *testing.T, review *protocol.GardenReview, seedID string) protocol.GardenReviewItem {
	t.Helper()
	for _, item := range review.Items {
		if item.SeedID == seedID {
			return item
		}
	}
	t.Fatalf("the review has no item for %s", seedID)
	return protocol.GardenReviewItem{}
}

func gardenReviewAwaitFailedFirstAdvice(app *testworld.Peer, reviewID string) {
	testworld.Await(app, protocol.EventGardenReviewUpdated, func(m protocol.GardenReviewUpdatedMessage) bool {
		if m.Review.Run.ID != reviewID {
			return false
		}
		for _, item := range m.Review.Items {
			if protocol.Deref(item.AdvisorState) != "retrying" {
				return false
			}
		}
		return true
	})
}

func TestAFailedReviewItemRetriesWithFreshEvidenceOrSettles(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		edited := gardenReviewRegisteredAbandonedSeed(t, w, cli, "first", "retry me")
		harvested := gardenReviewRegisteredAbandonedSeed(t, w, cli, "second", "finished elsewhere")
		review := gardenReviewStart(t, cli)
		w.advance(0)
		w.advance(time.Minute)
		w.advance(2 * time.Minute)
		for _, item := range gardenReviewShow(t, cli, review.Run.ID).Review.Items {
			if item.Status != "failed" || item.Resolution != "unresolved" {
				t.Fatalf("after three failed advice attempts %s is %s and %s, want failed and unresolved", item.SeedID, item.Status, item.Resolution)
			}
		}
		receipts := gardenReviewReceipts(review)

		if _, err := cli.SeedEdit(edited, "Use this newer body for the retry."); err != nil {
			t.Fatal(err)
		}
		retried, err := cli.SeedReviewRetry(review.Run.ID, edited)
		if err != nil {
			t.Fatalf("retry the failed item: %v", err)
		}
		fresh := gardenReviewItem(t, retried.Review, edited)
		if (fresh.Status != "queued" && fresh.Status != "running") || fresh.EvidenceVersion == receipts[edited].EvidenceVersion || retried.Review.Run.Status != "running" {
			t.Errorf("the retried item = %s with evidence %s in a %s review, want it back with the advisor on fresh evidence", fresh.Status, fresh.EvidenceVersion, retried.Review.Run.Status)
		}
		if _, err := cli.SeedReviewKeep(edited, protocol.SeedReviewActionContext{ReviewID: review.Run.ID, EvidenceVersion: fresh.EvidenceVersion}); err != nil {
			t.Fatalf("keep the retried item on its fresh receipt: %v", err)
		}

		registerSessions(t, w, cli, "closer")
		if _, err := cli.SeedTransition("closer", harvested, "harvest", "The work is complete.", "", false, client.SeedTransitionOptions{}); err != nil {
			t.Fatal(err)
		}
		settled, err := cli.SeedReviewRetry(review.Run.ID, harvested)
		if err != nil {
			t.Fatalf("retry the harvested seed's item: %v", err)
		}
		if item := gardenReviewItem(t, settled.Review, harvested); item.Resolution != "no_longer_applicable" || settled.Review.Run.Status != "complete" {
			t.Errorf("retrying the harvested seed's item left it %s in a %s review, want it no longer applicable and the review complete", item.Resolution, settled.Review.Run.Status)
		}
	})
}
