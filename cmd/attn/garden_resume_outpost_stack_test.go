package main_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type gardenOutpostSeed struct {
	home, outpost *testworld.Stack
	pair          *testworld.OutpostPair
	session, seed string
	conversation  string
	cwd           string
}

func closedOutpostSeedTender(t *testing.T, harness fakeagent.Harness) gardenOutpostSeed {
	t.Helper()
	home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	home.Vars = append(home.Vars, "ATTN_HEADLESS_TASKS=on")
	outpost := testworld.NewStack(t, testworld.WithAgents(harness))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	cwd := outpost.Path("agent")
	outpost.SetCodexFolderTrust(cwd, "trusted")
	id := home.Spawn(pair.App, harness, cwd, func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	run := outpost.LaunchedCarrying(pair.App.Terminal(id))
	pair.App.TypeLine(id, "first work")
	run.Prompted()
	run.Reply("Done. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(pair.App, id, func(s protocol.Session) bool { return s.Usage != nil })
	planted, err := home.Client().SeedPlant(protocol.SessionID(id), "Finish the outpost work", "Finish it.", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := home.Client().SeedTransition(protocol.SessionID(id), planted.Seed.ID, "tend", "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := home.Client().Unregister(protocol.SessionID(id)); err != nil {
		t.Fatal(err)
	}
	testworld.Await(pair.App, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == id
	})
	setSetting(t, pair.App, "garden.advisor", `{"agent":"claude"}`)
	return gardenOutpostSeed{home: home, outpost: outpost, pair: pair, session: id, seed: planted.Seed.ID, conversation: run.ConversationID, cwd: cwd}
}

func (w gardenOutpostSeed) card(t *testing.T) *protocol.SeedContinuation {
	t.Helper()
	read := testworld.Request(w.pair.App, protocol.SeedDocumentGetMessage{Cmd: protocol.CmdSeedDocumentGet, RequestID: "card-" + w.seed, SeedID: w.seed},
		protocol.EventSeedDocumentGetResult, func(m protocol.SeedDocumentGetResultMessage) bool { return m.RequestID == "card-"+w.seed })
	if read.Document == nil || read.Document.Seed.Continuation == nil {
		t.Fatalf("the seed card has no continuation: %+v", read)
	}
	return read.Document.Seed.Continuation
}

func (w gardenOutpostSeed) show(t *testing.T) string {
	t.Helper()
	shown := w.home.Attn("seed", "show", w.seed)
	if shown.Code != 0 {
		t.Fatalf("seed show exited %d: %s", shown.Code, shown.Stderr)
	}
	return shown.Stdout
}

func (w gardenOutpostSeed) pressResume(review *protocol.SeedReviewActionContext) protocol.SeedResumeResultMessage {
	return testworld.Request(w.pair.App, protocol.SeedResumeMessage{Cmd: protocol.CmdSeedResume, SeedID: w.seed, RequestID: protocol.Ptr("resume-" + w.seed), Review: review},
		protocol.EventSeedResumeResult, func(m protocol.SeedResumeResultMessage) bool { return m.RequestID == "resume-"+w.seed })
}

func (w gardenOutpostSeed) reviewItem(t *testing.T) protocol.GardenReviewItem {
	t.Helper()
	started, err := w.home.Client().SeedReviewStart()
	if err != nil || started.Review == nil {
		t.Fatalf("start review: %+v, %v", started, err)
	}
	w.home.HeadlessTask().Answer(`{"recommendation":"keep_growing","explanation":"Work remains.","evidence":["The seed is unfinished."]}`)
	for _, item := range started.Review.Items {
		if item.SeedID == w.seed {
			return item
		}
	}
	t.Fatalf("the review has no item for %s: %+v", w.seed, started.Review.Items)
	return protocol.GardenReviewItem{}
}

func (w gardenOutpostSeed) ageIntoReview() {
	mark := w.pair.App.EventCount()
	w.home.Advance(garden.DefaultStaleWindow)
	stopRetries := w.home.FireTimers(bridge.RetryTimer)
	defer stopRetries()
	outpostReachSince(w.pair.App, mark, "outpost", protocol.OutpostReachUnreachable)
	outpostReachSince(w.pair.App, w.pair.App.EventCount(), "outpost", protocol.OutpostReachReachable)
}

func reviewEvidenceText(item protocol.GardenReviewItem) string {
	var text strings.Builder
	for _, evidence := range item.Evidence {
		text.WriteString(evidence.Label + ": " + evidence.Text + "\n")
	}
	return text.String()
}

func TestGardenOffersResumeOfAClosedOutpostSessionAndReopensItOnItsOutpost(t *testing.T) {
	for _, harness := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		t.Run(string(harness), func(t *testing.T) {
			w := closedOutpostSeedTender(t, harness)

			card := w.card(t)
			if !card.ResumeAvailable || protocol.Deref(card.Outpost) != "outpost" || card.DirectoryState != "unchecked" || card.HandoverPlacement != "placement_required" {
				t.Fatalf("card=%+v, want Resume offered on outpost, unchecked, and a Handover that asks where", card)
			}
			if show := w.show(t); !strings.Contains(show, "resume  on outpost outpost") {
				t.Fatalf("seed show does not offer Resume on the outpost:\n%s", show)
			}

			w.ageIntoReview()
			item := w.reviewItem(t)
			if !slices.Contains(item.Actions, "resume") || protocol.Deref(item.Outpost) != "outpost" || !strings.Contains(reviewEvidenceText(item), `on outpost "outpost"`) {
				t.Fatalf("review actions=%v outpost=%q evidence:\n%s", item.Actions, protocol.Deref(item.Outpost), reviewEvidenceText(item))
			}

			resumed := w.pressResume(&protocol.SeedReviewActionContext{ReviewID: item.RunID, EvidenceVersion: item.EvidenceVersion})
			if !resumed.Success {
				t.Fatalf("Resume refused: %s", protocol.Deref(resumed.Error))
			}
			second := w.outpost.LaunchedCarrying(w.pair.App.Terminal(w.session))
			if !second.Resumed || second.ConversationID != w.conversation {
				t.Fatalf("resumed=%t conversation=%s, want %s", second.Resumed, second.ConversationID, w.conversation)
			}
			live := testworld.AwaitSession(w.pair.App, w.session, func(s protocol.Session) bool { return protocol.Deref(s.Host) == w.pair.Host })
			t.Logf("%s Garden Resume: host=%s directory=%s conversation=%s", harness, protocol.Deref(live.Host), live.Directory, second.ConversationID)
		})
	}
}

func TestGardenReadsNeverAskTheOutpostAndResumeRefusesWithWhatItFindsThere(t *testing.T) {
	w := closedOutpostSeedTender(t, fakeagent.Codex)
	refused := func(want ...string) {
		t.Helper()
		result := w.pressResume(nil)
		for _, text := range want {
			if result.Success || !strings.Contains(protocol.Deref(result.Error), text) {
				t.Fatalf("Resume=%+v error=%q, want %q", result, protocol.Deref(result.Error), text)
			}
		}
		shown, err := w.home.Client().SessionShow(protocol.SessionID(w.session))
		if err != nil || protocol.Deref(shown.Entry.ClosedAt) == "" {
			t.Fatalf("a refused Resume changed the ledger: %+v, %v", shown, err)
		}
		t.Logf("refused: %s", protocol.Deref(result.Error))
	}

	rollouts, err := filepath.Glob(filepath.Join(w.outpost.Dir, "toolhome", ".codex", "sessions", "*", "*", "*", "*-"+w.conversation+".jsonl"))
	if err != nil || len(rollouts) != 1 {
		t.Fatalf("rollouts=%v, %v", rollouts, err)
	}
	if err := os.Rename(rollouts[0], rollouts[0]+".saved"); err != nil {
		t.Fatal(err)
	}
	if card := w.card(t); !card.ResumeAvailable || card.DirectoryState != "unchecked" {
		t.Fatalf("a read saw the outpost's storage: %+v", card)
	}
	refused(`on outpost "outpost"`, "conversation "+w.conversation, "no longer in codex's storage")
	if err := os.Rename(rollouts[0]+".saved", rollouts[0]); err != nil {
		t.Fatal(err)
	}

	if err := os.Rename(w.cwd, w.cwd+".saved"); err != nil {
		t.Fatal(err)
	}
	if card := w.card(t); !card.ResumeAvailable || card.DirectoryState != "unchecked" {
		t.Fatalf("a read saw the outpost's disk, so it asked the outpost: %+v", card)
	}
	refused("no longer exists", `on outpost "outpost"`, w.cwd)
	if err := os.Rename(w.cwd+".saved", w.cwd); err != nil {
		t.Fatal(err)
	}

	w.ageIntoReview()
	mark := w.pair.App.EventCount()
	requireStdout(t, w.home.Attn("settings", "set", "outposts_enabled", "false"), "false")
	outpostReachSince(w.pair.App, mark, "outpost", protocol.OutpostReachUnreachable)
	card := w.card(t)
	if card.ResumeAvailable || card.DirectoryState != "unreachable" || !strings.Contains(protocol.Deref(card.ResumeReason), "unreachable") {
		t.Fatalf("card while unreachable=%+v", card)
	}
	if show := w.show(t); !strings.Contains(show, `resume  unavailable: outpost "outpost" is unreachable`) || strings.Contains(show, "no longer exists") {
		t.Fatalf("seed show while unreachable:\n%s", show)
	}
	item := w.reviewItem(t)
	if slices.Contains(item.Actions, "resume") || !strings.Contains(reviewEvidenceText(item), `on outpost "outpost", which is unreachable`) {
		t.Fatalf("review while unreachable: actions=%v evidence:\n%s", item.Actions, reviewEvidenceText(item))
	}
	refused(`outpost "outpost" is unreachable`)

	mark = w.pair.App.EventCount()
	requireStdout(t, w.home.Attn("settings", "set", "outposts_enabled", "true"), "true")
	outpostReachSince(w.pair.App, mark, "outpost", protocol.OutpostReachReachable)
	if card := w.card(t); !card.ResumeAvailable || card.DirectoryState != "unchecked" {
		t.Fatalf("card after the outpost came back=%+v", card)
	}
}

func (w gardenOutpostSeed) setOutposts(t *testing.T, enabled string, reach protocol.OutpostReach) {
	t.Helper()
	mark := w.pair.App.EventCount()
	requireStdout(t, w.home.Attn("settings", "set", "outposts_enabled", enabled), enabled)
	outpostReachSince(w.pair.App, mark, "outpost", reach)
}

func (w gardenOutpostSeed) reviewUpdateSince(mark int, runID string, matches func(protocol.GardenReviewItem) bool) protocol.GardenReviewItem {
	var found protocol.GardenReviewItem
	testworld.AwaitSince(w.pair.App, mark, protocol.EventGardenReviewUpdated, func(m protocol.GardenReviewUpdatedMessage) bool {
		if m.Review.Run.ID != runID {
			return false
		}
		for _, item := range m.Review.Items {
			if item.SeedID == w.seed && matches(item) {
				found = item
				return true
			}
		}
		return false
	})
	return found
}

func reviewItemOffersResumeOnline(item protocol.GardenReviewItem) bool {
	return item.Status == "ready" && slices.Contains(item.Actions, "resume") && protocol.Deref(item.Outpost) == "outpost" && strings.Contains(reviewEvidenceText(item), `on outpost "outpost".`)
}

func reviewItemShowsOutpostUnreachable(item protocol.GardenReviewItem) bool {
	return item.Status == "ready" && !slices.Contains(item.Actions, "resume") && strings.Contains(reviewEvidenceText(item), "which is unreachable. Resume returns when the outpost is back.")
}

func reviewReceipt(item protocol.GardenReviewItem) protocol.SeedReviewActionContext {
	return protocol.SeedReviewActionContext{ReviewID: item.RunID, EvidenceVersion: item.EvidenceVersion}
}

func TestAReadyGardenReviewFollowsItsOutpostsReachAndResumes(t *testing.T) {
	w := closedOutpostSeedTender(t, fakeagent.Codex)
	w.ageIntoReview()
	w.setOutposts(t, "false", protocol.OutpostReachUnreachable)

	mark := w.pair.App.EventCount()
	started := w.reviewItem(t)
	offline := w.reviewUpdateSince(mark, started.RunID, reviewItemShowsOutpostUnreachable)
	t.Logf("ready while offline: actions=%v", offline.Actions)

	mark = w.pair.App.EventCount()
	w.setOutposts(t, "true", protocol.OutpostReachReachable)
	online := w.reviewUpdateSince(mark, offline.RunID, reviewItemOffersResumeOnline)
	if online.EvidenceVersion != offline.EvidenceVersion || protocol.Deref(online.Recommendation) != "keep_growing" {
		t.Fatalf("the reconnect changed the advice: before=%+v after=%+v", offline, online)
	}
	t.Logf("after reconnect, without pressing anything: actions=%v", online.Actions)

	mark = w.pair.App.EventCount()
	w.setOutposts(t, "false", protocol.OutpostReachUnreachable)
	w.reviewUpdateSince(mark, offline.RunID, reviewItemShowsOutpostUnreachable)
	refused := w.pressResume(new(reviewReceipt(online)))
	if refused.Success || !strings.Contains(protocol.Deref(refused.Error), `outpost "outpost" is unreachable`) {
		t.Fatalf("Resume with the outpost down: %+v", refused)
	}
	shown, err := w.home.Client().SeedReviewShow(offline.RunID)
	if err != nil || shown.Review == nil || !reviewItemShowsOutpostUnreachable(shown.Review.Items[0]) || shown.Review.Items[0].Resolution != "unresolved" {
		t.Fatalf("after the refused Resume: %+v, %v", shown.Review, err)
	}
	t.Logf("refused while offline, item still ready: %s", protocol.Deref(refused.Error))

	mark = w.pair.App.EventCount()
	w.setOutposts(t, "true", protocol.OutpostReachReachable)
	current := w.reviewUpdateSince(mark, offline.RunID, reviewItemOffersResumeOnline)
	resumed := w.pressResume(new(reviewReceipt(current)))
	if !resumed.Success {
		t.Fatalf("Resume after the reconnect: %s", protocol.Deref(resumed.Error))
	}
	second := w.outpost.LaunchedCarrying(w.pair.App.Terminal(w.session))
	if !second.Resumed || second.ConversationID != w.conversation {
		t.Fatalf("resumed=%t conversation=%s, want %s", second.Resumed, second.ConversationID, w.conversation)
	}
	finished, err := w.home.Client().SeedReviewShow(offline.RunID)
	if err != nil || finished.Review == nil || finished.Review.Run.Status != "complete" || protocol.Deref(finished.Review.Items[0].ResolvedAction) != "resume" {
		t.Fatalf("after Resume: %+v, %v", finished.Review, err)
	}
	t.Logf("resumed on the outpost; review %s", finished.Review.Run.Status)
}

func TestReachChangesLeaveAReadyGardenReviewDecidable(t *testing.T) {
	w := closedOutpostSeedTender(t, fakeagent.Codex)
	w.ageIntoReview()

	mark := w.pair.App.EventCount()
	started := w.reviewItem(t)
	online := w.reviewUpdateSince(mark, started.RunID, reviewItemOffersResumeOnline)

	mark = w.pair.App.EventCount()
	w.setOutposts(t, "false", protocol.OutpostReachUnreachable)
	offline := w.reviewUpdateSince(mark, online.RunID, reviewItemShowsOutpostUnreachable)
	if offline.EvidenceVersion != online.EvidenceVersion {
		t.Fatalf("losing the outpost changed the evidence: before=%+v after=%+v", online, offline)
	}
	kept, err := w.home.Client().SeedReviewKeep(w.seed, reviewReceipt(offline))
	if err != nil || kept.Review == nil || kept.Review.Run.Status != "complete" {
		t.Fatalf("keep with the outpost down: %+v, %v", kept, err)
	}
	t.Logf("kept while offline; review %s", kept.Review.Run.Status)
}

func TestAReadyGardenReviewItemWhoseSeedChangedCanBeRetriedFromTheReview(t *testing.T) {
	w := closedOutpostSeedTender(t, fakeagent.Codex)
	w.ageIntoReview()

	mark := w.pair.App.EventCount()
	started := w.reviewItem(t)
	ready := w.reviewUpdateSince(mark, started.RunID, reviewItemOffersResumeOnline)

	if _, err := w.home.Client().SeedNote("", w.seed, "new evidence", "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.home.Client().SeedReviewKeep(w.seed, reviewReceipt(ready)); err == nil || !strings.Contains(err.Error(), "changed since this review item was loaded") {
		t.Fatalf("keep on stale evidence: %v", err)
	}
	shown, err := w.home.Client().SeedReviewShow(ready.RunID)
	if err != nil || shown.Review == nil || shown.Review.Items[0].Status != "invalidated" || shown.Review.Items[0].Resolution != "unresolved" {
		t.Fatalf("show after the refusal: %+v, %v", shown.Review, err)
	}
	t.Logf("refresh after the refusal: status=%s error=%q", shown.Review.Items[0].Status, protocol.Deref(shown.Review.Items[0].Error))

	mark = w.pair.App.EventCount()
	retried, err := w.home.Client().SeedReviewRetry(ready.RunID, w.seed)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retried.Review.Items[0].EvidenceVersion == ready.EvidenceVersion {
		t.Fatalf("the retry kept the stale evidence: %+v", retried.Review.Items[0])
	}
	w.home.HeadlessTask().Answer(`{"recommendation":"keep_growing","explanation":"Work remains.","evidence":["The seed is unfinished."]}`)
	fresh := w.reviewUpdateSince(mark, ready.RunID, reviewItemOffersResumeOnline)
	if !strings.Contains(reviewEvidenceText(fresh), "new evidence") {
		t.Fatalf("the retried item misses the note:\n%s", reviewEvidenceText(fresh))
	}
	kept, err := w.home.Client().SeedReviewKeep(w.seed, reviewReceipt(fresh))
	if err != nil || kept.Review.Run.Status != "complete" {
		t.Fatalf("keep on fresh evidence: %+v, %v", kept, err)
	}
}

func TestAReadyGardenReviewItemWhoseSeedClosedSettlesWhenActedOn(t *testing.T) {
	w := closedOutpostSeedTender(t, fakeagent.Codex)
	w.ageIntoReview()

	mark := w.pair.App.EventCount()
	started := w.reviewItem(t)
	ready := w.reviewUpdateSince(mark, started.RunID, reviewItemOffersResumeOnline)

	if _, err := w.home.Client().SeedTransition("", w.seed, "harvest", "Finished elsewhere.", true, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.home.Client().SeedReviewKeep(w.seed, reviewReceipt(ready)); err == nil || !strings.Contains(err.Error(), "no longer needs review") {
		t.Fatalf("keep on a harvested seed: %v", err)
	}
	shown, err := w.home.Client().SeedReviewShow(ready.RunID)
	if err != nil || shown.Review == nil || shown.Review.Items[0].Resolution != "no_longer_applicable" || shown.Review.Run.Status != "complete" {
		t.Fatalf("show after the refusal: %+v, %v", shown.Review, err)
	}
	t.Logf("review %s, item %s", shown.Review.Run.Status, shown.Review.Items[0].Resolution)
}

func TestOutpostReachLeavesAHomeSeedsReviewItemAlone(t *testing.T) {
	w := closedOutpostSeedTender(t, fakeagent.Codex)
	cwd := w.home.Path("home-agent")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	id := w.home.Spawn(w.pair.App, fakeagent.Claude, cwd)
	run := w.home.LaunchedCarrying(w.pair.App.Terminal(id))
	w.pair.App.TypeLine(id, "home work")
	run.Prompted()
	run.Reply("Done. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(w.pair.App, id, func(s protocol.Session) bool { return s.Usage != nil })
	planted, err := w.home.Client().SeedPlant(protocol.SessionID(id), "Finish the home work", "Finish it.", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.home.Client().SeedTransition(protocol.SessionID(id), planted.Seed.ID, "tend", "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := w.home.Client().Unregister(protocol.SessionID(id)); err != nil {
		t.Fatal(err)
	}
	testworld.Await(w.pair.App, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == id
	})
	w.ageIntoReview()

	const advice = `{"recommendation":"keep_growing","explanation":"Work remains.","evidence":["The seed is unfinished."]}`
	mark := w.pair.App.EventCount()
	started, err := w.home.Client().SeedReviewStart()
	if err != nil || started.Review == nil || len(started.Review.Items) != 2 {
		t.Fatalf("start review: %+v, %v", started.Review, err)
	}
	for advised := 0; advised < 2; {
		task := w.home.HeadlessTask()
		if !strings.Contains(task.Prompt, "Review one open Garden seed") {
			task.Answer("WAITING")
			continue
		}
		task.Answer(advice)
		advised++
	}
	var homeBefore protocol.GardenReviewItem
	testworld.AwaitSince(w.pair.App, mark, protocol.EventGardenReviewUpdated, func(m protocol.GardenReviewUpdatedMessage) bool {
		for _, item := range m.Review.Items {
			if item.Status != "ready" {
				return false
			}
			if item.SeedID == planted.Seed.ID {
				homeBefore = item
			}
		}
		return true
	})

	mark = w.pair.App.EventCount()
	w.setOutposts(t, "false", protocol.OutpostReachUnreachable)
	w.reviewUpdateSince(mark, started.Review.Run.ID, reviewItemShowsOutpostUnreachable)
	shown, err := w.home.Client().SeedReviewShow(started.Review.Run.ID)
	if err != nil || shown.Review == nil {
		t.Fatalf("show: %+v, %v", shown, err)
	}
	for _, item := range shown.Review.Items {
		if item.SeedID != planted.Seed.ID {
			continue
		}
		if item.EvidenceVersion != homeBefore.EvidenceVersion || !slices.Equal(item.Actions, homeBefore.Actions) || reviewEvidenceText(item) != reviewEvidenceText(homeBefore) {
			t.Fatalf("the home seed's item changed with the outpost's reach:\nbefore=%+v\nafter=%+v", homeBefore, item)
		}
		t.Logf("home item unchanged: actions=%v", item.Actions)
	}
	if _, err := w.home.Client().SeedReviewKeep(planted.Seed.ID, reviewReceipt(homeBefore)); err != nil {
		t.Fatalf("keep the home seed: %v", err)
	}
}
