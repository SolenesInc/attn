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
	closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), session))
	if closed := showSession(t, cli, session); protocol.Deref(closed.ClosedAt) == "" {
		t.Fatalf("the ledger shows %+v after the pane closed, want it closed", closed)
	}

	resumed := seedResumeRequest(app, seed)
	if !resumed.Success || protocol.Deref(resumed.SessionID) != session || protocol.Deref(resumed.AlreadyRunning) {
		t.Fatalf("resuming the closed tender = %+v, want %s relaunched", resumed, session)
	}
	seedResumeContinues(t, w, first, session)
	if workspace := protocol.Deref(resumed.WorkspaceID); workspace != protocol.Deref(delegated.WorkspaceID) {
		t.Errorf("the resume landed in workspace %s, want the tender's own %s", workspace, protocol.Deref(delegated.WorkspaceID))
	}
	if panes := delegatePaneSessions(workspaceOfDelegate(t, w, protocol.Deref(resumed.WorkspaceID))); !slices.Equal(panes, []string{session}) {
		t.Errorf("the relaunched workspace holds panes for %q, want one pane for %s", panes, session)
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
	closePane(app, seedResumePane(t, w, protocol.Deref(resumed.WorkspaceID), session))
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
	if err := cli.RegisterWithAgent("outsider", "outsider", outsider, "codex"); err != nil {
		t.Fatal(err)
	}
	unlaunched := plantSeedAs(t, cli, "", "held by a session attn never launched")
	lifeMove(t, cli, "outsider", unlaunched, "tend", "", "")
	if err := cli.Unregister("outsider"); err != nil {
		t.Fatal(err)
	}

	removed := seedResumeDelegate(t, w, fakeagent.Codex, "removed")
	w.Launched(removed.SessionID)
	closePane(app, seedResumePane(t, w, protocol.Deref(removed.WorkspaceID), removed.SessionID))
	if err := os.RemoveAll(removed.Directory); err != nil {
		t.Fatal(err)
	}

	forgotten := seedResumeDelegate(t, w, fakeagent.Claude, "forgotten")
	conversation := w.Launched(forgotten.SessionID).ConversationID
	closePane(app, seedResumePane(t, w, protocol.Deref(forgotten.WorkspaceID), forgotten.SessionID))
	sessionRecoveryDeleteTranscript(t, conversation)

	workspaces := seedResumeWorkspaceIDs(w)
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
	if after := seedResumeWorkspaceIDs(w); !slices.Equal(after, workspaces) {
		t.Errorf("the refused resumes changed the workspaces from %v to %v", workspaces, after)
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

func seedResumePane(t *testing.T, w *world, workspaceID, sessionID string) sessionPane {
	t.Helper()
	for _, pane := range workspaceOfDelegate(t, w, workspaceID).Layout.Panes {
		if protocol.Deref(pane.SessionID) == sessionID {
			return sessionPane{session: sessionID, workspace: workspaceID, pane: pane.PaneID}
		}
	}
	t.Fatalf("workspace %s has no pane for %s", workspaceID, sessionID)
	return sessionPane{}
}

func seedResumeContinues(t *testing.T, w *world, first *fakeagent.Run, sessionID string) {
	t.Helper()
	if relaunched := w.Launched(sessionID); !relaunched.Resumed || relaunched.ConversationID != first.ConversationID {
		t.Errorf("the resume ran %s %q, want it resuming conversation %s", relaunched.Harness, relaunched.Argv, first.ConversationID)
	}
}

func seedResumeWorkspaceIDs(w *world) []string {
	var ids []string
	for _, workspace := range w.App().Initial.Workspaces {
		ids = append(ids, workspace.ID)
	}
	slices.Sort(ids)
	return ids
}
