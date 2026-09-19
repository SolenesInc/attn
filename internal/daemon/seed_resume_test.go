package daemon

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/toolhome"
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

func TestSeedResumeRespawnsClosedTender(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	leafID, seedID := delegateBoundSeed(t, d, backend, sourceSessionID, "codex")
	writeCodexRolloutFixture(t, "codex-conv-xyz")
	d.persistResumeSessionID(leafID, "codex-conv-xyz")

	d.handleUnregister(drainedConn(t), &protocol.UnregisterMessage{ID: leafID})
	d.waitForSessionTeardown(leafID)
	if d.store.Get(leafID) != nil {
		t.Fatalf("session %s still registered after close", leafID)
	}
	if got := d.store.GetResumeSessionID(leafID); got != "codex-conv-xyz" {
		t.Fatalf("ledger resume id after close = %q, want codex-conv-xyz", got)
	}
	before, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatalf("readSeed before: %v", err)
	}
	notesBefore := seedNoteCount(t, d, seedID)
	since := spawnCount(backend)

	outcome, err := d.resumeSeed(seedID)
	if err != nil {
		t.Fatalf("resumeSeed: %v", err)
	}
	if outcome.SessionID != leafID || outcome.AlreadyRunning {
		t.Fatalf("outcome = %+v, want session=%s already_running=false", outcome, leafID)
	}

	dispatch, ok := d.gardenDispatch(leafID)
	if !ok {
		t.Fatalf("no dispatch record for %s", leafID)
	}
	wantDir, err := validateDelegationDirectory(dispatch.Cwd)
	if err != nil {
		t.Fatalf("canonicalize dispatch cwd: %v", err)
	}
	session := d.store.Get(leafID)
	if session == nil || session.Directory != wantDir {
		t.Fatalf("resumed session = %+v, want dir=%s", session, wantDir)
	}
	workspaceID := "workspace-" + leafID
	if outcome.WorkspaceID != workspaceID || d.store.GetWorkspace(workspaceID) == nil {
		t.Fatalf("resume workspace = %q, GetWorkspace=%v", outcome.WorkspaceID, d.store.GetWorkspace(workspaceID))
	}
	layout := d.store.GetWorkspaceLayout(workspaceID)
	if layout == nil || len(layout.Panes) != 1 || layout.Panes[0].SessionID != leafID {
		t.Fatalf("resume layout = %+v, want one pane for %s", layout, leafID)
	}

	spawn := resumeSpawnForSession(t, backend, leafID, since)
	if spawn.ResumeSessionID != "codex-conv-xyz" {
		t.Fatalf("resume spawn ResumeSessionID = %q, want codex-conv-xyz", spawn.ResumeSessionID)
	}

	after, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatalf("readSeed after: %v", err)
	}
	if after.Status != before.Status || after.TenderSession != before.TenderSession {
		t.Fatalf("seed moved: %+v, want unchanged status=%s tender=%s", after, before.Status, before.TenderSession)
	}
	if got := seedNoteCount(t, d, seedID); got != notesBefore {
		t.Fatalf("log holds %d notes, want unchanged %d (resume must not write one)", got, notesBefore)
	}
}

