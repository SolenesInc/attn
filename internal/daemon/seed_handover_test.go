package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func resolvedHandoverRequest(seed garden.Seed, docRev int64, requestID, sourceSessionID, handoff string) *resolvedDelegationLaunch {
	return &resolvedDelegationLaunch{RequestID: requestID, SourceSessionID: protocol.Ptr(sourceSessionID),
		Handover: &protocol.SeedHandoverRequest{
			SeedID: seed.ID, Handoff: protocol.Ptr(handoff), ExpectedRev: int(docRev),
			ExpectedTenderSession: seed.TenderSession, ExpectedTenderMember: seed.TenderMember,
		},
	}
}

func handoverRequest(d *Daemon, seed garden.Seed, requestID, sourceSessionID, handoff string) *protocol.DelegateMessage {
	cwd := d.store.Get(sourceSessionID).Directory
	if dispatch, ok := d.gardenDispatch(seed.TenderSession); ok && dispatch.Cwd != "" {
		cwd = dispatch.Cwd
	}
	msg := &protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: requestID, SourceSessionID: protocol.Ptr(sourceSessionID),
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindSeed, SeedID: protocol.Ptr(seed.ID), Handover: &protocol.DelegateHandover{Note: protocol.Ptr(handoff)}},
		Cwd:        cwd, Agent: protocol.Ptr("codex"),
	}
	if _, err := attngit.GetRepoRoot(cwd); err == nil {
		branch, branchErr := attngit.GetCurrentBranch(cwd)
		if branchErr == nil && branch != "" {
			msg.Checkout = &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindReuse, Branch: branch}
		}
	}
	return msg
}

func TestSeedHandoverReusesTheExactDirectoryAndConvergesOnRetry(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	writeCrewHomes(t, d.dataRoot)
	d.ensureCrewCollections()
	d.importCrewHomes()
	if _, err := d.claimCrewBinding("alder", sourceSessionID); err != nil {
		t.Fatalf("bind alder: %v", err)
	}
	consumeDelegatedPrompt(t, backend)
	oldSessionID, seedID := delegateBoundSeed(t, d, backend, sourceSessionID, "codex")
	oldDispatch, ok := d.gardenDispatch(oldSessionID)
	if !ok {
		t.Fatal("old execution was not saved")
	}
	dirty := filepath.Join(oldDispatch.Cwd, "unfinished.txt")
	if err := os.WriteFile(dirty, []byte("still here"), 0o600); err != nil {
		t.Fatal(err)
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatal(err)
	}

	var prompt string
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.InitialPromptFile == "" {
			return
		}
		raw, readErr := os.ReadFile(opts.InitialPromptFile)
		if readErr != nil {
			t.Errorf("read Handover prompt: %v", readErr)
			return
		}
		prompt = string(raw)
		if removeErr := os.Remove(opts.InitialPromptFile); removeErr != nil {
			t.Errorf("remove Handover prompt: %v", removeErr)
		}
	}
	watchSeed(t, d, sourceSessionID, seedID, true)
	msg := handoverRequest(d, seed, "handover-reuse", sourceSessionID, "Continue from the failing test.")
	op, err := d.startDelegation(msg)
	if err != nil {
		t.Fatal(err)
	}
	done := waitDelegationOperation(t, d, op.OperationID)
	if done.State != protocol.DelegationOperationStateCompleted || done.Result == nil {
		t.Fatalf("Handover operation = %+v", done)
	}
	if attngit.CanonicalizePath(done.Result.Directory) != attngit.CanonicalizePath(oldDispatch.Cwd) {
		t.Fatalf("directory = %q, want exact reused cwd %q", done.Result.Directory, oldDispatch.Cwd)
	}
	if raw, err := os.ReadFile(dirty); err != nil || string(raw) != "still here" {
		t.Fatalf("unfinished work = %q, %v", raw, err)
	}

	after, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Body != seed.Body || after.TenderSession != done.SessionID || after.LastExecutionID != done.SessionID {
		t.Fatalf("handed-over seed = %+v", after)
	}
	if d.store.Get(oldSessionID) == nil {
		t.Fatal("the previous conversation was removed")
	}
	oldAfter, _ := d.gardenDispatch(oldSessionID)
	if oldAfter.SupersededBy != done.SessionID {
		t.Fatalf("old dispatch superseded_by = %q, want %q", oldAfter.SupersededBy, done.SessionID)
	}
	if _, active := d.gardenDispatchCrown(oldSessionID); active {
		t.Fatal("the previous conversation still claims the seed as its dispatch crown")
	}
	newDispatch, ok := d.gardenDispatch(done.SessionID)
	if !ok || newDispatch.DispatcherSession != sourceSessionID || newDispatch.DispatcherMember != "alder" {
		t.Fatalf("new dispatch did not record its dispatcher Tender: %+v, found=%v", newDispatch, ok)
	}
	notes, _, err := d.readNotes(seedID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Kind != garden.NoteKindHandoff || notes[0].Body != "Continue from the failing test." {
		t.Fatalf("handoff notes = %+v", notes)
	}
	if strings.Contains(prompt, seed.Body) || strings.Contains(prompt, notes[0].Body) || !strings.Contains(prompt, "attn seed show "+seedID) {
		t.Fatalf("Handover prompt must reference the seed without copying its mutable content:\n%s", prompt)
	}

	if watching, err := d.store.GardenSeedWatching(done.SessionID, seedID); err != nil || !watching {
		t.Fatalf("new Handover did not subscribe successor: %v %v", watching, err)
	}
	watchSeed(t, d, done.SessionID, seedID, true)
	retry, err := d.startDelegation(msg)
	if err != nil {
		t.Fatal(err)
	}
	if watching, err := d.store.GardenSeedWatching(done.SessionID, seedID); err != nil || watching {
		t.Fatalf("Handover replay restored removed watch: %v %v", watching, err)
	}
	if retry.OperationID != done.OperationID || retry.SessionID != done.SessionID {
		t.Fatalf("retry diverged: first=%+v retry=%+v", done, retry)
	}
	notes, _, err = d.readNotes(seedID, 10)
	if err != nil || len(notes) != 1 {
		t.Fatalf("retry duplicated the handoff: notes=%+v err=%v", notes, err)
	}
}

