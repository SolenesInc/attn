package daemon

import (
	"errors"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
)

func crewRestartCall(t *testing.T, d *Daemon, member, requestID string) protocol.Response {
	t.Helper()
	current, doc, err := d.crewMember(member)
	expectedRevision := 0
	if err != nil {
		if strings.Contains(err.Error(), "outpost") {
			current.BindingSession = ""
		} else {
			t.Fatalf("read current crew day: %v", err)
		}
	} else {
		expectedRevision = int(doc.Rev)
		if !d.crewBindingLive(current) {
			current.BindingSession = ""
		}
	}
	msg := protocol.CrewRestartMessage{
		Cmd: protocol.CmdCrewRestart, Member: member, RequestID: requestID,
		ExpectedSessionID: protocol.Ptr(current.BindingSession), ExpectedRevision: protocol.Ptr(expectedRevision),
	}
	return gardenCall(t, func(c net.Conn) { d.handleCrewRestart(c, &msg) })
}

func TestCrewRestart_AHandoffThatFailsAfterFilingKeepsTheLetterForOneRetry(t *testing.T) {
	cases := []struct {
		name          string
		breakHandoff  func(d *Daemon, backend *fakeSpawnBackend)
		repairHandoff func(d *Daemon, backend *fakeSpawnBackend)
		wantHandoffOk bool
	}{
		{
			name: "successor spawn fails",
			breakHandoff: func(_ *Daemon, backend *fakeSpawnBackend) {
				backend.mu.Lock()
				backend.spawnErr = errors.New("no room for successor")
				backend.mu.Unlock()
			},
			repairHandoff: func(_ *Daemon, backend *fakeSpawnBackend) {
				backend.mu.Lock()
				backend.spawnErr = nil
				backend.mu.Unlock()
			},
			wantHandoffOk: true,
		},
		{
			name: "teardown preparation fails",
			breakHandoff: func(d *Daemon, _ *fakeSpawnBackend) {
				d.prepareSessionTeardownHook = func(string) error { return errors.New("tombstone write failed") }
			},
			repairHandoff: func(d *Daemon, _ *fakeSpawnBackend) { d.prepareSessionTeardownHook = nil },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, backend, _ := newWakeableDaemon(t)
			woken, err := d.crewWake("keel", "")
			if err != nil {
				t.Fatalf("wake: %v", err)
			}
			if result := crewRestartCall(t, d, "keel", "restart-fail"); !result.Ok {
				t.Fatalf("restart: %v", protocol.Deref(result.Error))
			}
			filesBefore := handoffFiles(t, d, "keel")
			tc.breakHandoff(d, backend)

			failedHandoff := crewHandoffCall(t, d, woken.SessionID, "The one filed letter.")
			if failedHandoff.Ok != tc.wantHandoffOk || (tc.wantHandoffOk && failedHandoff.CrewHandoffResult.NapError == nil) {
				t.Fatalf("failed handoff = %+v", failedHandoff)
			}
			filesAfterFailure := handoffFiles(t, d, "keel")
			if len(filesAfterFailure) != len(filesBefore)+1 {
				t.Fatalf("handoff files before/after = %d/%d, want one filed letter", len(filesBefore), len(filesAfterFailure))
			}
			failed := memberByID(t, crewList(t, d), "keel")
			if failed.Restart == nil || failed.Restart.State != protocol.CrewRestartStateFailed {
				t.Fatalf("failed restart = %+v", failed.Restart)
			}
			if got := protocol.Deref(failed.Restart.LetterPath); got == "" || !slices.Contains(filesAfterFailure, filepath.Base(got)) || slices.Contains(filesBefore, filepath.Base(got)) {
				t.Fatalf("restart letter = %q, want the newly filed letter among %v", got, filesAfterFailure)
			}

			tc.repairHandoff(d, backend)
			retried := crewRestartCall(t, d, "keel", "restart-retry")
			if !retried.Ok || retried.CrewRestartResult.Restart.State != protocol.CrewRestartStateCompleted {
				t.Fatalf("retried restart = %+v error=%v", retried.CrewRestartResult, protocol.Deref(retried.Error))
			}
			if len(handoffFiles(t, d, "keel")) != len(filesAfterFailure) {
				t.Fatal("retry wrote a second handoff letter")
			}
			if protocol.Deref(retried.CrewRestartResult.Member.BindingSession) != protocol.Deref(retried.CrewRestartResult.Restart.SuccessorSessionID) {
				t.Fatalf("retry binding/result = %+v", retried.CrewRestartResult)
			}
		})
	}
}

