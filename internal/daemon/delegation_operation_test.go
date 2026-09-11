package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

func waitDelegationOperation(t *testing.T, d *Daemon, id string) *protocol.DelegationOperation {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last *protocol.DelegationOperation
	for time.Now().Before(deadline) {
		op, err := d.delegationOperation(id)
		if err != nil {
			t.Fatalf("delegationOperation(%s): %v", id, err)
		}
		if op.State == protocol.DelegationOperationStateCompleted || op.State == protocol.DelegationOperationStateFailed {
			return op
		}
		last = op
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for delegation operation %s: last=%+v", id, last)
	return nil
}

func explicitOperationMessage(d *Daemon, requestID, sourceID, brief, label string) protocol.DelegateMessage {
	return protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: requestID, SourceSessionID: protocol.Ptr(sourceID),
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr(brief)},
		Cwd:        d.store.Get(sourceID).Directory, Agent: protocol.Ptr("codex"), Label: protocol.Ptr(label),
	}
}

func TestDelegationOperationSequentialAndResponseLossRetryConverge(t *testing.T) {
	d := newDelegationDaemon(t)
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSource(t, d, backend)
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, sourceID); err != nil {
		t.Fatal(err)
	}
	consumeDelegatedPrompt(t, backend)
	msg := explicitOperationMessage(d, "stable-request", sourceID, "Do the work once.", "once")

	first, err := d.startDelegation(&msg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.startDelegation(&msg)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID != second.OperationID || first.SessionID != second.SessionID {
		t.Fatalf("retries diverged: first=%+v second=%+v", first, second)
	}
	done := waitDelegationOperation(t, d, first.OperationID)
	if done.Result == nil {
		t.Fatalf("completed operation has no result: %+v failure=%s", done, protocol.Deref(done.Error))
	}
	if done.WorktreePath != nil {
		t.Fatalf("ordinary delegation reported worktree_path=%q", protocol.Deref(done.WorktreePath))
	}
	if got := len(d.store.List("")); got != 2 {
		t.Fatalf("sessions=%d, want source + one delegate", got)
	}
	if _, bound := d.gardenDispatchCrown(done.SessionID); !bound {
		t.Fatalf("the converged delegation bound no seed to session %s", done.SessionID)
	}
}

func TestDelegationOperationReservesOperationIDNamespace(t *testing.T) {
	d := newDelegationDaemon(t)
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSource(t, d, backend)
	msg := explicitOperationMessage(d, "op-caller-value", sourceID, "Must reject.", "reserved")
	_, err := d.startDelegation(&msg)
	if err == nil || !strings.Contains(err.Error(), "reserved operation prefix") {
		t.Fatalf("error=%v", err)
	}
}

func TestExplicitSeedDispatchRequiresHandoverBeforeCreatingWorktree(t *testing.T) {
	root := t.TempDir()
	repo := initDelegationRepo(t, root, "repo")
	d := newDelegationDaemon(t)
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSourceAt(t, d, backend, repo)
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "held work", Body: protocol.Ptr("Keep ownership explicit.")})
	move(t, d, sourceID, seed.ID, garden.VerbTend, "", "")
	path := filepath.Join(root, "repo--unexpected")
	msg := protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "seed-without-handover", SourceSessionID: protocol.Ptr(sourceID),
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindSeed, SeedID: protocol.Ptr(seed.ID)},
		Cwd:        repo, Agent: protocol.Ptr("codex"),
		Checkout: &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindNewWorktree, Branch: "feat/unexpected", From: protocol.Ptr("HEAD"), Path: protocol.Ptr(path)},
	}
	op, err := d.startDelegation(&msg)
	if err != nil {
		t.Fatal(err)
	}
	done := waitDelegationOperation(t, d, op.OperationID)
	if done.State != protocol.DelegationOperationStateFailed || done.Failure == nil || !strings.Contains(done.Failure.Message, "use --handover") {
		t.Fatalf("operation = %+v, want explicit handover refusal", done)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("refused dispatch created worktree %s: %v", path, err)
	}
	if attngit.RefExists(repo, "feat/unexpected") {
		t.Fatal("refused dispatch created its branch")
	}
}