func TestSeedResumeLiftsTheLedgerCloseTheAppLeftBehind(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	leafID, seedID := delegateBoundSeed(t, d, backend, sourceSessionID, "codex")
	writeCodexRolloutFixture(t, "codex-conv-closed")
	d.persistResumeSessionID(leafID, "codex-conv-closed")

	d.handleUnregister(drainedConn(t), &protocol.UnregisterMessage{ID: leafID})
	if entry := d.store.SessionLedgerEntry(leafID); entry == nil || protocol.Deref(entry.ClosedAt) == "" {
		t.Fatalf("ledger entry after close = %+v, want the close recorded", entry)
	}
	since := spawnCount(backend)

	outcome, err := d.resumeSeed(seedID)
	if err != nil {
		t.Fatalf("resumeSeed: %v", err)
	}
	if outcome.SessionID != leafID || outcome.AlreadyRunning {
		t.Fatalf("outcome = %+v, want session=%s already_running=false", outcome, leafID)
	}
	if session := d.store.Get(leafID); session == nil {
		t.Fatal("store.Get after resume = nil, want the reopened session on every live surface")
	}
	if entry := d.store.SessionLedgerEntry(leafID); entry == nil || protocol.Deref(entry.ClosedAt) != "" {
		t.Fatalf("ledger entry after resume = %+v, want the close lifted", entry)
	}
	if spawn := resumeSpawnForSession(t, backend, leafID, since); spawn.ResumeSessionID != "codex-conv-closed" {
		t.Fatalf("resume spawn ResumeSessionID = %q, want codex-conv-closed", spawn.ResumeSessionID)
	}
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

func TestSeedResumeAlreadyRunningFocusesInsteadOfSpawning(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	leafID, seedID := delegateBoundSeed(t, d, backend, sourceSessionID, "codex")
	backend.sessionIDs = append(backend.sessionIDs, leafID)

	before := spawnCount(backend)
	outcome, err := d.resumeSeed(seedID)
	if err != nil {
		t.Fatalf("resumeSeed: %v", err)
	}
	if !outcome.AlreadyRunning || outcome.SessionID != leafID {
		t.Fatalf("outcome = %+v, want already_running session=%s", outcome, leafID)
	}
	if got := spawnCount(backend); got != before {
		t.Fatalf("spawn count = %d, want unchanged %d (already-running resume must not spawn)", got, before)
	}
}

func TestSeedResumeRefusesWhenTranscriptGoneWithoutCreatingAnything(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	leafID, seedID := delegateBoundSeed(t, d, backend, sourceSessionID, "claude")

	d.handleUnregister(drainedConn(t), &protocol.UnregisterMessage{ID: leafID})
	d.waitForSessionTeardown(leafID)
	d.persistResumeSessionID(leafID, leafID)
	t.Setenv(toolhome.EnvVar, t.TempDir())
	before, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatalf("readSeed before: %v", err)
	}
	spawnsBefore := spawnCount(backend)
	notesBefore := seedNoteCount(t, d, seedID)

	if _, err := d.resumeSeed(seedID); err == nil || !strings.Contains(err.Error(), "original conversation is unavailable") {
		t.Fatalf("resumeSeed error = %v, want unavailable conversation refusal", err)
	}
	if got := spawnCount(backend); got != spawnsBefore {
		t.Fatalf("spawn count = %d, want unchanged %d", got, spawnsBefore)
	}
	if ws := d.store.GetWorkspace("workspace-" + leafID); ws != nil {
		t.Fatalf("resume left a phantom workspace: %+v", ws)
	}
	after, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatalf("readSeed after: %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("seed changed on refused resume:\n before=%+v\n  after=%+v", before, after)
	}
	if got := seedNoteCount(t, d, seedID); got != notesBefore {
		t.Fatalf("log holds %d notes, want unchanged %d", got, notesBefore)
	}
}