func TestCrewRestart_PersistsAnAsleepWakeLaunchFailure(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	backend.mu.Lock()
	backend.spawnErr = errors.New("launcher is unavailable")
	backend.mu.Unlock()

	response := crewRestartCall(t, d, "alder", "failed-wake")
	if response.Ok || !strings.Contains(protocol.Deref(response.Error), "launcher is unavailable") {
		t.Fatalf("failed wake response = %+v", response)
	}
	member := memberByID(t, crewList(t, d), "alder")
	if member.Restart == nil || member.Restart.State != protocol.CrewRestartStateFailed ||
		!strings.Contains(protocol.Deref(member.Restart.Error), "launcher is unavailable") {
		t.Fatalf("persisted failed wake = %+v", member.Restart)
	}
	if member.BindingSession != nil {
		t.Fatalf("failed wake retained binding %q", *member.BindingSession)
	}
}

func TestCrewRestart_AsleepRecoveryReplacesANonrunningBoundSession(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	runtime := &crewRuntimeBackend{fakeSpawnBackend: backend, running: make(map[string]bool)}
	d.ptyBackend = runtime
	if _, err := setCrewRestart(d, "alder", &crew.Restart{
		RequestID: "asleep-crash", State: crew.RestartQueued,
	}); err != nil {
		t.Fatalf("seed asleep restart intent: %v", err)
	}
	dead, err := d.crewWake("alder", "")
	if err != nil {
		t.Fatalf("simulate wake before completion: %v", err)
	}
	runtime.running[dead.SessionID] = false

	d.reconcileCrewRestarts()
	member := memberByID(t, crewList(t, d), "alder")
	if member.Restart == nil {
		t.Fatal("recovered asleep wake has no restart state")
	}
	successor := protocol.Deref(member.Restart.SuccessorSessionID)
	if member.Restart.State != protocol.CrewRestartStateCompleted ||
		successor == "" || successor == dead.SessionID || protocol.Deref(member.BindingSession) != successor {
		t.Fatalf("recovered asleep wake = %+v", member)
	}
	if !runtime.running[successor] || len(spawnedSessions(t, backend)) != 2 {
		t.Fatalf("recovery successor running=%t spawns=%d", runtime.running[successor], len(spawnedSessions(t, backend)))
	}
}