func TestConcurrentReuseDelegationsRequireExplicitSharing(t *testing.T) {
	root := t.TempDir()
	repo := initDelegationRepo(t, root, "repo")
	d := newDelegationDaemon(t)
	backend := &fakeSpawnBackend{}
	setupDelegationSource(t, d, backend)
	branch, err := attngit.GetCurrentBranch(repo)
	if err != nil {
		t.Fatal(err)
	}
	firstSpawn := make(chan struct{})
	releaseFirst := make(chan struct{})
	var spawnOnce sync.Once
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		spawnOnce.Do(func() {
			close(firstSpawn)
			<-releaseFirst
		})
		backend.mu.Lock()
		backend.sessionIDs = append(backend.sessionIDs, opts.ID)
		backend.mu.Unlock()
	}
	request := func(id string) protocol.DelegateMessage {
		return protocol.DelegateMessage{
			Cmd: protocol.CmdDelegate, RequestID: id,
			Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr("Use the shared checkout safely.")},
			Cwd:        repo, Agent: protocol.Ptr("codex"), Label: protocol.Ptr(id),
			Checkout: &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindReuse, Branch: branch},
		}
	}
	firstRequest := request("reuse-first")
	first, err := d.startDelegation(&firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstSpawn:
	case <-time.After(2 * time.Second):
		t.Fatal("first delegation did not reach spawn")
	}
	secondRequest := request("reuse-second")
	second, err := d.startDelegation(&secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	close(releaseFirst)
	firstDone := waitDelegationOperation(t, d, first.OperationID)
	secondDone := waitDelegationOperation(t, d, second.OperationID)
	if firstDone.State != protocol.DelegationOperationStateCompleted {
		t.Fatalf("first operation = %+v", firstDone)
	}
	if secondDone.State != protocol.DelegationOperationStateFailed || secondDone.Failure == nil || !strings.Contains(secondDone.Failure.Message, "--allow-worktree-reuse") {
		t.Fatalf("second operation = %+v, want sharing refusal", secondDone)
	}
}

func TestRecoveredDelegationResultDistinguishesReusedWorktree(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	session := &protocol.Session{ID: "session", WorkspaceID: "workspace", Directory: "/tmp/shared", IsWorktree: protocol.Ptr(true)}
	reused := d.completedDelegationResult(session, delegationPlacementNew, false)
	if reused.WorktreeCreated != nil {
		t.Fatalf("reused worktree reported created=%v", protocol.Deref(reused.WorktreeCreated))
	}
	created := d.completedDelegationResult(session, delegationPlacementNew, true)
	if !protocol.Deref(created.WorktreeCreated) {
		t.Fatal("owned worktree lost created receipt")
	}
}

func TestDelegationOperationConcurrentRetriesConverge(t *testing.T) {
	d := newDelegationDaemon(t)
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	msg := explicitOperationMessage(d, "concurrent-request", sourceID, "Launch once concurrently.", "parallel")
	const callers = 12
	results := make(chan *protocol.DelegationOperation, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := msg
			op, err := d.startDelegation(&copy)
			if err != nil {
				errs <- err
				return
			}
			results <- op
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	operationID := ""
	for op := range results {
		if operationID == "" {
			operationID = op.OperationID
		}
		if op.OperationID != operationID {
			t.Fatalf("operation ids diverged: %s != %s", op.OperationID, operationID)
		}
	}
	done := waitDelegationOperation(t, d, operationID)
	if done.Result == nil || len(d.store.List("")) != 2 {
		t.Fatalf("operation=%+v failure=%s sessions=%d", done, protocol.Deref(done.Error), len(d.store.List("")))
	}
}

// Not synctest-able: the launch creates a real git worktree, and acceptance measures wall-clock
// including the read-only Git call that pins the explicit base ref before journaling.
func TestDelegationOperationAcceptedBeforeSlowPreparation(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	d := newDelegationDaemon(t)
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, sourceID); err != nil {
		t.Fatal(err)
	}
	consumeDelegatedPrompt(t, backend)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	d.delegationWorktreePrepareHook = func(string) {
		entered <- struct{}{}
		<-release
	}
	start := time.Now()
	slow := explicitOperationMessage(d, "slow-request", sourceID, "Slow worktree launch.", "slow")
	slow.Cwd = mainRepo
	slow.Checkout = &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindNewWorktree, Branch: "feat/slow", From: protocol.Ptr("HEAD"), Path: protocol.Ptr(filepath.Join(root, "repo--slow"))}
	op, err := d.startDelegation(&slow)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 250*time.Millisecond {
		t.Fatalf("durable acceptance was not prompt: %v", time.Since(start))
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("launch did not reach controlled slow seam")
	}
	inProgress, err := d.delegationOperation(op.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if inProgress.State != protocol.DelegationOperationStatePreparing {
		t.Fatalf("state=%s, want preparing", inProgress.State)
	}
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "replacement-chief"); err != nil {
		t.Fatal(err)
	}
	close(release)
	done := waitDelegationOperation(t, d, op.OperationID)
	if done.State != protocol.DelegationOperationStateCompleted {
		t.Fatalf("done=%+v", done)
	}
	if ids := d.delegatedFromChiefSessionIDs(); !ids[done.SessionID] {
		t.Fatalf("chief-ness was not fixed at admission: %v", ids)
	}
}

