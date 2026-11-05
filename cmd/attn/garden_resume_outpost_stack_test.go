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
			if !slices.Contains(item.Actions, "resume") || !strings.Contains(reviewEvidenceText(item), `on outpost "outpost"`) {
				t.Fatalf("review actions=%v evidence:\n%s", item.Actions, reviewEvidenceText(item))
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
