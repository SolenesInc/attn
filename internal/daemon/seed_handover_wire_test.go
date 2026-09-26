package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAHandoverStartsTheSuccessorWhereThePredecessorLeftOff(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	source := w.Spawn(app, fakeagent.Claude, w.Path("source"))
	w.Launched(source)
	if err := os.MkdirAll(w.Path("api"), 0o755); err != nil {
		t.Fatal(err)
	}
	predecessor, err := cli.Delegate(delegateFrom(source, w.Path("api"), "Investigate the tracked task.", fakeagent.Codex))
	if err != nil {
		t.Fatal(err)
	}
	w.Launched(predecessor.SessionID)
	seed := predecessor.SeedID
	sibling := plantSeedAs(t, cli, source, "second responsibility")
	lifeMove(t, cli, predecessor.SessionID, sibling, "tend", "", "")
	unfinished := seedArtifactsWrite(t, predecessor.Directory, "unfinished.txt", []byte("still here"))
	body := lifeShow(t, cli, seed).Seed.Body
	observer := w.Spawn(app, fakeagent.Codex, w.Path("observer"))
	for _, watcher := range []string{source, observer} {
		if _, err := cli.SeedWatch(watcher, seed, false); err != nil {
			t.Fatal(err)
		}
	}

	request := seedHandoverRequest(source, predecessor.Directory, seed, "Continue from the failing test.")
	successor, err := cli.Delegate(request)
	if err != nil {
		t.Fatalf("handing %s over: %v", seed, err)
	}
	if successor.Directory != predecessor.Directory || protocol.Deref(successor.PredecessorSessionID) != predecessor.SessionID {
		t.Errorf("the successor works in %s after %q, want %s after %s",
			successor.Directory, protocol.Deref(successor.PredecessorSessionID), predecessor.Directory, predecessor.SessionID)
	}
	if got, err := os.ReadFile(unfinished); err != nil || string(got) != "still here" {
		t.Errorf("the unfinished work reads %q (%v), want it intact", got, err)
	}
	prompt := w.Launched(successor.SessionID).Prompted()
	if !strings.Contains(prompt, "attn seed show "+seed) || strings.Contains(prompt, body) || strings.Contains(prompt, "Continue from the failing test.") {
		t.Errorf("the successor was prompted %q, want a pointer to attn seed show %s without the seed's body or handoff", prompt, seed)
	}
	seedHandoverHeldBy(t, cli, seed, successor.SessionID, 1)
	if notes := lifeShow(t, cli, seed).Notes; notes[0].Kind != "handoff" || notes[0].Body != "Continue from the failing test." {
		t.Errorf("the seed's log leads with %+v, want the handoff", notes[0])
	}
	if held := lifeShow(t, cli, sibling).Seed.TenderSession; held != predecessor.SessionID {
		t.Errorf("the handover moved %s, which %s also tended, to %q", sibling, predecessor.SessionID, held)
	}
	if kept := sessionOfDelegate(t, w, predecessor.SessionID); kept.ID != predecessor.SessionID || protocol.Deref(kept.SeedID) == seed {
		t.Errorf("the predecessor reads as %+v, want its conversation kept without the seed", kept)
	}
	if got := protocol.Deref(sessionOfDelegate(t, w, successor.SessionID).SeedID); got != seed {
		t.Errorf("the successor's session carries seed %q, want %s", got, seed)
	}

	if prompt := w.Launched(observer).Prompted(); !strings.Contains(prompt, inboxDoorbell) {
		t.Fatalf("the seed's watcher was prompted %q, want the inbox doorbell", prompt)
	}
	if bells := readInbox(t, cli, observer, 0).Items; len(bells) != 1 || protocol.Deref(bells[0].Hint) != "tended" || !strings.Contains(bells[0].Content, seed) {
		t.Errorf("the seed's watcher received %q, want one bell that %s is tended", inboxContents(bells), seed)
	}
	if bells := readInbox(t, cli, source, 0).Items; len(bells) != 0 {
		t.Errorf("the source received %q, want no bell for its own handover", inboxContents(bells))
	}
	if items := readInbox(t, cli, successor.SessionID, 0).Items; slices.ContainsFunc(items, func(item protocol.AgentInboxItem) bool { return protocol.Deref(item.Hint) == "tended" }) {
		t.Errorf("the successor, prompted directly, also received %q", inboxContents(items))
	}

	if shown, err := cli.SeedShow(successor.SessionID, seed); err != nil || !shown.Watching {
		t.Errorf("the successor does not watch the seed it was handed (%v)", err)
	}
	if _, err := cli.SeedWatch(successor.SessionID, seed, true); err != nil {
		t.Fatal(err)
	}
	accepted, err := cli.DelegationStatus(request.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if retried, err := cli.StartDelegation(request); err != nil || retried.OperationID != accepted.OperationID || retried.SessionID != successor.SessionID {
		t.Errorf("retrying the handover = %+v, %v; want operation %s for %s", retried, err, accepted.OperationID, successor.SessionID)
	}
	if shown, err := cli.SeedShow(successor.SessionID, seed); err != nil || shown.Watching {
		t.Errorf("after the retry the successor watches %s again (%v)", seed, err)
	}
	seedHandoverHeldBy(t, cli, seed, successor.SessionID, 1)
}

func TestAHandedOverSeedResumesTheSuccessorsConversation(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	predecessor := seedResumeDelegate(t, w, fakeagent.Codex, "api")
	w.Launched(predecessor.SessionID)

	successor, err := cli.Delegate(seedHandoverRequest("", predecessor.Directory, predecessor.SeedID, "Pick up where the tests failed."))
	if err != nil {
		t.Fatal(err)
	}
	first := w.Launched(successor.SessionID)
	closePane(app, seedResumePane(t, w, protocol.Deref(successor.WorkspaceID), successor.SessionID))

	if resumed := seedResumeRequest(app, predecessor.SeedID); !resumed.Success || protocol.Deref(resumed.SessionID) != successor.SessionID {
		t.Fatalf("resuming the handed-over seed = %+v, want %s relaunched", resumed, successor.SessionID)
	}
	seedResumeContinues(t, w, first, successor.SessionID)
	seedHandoverHeldBy(t, cli, predecessor.SeedID, successor.SessionID, 1)
}

func TestAHandoverWithoutAPreviousExecutionStartsWhereItWasAsked(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	cli := w.Client()
	seed := plantSeedAs(t, cli, "", "unplaced work")
	placed := w.Path("placed")
	if err := os.MkdirAll(placed, 0o755); err != nil {
		t.Fatal(err)
	}

	successor, err := cli.Delegate(seedHandoverRequest("", placed, seed, ""))
	if err != nil {
		t.Fatal(err)
	}
	w.Launched(successor.SessionID)
	if successor.Directory != placed {
		t.Errorf("the successor works in %s, want the submitted %s", successor.Directory, placed)
	}
	seedHandoverHeldBy(t, cli, seed, successor.SessionID, 0)
	if notes := lifeShow(t, cli, seed).NotesTotal; notes != 0 {
		t.Errorf("the handover of an unplaced seed wrote %d notes, want none", notes)
	}
}

func TestAHandoverRecreatesTheSavedBranchAfterItsWorktreeWasDeleted(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	repo := w.Path("repo")
	gitRepo(t, repo)
	predecessor, err := cli.Delegate(delegateCheckoutAt(filepath.Join(repo, "web"), delegateNewWorktree("feature/handover", "main")))
	if err != nil {
		t.Fatal(err)
	}
	w.Launched(predecessor.SessionID)
	worktreeRoot := filepath.Dir(predecessor.Directory)
	closePane(app, seedResumePane(t, w, protocol.Deref(predecessor.WorkspaceID), predecessor.SessionID))

	deleted := testworld.Request(app, protocol.DeleteWorktreeMessage{Cmd: protocol.CmdDeleteWorktree, Path: worktreeRoot, Force: protocol.Ptr(true)},
		protocol.EventDeleteWorktreeResult, func(r protocol.DeleteWorktreeResultMessage) bool { return r.Path == worktreeRoot })
	if !deleted.Success {
		t.Fatalf("deleting %s: %s", worktreeRoot, protocol.Deref(deleted.Error))
	}
	if branches := runGit(t, repo, "branch", "--list", "feature/handover"); strings.TrimSpace(branches) == "" {
		t.Fatal("deleting the worktree deleted the branch an open seed depends on")
	}

	request := seedHandoverRequest("", filepath.Join(repo, "web"), predecessor.SeedID, "The old worktree was removed.")
	request.Checkout = &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindExistingBranchWorktree, Branch: "feature/handover", Path: protocol.Ptr(worktreeRoot)}
	successor, err := cli.Delegate(request)
	if err != nil {
		t.Fatalf("handing over into the deleted worktree: %v", err)
	}
	w.Launched(successor.SessionID)
	if successor.Directory != predecessor.Directory || !protocol.Deref(successor.WorktreeCreated) {
		t.Errorf("the successor works in %s (worktree created %t), want %s recreated", successor.Directory, protocol.Deref(successor.WorktreeCreated), predecessor.Directory)
	}
	if branch := strings.TrimSpace(runGit(t, worktreeRoot, "rev-parse", "--abbrev-ref", "HEAD")); branch != "feature/handover" {
		t.Errorf("the recreated worktree is on %q, want feature/handover", branch)
	}
}