func TestDelegationOperationRestartResumesAcceptedRecord(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	persistent, err := store.NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	d1 := newDelegationDaemon(t)
	_ = d1.store.Close()
	d1.store = persistent
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSource(t, d1, backend)
	msg := explicitOperationMessage(d1, "restart-request", sourceID, "Resume after restart.", "restart")
	requestJSON, _ := json.Marshal(msg)
	record, claimed, err := d1.store.ClaimDelegationOperation(msg.RequestID, "operation-restart", "session-restart", "", "", string(requestJSON), time.Now())
	if err != nil || !claimed {
		t.Fatalf("claim: claimed=%v err=%v", claimed, err)
	}
	if err := d1.store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := store.NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	d2 := NewForTesting(filepath.Join(d1.dataRoot, "two.sock"))
	d2.daemonInstanceID = d1.daemonInstanceID
	_ = d2.store.Close()
	d2.store = reopened
	d2.ptyBackend = &fakeSpawnBackend{}
	d2.ensureGardenCollections()
	consumeDelegatedPrompt(t, d2.ptyBackend.(*fakeSpawnBackend))
	d2.loadWorkspacesFromStore()
	d2.resumePendingDelegations()
	done := waitDelegationOperation(t, d2, record.Operation.OperationID)
	if done.State != protocol.DelegationOperationStateCompleted || done.SessionID != "session-restart" {
		t.Fatalf("resumed operation=%+v failure=%+v error=%q", done, done.Failure, protocol.Deref(done.Error))
	}
}

func TestDelegationOperationAdoptsReconciledReservedRuntime(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	workspaceID, sourceID, cwd := setupDelegationSource(t, d, backend)
	encoded, _ := json.Marshal(map[string]any{"cmd": "delegate", "request_id": "spawn-crash", "source_session_id": sourceID, "brief": "Adopt the surviving runtime.", "agent": "codex", "label": "adopted"})
	record, _, err := d.store.ClaimDelegationOperation("spawn-crash", "operation-spawn-crash", "session-spawn-crash", "", "", string(encoded), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{ID: record.Operation.SessionID, WorkspaceID: workspaceID, Label: "adopted", Agent: protocol.SessionAgentCodex, Directory: cwd, State: protocol.SessionStateLaunching, StateSince: now, StateUpdatedAt: now, LastSeen: now})
	backend.sessionIDs = append(backend.sessionIDs, record.Operation.SessionID)
	d.runDelegationOperation(record.Operation.OperationID)
	done := waitDelegationOperation(t, d, record.Operation.OperationID)
	if done.State != protocol.DelegationOperationStateCompleted || done.WorkspaceID == nil || protocol.Deref(done.WorkspaceID) != workspaceID {
		t.Fatalf("operation=%+v", done)
	}
	adopted := d.store.Get(record.Operation.SessionID)
	if adopted == nil || adopted.WorkspaceID != workspaceID || adopted.Label != "adopted" {
		t.Fatalf("adopted session=%+v", adopted)
	}
	if got := len(backend.spawnOpts); got != 1 {
		t.Fatalf("spawn count=%d, want only source runtime", got)
	}
}

