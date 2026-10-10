package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
	"github.com/victorarias/attn/internal/toolhome"
)

func TestResumingASeedPreservesItsConversationAndProjectAssignment(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	delegated := seedResumeDelegate(t, w, fakeagent.Codex, "api")
	session, seed := delegated.SessionID, delegated.SeedID
	first := w.Launched(string(session))

	running := seedResumeRequest(app, seed)
	if !running.Success || !protocol.Deref(running.AlreadyRunning) || protocol.Deref(running.SessionID) != session {
		t.Fatalf("resuming while %s runs = %+v, want it focused as already running", session, running)
	}
	first.Prompted()
	app.TypeLine(string(session), "still you?")
	if got := first.Prompted(); got != "still you?" {
		t.Fatalf("after the resume the running codex received %q, want the next input", got)
	}

	before := lifeShow(t, cli, seed)
	closePane(app, sessionPane{session: string(session)})
	if closed := showSession(t, cli, string(session)); protocol.Deref(closed.ClosedAt) == "" {
		t.Fatalf("the ledger shows %+v after the pane closed, want it closed", closed)
	}

	resumed := seedResumeRequest(app, seed)
	if !resumed.Success || protocol.Deref(resumed.SessionID) != session || protocol.Deref(resumed.AlreadyRunning) {
		t.Fatalf("resuming the closed tender = %+v, want %s relaunched", resumed, session)
	}
	seedResumeContinues(t, w, first, string(session))
	if profile := protocol.Deref(resumed.ProfileID); profile != protocol.Deref(delegated.ProfileID) {
		t.Errorf("the resume came back in profile %s, want the tender's own %s", profile, protocol.Deref(delegated.ProfileID))
	}
	if relaunched := sessionOfDelegate(t, w, string(session)); relaunched.Directory != delegated.Directory {
		t.Errorf("the relaunched session works in %s, want %s", relaunched.Directory, delegated.Directory)
	}
	if reopened := showSession(t, cli, string(session)); protocol.Deref(reopened.ClosedAt) != "" {
		t.Errorf("the ledger still shows %s closed at %s after the resume", session, protocol.Deref(reopened.ClosedAt))
	}
	if after := lifeShow(t, cli, seed); after.Seed.Status != before.Seed.Status || after.Seed.TenderSession != before.Seed.TenderSession || after.NotesTotal != before.NotesTotal {
		t.Errorf("the resume moved the seed from %s under %s with %d notes to %s under %s with %d notes",
			before.Seed.Status, before.Seed.TenderSession, before.NotesTotal, after.Seed.Status, after.Seed.TenderSession, after.NotesTotal)
	}

	child, err := cli.SeedPlant(session, "Payments", "Implement payments.", seed, "", "")
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := cli.SeedPlant(session, "Receipts", "Implement receipts.", seed, "", "")
	if err != nil {
		t.Fatal(err)
	}
	lifeMove(t, cli, string(session), child.Seed.ID, "tend", "", "")
	lifeMove(t, cli, string(session), child.Seed.ID, "park", "", "")
	closePane(app, sessionPane{session: string(session)})
	reclaimed := seedResumeRequest(app, child.Seed.ID)
	if !reclaimed.Success || protocol.Deref(reclaimed.SessionID) != session {
		t.Fatalf("resuming the parked child = %+v, want %s relaunched", reclaimed, session)
	}
	seedResumeContinues(t, w, first, string(session))
	if got := lifeShow(t, cli, child.Seed.ID).Seed; got.Status != "growing" || got.TenderSession != session || protocol.Deref(got.LastExecutionID) != session {
		t.Errorf("the resumed parked child = %+v, want it growing again under %s", got, session)
	}
	ready, err := cli.SeedReady(session, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if ready.ScopeID != seed || !slices.ContainsFunc(ready.Seeds, func(s protocol.Seed) bool { return s.ID == sibling.Seed.ID }) {
		t.Errorf("ready after resuming a child = %+v, want the original project %s with sibling %s", ready, seed, sibling.Seed.ID)
	}
}

func TestAResumeThatCannotReachItsConversationIsRefusedAndCreatesNothing(t *testing.T) {
	w := newWorld(t, fakeagent.Codex, fakeagent.Claude)
	app, cli := w.App(), w.Client()

	untended := plantSeedAs(t, cli, "", "nobody holds this")

	outsider := w.Path("outsider")
	if err := os.MkdirAll(outsider, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := w.InjectSession("outsider", "outsider", outsider, protocol.SessionAgentCodex); err != nil {
		t.Fatal(err)
	}
	unlaunched := plantSeedAs(t, cli, "", "held by a session attn never launched")
	lifeMove(t, cli, "outsider", unlaunched, "tend", "", "")
	if err := cli.Unregister("outsider"); err != nil {
		t.Fatal(err)
	}

	removed := seedResumeDelegate(t, w, fakeagent.Codex, "removed")
	w.Launched(string(removed.SessionID))
	closePane(app, sessionPane{session: string(removed.SessionID)})
	if err := os.RemoveAll(removed.Directory); err != nil {
		t.Fatal(err)
	}

	forgotten := seedResumeDelegate(t, w, fakeagent.Codex, "forgotten")
	conversation := w.Launched(string(forgotten.SessionID)).ConversationID
	closePane(app, sessionPane{session: string(forgotten.SessionID)})
	sessionRecoveryDeleteTranscript(t, conversation)

	desktops := seedResumeDesktops(w)
	for _, refusal := range []struct {
		name, seed string
		wants      []string
	}{
		{"an unplanted seed", "s-zzzzzz", []string{"s-zzzzzz"}},
		{"a seed nobody tended", untended, []string{untended, "no agent conversation to resume"}},
		{"a tender attn never launched", unlaunched, []string{unlaunched, "no saved launch contract"}},
		{"a tender whose directory is gone", removed.SeedID, []string{removed.SeedID, "the original directory no longer exists: " + removed.Directory}},
		{"a tender whose conversation is gone", forgotten.SeedID, []string{forgotten.SeedID, "the original conversation is unavailable"}},
	} {
		var before *protocol.SeedShowResult
		if refusal.seed != "s-zzzzzz" {
			before = lifeShow(t, cli, refusal.seed)
		}
		result := seedResumeRequest(app, refusal.seed)
		if result.Success {
			t.Errorf("resuming %s = %+v, want it refused", refusal.name, result)
			continue
		}
		for _, want := range refusal.wants {
			if !strings.Contains(protocol.Deref(result.Error), want) {
				t.Errorf("the refusal to resume %s does not name %q: %s", refusal.name, want, protocol.Deref(result.Error))
			}
		}
		if before != nil {
			if after := lifeShow(t, cli, refusal.seed); after.Seed.Rev != before.Seed.Rev || after.NotesTotal != before.NotesTotal {
				t.Errorf("the refused resume of %s changed the seed: %+v, then %+v", refusal.name, before.Seed, after.Seed)
			}
		}
	}
	if after := seedResumeDesktops(w); !slices.Equal(after, desktops) {
		t.Errorf("the refused resumes changed the desktops from %v to %v", desktops, after)
	}
	for _, s := range w.App().Initial.Sessions {
		if s.ID == removed.SessionID || s.ID == forgotten.SessionID || s.ID == "outsider" {
			t.Errorf("a refused resume brought back session %s", s.ID)
		}
	}
}

func seedResumeDelegate(t *testing.T, w *world, agent fakeagent.Harness, dir string) *protocol.DelegateResult {
	t.Helper()
	cwd := w.Path(dir)
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	request := brief(cwd, "Investigate the tracked task.")
	request.Agent = protocol.Ptr(string(agent))
	delegated, err := w.Client().Delegate(request)
	if err != nil {
		t.Fatalf("delegate to %s in %s: %v", agent, cwd, err)
	}
	return delegated
}

func seedResumeRequest(app *testworld.Peer, seedID string) protocol.SeedResumeResultMessage {
	app.T.Helper()
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.SeedResumeMessage{Cmd: protocol.CmdSeedResume, SeedID: seedID, RequestID: protocol.Ptr(requestID)},
		protocol.EventSeedResumeResult, func(r protocol.SeedResumeResultMessage) bool { return r.RequestID == requestID })
}

func seedResumeContinues(t *testing.T, w *world, first *fakeagent.Run, sessionID string) {
	t.Helper()
	if relaunched := w.Launched(sessionID); !relaunched.Resumed || relaunched.ConversationID != first.ConversationID {
		t.Errorf("the resume ran %s %q, want it resuming conversation %s", relaunched.Harness, relaunched.Argv, first.ConversationID)
	}
}

func seedResumeDesktops(w *world) []string {
	var ids []string
	for _, desktop := range w.App().Initial.Desktops {
		ids = append(ids, desktop.ID+":"+strings.Join(delegatePaneSessions(desktop), ","))
	}
	slices.Sort(ids)
	return ids
}

func TestAResumeWhoseAgentCannotStartLeavesNoPaneBehind(t *testing.T) {
	w := newWorld(t, fakeagent.Pi)
	app, cli := w.App(), w.Client()
	pluginDriverSettings(app, "pi")
	delegated := seedResumeDelegate(t, w, fakeagent.Pi, "api")
	w.Launched(string(delegated.SessionID))
	lifeMove(t, cli, string(delegated.SessionID), delegated.SeedID, "park", "", "")
	closePane(app, sessionPane{session: string(delegated.SessionID)})
	before := paneSessions(w)

	defer w.RefusePiLaunches("pi could not start: the model provider is unreachable")()
	if resumed := seedResumeRequest(app, delegated.SeedID); resumed.Success || !strings.Contains(protocol.Deref(resumed.Error), "provider is unreachable") {
		t.Fatalf("resuming a seed whose agent cannot start = %+v, want the launch failure", resumed)
	}
	if after := paneSessions(w); !slices.Equal(after, before) {
		t.Errorf("the failed resume left panes for %q, want only %q", after, before)
	}
	if after := lifeShow(t, cli, delegated.SeedID).Seed; after.TenderSession != delegated.SessionID || after.Status != "growing" {
		t.Errorf("the failed resume left the seed %s under %q, want growing under %s", after.Status, after.TenderSession, delegated.SessionID)
	}
	registerSessions(t, w, cli, "next-tender")
	lifeMove(t, cli, "next-tender", delegated.SeedID, "tend", "", "")
}

func TestSeedEditsAndHarvestSurviveReviewedResume(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	w := &world{World: prepareWorld(t, fakeagent.Claude), gardenClock: func() time.Time { return time.Unix(0, now.Load()) }}
	w.start()
	app, cli := w.App(), w.Client()
	driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"resume": true, "state_reporting": true})
	awaitDriverAvailable(app, "snipe")
	id, run := spawnDriven(w, app, driver, w.Path("api"))
	driver.mustReport("session.report_metadata", map[string]any{
		"session_id": run.SessionID, "run_id": run.RunID, "seq": 1,
		"resume_session_id": "saved-conversation",
		"metadata":          map[string]string{"native_id": "saved-conversation"},
	})
	seed := gardenReviewPlantTended(t, cli, id, "Resume the tracked task")
	closePane(app, sessionPane{session: id})
	setSetting(t, app, "garden.advisor", `{"agent":"claude"}`)
	now.Store(time.Unix(0, now.Load()).Add(garden.DefaultStaleWindow).UnixNano())
	review := gardenReviewStart(t, cli)
	driver.mu.Lock()
	driver.holdLaunch = true
	driver.mu.Unlock()
	app.Send(protocol.SeedResumeMessage{
		Cmd: protocol.CmdSeedResume, SeedID: seed, RequestID: protocol.Ptr("resume"),
		Review: &protocol.SeedReviewActionContext{ReviewID: review.Run.ID, EvidenceVersion: gardenReviewItem(t, &review, seed).EvidenceVersion},
	})
	reply := driver.asked("driver.resume", nil)
	if _, err := cli.SeedEdit(seed, "Updated assignment while the agent starts."); err != nil {
		t.Fatal(err)
	}
	lifeMove(t, cli, id, seed, "harvest", "The work is complete.", "")
	changed := lifeShow(t, cli, seed).Seed
	driver.answer(reply, map[string]any{"argv": []string{"/bin/cat"}})
	resumed := testworld.Await(app, protocol.EventSeedResumeResult, func(r protocol.SeedResumeResultMessage) bool { return r.RequestID == "resume" })
	if !resumed.Success {
		t.Fatalf("resume after editing and harvesting: %s", protocol.Deref(resumed.Error))
	}
	if after := lifeShow(t, cli, seed).Seed; after.Rev != changed.Rev {
		t.Errorf("resume rewrote the seed: revision %d -> %d", changed.Rev, after.Rev)
	}
}