func TestSeedHandoverCannotBeUndoneByAnInFlightMetadataRefresh(t *testing.T) {
	tests := []struct {
		name    string
		refresh func(*Daemon, string) error
	}{
		{
			name: "execution",
			refresh: func(d *Daemon, sessionID string) error {
				_, err := d.captureGardenSessionExecution(d.store.Get(sessionID))
				return err
			},
		},
		{
			name: "resume identity",
			refresh: func(d *Daemon, sessionID string) error {
				return d.rememberDispatchResume(sessionID, "late-native-conversation")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			d, backend, sourceSessionID := newGardenDelegationDaemon(t)
			consumeDelegatedPrompt(t, backend)
			oldSessionID, seedID := delegateBoundSeed(t, d, backend, sourceSessionID, "codex")
			seed, _, err := d.readSeed(seedID)
			if err != nil {
				t.Fatal(err)
			}

			refreshRead := make(chan struct{})
			finishRefresh := make(chan struct{})
			var pause, release sync.Once
			d.gardenDispatchBeforeWrite = func(sessionID string) {
				if sessionID != oldSessionID {
					return
				}
				pause.Do(func() {
					close(refreshRead)
					<-finishRefresh
				})
			}
			releaseRefresh := func() { release.Do(func() { close(finishRefresh) }) }
			defer releaseRefresh()

			refreshDone := make(chan error, 1)
			go func() { refreshDone <- test.refresh(d, oldSessionID) }()
			<-refreshRead

			op, err := d.startDelegation(handoverRequest(
				d, seed, "handover-vs-"+strings.ReplaceAll(test.name, " ", "-"), sourceSessionID, ""))
			if err != nil {
				t.Fatal(err)
			}
			done := waitDelegationOperation(t, d, op.OperationID)
			if done.State != protocol.DelegationOperationStateCompleted || done.Result == nil {
				t.Fatalf("Handover operation = %+v", done)
			}

			releaseRefresh()
			if err := <-refreshDone; err != nil {
				t.Fatalf("metadata refresh after Handover: %v", err)
			}
			oldAfter, ok := d.gardenDispatch(oldSessionID)
			if !ok || oldAfter.SupersededBy != done.SessionID {
				t.Fatalf("old dispatch after refresh = %+v, ok=%v", oldAfter, ok)
			}
			if crown, active := d.gardenDispatchCrown(oldSessionID); active {
				t.Fatalf("stale refresh restored old crown %q", crown)
			}
		})
	}
}