func TestLegacyDelegationOperationWithoutLiveRuntimeRequiresExplicitRetry(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	workspaceID, sourceID, cwd := setupDelegationSource(t, d, backend)
	encoded, _ := json.Marshal(map[string]any{"cmd": "delegate", "request_id": "spawn-missing", "source_session_id": sourceID, "brief": "Recover the missing runtime.", "agent": "codex", "label": "respawned"})
	record, _, err := d.store.ClaimDelegationOperation("spawn-missing", "operation-spawn-missing", "session-spawn-missing", "", "", string(encoded), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{ID: record.Operation.SessionID, WorkspaceID: workspaceID, Label: "respawned", Agent: protocol.SessionAgentCodex, Directory: cwd, State: protocol.SessionStateRecoverable, StateSince: now, StateUpdatedAt: now, LastSeen: now})
	d.runDelegationOperation(record.Operation.OperationID)
	done := waitDelegationOperation(t, d, record.Operation.OperationID)
	if done.State != protocol.DelegationOperationStateFailed || done.Failure == nil || !strings.Contains(done.Failure.Message, "new request") {
		t.Fatalf("operation=%+v", done)
	}
	if got := len(backend.spawnOpts); got != 1 {
		t.Fatalf("spawn count=%d, want only source runtime", got)
	}
}

func TestDelegationOperationTerminalFailureRetryDoesNotRelaunch(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSource(t, d, backend)
	value := explicitOperationMessage(d, "failed-request", sourceID, "Fail once.", "failed")
	value.Agent = protocol.Ptr("missing-agent")
	msg := &value
	first, err := d.startDelegation(msg)
	if err != nil {
		t.Fatal(err)
	}
	failed := waitDelegationOperation(t, d, first.OperationID)
	if failed.State != protocol.DelegationOperationStateFailed {
		t.Fatalf("operation=%+v", failed)
	}
	second, err := d.startDelegation(msg)
	if err != nil {
		t.Fatal(err)
	}
	if second.OperationID != first.OperationID || second.State != protocol.DelegationOperationStateFailed {
		t.Fatalf("retry=%+v first=%+v", second, first)
	}
	if len(d.store.List("")) != 1 {
		t.Fatalf("failure retry created a session")
	}
}

func TestDelegationRestartDoesNotOwnWorktreeFromPathJournalAlone(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	path := filepath.Join(root, "repo--external")
	msg := explicitOperationMessage(d, "path-only", sourceID, "Do not adopt external work.", "path-only")
	msg.Cwd = mainRepo
	msg.Checkout = &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindNewWorktree, Branch: "feat/external", From: protocol.Ptr("HEAD"), Path: protocol.Ptr(path)}
	encoded, _ := json.Marshal(msg)
	record, _, err := d.store.ClaimDelegationOperation(msg.RequestID, "operation-path-only", "session-path-only", "", "", string(encoded), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := d.store.UpdateDelegationOperation(record.Operation.OperationID, protocol.DelegationOperationStatePreparing,
		"preparing worktree "+path, "", "", path, nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "feat/external", path)
	d.runDelegationOperation(record.Operation.OperationID)
	failed := waitDelegationOperation(t, d, record.Operation.OperationID)
	if failed.State != protocol.DelegationOperationStateFailed {
		t.Fatalf("operation=%+v", failed)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("external worktree was removed: %v", err)
	}
	stored, err := d.store.GetDelegationOperation(record.Operation.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.WorktreeOwned {
		t.Fatal("path intent was incorrectly promoted to cleanup ownership")
	}
}

func TestDelegationRestartDoesNotDeleteReplacementForPreviouslyOwnedWorktree(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	path := filepath.Join(root, "repo--replacement")
	msg := explicitOperationMessage(d, "owned-replaced", sourceID, "Do not delete replacement work.", "owned-replaced")
	msg.Cwd = mainRepo
	msg.Checkout = &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindNewWorktree, Branch: "feat/replacement", From: protocol.Ptr("HEAD"), Path: protocol.Ptr(path)}
	encoded, _ := json.Marshal(msg)
	record, _, err := d.store.ClaimDelegationOperation(msg.RequestID, "operation-owned-replaced", "session-owned-replaced", "", "", string(encoded), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "feat/replacement", path)
	if err := d.store.MarkDelegationWorktreeOwned(record.Operation.OperationID, path, "original-owner-token", time.Now()); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "worktree", "remove", path)
	runGitDaemon(t, mainRepo, "branch", "-D", "feat/replacement")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "feat/replacement", path)

	d.runDelegationOperation(record.Operation.OperationID)
	failed := waitDelegationOperation(t, d, record.Operation.OperationID)
	if failed.State != protocol.DelegationOperationStateFailed {
		t.Fatalf("operation=%+v", failed)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("replacement worktree was removed: %v", err)
	}
}

