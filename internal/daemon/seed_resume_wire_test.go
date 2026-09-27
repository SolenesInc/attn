package daemon_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestResumingASeedRelaunchesItsTenderInItsOwnConversation(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	delegated := seedResumeDelegate(t, w, fakeagent.Codex, "api")
	session, seed := delegated.SessionID, delegated.SeedID
	first := w.Launched(session)

	running := seedResumeRequest(app, seed)
	if !running.Success || !protocol.Deref(running.AlreadyRunning) || protocol.Deref(running.SessionID) != session {
		t.Fatalf("resuming while %s runs = %+v, want it focused as already running", session, running)
	}
	first.Prompted()
	app.TypeLine(session, "still you?")
	if got := first.Prompted(); got != "still you?" {
		t.Fatalf("after the resume the running codex received %q, want the next input", got)
	}

	before := lifeShow(t, cli, seed)
	closePane(app, sessionPane{session: session})
	if closed := showSession(t, cli, session); protocol.Deref(closed.ClosedAt) == "" {
		t.Fatalf("the ledger shows %+v after the pane closed, want it closed", closed)
	}

	resumed := seedResumeRequest(app, seed)
	if !resumed.Success || protocol.Deref(resumed.SessionID) != session || protocol.Deref(resumed.AlreadyRunning) {
		t.Fatalf("resuming the closed tender = %+v, want %s relaunched", resumed, session)
	}
	seedResumeContinues(t, w, first, session)
	if profile := protocol.Deref(resumed.ProfileID); profile != protocol.Deref(delegated.ProfileID) {
		t.Errorf("the resume came back in profile %s, want the tender's own %s", profile, protocol.Deref(delegated.ProfileID))
	}
	if relaunched := sessionOfDelegate(t, w, session); relaunched.Directory != delegated.Directory {
		t.Errorf("the relaunched session works in %s, want %s", relaunched.Directory, delegated.Directory)
	}
	if reopened := showSession(t, cli, session); protocol.Deref(reopened.ClosedAt) != "" {
		t.Errorf("the ledger still shows %s closed at %s after the resume", session, protocol.Deref(reopened.ClosedAt))
	}
	if after := lifeShow(t, cli, seed); after.Seed.Status != before.Seed.Status || after.Seed.TenderSession != before.Seed.TenderSession || after.NotesTotal != before.NotesTotal {
		t.Errorf("the resume moved the seed from %s under %s with %d notes to %s under %s with %d notes",
			before.Seed.Status, before.Seed.TenderSession, before.NotesTotal, after.Seed.Status, after.Seed.TenderSession, after.NotesTotal)
	}

	lifeMove(t, cli, session, seed, "park", "", "")
	closePane(app, sessionPane{session: session})
	reclaimed := seedResumeRequest(app, seed)
	if !reclaimed.Success || protocol.Deref(reclaimed.SessionID) != session {
		t.Fatalf("resuming the parked seed = %+v, want %s relaunched", reclaimed, session)
	}
	seedResumeContinues(t, w, first, session)
	if got := lifeShow(t, cli, seed).Seed; got.Status != "growing" || got.TenderSession != session || protocol.Deref(got.LastExecutionID) != session {
		t.Errorf("the resumed parked seed = %+v, want it growing again under %s", got, session)
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
	w.Launched(removed.SessionID)
	closePane(app, sessionPane{session: removed.SessionID})
	if err := os.RemoveAll(removed.Directory); err != nil {
		t.Fatal(err)
	}

	forgotten := seedResumeDelegate(t, w, fakeagent.Claude, "forgotten")
	conversation := w.Launched(forgotten.SessionID).ConversationID
	closePane(app, sessionPane{session: forgotten.SessionID})
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
	w.Launched(delegated.SessionID)
	closePane(app, sessionPane{session: delegated.SessionID})
	before := paneSessions(w)
	seed := lifeShow(t, cli, delegated.SeedID).Seed

	defer w.RefusePiLaunches("pi could not start: the model provider is unreachable")()
	if resumed := seedResumeRequest(app, delegated.SeedID); resumed.Success || !strings.Contains(protocol.Deref(resumed.Error), "provider is unreachable") {
		t.Fatalf("resuming a seed whose agent cannot start = %+v, want the launch failure", resumed)
	}
	if after := paneSessions(w); !slices.Equal(after, before) {
		t.Errorf("the failed resume left panes for %q, want only %q", after, before)
	}
	if after := lifeShow(t, cli, delegated.SeedID).Seed; after.TenderSession != seed.TenderSession || after.Status != seed.Status {
		t.Errorf("the failed resume changed the seed to %s under %q, want %s under %s", after.Status, after.TenderSession, seed.Status, seed.TenderSession)
	}
}

func TestResumingASeedWhoseTendersProfileWasDeletedResumesInTheAppsProfile(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	delegated := seedResumeDelegate(t, w, fakeagent.Codex, "api")
	first := w.Launched(delegated.SessionID)
	closePane(app, sessionPane{session: delegated.SessionID})

	kept := profileRequest(app, "create-kept", protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, Name: "Kept", RequestID: "create-kept"})
	var deleted protocol.Profile
	for _, profile := range app.Initial.Profiles {
		if profile.ID == protocol.Deref(delegated.ProfileID) {
			deleted = profile
		}
	}
	profileRequest(app, "delete-tenders", protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, ProfileID: deleted.ID, ExpectedRevision: deleted.Revision, DestinationProfileID: kept.ID, RequestID: "delete-tenders"})

	resumed := seedResumeRequest(app, delegated.SeedID)
	if !resumed.Success || protocol.Deref(resumed.ProfileID) != kept.ID {
		t.Fatalf("resuming a seed whose tender's profile %s was deleted = %+v, want it resumed in %s", deleted.ID, resumed, kept.ID)
	}
	seedResumeContinues(t, w, first, delegated.SessionID)
}

func profileRequest(app *testworld.Peer, requestID string, cmd any) protocol.Profile {
	app.T.Helper()
	result := testworld.Request(app, cmd, protocol.EventProfileActionResult,
		func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == requestID })
	if !result.Success {
		app.T.Fatalf("%+v = %+v", cmd, result)
	}
	return protocol.Deref(result.Profile)
}