func TestSeedHandoverBroadcastRejectsAnOlderCommittedMetadataRefresh(t *testing.T) {
	tests := []struct {
		name    string
		refresh func(*Daemon, string) error
	}{
		{
			name: "execution",
			refresh: func(d *Daemon, sessionID string) error {
				_, err := d.captureGardenSessionExecution(d.store.Get(sessionID))
				return err
			},
		},
		{
			name: "resume identity",
			refresh: func(d *Daemon, sessionID string) error {
				return d.rememberDispatchResume(sessionID, "late-native-conversation")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			d, backend, sourceSessionID := newGardenDelegationDaemon(t)
			consumeDelegatedPrompt(t, backend)
			oldSessionID, seedID := delegateBoundSeed(t, d, backend, sourceSessionID, "codex")
			seed, _, err := d.readSeed(seedID)
			if err != nil {
				t.Fatal(err)
			}
			if got := protocol.Deref(d.sessionForBroadcast(d.store.Get(oldSessionID)).SeedID); got != seedID {
				t.Fatalf("old session seed before Handover = %q, want %s", got, seedID)
			}

			refreshCommitted := make(chan struct{})
			finishRefresh := make(chan struct{})
			var pause, release sync.Once
			d.gardenDispatchAfterWrite = func(sessionID string) {
				if sessionID != oldSessionID {
					return
				}
				pause.Do(func() {
					close(refreshCommitted)
					<-finishRefresh
				})
			}
			releaseRefresh := func() { release.Do(func() { close(finishRefresh) }) }
			defer releaseRefresh()

			refreshDone := make(chan error, 1)
			go func() { refreshDone <- test.refresh(d, oldSessionID) }()
			<-refreshCommitted

			op, err := d.startDelegation(handoverRequest(
				d, seed, "handover-after-commit-"+strings.ReplaceAll(test.name, " ", "-"), sourceSessionID, ""))
			if err != nil {
				t.Fatal(err)
			}
			done := waitDelegationOperation(t, d, op.OperationID)
			if done.State != protocol.DelegationOperationStateCompleted || done.Result == nil {
				t.Fatalf("Handover operation = %+v", done)
			}

			releaseRefresh()
			if err := <-refreshDone; err != nil {
				t.Fatalf("committed metadata refresh after Handover: %v", err)
			}
			oldBroadcast := d.sessionForBroadcast(d.store.Get(oldSessionID))
			if oldBroadcast.SeedID != nil {
				t.Fatalf("superseded session regained seed_id %q", protocol.Deref(oldBroadcast.SeedID))
			}
			newBroadcast := d.sessionForBroadcast(d.store.Get(done.SessionID))
			if got := protocol.Deref(newBroadcast.SeedID); got != seedID {
				t.Fatalf("handed-over session seed_id = %q, want %s", got, seedID)
			}
		})
	}
}