func TestAReviewedResumeSettlesItsDecisionWhenLaunchFails(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	w := &world{World: prepareWorld(t, fakeagent.Pi, fakeagent.Claude), gardenClock: func() time.Time { return time.Unix(0, now.Load()) }}
	w.start()
	app, cli := w.App(), w.Client()
	pluginDriverSettings(app, "pi")
	delegated := seedResumeDelegate(t, w, fakeagent.Pi, "api")
	w.Launched(string(delegated.SessionID))
	closePane(app, sessionPane{session: string(delegated.SessionID)})
	now.Store(time.Unix(0, now.Load()).Add(garden.DefaultStaleWindow).UnixNano())
	setSetting(t, app, "garden.advisor", `{"agent":"claude"}`)
	t.Setenv("ATTN_HEADLESS_TASKS", "on")
	review := gardenReviewStart(t, cli)
	w.HeadlessTask().Answer(`{"recommendation":"resume","explanation":"Continue the saved conversation.","evidence":["The original conversation is available."]}`)
	testworld.Await(app, protocol.EventGardenReviewUpdated, func(m protocol.GardenReviewUpdatedMessage) bool {
		return m.Review.Run.ID == review.Run.ID && gardenReviewItem(t, &m.Review, delegated.SeedID).Status == "ready"
	})
	defer w.RefusePiLaunches("the model provider is unreachable")()
	resumed := testworld.Request(app, protocol.SeedResumeMessage{
		Cmd: protocol.CmdSeedResume, SeedID: delegated.SeedID, RequestID: protocol.Ptr("resume"),
		Review: &protocol.SeedReviewActionContext{ReviewID: review.Run.ID, EvidenceVersion: gardenReviewItem(t, &review, delegated.SeedID).EvidenceVersion},
	}, protocol.EventSeedResumeResult, func(r protocol.SeedResumeResultMessage) bool { return r.RequestID == "resume" })
	if resumed.Success || !strings.Contains(protocol.Deref(resumed.Error), "provider is unreachable") {
		t.Fatalf("review resume = %+v, want the launch failure", resumed)
	}
	if after := gardenReviewShow(t, cli, review.Run.ID).Review; after.Run.Status != "complete" {
		t.Errorf("review after launch failure is %s, want complete", after.Run.Status)
	}
}