func TestDelegationRestartResumesPreviouslyOwnedWorktreeWithMatchingMarker(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	path := attngit.GenerateWorktreePath(mainRepo, "feat/owned")
	msg := explicitOperationMessage(d, "owned-resume", sourceID, "Resume original work.", "owned-resume")
	msg.Cwd = mainRepo
	msg.Checkout = &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindNewWorktree, Branch: "feat/owned", From: protocol.Ptr("HEAD")}
	encoded, _ := json.Marshal(msg)
	record, _, err := d.store.ClaimDelegationOperation(msg.RequestID, "operation-owned-resume", "session-owned-resume", "", "", string(encoded), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "feat/owned", path)
	const ownerToken = "matching-owner-token"
	if err := writeDelegationWorktreeOwner(path, ownerToken); err != nil {
		t.Fatal(err)
	}
	if err := d.store.MarkDelegationWorktreeOwned(record.Operation.OperationID, path, ownerToken, time.Now()); err != nil {
		t.Fatal(err)
	}

	d.runDelegationOperation(record.Operation.OperationID)
	completed := waitDelegationOperation(t, d, record.Operation.OperationID)
	if completed.State != protocol.DelegationOperationStateCompleted || completed.Result == nil {
		t.Fatalf("operation=%+v failure=%+v error=%q", completed, completed.Failure, protocol.Deref(completed.Error))
	}
	if completed.Result.SessionID != record.Operation.SessionID || !protocol.Deref(completed.Result.WorktreeCreated) {
		t.Fatalf("result=%+v", completed.Result)
	}
}

func TestDelegationRestartLeavesOwnedWorktreeWhenAnotherSessionOccupiesIt(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	path := filepath.Join(root, "repo--occupied")
	msg := explicitOperationMessage(d, "owned-occupied", sourceID, "Do not disturb the occupant.", "owned-occupied")
	msg.Cwd = mainRepo
	msg.Checkout = &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindNewWorktree, Branch: "feat/occupied", From: protocol.Ptr("HEAD"), Path: protocol.Ptr(path)}
	encoded, _ := json.Marshal(msg)
	record, _, err := d.store.ClaimDelegationOperation(msg.RequestID, "operation-owned-occupied", "session-owned-occupied", "", "", string(encoded), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "feat/occupied", path)
	const ownerToken = "occupied-owner-token"
	if err := writeDelegationWorktreeOwner(path, ownerToken); err != nil {
		t.Fatal(err)
	}
	if err := d.store.MarkDelegationWorktreeOwned(record.Operation.OperationID, path, ownerToken, time.Now()); err != nil {
		t.Fatal(err)
	}
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{ID: "other-session", Label: "other", Agent: protocol.SessionAgentCodex, Directory: path, State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now})
	backend.sessionIDs = append(backend.sessionIDs, "other-session")

	d.runDelegationOperation(record.Operation.OperationID)
	failed := waitDelegationOperation(t, d, record.Operation.OperationID)
	if failed.State != protocol.DelegationOperationStateFailed {
		t.Fatalf("operation=%+v", failed)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("occupied worktree was removed: %v", err)
	}
	if d.store.Get("other-session") == nil {
		t.Fatal("occupying session was removed")
	}
}