func seedHandoverRequest(source, cwd, seedID, note string) protocol.DelegateMessage {
	request := protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "handover-" + seedID, Cwd: cwd, Agent: protocol.Ptr(string(fakeagent.Codex)),
		Assignment: protocol.DelegateAssignment{
			Kind: protocol.DelegateAssignmentKindSeed, SeedID: protocol.Ptr(seedID), Handover: &protocol.DelegateHandover{},
		},
	}
	if source != "" {
		request.SourceSessionID = protocol.Ptr(source)
	}
	if note != "" {
		request.Assignment.Handover.Note = protocol.Ptr(note)
	}
	return request
}

func seedHandoverHeldBy(t *testing.T, cli *client.Client, seedID, sessionID string, handoffs int) {
	t.Helper()
	shown, err := cli.SeedShow("", seedID)
	if err != nil {
		t.Fatal(err)
	}
	if shown.Seed.TenderSession != sessionID || protocol.Deref(shown.Seed.LastExecutionID) != sessionID {
		t.Errorf("%s is tended by %q in execution %q, want %s", seedID, shown.Seed.TenderSession, protocol.Deref(shown.Seed.LastExecutionID), sessionID)
	}
	found := 0
	for _, note := range shown.Notes {
		if note.Kind == "handoff" {
			found++
		}
	}
	if found != handoffs {
		t.Errorf("%s holds %d handoff notes, want %d", seedID, found, handoffs)
	}
}