func TestSeedResumeReclaimsParkedSeedFromItsLastExecution(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	leafID, seedID := delegateBoundSeed(t, d, backend, sourceSessionID, "codex")
	writeCodexRolloutFixture(t, "codex-parked")
	d.persistResumeSessionID(leafID, "codex-parked")

	parked := move(t, d, leafID, seedID, garden.VerbPark, "", "")
	if parked.Status != garden.StatusDormant || parked.TenderSession != "" {
		t.Fatalf("parked seed = %+v", parked)
	}
	if protocol.Deref(parked.LastExecutionID) != leafID {
		t.Fatalf("last_execution_id = %q, want %q", protocol.Deref(parked.LastExecutionID), leafID)
	}
	d.handleUnregister(drainedConn(t), &protocol.UnregisterMessage{ID: leafID})
	d.waitForSessionTeardown(leafID)

	outcome, err := d.resumeSeed(seedID)
	if err != nil {
		t.Fatalf("resumeSeed: %v", err)
	}
	if outcome.SessionID != leafID || outcome.AlreadyRunning {
		t.Fatalf("outcome = %+v, want reopened session %s", outcome, leafID)
	}
	resumed, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Status != garden.StatusGrowing || resumed.TenderSession != leafID || resumed.LastExecutionID != leafID {
		t.Fatalf("resumed seed = %+v", resumed)
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

func TestReopenSessionRuntimeRunsBindingWhenAnotherRestoreWon(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	leafID, _ := delegateBoundSeed(t, d, backend, sourceSessionID, "codex")
	prior := d.store.Get(leafID)
	if prior == nil {
		t.Fatal("delegated session is missing")
	}
	backend.mu.Lock()
	backend.sessionIDs = []string{leafID}
	backend.mu.Unlock()

	bindings := 0
	outcome, err := d.reopenSessionRuntime(sessionReopenPlan{
		SessionID: leafID, Directory: prior.Directory, WorkspaceID: prior.WorkspaceID,
	}, d.newDelegationRollback(), func() error {
		bindings++
		return nil
	})
	if err != nil {
		t.Fatalf("reopenSessionRuntime: %v", err)
	}
	if outcome == nil || !outcome.AlreadyRunning || bindings != 1 {
		t.Fatalf("outcome = %+v, bindings = %d; want already running with one binding", outcome, bindings)
	}
}

func TestSeedResumeBindingIsAtomicWhenTheSeedChangesDuringLaunch(t *testing.T) {
	d := newGardenDaemon(t)
	seedWire := plant(t, d, protocol.SeedPlantMessage{Title: "racing resume", Body: protocol.Ptr("old body")})
	seed, doc, err := d.readSeed(seedWire.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: "resumed-session", Label: "resumed", Agent: protocol.SessionAgentCodex,
		Directory: t.TempDir(), State: protocol.SessionStateIdle,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	editSeed(t, d, seed.ID, "new body")

	err = d.bindResumedSeed(seed, doc, "resumed-session", d.store.Get("resumed-session").Directory, "codex", "native-resume")
	if err == nil || !strings.Contains(err.Error(), "changed while its conversation was resuming") {
		t.Fatalf("bindResumedSeed error = %v, want guarded conflict", err)
	}
	after, _, err := d.readSeed(seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Body != "new body" || after.TenderSession != "" || after.LastExecutionID != "" {
		t.Fatalf("failed binding partially moved seed: %+v", after)
	}
	if _, ok := d.gardenDispatch("resumed-session"); ok {
		t.Fatal("failed binding wrote a dispatch without moving the seed")
	}
}

func TestSeedResumeValidation(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T, d *Daemon) (seedID, tender string)
		message string
	}{
		{
			name: "unknown seed",
			setup: func(t *testing.T, d *Daemon) (string, string) {
				return "s-zzzzzz", ""
			},
		},
		{
			name: "untended seed",
			setup: func(t *testing.T, d *Daemon) (string, string) {
				seed := plant(t, d, protocol.SeedPlantMessage{
					SourceSessionID: protocol.Ptr("sess-a"), Title: "nobody holds this",
				})
				return seed.ID, ""
			},
		},
		{
			name:    "tender attn never launched",
			message: "no longer in the ledger",
			setup: func(t *testing.T, d *Daemon) (string, string) {
				seed := plant(t, d, protocol.SeedPlantMessage{
					SourceSessionID: protocol.Ptr("sess-a"), Title: "held by a ghost",
				})
				addGardenSession(t, d, "ghost")
				move(t, d, "ghost", seed.ID, garden.VerbTend, "", "")
				d.store.Remove("ghost")
				return seed.ID, "ghost"
			},
		},
		{
			name:    "directory is gone",
			message: "no longer in the ledger",
			setup: func(t *testing.T, d *Daemon) (string, string) {
				seed := plant(t, d, protocol.SeedPlantMessage{
					SourceSessionID: protocol.Ptr("sess-a"), Title: "worktree removed",
				})
				addGardenSession(t, d, "ghost")
				move(t, d, "ghost", seed.ID, garden.VerbTend, "", "")
				d.store.Remove("ghost")
				if err := d.recordGardenDispatch("ghost", seed.ID, "", filepath.Join(t.TempDir(), "does-not-exist"), "codex", false); err != nil {
					t.Fatalf("recordGardenDispatch: %v", err)
				}
				return seed.ID, "ghost"
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newGardenDaemon(t)
			d.ptyBackend = &fakeSpawnBackend{}
			seedID, tender := tc.setup(t, d)
			if _, err := d.resumeSeed(seedID); err == nil {
				t.Fatal("resumeSeed succeeded, want a refusal")
			} else if tc.message != "" && !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("resumeSeed error = %q, want path/message %q", err, tc.message)
			}
			if tender != "" {
				if ws := d.store.GetWorkspace("workspace-" + tender); ws != nil {
					t.Fatalf("resume left a phantom workspace: %+v", ws)
				}
			}
		})
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
