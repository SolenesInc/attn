package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
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

func TestDelegationOperationWaitsForStartupRecovery(t *testing.T) {
	d := newDelegationDaemon(t)
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSource(t, d, backend)
	msg := explicitOperationMessage(d, "startup-recovery", sourceID, "Wait for recovered workers.", "waiting")
	msg.Agent = protocol.Ptr("missing-agent")
	synctest.Test(t, func(t *testing.T) {
		d.done = make(chan struct{})
		d.setRecovering(true)
		op, err := d.startDelegation(&msg)
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		pending, err := d.store.GetDelegationOperation(op.OperationID)
		if err != nil || pending.Operation.State != protocol.DelegationOperationStateAccepted {
			t.Fatalf("operation advanced during recovery: %+v, %v", pending, err)
		}
		d.setRecovering(false)
		synctest.Wait()
		done, err := d.store.GetDelegationOperation(op.OperationID)
		if err != nil || done.Operation.State != protocol.DelegationOperationStateFailed {
			t.Fatalf("operation did not resume after recovery: %+v, %v", done, err)
		}
	})
}

func TestDelegationRecoveryWithoutSourceSession(t *testing.T) {
	for _, handover := range []bool{false, true} {
		t.Run(fmt.Sprint("handover=", handover), func(t *testing.T) {
			d := newDelegationDaemon(t)
			backend := &fakeSpawnBackend{}
			_, sourceID, _ := setupDelegationSource(t, d, backend)
			consumeDelegatedPrompt(t, backend)
			msg := explicitOperationMessage(d, "removed-source", sourceID, "Keep the successor.", "successor")
			msg.Agent = nil
			if handover {
				seed := plant(t, d, protocol.SeedPlantMessage{Title: "handover work", Body: protocol.Ptr("Keep the successor.")})
				tendAs(t, d, seed.ID, sourceID)
				msg.Assignment = protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindSeed, SeedID: protocol.Ptr(seed.ID), Handover: &protocol.DelegateHandover{}}
			}
			op, err := d.startDelegation(&msg)
			if err != nil {
				t.Fatal(err)
			}
			first := waitDelegationOperation(t, d, op.OperationID)
			if first.Result == nil {
				t.Fatalf("launch failed: %+v", first)
			}
			backend.mu.Lock()
			backend.sessionIDs = append(backend.sessionIDs, op.SessionID)
			spawns := len(backend.spawnOpts)
			backend.mu.Unlock()
			released := time.Now().Add(10 * time.Second)
			for !d.beginDelegationRun(op.OperationID) {
				if time.Now().After(released) {
					t.Fatal("the completed launch never released its run")
				}
				time.Sleep(time.Millisecond)
			}
			d.endDelegationRun(op.OperationID)
			d.store.Remove(sourceID)
			if err := d.store.UpdateDelegationOperation(op.OperationID, protocol.DelegationOperationStatePreparing, "interrupted after spawn", "", "", "", nil, nil, time.Now()); err != nil {
				t.Fatal(err)
			}
			d.runDelegationOperation(op.OperationID)
			done := waitDelegationOperation(t, d, op.OperationID)
			if done.State != protocol.DelegationOperationStateCompleted || done.Result == nil || done.Result.SeedID != first.Result.SeedID {
				t.Fatalf("recovery lost successor: %+v", done)
			}
			if len(backend.spawnOpts) != spawns {
				t.Fatal("recovery spawned another worker")
			}
		})
	}
}

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
	if err := d.store.SetInstanceRole(instanceRoleChiefOfStaff, sourceID); err != nil {
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
	if err := d.store.SetInstanceRole(instanceRoleChiefOfStaff, "replacement-chief"); err != nil {
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
	if err := d.writeDelegationWorktreeOwner(path, ownerToken); err != nil {
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
	if err := d.writeDelegationWorktreeOwner(path, ownerToken); err != nil {
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
