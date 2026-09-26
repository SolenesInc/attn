package daemon

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

func resumeSpawnForSession(t *testing.T, backend *fakeSpawnBackend, sessionID string, since int) ptybackend.SpawnOptions {
	t.Helper()
	backend.mu.Lock()
	defer backend.mu.Unlock()
	for i := since; i < len(backend.spawnOpts); i++ {
		if backend.spawnOpts[i].ID == sessionID {
			return backend.spawnOpts[i]
		}
	}
	t.Fatalf("no spawn recorded for %s at/after index %d (spawns=%d)", sessionID, since, len(backend.spawnOpts))
	return ptybackend.SpawnOptions{}
}

func seedNoteCount(t *testing.T, d *Daemon, seedID string) int {
	t.Helper()
	notes, err := d.readNotesDomain(seedID)
	if err != nil {
		t.Fatalf("readNotesDomain(%s): %v", seedID, err)
	}
	return len(notes)
}

func spawnCount(backend *fakeSpawnBackend) int {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return len(backend.spawnOpts)
}

func delegateBoundSeed(t *testing.T, d *Daemon, backend *fakeSpawnBackend, sourceSessionID, agent string) (string, string) {
	t.Helper()
	if d.daemonInstanceID == "" {
		id, err := enrollment.EnsureDaemonID(d.dataRoot)
		if err != nil {
			t.Fatalf("prepare test Garden identity: %v", err)
		}
		d.daemonInstanceID = id
		if err := d.ensureEnrollment(); err != nil {
			t.Fatalf("prepare test Garden enrollment: %v", err)
		}
	}
	d.ensureGardenCollections()
	consumeDelegatedPrompt(t, backend)
	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Investigate the tracked task."),
		Agent:           protocol.Ptr(agent),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	seedID, bound := d.gardenDispatchCrown(result.SessionID)
	if !bound {
		t.Fatal("the delegation bound no seed to its session")
	}
	return result.SessionID, seedID
}

func TestAFailedResumeReturnsTheTenderToTheLedger(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	leafID, seedID := delegateBoundSeed(t, d, backend, sourceSessionID, "codex")
	writeCodexRolloutFixture(t, "codex-conv-doomed")
	d.persistResumeSessionID(leafID, "codex-conv-doomed")

	d.closeSession(leafID, store.SessionClose{By: "sess-dispatcher", Reason: "brief delivered"})
	closed := d.store.SessionLedgerEntry(leafID)
	if closed == nil || protocol.Deref(closed.ClosedAt) == "" {
		t.Fatalf("ledger entry after the close = %+v, want it closed", closed)
	}
	backend.spawnErr = errors.New("no pty available")

	if _, err := d.resumeSeed(seedID); err == nil {
		t.Fatal("resumeSeed error = nil, want the spawn failure")
	}
	if session := d.store.Get(leafID); session != nil {
		t.Fatalf("store.Get after a failed resume = %+v, want no live session", session)
	}
	if entry := d.store.SessionLedgerEntry(leafID); !reflect.DeepEqual(entry, closed) {
		t.Errorf("a failed resume rewrote the ledger row:\n got=%+v\nwant=%+v", entry, closed)
	}
}