func TestCrewRestart_ReconcileKeepsAFiledRestartQueuedWhileTheSuccessorProbeFails(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	runtime := &crewRuntimeBackend{fakeSpawnBackend: backend, running: make(map[string]bool), infoErr: make(map[string]error)}
	d.ptyBackend = runtime
	woken, err := d.crewWake("alder", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if response := crewRestartCall(t, d, "alder", "probe-flake"); !response.Ok {
		t.Fatalf("queue restart: %v", protocol.Deref(response.Error))
	}
	member, _, err := d.crewMember("alder")
	if err != nil {
		t.Fatalf("read member: %v", err)
	}
	if _, err := d.crewLetterForHandoff(member, woken.SessionID, "A real filed handoff.", false); err != nil {
		t.Fatalf("file handoff: %v", err)
	}
	member, _, err = d.crewMember("alder")
	if err != nil {
		t.Fatalf("read filed member: %v", err)
	}
	teardown, err := d.prepareSessionTeardown(woken.SessionID)
	if err != nil {
		t.Fatalf("prepare old day: %v", err)
	}
	successor, err := d.crewNap(member, woken.SessionID, teardown)
	if err != nil {
		t.Fatalf("spawn successor: %v", err)
	}
	runtime.infoErr[successor] = errors.New("worker rpc: connection refused")

	d.reconcileCrewRestarts()
	flaky := memberByID(t, crewList(t, d), "alder")
	if flaky.Restart == nil || flaky.Restart.State != protocol.CrewRestartStateQueued || protocol.Deref(flaky.BindingSession) != successor {
		t.Fatalf("restart after a failed successor probe = %+v, want it still queued on %s", flaky.Restart, successor)
	}

	delete(runtime.infoErr, successor)
	replayed := crewRestartCall(t, d, "alder", "probe-flake")
	if !replayed.Ok {
		t.Fatalf("replay restart: %v", protocol.Deref(replayed.Error))
	}
	after := memberByID(t, crewList(t, d), "alder")
	if after.Restart == nil || after.Restart.State != protocol.CrewRestartStateCompleted ||
		protocol.Deref(after.Restart.SuccessorSessionID) != successor {
		t.Fatalf("recovered restart = %+v", after.Restart)
	}
	if len(spawnedSessions(t, backend)) != 2 {
		t.Fatalf("recovery launched another successor: %d spawns", len(spawnedSessions(t, backend)))
	}
}

func setCrewRestart(d *Daemon, memberID string, restart *crew.Restart) (crew.Member, error) {
	return d.updateCrewMember(memberID, func(member *crew.Member) (bool, error) {
		copy := *restart
		member.Restart = &copy
		return true, nil
	})
}

func TestCrewRestart_ARetryRepairsBothMailboxCrashGapsWithoutAskingTwice(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("alder", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if _, err := setCrewRestart(d, "alder", &crew.Restart{
		RequestID: "crash-gap", SessionID: woken.SessionID, State: crew.RestartQueued,
	}); err != nil {
		t.Fatalf("seed queued restart: %v", err)
	}

	d.reconcileCrewRestarts()
	unread, err := d.store.UnreadAgentMailboxDeliveries(woken.SessionID)
	if err != nil || len(unread) != 1 {
		t.Fatalf("repaired inbox = %+v err=%v", unread, err)
	}
	read, remaining, err := d.store.ReadAgentMailbox(woken.SessionID, 1, time.Now())
	if err != nil || len(read) != 1 || remaining != 0 {
		t.Fatalf("write read receipt before simulated crash: %+v remaining=%d err=%v", read, remaining, err)
	}

	afterReadGap := crewRestartCall(t, d, "alder", "crash-gap")
	if !afterReadGap.Ok || afterReadGap.CrewRestartResult.Restart.State != protocol.CrewRestartStateRequested {
		t.Fatalf("repair after mailbox read = %+v", afterReadGap)
	}
	unread, err = d.store.UnreadAgentMailboxDeliveries(woken.SessionID)
	if err != nil || len(unread) != 0 {
		t.Fatalf("read-gap repair queued another request: %+v err=%v", unread, err)
	}

	d.completeCrewRestart("alder", "crash-gap", woken.SessionID, "/tmp/letter", "successor")
	d.noteCrewRestartMailboxRead(read)
	d.ensureCrewRestartRequest("alder", crew.Restart{RequestID: "crash-gap", SessionID: woken.SessionID, State: crew.RestartQueued})
	if state := memberByID(t, crewList(t, d), "alder").Restart.State; state != protocol.CrewRestartStateCompleted {
		t.Fatalf("late delivery bookkeeping regressed restart to %q", state)
	}
}

func TestCrewRestart_AnOutpostCannotOwnTheLifecycle(t *testing.T) {
	const home = "d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	d := newEnrolledDaemon(t, home)
	t.Cleanup(d.stopEventBus)
	writeCrewHomes(t, d.dataRoot)
	d.ensureCrewCollections()
	d.importCrewHomes()

	result := crewRestartCall(t, d, "keel", "outpost-restart")
	if result.Ok || !strings.Contains(protocol.Deref(result.Error), home) {
		t.Fatalf("outpost restart result = ok:%t error:%q", result.Ok, protocol.Deref(result.Error))
	}
}

func TestCrewRestart_ADayThatExitsMidHandoffLeavesTheRestartToTheHandoff(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	runtime := &crewRuntimeBackend{fakeSpawnBackend: backend, running: make(map[string]bool)}
	d.ptyBackend = runtime
	woken, err := d.crewWake("alder", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if _, err := setCrewRestart(d, "alder", &crew.Restart{RequestID: "mid-handoff", SessionID: woken.SessionID, State: crew.RestartRequested}); err != nil {
		t.Fatalf("seed pending restart: %v", err)
	}
	d.recordCrewLetter("alder", woken.SessionID, filepath.Join(t.TempDir(), "letter.md"))
	runtime.running[woken.SessionID] = false

	d.releaseExitedCrewBinding(woken.SessionID)

	member := memberByID(t, crewList(t, d), "alder")
	if member.Restart == nil || member.Restart.State != protocol.CrewRestartStateRequested {
		t.Fatalf("restart = %+v, want still requested while the handoff settles it", member.Restart)
	}
	if protocol.Deref(member.BindingSession) != "" || len(spawnedSessions(t, backend)) != 1 {
		t.Fatalf("binding/spawns = %q/%d, want released with no wake", protocol.Deref(member.BindingSession), len(spawnedSessions(t, backend)))
	}
}

func TestCrewRestart_ReconcileWakesAPendingRestartWhoseDayWasAlreadyReleased(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	runtime := &crewRuntimeBackend{fakeSpawnBackend: backend, running: make(map[string]bool)}
	d.ptyBackend = runtime
	woken, err := d.crewWake("alder", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	runtime.running[woken.SessionID] = false
	if _, err := d.releaseCrewBinding("alder", woken.SessionID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := setCrewRestart(d, "alder", &crew.Restart{RequestID: "released-day", SessionID: woken.SessionID, State: crew.RestartQueued}); err != nil {
		t.Fatalf("seed pending restart: %v", err)
	}

	d.reconcileCrewRestarts()

	member := memberByID(t, crewList(t, d), "alder")
	if member.Restart == nil || member.Restart.State != protocol.CrewRestartStateCompleted {
		t.Fatalf("reconciled restart = %+v, want completed by a wake", member.Restart)
	}
	if protocol.Deref(member.BindingSession) == "" || len(spawnedSessions(t, backend)) != 2 {
		t.Fatalf("binding/spawns = %q/%d, want a fresh day", protocol.Deref(member.BindingSession), len(spawnedSessions(t, backend)))
	}
}