func TestSeedHandoverLaunchFailurePreservesTheCommittedTransfer(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	oldSessionID, seedID := delegateBoundSeed(t, d, backend, sourceSessionID, "codex")
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatal(err)
	}
	backend.spawnErr = syscall.EPERM

	op, err := d.startDelegation(handoverRequest(d, seed, "handover-fails", sourceSessionID, "This must not land."))
	if err != nil {
		t.Fatal(err)
	}
	done := waitDelegationOperation(t, d, op.OperationID)
	if done.State != protocol.DelegationOperationStateFailed {
		t.Fatalf("operation = %+v, want failed", done)
	}
	after, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TenderSession != done.SessionID || after.LastExecutionID != done.SessionID {
		t.Fatalf("failed Handover lost committed ownership: %+v", after)
	}
	if session := d.store.Get(done.SessionID); session != nil {
		t.Fatalf("failed Handover left a worker: %+v", session)
	}
	if notes := seedNoteCount(t, d, seedID); notes != 1 {
		t.Fatalf("failed Handover retained %d notes, want its durable handoff", notes)
	}
	oldDispatch, _ := d.gardenDispatch(oldSessionID)
	if oldDispatch.SupersededBy != done.SessionID {
		t.Fatalf("predecessor dispatch was not superseded: %+v", oldDispatch)
	}
}

func TestSeedHandoverLosesCleanlyWhenTheSeedMovesDuringLaunch(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	oldSessionID, seedID := delegateBoundSeed(t, d, backend, sourceSessionID, "codex")
	addGardenSession(t, d, "racing-session")
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatal(err)
	}
	d.seedHandoverBeforeCommit = func() {
		if _, _, moveErr := d.applySeedTransition(seedID, garden.VerbTend, garden.Ask{
			Actor: garden.Tender{Session: "racing-session"}, Force: true,
		}); moveErr != nil {
			t.Errorf("move the seed during launch: %v", moveErr)
		}
	}

	op, err := d.startDelegation(handoverRequest(d, seed, "handover-race", sourceSessionID, "This must not land."))
	if err != nil {
		t.Fatal(err)
	}
	done := waitDelegationOperation(t, d, op.OperationID)
	if done.State != protocol.DelegationOperationStateFailed || done.Failure == nil || !strings.Contains(done.Failure.Message, "ownership or state changed") {
		t.Fatalf("operation = %+v, want guarded race failure", done)
	}
	after, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TenderSession != "racing-session" || after.LastExecutionID != "racing-session" {
		t.Fatalf("race winner was overwritten: %+v", after)
	}
	if session := d.store.Get(done.SessionID); session != nil {
		t.Fatalf("losing Handover left a worker: %+v", session)
	}
	if d.store.Get(oldSessionID) == nil {
		t.Fatal("the old conversation was removed by the losing Handover")
	}
	notes, _, err := d.readNotes(seedID, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, note := range notes {
		if note.Kind == garden.NoteKindHandoff {
			t.Fatalf("losing Handover wrote its handoff: %+v", note)
		}
	}
}