func TestPiResumeAndReopenRequireTheSavedConversation(t *testing.T) {
	w := newWorld(t, fakeagent.Pi)
	app, cli := w.App(), w.Client()
	pluginDriverSettings(app, "pi")
	delegated := seedResumeDelegate(t, w, fakeagent.Pi, "api")
	first := w.Launched(string(delegated.SessionID))
	close := func() {
		closePane(app, sessionPane{session: string(delegated.SessionID)})
	}
	close()

	if got := lifeShow(t, cli, delegated.SeedID).Seed.Continuation; got == nil || !got.ResumeAvailable {
		t.Fatalf("Pi seed with a stored conversation = %+v, want resume available", got)
	}
	if got := seedResumeRequest(app, delegated.SeedID); !got.Success {
		t.Fatalf("resume stored Pi conversation: %+v", got)
	}
	seedResumeContinues(t, w, first, string(delegated.SessionID))
	close()
	if got := reopenOverTheWebSocket(app, string(delegated.SessionID)); !got.Success {
		t.Fatalf("reopen stored Pi conversation: %+v", got)
	}
	seedResumeContinues(t, w, first, string(delegated.SessionID))
	close()

	home, err := toolhome.Dir()
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(home, ".pi", "agent", "sessions", "*", "*"+first.ConversationID+".jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("Pi conversation files = %v, %v", files, err)
	}
	if err := os.Remove(files[0]); err != nil {
		t.Fatal(err)
	}

	before := lifeShow(t, cli, delegated.SeedID)
	if got := before.Seed.Continuation; got == nil || got.ResumeAvailable || !strings.Contains(protocol.Deref(got.ResumeReason), "pi's storage") {
		t.Fatalf("Pi seed after its file was deleted = %+v, want unavailable with storage reason", got)
	}
	verdict := reopenVerdict(t, cli, string(delegated.SessionID))
	if verdict.Reopenable || !slices.Equal(verdict.Actions, []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshSamePlace}) {
		t.Fatalf("Pi reopen after deletion = %+v, want only an explicit fresh start", verdict)
	}
	desktops := seedResumeDesktops(w)
	panes := paneSessions(w)
	resumed := seedResumeRequest(app, delegated.SeedID)
	reopened := reopenOverTheWebSocket(app, string(delegated.SessionID))
	for name, result := range map[string]struct {
		success bool
		reason  string
	}{
		"Resume": {resumed.Success, protocol.Deref(resumed.Error)},
		"Reopen": {reopened.Success, protocol.Deref(reopened.Error)},
	} {
		if result.success || !strings.Contains(result.reason, first.ConversationID) || !strings.Contains(result.reason, "pi's storage") {
			t.Errorf("Pi %s after deletion = %+v, want refusal naming the missing conversation", name, result)
		}
	}
	if after := lifeShow(t, cli, delegated.SeedID); after.Seed.Rev != before.Seed.Rev || after.NotesTotal != before.NotesTotal {
		t.Errorf("refused Pi resume changed the seed: %+v -> %+v", before, after)
	}
	if after := seedResumeDesktops(w); !slices.Equal(after, desktops) {
		t.Errorf("refusal changed workspaces: %v -> %v", desktops, after)
	}
	if after := paneSessions(w); !slices.Equal(after, panes) {
		t.Errorf("refusal changed panes: %v -> %v", panes, after)
	}
	if got := showSession(t, cli, string(delegated.SessionID)); protocol.Deref(got.ClosedAt) == "" {
		t.Error("refusal reopened the ledger session")
	}
	if _, err := os.Stat(files[0]); !os.IsNotExist(err) {
		t.Errorf("refusal recreated Pi storage: %v", err)
	}
}