func TestSeedResumeRollsBackWhenSeedChangesAfterSpawn(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	leafID, seedID := delegateBoundSeed(t, d, backend, sourceSessionID, "codex")
	writeCodexRolloutFixture(t, "codex-racing-resume")
	d.persistResumeSessionID(leafID, "codex-racing-resume")
	move(t, d, leafID, seedID, garden.VerbPark, "", "")
	d.handleUnregister(drainedConn(t), &protocol.UnregisterMessage{ID: leafID})
	d.waitForSessionTeardown(leafID)

	changed := false
	lifecycleLockedDuringRollback := false
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.ID != leafID || changed {
			return
		}
		changed = true
		editSeed(t, d, seedID, "changed during launch")
	}
	backend.onKill = func() {
		d.sessionLifecycleLocksMu.Lock()
		entry := d.sessionLifecycleLocks[leafID]
		d.sessionLifecycleLocksMu.Unlock()
		if entry == nil {
			return
		}
		if entry.lock.TryLock() {
			entry.lock.Unlock()
			return
		}
		lifecycleLockedDuringRollback = true
	}

	client := newInternalWSClient()
	d.handleSeedResume(client, &protocol.SeedResumeMessage{
		Cmd: protocol.CmdSeedResume, RequestID: protocol.Ptr("resume-race"), SeedID: seedID,
	})
	message := <-client.send
	var reply protocol.SeedResumeResultMessage
	if err := json.Unmarshal(message.payload, &reply); err != nil {
		t.Fatalf("decode seed resume response: %v", err)
	}
	if reply.Success || reply.RequestID != "resume-race" ||
		!strings.Contains(protocol.Deref(reply.Error), "changed while its conversation was resuming") {
		t.Fatalf("seed resume response = %+v, want post-spawn revision conflict", reply)
	}
	d.waitForSessionTeardown(leafID)
	if session := d.store.Get(leafID); session != nil {
		t.Fatalf("rollback left session registered: %+v", session)
	}
	if workspace := d.store.GetWorkspace(reopenWorkspaceID(leafID)); workspace != nil {
		t.Fatalf("rollback left workspace registered: %+v", workspace)
	}
	if !lifecycleLockedDuringRollback {
		t.Fatal("session lifecycle lock was not retained through rollback")
	}
	seed, _, readErr := d.readSeed(seedID)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if seed.Body != "changed during launch" || seed.Status != garden.StatusDormant || seed.TenderSession != "" {
		t.Fatalf("rollback overwrote the concurrent seed change: %+v", seed)
	}
}

func TestSeedResumeRollbackPreservesWorkerlessActiveLedgerRow(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	leafID, seedID := delegateBoundSeed(t, d, backend, sourceSessionID, "codex")
	writeCodexRolloutFixture(t, "codex-workerless-resume")
	d.persistResumeSessionID(leafID, "codex-workerless-resume")
	move(t, d, leafID, seedID, garden.VerbPark, "", "")
	prior := d.store.Get(leafID)
	if prior == nil || d.sessionHasLiveWorker(leafID) {
		t.Fatalf("fixture session = %+v, live=%v; want workerless active row", prior, d.sessionHasLiveWorker(leafID))
	}

	changed := false
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.ID != leafID || changed {
			return
		}
		changed = true
		editSeed(t, d, seedID, "changed during workerless launch")
	}

	client := newInternalWSClient()
	d.handleSeedResume(client, &protocol.SeedResumeMessage{
		Cmd: protocol.CmdSeedResume, RequestID: protocol.Ptr("resume-workerless"), SeedID: seedID,
	})
	message := <-client.send
	var reply protocol.SeedResumeResultMessage
	if err := json.Unmarshal(message.payload, &reply); err != nil {
		t.Fatalf("decode seed resume response: %v", err)
	}
	if reply.Success || !strings.Contains(protocol.Deref(reply.Error), "changed while its conversation was resuming") {
		t.Fatalf("seed resume response = %+v, want post-spawn revision conflict", reply)
	}
	got := d.store.Get(leafID)
	if !reflect.DeepEqual(got, prior) {
		t.Fatalf("active ledger row after rollback = %+v, want %+v", got, prior)
	}
	if !backend.WasKilledAndRemoved(leafID) {
		t.Fatal("rollback did not stop the replacement runtime")
	}
}

func TestSeedResumeRollsBackPaneWhenSpawnFails(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{
		SourceSessionID: protocol.Ptr("sess-a"), Title: "Resume me",
	})
	addGardenSession(t, d, "ghost-session")
	move(t, d, "ghost-session", seed.ID, garden.VerbTend, "", "")
	d.store.Remove("ghost-session")
	if err := d.recordGardenDispatch("ghost-session", seed.ID, "", t.TempDir(), "codex", false); err != nil {
		t.Fatalf("recordGardenDispatch: %v", err)
	}
	d.ptyBackend = &failingSpawnBackend{err: syscall.EPERM}

	if _, err := d.resumeSeed(seed.ID); err == nil {
		t.Fatal("resumeSeed succeeded, want spawn failure")
	}
	if ws := d.store.GetWorkspace("workspace-ghost-session"); ws != nil {
		t.Fatalf("workspace survived a failed resume: %+v", ws)
	}
}