func TestSeedHandoverUsesTheSubmittedDirectory(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	seedWire := plant(t, d, protocol.SeedPlantMessage{Title: "unplaced work", Body: protocol.Ptr("Do the work.")})
	seed, _, err := d.readSeed(seedWire.ID)
	if err != nil {
		t.Fatal(err)
	}

	placed := handoverRequest(d, seed, "handover-placed", sourceSessionID, "")
	placed.Cwd = t.TempDir()
	op, err := d.startDelegation(placed)
	if err != nil {
		t.Fatal(err)
	}
	done := waitDelegationOperation(t, d, op.OperationID)
	if done.State != protocol.DelegationOperationStateCompleted || done.Result == nil {
		t.Fatalf("placed operation = %+v", done)
	}
	if attngit.CanonicalizePath(done.Result.Directory) != attngit.CanonicalizePath(placed.Cwd) {
		t.Fatalf("directory = %q, want submitted %q", done.Result.Directory, placed.Cwd)
	}
	after, _, err := d.readSeed(seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TenderSession != done.SessionID || seedNoteCount(t, d, seed.ID) != 0 {
		t.Fatalf("placed Handover = %+v notes=%d", after, seedNoteCount(t, d, seed.ID))
	}
}

func TestSeedHandoverFinishesAfterAnInterruptedLaunchCapturedTheNewSession(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	_, seedID := delegateBoundSeed(t, d, backend, sourceSessionID, "codex")
	seed, doc, err := d.readSeed(seedID)
	if err != nil {
		t.Fatal(err)
	}
	now := string(protocol.TimestampNow())
	newSessionID := "interrupted-handover"
	d.store.Add(&protocol.Session{
		ID: newSessionID, Label: "new worker", Agent: protocol.SessionAgentCodex,
		Directory: t.TempDir(), State: protocol.SessionStateIdle,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	if _, err := d.captureGardenSessionExecution(d.store.Get(newSessionID)); err != nil {
		t.Fatal(err)
	}
	before, ok := d.gardenDispatch(newSessionID)
	if !ok || before.Crown != "" {
		t.Fatalf("interrupted dispatch = %+v, ok=%v", before, ok)
	}
	msg := resolvedHandoverRequest(seed, doc.Rev, "handover-recovered", sourceSessionID, "Recovered after restart.")
	if _, err := d.bindSeedHandover(msg, "op-recovered", newSessionID, d.store.Get(newSessionID).Directory, "codex", false); err != nil {
		t.Fatalf("bind recovered Handover: %v", err)
	}
	after, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TenderSession != newSessionID || after.LastExecutionID != newSessionID {
		t.Fatalf("recovered Handover = %+v", after)
	}
	dispatch, ok := d.gardenDispatch(newSessionID)
	if !ok || dispatch.Crown != seedID || dispatch.OperationID != "op-recovered" {
		t.Fatalf("recovered dispatch = %+v, ok=%v", dispatch, ok)
	}
}

func TestSeedHandoverDoesNotTakeAnotherSeedSharingTheOldSession(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	oldSessionID, crownSeedID := delegateBoundSeed(t, d, backend, sourceSessionID, "codex")
	secondWire := plant(t, d, protocol.SeedPlantMessage{Title: "second responsibility", Body: protocol.Ptr("Keep the first seed assigned too.")})
	move(t, d, oldSessionID, secondWire.ID, garden.VerbTend, "", "")
	second, _, err := d.readSeed(secondWire.ID)
	if err != nil {
		t.Fatal(err)
	}

	op, err := d.startDelegation(handoverRequest(d, second, "handover-one-of-two", sourceSessionID, ""))
	if err != nil {
		t.Fatal(err)
	}
	done := waitDelegationOperation(t, d, op.OperationID)
	if done.State != protocol.DelegationOperationStateCompleted {
		t.Fatalf("operation = %+v", done)
	}
	if crown, ok := d.gardenDispatchCrown(oldSessionID); !ok || crown != crownSeedID {
		t.Fatalf("old session crown = %q, ok=%v, want %s", crown, ok, crownSeedID)
	}
	first, _, err := d.readSeed(crownSeedID)
	if err != nil {
		t.Fatal(err)
	}
	if first.TenderSession != oldSessionID {
		t.Fatalf("unrelated seed moved: %+v", first)
	}
}

func TestSeedHandoverRecreatesTheSavedBranchAfterWorktreeDeletion(t *testing.T) {
	root := t.TempDir()
	repo := initDelegationRepo(t, root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "nested", "context.txt"), []byte("context"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, repo, "add", "nested/context.txt")
	runGitDaemon(t, repo, "commit", "-m", "add nested context")
	worktree := filepath.Join(root, "repo--feature")
	runGitDaemon(t, repo, "worktree", "add", "-b", "feature/handover", worktree)

	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	d.registerCreatedWorktree(repo, worktree, "feature/handover")
	now := string(protocol.TimestampNow())
	oldSessionID := "old-worktree-session"
	d.store.Add(&protocol.Session{
		ID: oldSessionID, Label: "old worker", Agent: protocol.SessionAgentCodex,
		Directory: filepath.Join(worktree, "nested"), State: protocol.SessionStateIdle,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	seedWire := plant(t, d, protocol.SeedPlantMessage{Title: "continue the feature", Body: protocol.Ptr("Finish the feature.")})
	move(t, d, oldSessionID, seedWire.ID, garden.VerbTend, "", "")
	seedBeforeDelete, _, err := d.readSeed(seedWire.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeDelete := d.continuationForSeed(seedBeforeDelete)
	if beforeDelete == nil || beforeDelete.Execution.RepositorySubdir != "nested" {
		t.Fatalf("saved repository subdirectory = %+v, want nested", beforeDelete)
	}
	if err := d.doDeleteWorktree(worktree, nil, deleteWorktreeOptions{Force: true}); err != nil {
		t.Fatalf("delete worktree: %v", err)
	}
	if !attngit.RefExists(repo, "feature/handover") {
		t.Fatal("worktree deletion removed a branch still owned by an open seed")
	}
	seed, _, err := d.readSeed(seedWire.ID)
	if err != nil {
		t.Fatal(err)
	}
	continuation := d.continuationForSeed(seed)
	if continuation == nil || continuation.HandoverPlacement != handoverRecreateBranch {
		t.Fatalf("continuation = %+v, want safe branch recreation", continuation)
	}

	request := handoverRequest(d, seed, "handover-recreate", sourceSessionID, "The old worktree was removed.")
	request.Cwd = filepath.Join(repo, "nested")
	request.Checkout = &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindExistingBranchWorktree, Branch: "feature/handover", Path: protocol.Ptr(worktree)}
	op, err := d.startDelegation(request)
	if err != nil {
		t.Fatal(err)
	}
	done := waitDelegationOperation(t, d, op.OperationID)
	if done.State != protocol.DelegationOperationStateCompleted || done.Result == nil {
		t.Fatalf("operation = %+v", done)
	}
	wantRoot := attngit.CanonicalizePath(worktree)
	wantDirectory := filepath.Join(wantRoot, "nested")
	if done.Result.Directory != wantDirectory || !protocol.Deref(done.Result.WorktreeCreated) {
		t.Fatalf("result = %+v, want recreated directory %s", done.Result, wantDirectory)
	}
	if branch, err := attngit.GetCurrentBranch(wantRoot); err != nil || branch != "feature/handover" {
		t.Fatalf("recreated branch = %q, %v", branch, err)
	}
}

func TestSeedHandoverMergesConversationMetadataAfterTheBind(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	oldSessionID, seedID := delegateBoundSeed(t, d, backend, sourceSessionID, "codex")
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatal(err)
	}

	consumeDelegatedPrompt(t, backend)
	op, err := d.startDelegation(handoverRequest(d, seed, "handover-hook", sourceSessionID, "Pick up where the tests failed."))
	if err != nil {
		t.Fatal(err)
	}
	done := waitDelegationOperation(t, d, op.OperationID)
	if done.State != protocol.DelegationOperationStateCompleted {
		t.Fatalf("Handover operation = %+v", done)
	}
	if err := d.rememberDispatchResume(done.SessionID, "codex-conv-hook"); err != nil {
		t.Fatalf("merge observed conversation metadata: %v", err)
	}
	if d.store.Get(done.SessionID) == nil {
		t.Fatal("the handed-over worker was terminated")
	}
	after, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TenderSession != done.SessionID || after.LastExecutionID != done.SessionID {
		t.Fatalf("handed-over seed = %+v", after)
	}
	dispatch, ok := d.gardenDispatch(done.SessionID)
	if !ok || dispatch.Crown != seedID || dispatch.Resume != "codex-conv-hook" || dispatch.OperationID != done.OperationID {
		t.Fatalf("new dispatch lost the hook's write or the bind: %+v", dispatch)
	}
	if oldAfter, _ := d.gardenDispatch(oldSessionID); oldAfter.SupersededBy != done.SessionID {
		t.Fatalf("old dispatch superseded_by = %q, want %q", oldAfter.SupersededBy, done.SessionID)
	}
	if notes := seedNoteCount(t, d, seedID); notes != 1 {
		t.Fatalf("handoff notes = %d, want exactly one after the retry", notes)
	}
}
