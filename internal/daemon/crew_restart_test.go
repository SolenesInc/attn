package daemon

import (
	"encoding/json"
	"errors"
	"net"
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

func readCrewRestartResult(t *testing.T, client *wsClient) protocol.CrewRestartResultMessage {
	t.Helper()
	select {
	case raw := <-client.send:
		var result protocol.CrewRestartResultMessage
		if err := json.Unmarshal(raw.payload, &result); err != nil {
			t.Fatalf("decode crew restart result: %v", err)
		}
		return result
	default:
		t.Fatal("missing crew restart response")
		return protocol.CrewRestartResultMessage{}
	}
}

func TestCrewRestart_TracksTheRequestUntilTheRealHandoffStartsASuccessor(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	filesBefore := handoffFiles(t, d, "trellis")

	first := crewRestartCall(t, d, "trellis", "restart-1")
	if !first.Ok {
		t.Fatalf("restart: %v", protocol.Deref(first.Error))
	}
	restart := first.CrewRestartResult.Restart
	if restart.State != protocol.CrewRestartStateQueued || restart.RequestID != "restart-1" || restart.SessionID != woken.SessionID {
		t.Fatalf("queued restart = %+v", restart)
	}
	if len(handoffFiles(t, d, "trellis")) != len(filesBefore) {
		t.Fatal("request delivery fabricated a handoff letter")
	}
	unread, err := d.store.UnreadAgentMailboxDeliveries(woken.SessionID)
	if err != nil || len(unread) != 1 || !strings.Contains(unread[0].Item.Prompt, "attn handoff --nap") {
		t.Fatalf("restart inbox = %+v err=%v", unread, err)
	}

	duplicate := crewRestartCall(t, d, "trellis", "restart-2")
	if !duplicate.Ok || duplicate.CrewRestartResult.Restart.RequestID != "restart-1" {
		t.Fatalf("duplicate restart = %+v", duplicate.CrewRestartResult)
	}
	unread, err = d.store.UnreadAgentMailboxDeliveries(woken.SessionID)
	if err != nil || len(unread) != 1 {
		t.Fatalf("duplicate restart queued %d inbox items: %v", len(unread), err)
	}

	inbox := gardenCall(t, func(c net.Conn) {
		d.handleAgentInbox(c, &protocol.AgentInboxMessage{Cmd: protocol.CmdAgentInbox, RecipientSessionID: woken.SessionID})
	})
	if !inbox.Ok || len(inbox.AgentInboxBatchResult.Items) != 1 {
		t.Fatalf("read restart inbox: %+v", inbox)
	}
	if state := memberByID(t, crewList(t, d), "trellis").Restart.State; state != protocol.CrewRestartStateRequested {
		t.Fatalf("restart state after inbox read = %q, want requested", state)
	}

	handoff := crewHandoffCall(t, d, woken.SessionID, "I wrote this letter myself.")
	if !handoff.Ok || handoff.CrewHandoffResult.NapError != nil {
		t.Fatalf("handoff: %+v", handoff)
	}
	completed := memberByID(t, crewList(t, d), "trellis")
	if completed.Restart == nil || completed.Restart.State != protocol.CrewRestartStateCompleted ||
		protocol.Deref(completed.Restart.SuccessorSessionID) != protocol.Deref(handoff.CrewHandoffResult.SessionID) {
		t.Fatalf("completed restart = %+v", completed.Restart)
	}
	if protocol.Deref(completed.BindingSession) != protocol.Deref(handoff.CrewHandoffResult.SessionID) || len(spawnedSessions(t, backend)) != 2 {
		t.Fatalf("binding/spawns after completion = %q/%d", protocol.Deref(completed.BindingSession), len(spawnedSessions(t, backend)))
	}
}

func TestCrewRestart_FailedSuccessorLaunchRetriesTheFiledLetterOnce(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("keel", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if result := crewRestartCall(t, d, "keel", "restart-fail"); !result.Ok {
		t.Fatalf("restart: %v", protocol.Deref(result.Error))
	}
	backend.mu.Lock()
	backend.spawnErr = errors.New("no room for successor")
	backend.mu.Unlock()

	failedHandoff := crewHandoffCall(t, d, woken.SessionID, "The one filed letter.")
	if !failedHandoff.Ok || failedHandoff.CrewHandoffResult.NapError == nil {
		t.Fatalf("failed handoff = %+v", failedHandoff)
	}
	failed := memberByID(t, crewList(t, d), "keel")
	if failed.Restart == nil || failed.Restart.State != protocol.CrewRestartStateFailed || failed.Restart.LetterPath == nil {
		t.Fatalf("failed restart = %+v", failed.Restart)
	}
	filesAfterFailure := handoffFiles(t, d, "keel")

	backend.mu.Lock()
	backend.spawnErr = nil
	backend.mu.Unlock()
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
}

func TestCrewRestart_AnAsleepMemberWakesDirectlyAndWebSocketCorrelatesIt(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	client := newInternalWSClient()
	initial := memberByID(t, crewList(t, d), "alder")
	d.handleCrewRestartWS(client, &protocol.CrewRestartMessage{
		Cmd: protocol.CmdCrewRestart, Member: "alder", RequestID: "wake-alder", ExpectedSessionID: protocol.Ptr(""),
		ExpectedRevision: protocol.Ptr(initial.Revision),
	})
	result := readCrewRestartResult(t, client)
	if !result.Success || result.RequestID != "wake-alder" || result.Restart == nil || result.Restart.State != protocol.CrewRestartStateCompleted {
		t.Fatalf("restart result = %+v", result)
	}
	if result.Member == nil || protocol.Deref(result.Member.BindingSession) != protocol.Deref(result.Restart.SuccessorSessionID) || len(spawnedSessions(t, backend)) != 1 {
		t.Fatalf("asleep wake member/restart/spawns = %+v/%+v/%d", result.Member, result.Restart, len(spawnedSessions(t, backend)))
	}

	d.handleCrewRestartWS(client, &protocol.CrewRestartMessage{Cmd: protocol.CmdCrewRestart, Member: "alder"})
	missing := readCrewRestartResult(t, client)
	if missing.Success || !strings.Contains(protocol.Deref(missing.Error), "request id") {
		t.Fatalf("missing request id result = %+v", missing)
	}
	d.handleCrewRestartWS(client, &protocol.CrewRestartMessage{Cmd: protocol.CmdCrewRestart, Member: "alder", RequestID: "missing-day"})
	missingDay := readCrewRestartResult(t, client)
	if missingDay.Success || !strings.Contains(protocol.Deref(missingDay.Error), "expected session id") {
		t.Fatalf("missing expected session result = %+v", missingDay)
	}
	d.handleCrewRestartWS(client, &protocol.CrewRestartMessage{
		Cmd: protocol.CmdCrewRestart, Member: "keel", RequestID: "missing-revision", ExpectedSessionID: protocol.Ptr(""),
	})
	missingRevision := readCrewRestartResult(t, client)
	if missingRevision.Success || !strings.Contains(protocol.Deref(missingRevision.Error), "expected revision") {
		t.Fatalf("missing expected revision result = %+v", missingRevision)
	}
}

func TestCrewRestart_RequiresTheDayTheUserActedOn(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	firstDay, err := d.crewWake("alder", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	actedOn := memberByID(t, crewList(t, d), "alder")
	if result := crewRestartCall(t, d, "alder", "newer-request"); !result.Ok {
		t.Fatalf("restart: %v", protocol.Deref(result.Error))
	}
	if handoff := crewHandoffCall(t, d, firstDay.SessionID, "Move to the next day."); !handoff.Ok || handoff.CrewHandoffResult.NapError != nil {
		t.Fatalf("handoff: %+v", handoff)
	}

	stale := protocol.CrewRestartMessage{
		Cmd: protocol.CmdCrewRestart, Member: "alder", RequestID: "delayed-request",
		ExpectedSessionID: protocol.Ptr(firstDay.SessionID), ExpectedRevision: protocol.Ptr(actedOn.Revision),
	}
	response := gardenCall(t, func(c net.Conn) { d.handleCrewRestart(c, &stale) })
	if response.Ok || !strings.Contains(protocol.Deref(response.Error), "day changed") {
		t.Fatalf("delayed restart = ok:%t error:%q", response.Ok, protocol.Deref(response.Error))
	}
	if len(spawnedSessions(t, backend)) != 2 {
		t.Fatalf("delayed request started another day: %d spawns", len(spawnedSessions(t, backend)))
	}
}

func TestCrewRestart_RevisionRejectsADelayedAsleepRequestAfterAnotherDay(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	initial := memberByID(t, crewList(t, d), "alder")
	requestA := protocol.CrewRestartMessage{
		Cmd: protocol.CmdCrewRestart, Member: "alder", RequestID: "asleep-a",
		ExpectedSessionID: protocol.Ptr(""), ExpectedRevision: protocol.Ptr(initial.Revision),
	}
	if response := gardenCall(t, func(c net.Conn) { d.handleCrewRestart(c, &requestA) }); !response.Ok {
		t.Fatalf("first asleep request: %v", protocol.Deref(response.Error))
	}
	firstDay := protocol.Deref(memberByID(t, crewList(t, d), "alder").BindingSession)
	if response := crewRestartCall(t, d, "alder", "awake-b"); !response.Ok {
		t.Fatalf("second restart: %v", protocol.Deref(response.Error))
	}
	handoff := crewHandoffCall(t, d, firstDay, "Start the second day.")
	if !handoff.Ok || handoff.CrewHandoffResult.NapError != nil {
		t.Fatalf("second-day handoff: %+v", handoff)
	}
	secondDay := protocol.Deref(handoff.CrewHandoffResult.SessionID)
	teardown, err := d.prepareSessionTeardown(secondDay)
	if err != nil {
		t.Fatalf("prepare second day to sleep: %v", err)
	}
	d.closeNappedSession(secondDay, teardown)

	client := newInternalWSClient()
	d.handleCrewRestartWS(client, &requestA)
	stale := readCrewRestartResult(t, client)
	if stale.Success || !stale.Conflict || stale.Member == nil || stale.Member.Revision == initial.Revision {
		t.Fatalf("delayed asleep request = %+v", stale)
	}
	if len(spawnedSessions(t, backend)) != 2 {
		t.Fatalf("delayed asleep request started another day: %d spawns", len(spawnedSessions(t, backend)))
	}
}

func TestCrewRestart_UsesTheWireAsleepBindingWhenRawBindingIsDangling(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	if _, err := d.updateCrewMember("alder", func(member *crew.Member) (bool, error) {
		member.BindingSession = "missing-session"
		return true, nil
	}); err != nil {
		t.Fatalf("seed dangling binding: %v", err)
	}
	shown := memberByID(t, crewList(t, d), "alder")
	if shown.BindingSession != nil {
		t.Fatalf("dangling raw binding reached the roster: %q", *shown.BindingSession)
	}
	msg := protocol.CrewRestartMessage{
		Cmd: protocol.CmdCrewRestart, Member: "alder", RequestID: "wire-asleep",
		ExpectedSessionID: protocol.Ptr(""), ExpectedRevision: protocol.Ptr(shown.Revision),
	}
	response := gardenCall(t, func(c net.Conn) { d.handleCrewRestart(c, &msg) })
	if !response.Ok || response.CrewRestartResult.Restart.State != protocol.CrewRestartStateCompleted {
		t.Fatalf("restart from wire-asleep roster = %+v", response)
	}
	if got := protocol.Deref(response.CrewRestartResult.Member.BindingSession); got == "" || got == "missing-session" {
		t.Fatalf("restart retained dangling binding %q", got)
	}
	if len(spawnedSessions(t, backend)) != 1 {
		t.Fatalf("wire-asleep restart spawned %d days, want 1", len(spawnedSessions(t, backend)))
	}
}

func TestCrewRestart_OperationRecordCASRejectsASettingsRace(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	member, doc, err := d.crewMember("alder")
	if err != nil {
		t.Fatalf("read member: %v", err)
	}
	candidate := member
	candidate.Restart = &crew.Restart{RequestID: "raced", State: crew.RestartQueued}
	if _, err := d.updateCrewMember("alder", func(current *crew.Member) (bool, error) {
		current.Effort = "high"
		return true, nil
	}); err != nil {
		t.Fatalf("race a settings write: %v", err)
	}

	err = d.recordCrewRestart(candidate, doc.Rev)
	var conflict *crewRestartConflictError
	if !errors.As(err, &conflict) || conflict.member.Revision <= int(doc.Rev) {
		t.Fatalf("operation record error = %v, conflict = %+v", err, conflict)
	}
	current := memberByID(t, crewList(t, d), "alder")
	if current.Restart != nil || protocol.Deref(current.Effort) != "high" || len(spawnedSessions(t, backend)) != 0 {
		t.Fatalf("raced operation changed member/spawned: %+v / %d", current, len(spawnedSessions(t, backend)))
	}
}

func TestCrewRestart_ReconcileCompletesAfterSuccessorSpawnWithoutLaunchingAgain(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("alder", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if response := crewRestartCall(t, d, "alder", "post-spawn"); !response.Ok {
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
	before := memberByID(t, crewList(t, d), "alder")
	if before.Restart == nil || before.Restart.State != protocol.CrewRestartStateQueued || protocol.Deref(before.BindingSession) != successor {
		t.Fatalf("simulated crash state = %+v", before)
	}

	d.reconcileCrewRestarts()
	after := memberByID(t, crewList(t, d), "alder")
	if after.Restart == nil || after.Restart.State != protocol.CrewRestartStateCompleted ||
		protocol.Deref(after.Restart.SuccessorSessionID) != successor {
		t.Fatalf("recovered restart = %+v", after.Restart)
	}
	if len(spawnedSessions(t, backend)) != 2 {
		t.Fatalf("recovery launched another successor: %d spawns", len(spawnedSessions(t, backend)))
	}
}

func TestCrewRestart_ReconcileCompletesAPendingRestartWhoseDayExited(t *testing.T) {
	for _, state := range []crew.RestartState{crew.RestartQueued, crew.RestartRequested} {
		t.Run(string(state), func(t *testing.T) {
			d, backend, _ := newWakeableDaemon(t)
			runtime := &crewRuntimeBackend{fakeSpawnBackend: backend, running: make(map[string]bool)}
			d.ptyBackend = runtime
			woken, err := d.crewWake("alder", "")
			if err != nil {
				t.Fatalf("wake: %v", err)
			}
			if _, err := d.setCrewRestart("alder", &crew.Restart{RequestID: "exited-day", SessionID: woken.SessionID, State: state}); err != nil {
				t.Fatalf("seed pending restart: %v", err)
			}
			runtime.running[woken.SessionID] = false

			d.reconcileCrewRestarts()
			member := memberByID(t, crewList(t, d), "alder")
			if member.Restart == nil || member.Restart.State != protocol.CrewRestartStateCompleted || member.Restart.RequestID != "exited-day" {
				t.Fatalf("reconciled restart = %+v", member.Restart)
			}
			if protocol.Deref(member.BindingSession) == woken.SessionID || len(spawnedSessions(t, backend)) != 2 {
				t.Fatalf("reconciled binding/spawns = %q/%d", protocol.Deref(member.BindingSession), len(spawnedSessions(t, backend)))
			}
		})
	}
}

func TestCrewRestart_ARetryRepairsBothMailboxCrashGapsWithoutAskingTwice(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("alder", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if _, err := d.setCrewRestart("alder", &crew.Restart{
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
