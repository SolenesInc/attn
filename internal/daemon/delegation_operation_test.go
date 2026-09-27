package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
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

func TestDelegationOperationAdoptsReconciledReservedRuntime(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceID, cwd := setupDelegationSource(t, d, backend)
	encoded, _ := json.Marshal(map[string]any{"cmd": "delegate", "request_id": "spawn-crash", "source_session_id": sourceID, "brief": "Adopt the surviving runtime.", "agent": "codex", "label": "adopted"})
	record, _, err := d.store.ClaimDelegationOperation("spawn-crash", "operation-spawn-crash", "session-spawn-crash", "", "", string(encoded), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{ID: record.Operation.SessionID, ProfileID: defaultProfileID(t, d.store), Label: "adopted", Agent: protocol.SessionAgentCodex, Directory: cwd, State: protocol.SessionStateLaunching, StateSince: now, StateUpdatedAt: now, LastSeen: now})
	backend.sessionIDs = append(backend.sessionIDs, record.Operation.SessionID)
	d.runDelegationOperation(record.Operation.OperationID)
	done := waitDelegationOperation(t, d, record.Operation.OperationID)
	if done.State != protocol.DelegationOperationStateCompleted || done.Result == nil || protocol.Deref(done.Result.ProfileID) != defaultProfileID(t, d.store) {
		t.Fatalf("operation=%+v", done)
	}
	adopted := d.store.Get(record.Operation.SessionID)
	if adopted == nil || adopted.ProfileID != defaultProfileID(t, d.store) || adopted.Label != "adopted" {
		t.Fatalf("adopted session=%+v", adopted)
	}
	if got := len(backend.spawnOpts); got != 1 {
		t.Fatalf("spawn count=%d, want only source runtime", got)
	}
}

func TestLegacyDelegationOperationWithoutLiveRuntimeRequiresExplicitRetry(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceID, cwd := setupDelegationSource(t, d, backend)
	encoded, _ := json.Marshal(map[string]any{"cmd": "delegate", "request_id": "spawn-missing", "source_session_id": sourceID, "brief": "Recover the missing runtime.", "agent": "codex", "label": "respawned"})
	record, _, err := d.store.ClaimDelegationOperation("spawn-missing", "operation-spawn-missing", "session-spawn-missing", "", "", string(encoded), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{ID: record.Operation.SessionID, ProfileID: defaultProfileID(t, d.store), Label: "respawned", Agent: protocol.SessionAgentCodex, Directory: cwd, State: protocol.SessionStateRecoverable, StateSince: now, StateUpdatedAt: now, LastSeen: now})
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
